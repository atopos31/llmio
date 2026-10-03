package service

import (
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// 状态页那条读的**真机代价**，以及它把聊天写请求拖住多久。
//
// 这条测试存在的唯一理由是那个已经发生过的线上故障：状态页每 2 秒轮询一次
// `ReadDBStats`，而它当时带了一句 `sum(length(CAST(列 AS BLOB)))`——「三列此刻
// 各占多少字节」。那句话在回滚后的 7.6 GiB 库上要 **4.24 秒**（三列此时都是
// TEXT，读的就是 7 GB 载荷），而库跑在 `journal_mode=delete`（回滚日志）下：
// **读事务挡写**。于是状态页每轮询一次，并发进来的聊天请求就在写锁上等满
// 5 秒 `busy_timeout`，然后 `cannot start a transaction within a transaction`
// ——整站 500。
//
// 所以这里量两件事，缺一不可：
//
//  1. **那条读要多久。** 新旧两种口径在同一份库上各量一遍：旧口径（读载荷求和）
//     与新口径（纯 `typeof`）。只报新的快，看不出"为什么非改不可"。
//  2. **它把写请求拖住多久。** 这才是故障的度量本身。一个后台 goroutine 按
//     50 毫秒的节奏做一次真实的 UPDATE，另一个 goroutine 反复跑那个读，
//     记下写请求被拖出的最长一次。旧口径下这个数会顶到秒级、越过 busy_timeout；
//     新口径下应当停在批的一小部分。**光看读的耗时不够**——一个 80 毫秒的读
//     也挡写 80 毫秒，只是挡不死人。
//
// 它在一份**可写副本**上跑：写请求要真写真提交，否则量不到锁竞争。副本用完即删
// （7 GiB 的副本留着只是占地方，这条测试每次都要一份新的）。
//
// 默认跳过。跑法：
//
//	$env:LLMIO_REAL_DB_COPY="D:\llmio-test\work-self\db\llmio.db"
//	go test ./service/ -run RealDatabaseStatsLatency -v -timeout 30m
//
// 源库要挑**回滚之后**那一份：三列都是明文，7 GB 全是载荷，旧口径才重现得出
// 4 秒。已经迁完的库上旧口径也很快（帧的长度在记录头里，不读载荷），
// 拿它跑只会得到一句"两者差不多"，然后得出错误结论。
func TestDBStats_RealDatabaseStatsLatency(t *testing.T) {
	srcPath := realDBForMigration(t)

	work := copyForMigrationNamed(t, srcPath, "work-p7", "stats.db")
	t.Cleanup(func() {
		_ = os.Remove(work)
		_ = os.Remove(work + "-journal")
	})

	if mode := journalMode(t, srcPath); mode != "delete" {
		t.Fatalf("源库的 journal_mode 是 %q，不是 delete——"+
			"回滚日志那套「读挡写」的前提不成立，这条测试量不出东西", mode)
	}

	writer := openSQL(t, work, 1)
	defer writer.Close()
	if _, err := writer.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		t.Fatalf("设 busy_timeout 失败：%v", err)
	}
	reader := openSQL(t, work, 1)
	defer reader.Close()

	victim := lastRowID(t, reader)
	t.Logf("")
	t.Logf("源库 %s（%s）", srcPath, humanBytesReal(fileSize(t, srcPath)))
	t.Logf("副本 %s（%s）｜journal_mode=delete ｜busy_timeout=5000ms（驱动默认，与生产一致）",
		work, humanBytesReal(fileSize(t, work)))
	t.Logf("锁冲突用一个真实 UPDATE 制造：chat_ios.id=%d", victim)

	// ── 一、两句读各自的耗时 ──
	//
	// 旧口径那句是**原样抄回来的**——就是闯祸那一版 DBStats 里的 `input_column_bytes`
	// 与 `output_column_bytes` 合起来的那句。三列一起求和，一个字节都不省。
	const heavyQ = `SELECT COALESCE(sum(length(CAST(input AS BLOB))` +
		` + length(CAST(of_string AS BLOB))` +
		` + length(CAST(of_string_array AS BLOB))), 0) FROM chat_ios WHERE deleted_at IS NULL`
	lightQ := fmt.Sprintf(
		`SELECT count(*) FROM chat_ios WHERE deleted_at IS NULL AND %s`, plaintextFilter)

	heavy, heavyOK := timeQuery(t, reader, heavyQ, 3)
	light, lightOK := timeQuery(t, reader, lightQ, 20)
	t.Logf("")
	t.Logf("【读】旧口径 length(CAST(...)) 求和：%s，成功 %d/3（%.2f 秒/次）",
		heavy.Round(time.Millisecond), heavyOK, heavy.Seconds()/3)
	t.Logf("【读】新口径 typeof 并集计数：%s，成功 %d/20（%.0f 毫秒/次）",
		light.Round(time.Millisecond), lightOK, light.Seconds()*1000/20)
	if heavyOK > 0 && lightOK > 0 {
		t.Logf("【读】快了约 %.0f 倍",
			float64(heavy/time.Duration(heavyOK))/float64(max(light/time.Duration(lightOK), time.Millisecond)))
	}

	// ── 二、它把写请求拖住多久 ──
	//
	// 三个窗口，写请求的节奏完全一样，只有读在变：空载 → 旧口径 → 新口径。
	// 只报"旧口径拖了 4 秒"不够，得看得出那 4 秒**确实是读造成的**。
	//
	// 先热身。刚复制出来的 7 GB 文件全是脏页，第一次写要等内核把它们刷下去——
	// 不热身的话"空载"那一档会量出一个两秒的假停顿，正回执行个"空载比新口径
	// 读还慢"的怪结论。热身的数**不计**。
	warmup := runWriter(t, writer, victim, 3*time.Second, nil)
	t.Logf("")
	t.Logf("【写】热身 %d 次 UPDATE（不计）：%s", warmup.count, warmup.stall())

	baseline := runWriter(t, writer, victim, 2*time.Second, nil)
	t.Logf("【写】空载 2 秒：%s", baseline.stall())

	heavyWin := runWriter(t, writer, victim, 30*time.Second, func(done <-chan struct{}) {
		for i := 0; i < 3; i++ {
			if isClosed(done) {
				return
			}
			if _, err := reader.Exec(heavyQ); err != nil {
				t.Logf("【写】旧口径读失败（撞上写锁）：%v", err)
			}
		}
	})
	t.Logf("【写】旧口径读 3 次期间：%s", heavyWin.stall())

	lightWin := runWriter(t, writer, victim, 30*time.Second, func(done <-chan struct{}) {
		for i := 0; i < 200; i++ {
			if isClosed(done) {
				return
			}
			resetDBStatsCache()
			if _, err := reader.Exec(lightQ); err != nil {
				t.Logf("【写】新口径读失败（撞上写锁）：%v", err)
			}
		}
	})
	t.Logf("【写】新口径读 200 次期间：%s", lightWin.stall())

	// ── 断言 ──
	//
	// 三条，都是"这套东西能不能活"的下限。具体毫秒数不钉——那是磁盘性能，
	// 换一台机器就变；钉的是**两者之间的关系**，那个关系不随机器走。
	//
	//  1. 新口径下写请求一次都不许失败。失败就是等满 busy_timeout 报
	//     SQLITE_BUSY，就是真机上那个 500。
	//  2. 新口径下最长等待也不许接近 busy_timeout。这一条防的是"还没失败但已经
	//     快到了"——真机上的负载比这里密。
	//  3. 旧口径必须明显更糟，差一个数量级。这不是形式主义：真要是哪天有人把
	//     length() 加回来，而 (1)(2) 恰好没红（这一轮写请求少、没撞上），它会红。
	if lightWin.failed > 0 {
		t.Fatalf("新口径下 %d 次写请求等满 busy_timeout 失败（最长 %s）——"+
			"生产上这就是那个 500", lightWin.failed, lightWin.worstAll)
	}
	if lightWin.worstAll >= sqliteBusyTimeout {
		t.Fatalf("新口径下写请求被拖了 %s，顶到 busy_timeout（%s）——生产上这就是一次 500",
			lightWin.worstAll, sqliteBusyTimeout)
	}
	if heavyWin.worstAll < 10*max(lightWin.worstAll, time.Millisecond) {
		t.Fatalf("旧口径最长等待 %s、新口径 %s，差不到一个数量级——"+
			"这台机器上量不出「读载荷挡写」这件事，本次结论不作数",
			heavyWin.worstAll, lightWin.worstAll)
	}
}

