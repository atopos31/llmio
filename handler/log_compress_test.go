package handler

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/pkg/compress"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

/**
 * 数据库压缩运维接口的门。
 *
 * 这里只钉**门**，不钉迁移本身——迁移的正确性由 service 包那组测试负责
 * （含真机整库往返）。这一层要说清的是几件"错了不会报错、只会静默做错事"
 * 的事：
 *
 *  1. **没备份时拒绝开跑。** 迁移原地改写历史行，跑起来不会报错、跑完也
 *     读得回来，所以"该拦没拦"在正常运行中完全看不出来——只在真出事、想把
 *     备份盖回去的那一刻才暴露，而那时已经晚了。
 *  2. **确认之后要如实记成"无备份"。** 记成 manual 会让后来翻存证的人以为
 *     有备份可退。
 *  3. **回滚两条都不满足时不许跑。** confirm 不是字面量、或 full 不为 true，
 *     都必须拒绝：从水位续跑会静默漏掉全表前一半的帧，而它跑完是"成功"的。
 */

func setupCompressHandlerDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "compress-handler.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.ChatIO{}, &models.Config{}, &models.Block{}, &models.BlockGroup{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	prevDB := models.DB
	prevPath := models.DBPath
	models.DB = db
	// DBPath 有两个用处，都要照顾到：
	//   1. `backupPath()` = DBPath + ".bak" —— 决定「备份在哪找」。
	//   2. `RunCompression` 要 os.Stat 它来拿库大小（判定备份是不是比库还旧）。
	// 所以它必须指向**一个真实存在**的文件，否则第 2 条会先失败，
	// 这几条用例就测不到备份那道门了。"没找到备份"由 t.TempDir() 保证：
	// 全新的空目录里不会有 .bak。
	models.DBPath = path

	// 放开跑成功的用例会真的起一个后台迁移 goroutine。它跑在空库上、
	// 毫秒级就结束，但**必须在下一个用例开始前确认它退出了**——
	// 否则下一个用例的 CompressRunning() 还是 true，会拿到"已经有一轮在执行"
	// 这个理由，于是断言"拒绝理由是备份"就变成了一个偶发失败。
	//
	// 这条等待可靠的前提是**占位在响应之前就完成了**（service.StartLogCompress）：
	// 轮询到的 false 只可能是"真的跑完了"，不可能是"还没开始"。占位若发生在响应
	// 之后，这里就会在 goroutine 醒来之前放行，然后把这个用例的 DB 拆掉——
	// 症状是一个与用例无关的 nil 指针 panic（见 TestLogCompressHandlersSpawnNoGoroutines）。
	t.Cleanup(func() {
		awaitCompressIdle(t)
		// 回收同样是后台 goroutine，而且它一旦在 models.DB/models.DBPath 被还原
		// 之后才跑，就会去读一个已经不存在的库——症状是一个与用例无关的
		// nil 指针 panic。StartReclaim 在**返回之前**就占了位，所以这里等得到。
		awaitReclaimIdle(t)
		models.DB = prevDB
		models.DBPath = prevPath
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// awaitReclaimIdle 等后台回收退出。
func awaitReclaimIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for service.ReclaimRunning() {
		if time.Now().After(deadline) {
			t.Fatal("后台回收 5 秒还没退出——有东西卡住了")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// awaitCompressIdle 等后台迁移退出。空库上它下一秒就好，超时说明真有东西卡住了。
func awaitCompressIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for service.CompressRunning() {
		if time.Now().After(deadline) {
			t.Fatal("后台迁移 5 秒还没退出——有东西卡住了")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// postJSON 跑一个 POST 端点，返回业务码与解析后的信封。
func postJSON(t *testing.T, h gin.HandlerFunc, path, body string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	h(c)

	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是 JSON（HTTP %d）：%s", w.Code, w.Body.String())
	}
	code, _ := env["code"].(float64)
	return int(code), env
}

// 后台**不许**在这两个 handler 里起——这条用源码盯住，不靠跑一遍看时序。
//
// 理由不是洁癖，是那条 2026-10-03 让 CI 变红的 nil 指针 panic：旧形状里
// `RunCompression` 自己 `go func() { service.RunLogCompress(...) }`，而占位
// （那个 CAS 标志）发生在那条 goroutine 里。于是响应已经写下 `started: true`，
// `CompressRunning()` 还可能读到 false——状态接口当场自相矛盾，前端据此把按钮
// 放回可点，并发进来的第二个请求也能透过去；测试里更难看：用例把 `models.DB`
// 还原成 nil 之后那条 goroutine 才醒来，整个包以一个与用例无关的 panic 收场。
//
// **为什么不用一条"跑起来看看"的用例**：那个窗口只有微秒级，而 Go 的异步抢占
// 会让那条 goroutine 常常在断言之前就醒过来——实测把旧形状放回去，服务层那条
// 时序断言照样是绿的。要判"占位在不在响应之前"，唯一稳的判据是**谁起的后台**：
// 后台一律走 `service.StartLogCompress` / `StartLogDecompress`。
func TestLogCompressHandlersSpawnNoGoroutines(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "log_compress.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 handler/log_compress.go 失败：%v", err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		goStmt, ok := n.(*ast.GoStmt)
		if !ok {
			return true
		}
		t.Errorf("%s：这里起了一个后台 goroutine。后台一律走 service.StartLogCompress / "+
			"StartLogDecompress——占位必须在响应写回之前完成，handler 自己起就把它推到了响应之后",
			fset.Position(goStmt.Pos()))
		return true
	})
}

func TestRunCompression_RefusesWithoutBackup(t *testing.T) {
	setupCompressHandlerDB(t)

	code, env := postJSON(t, RunCompression, "/api/logs/compression/run", `{}`)

	if code != http.StatusBadRequest {
		t.Fatalf("没备份又没确认时业务码是 %d，期望 %d（msg=%v）", code, http.StatusBadRequest, env["message"])
	}
	msg, _ := env["message"].(string)
	if !bytes.Contains([]byte(msg), []byte("备份")) {
		t.Fatalf("拒绝理由里没提到备份，操作员不知道该做什么：%q", msg)
	}
	if service.CompressRunning() {
		t.Fatal("被拒绝的请求竟然还是把迁移跑起来了")
	}
}

func TestRunCompression_RecordsForcedWhenAcknowledged(t *testing.T) {
	setupCompressHandlerDB(t)

	code, _ := postJSON(t, RunCompression, "/api/logs/compression/run",
		`{"acknowledge_no_backup":true}`)
	if code != http.StatusOK {
		t.Fatalf("带确认时业务码是 %d，期望 200", code)
	}

	// 存证必须如实：这一趟是在没有可用备份的情况下跑的。
	// 记成 manual 会让后来翻记录的人以为有备份可退。
	var row models.Config
	if err := models.DB.Where("key = ?", models.KeyLogCompressBackup).First(&row).Error; err != nil {
		t.Fatalf("没写下备份存证：%v", err)
	}
	var rec models.LogCompressBackup
	if err := json.Unmarshal([]byte(row.Value), &rec); err != nil {
		t.Fatalf("存证不是合法 JSON：%v", err)
	}
	if rec.Source != "forced" {
		t.Fatalf("存证记的是 %q，期望 %q——无备份这件事不能记成别的", rec.Source, "forced")
	}
}

func TestRunCompression_AcceptsEmptyBody(t *testing.T) {
	setupCompressHandlerDB(t)

	// 空 body 应当走到备份门（然后被拒），而不是 body 解析失败：
	// 前端点「开始迁移」时没有要传的东西，逼它拼一个 {} 是多余的约定。
	code, env := postJSON(t, RunCompression, "/api/logs/compression/run", "")
	if code != http.StatusBadRequest {
		t.Fatalf("空 body 的业务码是 %d，期望 %d", code, http.StatusBadRequest)
	}
	if msg, _ := env["message"].(string); !bytes.Contains([]byte(msg), []byte("备份")) {
		t.Fatalf("空 body 没走到备份门，而是 %q——它被当成了格式错误", msg)
	}
}

// 回收的门只有一道：一轮只能有一个。它不像迁移那样还要过备份这一类确认
// （回收不动数据、可重复、可中断），但它**全程持写锁**，所以前端那道确认
// 对话框是产品判断，不是接口约束。
func TestReclaimStorage_StartsAsynchronously(t *testing.T) {
	setupCompressHandlerDB(t)

	code, env := postJSON(t, ReclaimStorage, "/api/logs/compression/reclaim", "")
	if code != http.StatusOK {
		t.Fatalf("回收的业务码是 %d，期望 200（msg=%v）", code, env["message"])
	}
	data, _ := env["data"].(map[string]any)
	if started, _ := data["started"].(bool); !started {
		t.Fatalf("响应里没写 started：%v", env["data"])
	}
}

func TestRollbackCompression_RequiresLiteralConfirm(t *testing.T) {
	setupCompressHandlerDB(t)

	// 前缀再多也不算：必须逐字相等。回滚会把整库变大，不该被误触。
	for _, body := range []string{
		`{"full":true}`,
		`{"full":true,"confirm":"yes"}`,
		`{"full":true,"confirm":"decompress now"}`,
	} {
		code, _ := postJSON(t, RollbackCompression, "/api/logs/compression/decompress", body)
		if code == http.StatusOK {
			t.Fatalf("body %s 被放行了——回滚不该被误触", body)
		}
		if service.CompressRunning() {
			t.Fatalf("body %s 被拒了却还是把回滚跑起来了", body)
		}
	}
}

func TestRollbackCompression_RequiresFull(t *testing.T) {
	setupCompressHandlerDB(t)

	// 回滚必须从 0 重扫：帧散布在全表，从水位续跑会静默漏掉前一半。
	// 而它漏掉的方式是"跑成功"，所以这道门只能在入口拦。
	code, env := postJSON(t, RollbackCompression, "/api/logs/compression/decompress",
		`{"confirm":"decompress"}`)
	if code == http.StatusOK {
		t.Fatal("full 不为 true 的回滚被放行了")
	}
	msg, _ := env["message"].(string)
	if !bytes.Contains([]byte(msg), []byte("full")) {
		t.Fatalf("拒绝理由没说清是 full 的问题：%q", msg)
	}
}

func TestGetCompressionStatus_ReportsBothCalibers(t *testing.T) {
	setupCompressHandlerDB(t)

	// 一行明文、一行帧。状态页要能分开数出来——"还没迁的行数"就是靠 typeof
	// 判的，它错了整张卡都在说假话。
	seedRow(t, []byte(`{"a":1}`))
	seedRow(t, frameBytes())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/logs/compression", nil)
	GetCompressionStatus(c)

	var env struct {
		Code int `json:"code"`
		Data struct {
			DB struct {
				Rows        int64 `json:"rows"`
				PendingRows int64 `json:"pending_rows"`
				FramedRows  int64 `json:"framed_rows"`
			} `json:"db"`
			Backup struct {
				Source string `json:"source"`
			} `json:"backup"`
			Running bool `json:"running"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是 JSON：%s", w.Body.String())
	}
	if env.Data.DB.Rows != 2 || env.Data.DB.PendingRows != 1 || env.Data.DB.FramedRows != 1 {
		t.Fatalf("行形态数错了：总 %d / 待迁移 %d / 已迁移 %d，期望 2 / 1 / 1",
			env.Data.DB.Rows, env.Data.DB.PendingRows, env.Data.DB.FramedRows)
	}
	if env.Data.Backup.Source != "missing" {
		t.Fatalf("探测结论是 %q，期望 missing（路径上确实什么都没有）", env.Data.Backup.Source)
	}
	if env.Data.Running {
		t.Fatal("没有任何一轮在跑，却报了 running")
	}
}

// seedRow 用裸 SQL 种一行。**必须走裸 SQL**：走模型的话 BeforeCreate
// 会把传进去的帧重新压一遍（或把明文再分块），种出来的形态就不是我想验的那个。
//
// 参数必须传 `models.BodyBytes` 而**不是裸 `[]byte`**：GORM 的 `Expr.Build`
// 在遇到紧跟在 `(` 之后的切片参数时会把它**逐元素展开**成值列表
// （`VALUES (` 正好命中了这个分支），于是 7 个字节的 `{"a":1}` 变成 7 个值，
// SQLite 报一句 `N values for M columns`——那个报错完全看不出是参数类型的问题。
// `BodyBytes` 实现了 `driver.Valuer`，走的是另一个分支，只占一个 `?`。
// 生产代码里那两条裸 UPDATE 用的也是它，所以这里与生产同形。
func seedRow(t *testing.T, input []byte) {
	t.Helper()
	if err := models.DB.Exec(
		`INSERT INTO chat_ios (input, created_at, updated_at) VALUES (?, datetime('now'), datetime('now'))`,
		models.BodyBytes(input)).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}
}

// frameBytes 造一个**真的**帧，走包自己的 Marshal 而不是手拼帧头。
//
// 手拼过一次，把 codec 与 type 的偏移记反了（type 在 6，codec 在 5）。
// 那样的字节在这条用例里照样"能过"（只数 typeof，不看内容），
// 却会让后来照着它抄的人拿到一段谁也解不开的帧——用包的编解码函数就没有
// 这种"测试自己发明格式"的机会。
func frameBytes() []byte {
	payload := []byte("0123456789abcdef")
	return compress.Marshal(compress.Frame{
		Codec:   compress.CodecRaw,
		Type:    compress.TypeRowFrame,
		RawLen:  uint32(len(payload)),
		Payload: payload,
	})
}

func TestVerifyBackup_JudgesStaleBySize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db.bak")
	if err := os.WriteFile(path, make([]byte, 10), 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	// 比库小 ⇒ stale。这条判据的意义是：盖回去会丢数据，所以它不算"可用备份"。
	if got := service.VerifyBackup(path, 1024); got.Source != "stale" {
		t.Fatalf("比库小的备份被判成 %q，期望 stale", got.Source)
	}
	if got := service.VerifyBackup(path, 10); got.Source != "manual" {
		t.Fatalf("不小于库的备份被判成 %q，期望 manual", got.Source)
	}
	if got := service.VerifyBackup(filepath.Join(dir, "nope"), 10); got.Source != "missing" {
		t.Fatalf("不存在的备份被判成 %q，期望 missing", got.Source)
	}
}