// sqliteBusyTimeout 是驱动默认的写锁耐心。生产上超过它，写请求就报
// `SQLITE_BUSY` 出去了（glebarez/sqlite 的默认值，见 db_stats.go 的说明）。
const sqliteBusyTimeout = 5 * time.Second

// verdictStall 给一次最长停顿配一句人话——回执里要一眼看出它是不是 500 那一档。
func verdictStall(d time.Duration) string {
	if d >= sqliteBusyTimeout {
		return "→ **越过 busy_timeout，生产上这一次写请求直接 500**"
	}
	if d >= time.Second {
		return "→ 写请求肉眼可见地卡住了（生产上再挤一点就越线）"
	}
	return ""
}

// ── 写请求的节奏 ──────────────────────────────────────────────────────────

type writeWindow struct {
	count    int
	failed   int
	worst    time.Duration // 成功那几次里最长的一回
	worstAll time.Duration // 含失败——失败的那次是**等满 busy_timeout** 才放弃的
}

// note 记一笔。分开记成功与失败：撞满 busy_timeout 的那一次耗时 5 秒，
// 而它是**失败**，混进"最长"里会让回执读成"写成功了但很慢"——恰恰相反，
// 生产上那一次就是 500。
func (w *writeWindow) note(took time.Duration, err error) {
	w.worstAll = max(w.worstAll, took)
	if err == nil {
		w.count++
		w.worst = max(w.worst, took)
		return
	}
	w.failed++
}

// stall 报这一档写请求的实情，供回执与判据共用。
func (w writeWindow) stall() string {
	if w.failed > 0 {
		return fmt.Sprintf("成功 %d 次 / **失败 %d 次**，最长等待 %s%s",
			w.count, w.failed, w.worstAll.Round(time.Millisecond), verdictStall(w.worstAll))
	}
	return fmt.Sprintf("成功 %d 次 / 失败 0 次，最长等待 %s%s",
		w.count, w.worstAll.Round(time.Millisecond), verdictStall(w.worstAll))
}

// runWriter 按 50 毫秒的节奏真写一段，同时跑 payload（nil 表示纯空载）。
//
// 窗口在两边都收工时结束：payload 跑完，或硬上限 d 到点。**不能让写循环按固定
// 时长先停**——旧口径那三次读要 12 秒，写循环要是 3 秒就收工，后面 9 秒的锁竞争
// 一次都量不到，而"被挡住的那一笔"恰恰落在那里。
//
// 写的是 `UPDATE chat_ios SET updated_at = ... WHERE id = ?`，一次真实的写事务：
// 取得 RESERVED，提交时升 EXCLUSIVE。有人在读（持 SHARED）时，这个升级就得等，
// 等多久正好是"读事务还剩多久"——就是生产上拖垮整站的那条路径，同一把锁。
func runWriter(t *testing.T, db *sql.DB, id uint, d time.Duration, payload func(<-chan struct{})) writeWindow {
	t.Helper()
	var (
		mu   sync.Mutex
		win  writeWindow
		wg   sync.WaitGroup
		done = make(chan struct{})
	)
	hardDeadline := time.Now().Add(d)

	if payload != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(done)
			payload(done)
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for !isClosed(done) && time.Now().Before(hardDeadline) {
			started := time.Now()
			_, err := db.Exec(
				`UPDATE chat_ios SET updated_at = ? WHERE id = ?`,
				time.Now().Format("2006-01-02 15:04:05"), id)
			took := time.Since(started)
			mu.Lock()
			win.note(took, err)
			mu.Unlock()
			select {
			case <-done:
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()

	// payload 为 nil 时 done 永不关闭，写循环就写满整个硬上限——空载那一档。
	wg.Wait()
	return win
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// ── 连接与度量 ────────────────────────────────────────────────────────────

// openSQL 打开一条**独占的连接**。写连接必须只有一条：多条会让同一时刻有两个
// 写事务排队，量出来的"写请求被拖住多久"就变成"排队排了多久"。
func openSQL(t *testing.T, path string, conns int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
	db.SetConnMaxLifetime(0)
	return db
}

func journalMode(t *testing.T, path string) string {
	t.Helper()
	db := openSQL(t, path, 1)
	defer db.Close()
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("读 journal_mode 失败：%v", err)
	}
	return mode
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("量文件大小失败：%v", err)
	}
	return info.Size()
}

func lastRowID(t *testing.T, db *sql.DB) uint {
	t.Helper()
	var id uint
	if err := db.QueryRow(`SELECT max(id) FROM chat_ios`).Scan(&id); err != nil {
		t.Fatalf("取一个可写的行 id 失败：%v", err)
	}
	if id == 0 {
		t.Fatal("库里一行都没有")
	}
	return id
}

// timeQuery 跑 n 次同一句读，返回合计耗时与成功次数。
//
// 失败要计数而不是 fatal：写连接随时可能持着 RESERVED，读在那一瞬间会拿到
// `SQLITE_BUSY`——那是这条测试**要量**的现象之一，不是测试坏了。
func timeQuery(t *testing.T, db *sql.DB, q string, n int) (time.Duration, int) {
	t.Helper()
	var ok int
	started := time.Now()
	for i := 0; i < n; i++ {
		var v int64
		if err := db.QueryRow(q).Scan(&v); err != nil {
			continue
		}
		ok++
	}
	return time.Since(started), ok
}
