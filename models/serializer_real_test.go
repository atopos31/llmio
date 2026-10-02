package models

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 真机验收：拿一份**生产库副本**跑，验的是手写种子行验不出来的东西——
// 真实数据里的每一个字节。
//
// 默认跳过（要一份 7 GB 的库）。跑法：
//
//	$env:LLMIO_REAL_DB_COPY="D:\llmio-test\work-phase2\real.db"
//	go test ./models/ -run RealDatabase -v -timeout 60m
//
// 这两个测试都会**写**那个库（AutoMigrate、兼容回填、建临时表），
// 所以只许指向副本。下面两道硬拒绝不是形式主义：源库是唯一一份原始数据。
func realDBCopy(t *testing.T) string {
	t.Helper()
	path := os.Getenv("LLMIO_REAL_DB_COPY")
	if path == "" {
		t.Skip("未设置 LLMIO_REAL_DB_COPY，跳过真机验收")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("路径不合法：%v", err)
	}
	lower := strings.ToLower(abs)
	if strings.HasPrefix(lower, `e:\projects\`) {
		t.Fatalf("拒绝在源目录上跑：%s——那里是原始数据，只能指向副本", abs)
	}
	base := strings.ToLower(filepath.Base(abs))
	if strings.HasPrefix(base, "llmio-repaired") {
		t.Fatalf("拒绝在只读参考副本上跑：%s", base)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("副本不存在：%v", err)
	}
	return abs
}

// 真机验收的第 0 步：先确认这份副本本身是完好的。
//
// 不是形式主义。库里只要有一个坏页，后面所有"逐行核对"都会以
// "database disk image is malformed" 收场，而那个报错完全看不出
// 是"库坏了"还是"压缩代码读错了"——这两件事的处置方式天差地别。
func TestCompressSerializer_RealDatabaseIntegrity(t *testing.T) {
	path := realDBCopy(t)

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开副本失败：%v", err)
	}
	closeOnCleanup(t, db)

	start := time.Now()
	var results []string
	if err := db.Raw(`PRAGMA integrity_check`).Scan(&results).Error; err != nil {
		// 库坏到连检查都跑不完。这台机器上真发生过：scp 传坏的副本，
		// 尾部一批页读不出来，症状是任意查询随机报 "database disk image is malformed"。
		t.Fatalf("副本自身已损坏，integrity_check 都跑不完（%v）——先修库，再谈压缩", err)
	}
	t.Logf("integrity_check 用时 %.1f s，返回 %d 行", time.Since(start).Seconds(), len(results))
	if len(results) == 1 && strings.EqualFold(results[0], "ok") {
		return
	}
	for i, r := range results {
		if i >= 10 {
			t.Logf("……另有 %d 条", len(results)-10)
			break
		}
		t.Logf("损坏项：%s", r)
	}
	t.Fatalf("副本自身已损坏（%d 项）——先修库，再谈压缩；这份副本上的结论都不作数", len(results))
}

// 第一件事：真库副本上跑一次完整启动，表结构必须一字不变。
//
// C1 说"不许改列类型"，理由在这台机器上已经坐实过一次：把 of_string 的
// 类型从 text 改成 varchar(255)，AutoMigrate 立刻把整张 chat_ios 重建了
// （建表语句从 `chat_ios` 变成 "chat_ios" ，那是重建的指纹）。
// 在 7 GB 的表上，那就是一次灾难。这里对真库再验一遍同样的断言。
func TestCompressSerializer_RealDatabaseSchemaUntouched(t *testing.T) {
	path := realDBCopy(t)

	before := chatIOsShape(t, path)
	statBefore, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读文件信息失败：%v", err)
	}

	start := time.Now()
	Init(context.Background(), path)
	elapsed := time.Since(start)
	closeOnCleanup(t, DB)

	t.Logf("启动耗时 %.2f s（%.1f MiB 的库）", elapsed.Seconds(), float64(statBefore.Size())/(1<<20))

	after := chatIOsShape(t, path)
	if before != after {
		t.Fatalf("AutoMigrate 改动了 chat_ios：\n之前：\n%s\n之后：\n%s", before, after)
	}
	if elapsed > 10*time.Second {
		t.Errorf("启动耗时 %.2f s 超过 10 s 的门槛——启动路径上不该有重活", elapsed.Seconds())
	}
}

// 第二件事：逐行核对。库里每一行的三个 body 列，用一个原始字节的视角
// 和模型层的视角各读一遍，两边必须对得上。
//
// 这是"历史明文行照样读得回来"在真数据上的唯一证明：手写种子里
// 只有两行，而这里有一万多行的真实内容（流式数组、非法 UTF-8、内嵌 NUL 都可能有）。
func TestCompressSerializer_RealDatabaseEveryRowReadsBack(t *testing.T) {
	path := realDBCopy(t)

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开副本失败：%v", err)
	}
	closeOnCleanup(t, db)
	ctx := context.Background()

	var total int64
	if err := db.Raw(`SELECT count(*) FROM chat_ios`).Row().Scan(&total); err != nil {
		t.Fatalf("统计行数失败：%v", err)
	}
	if total == 0 {
		t.Fatal("副本里没有 chat_ios 行，这份副本不对劲")
	}

	const batch = 64
	var (
		read      int64
		byteDiff  int64
		lastID    uint
		startedAt = time.Now()
	)
	for {
		// 列名必须显式写出来：Scan 是按名字映射的，OfStringArr 默认会映成
		// of_string_arr，那一列就整列读成 nil —— 测试会因此悄悄失去意义。
		type rawRow struct {
			ID          uint   `gorm:"column:id"`
			Input       []byte `gorm:"column:input"`
			OfString    []byte `gorm:"column:of_string"`
			OfStringArr []byte `gorm:"column:of_string_array"`
		}
		var raws []rawRow
		if err := db.Raw(`SELECT id, input, of_string, of_string_array
			FROM chat_ios WHERE id > ? ORDER BY id LIMIT ?`, lastID, batch).Scan(&raws).Error; err != nil {
			t.Fatalf("读原始行失败（lastID=%d）：%v", lastID, err)
		}
		if len(raws) == 0 {
			break
		}
		ids := make([]uint, 0, len(raws))
		for _, r := range raws {
			ids = append(ids, r.ID)
		}
		lastID = ids[len(ids)-1]

		// 用模型读同一批：这条路径会走 serializer 的 Scan，也就是生产读路径
		rows, err := gorm.G[ChatIO](db).Where("id IN ?", ids).Find(ctx)
		if err != nil {
			t.Fatalf("模型读失败（lastID=%d）：%v", lastID, err)
		}
		byID := make(map[uint]ChatIO, len(rows))
		for _, r := range rows {
			byID[r.ID] = r
		}

		for _, raw := range raws {
			got, ok := byID[raw.ID]
			if !ok {
				t.Fatalf("行 %d 模型读不出来", raw.ID)
			}
			read++

			// input 在 Phase 2 还是明文列：必须逐字节相等
			if string(raw.Input) != got.Input {
				t.Fatalf("行 %d 的 input 变了（原始 %d 字节 → 读回 %d 字节）",
					raw.ID, len(raw.Input), len(got.Input))
			}
			// of_string 同理（可能为 NULL → 空串）
			if string(raw.OfString) != got.OfString {
				t.Fatalf("行 %d 的 of_string 变了（原始 %d 字节 → 读回 %d 字节）",
					raw.ID, len(raw.OfString), len(got.OfString))
			}
			// of_string_array 比语义而不是比字节：历史文本可能带多余空白，
			// json.Marshal 的形态不保证一致，但内容必须一模一样。
			var want []string
			if len(bytes.TrimSpace(raw.OfStringArr)) > 0 {
				if err := json.Unmarshal(raw.OfStringArr, &want); err != nil {
					t.Fatalf("行 %d 的 of_string_array 不是合法 JSON 数组：%v", raw.ID, err)
				}
			}
			gotJSON, err := json.Marshal(got.OfStringArray)
			if err != nil {
				t.Fatalf("行 %d 序列化失败：%v", raw.ID, err)
			}
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("行 %d 的 of_string_array 变了：%d 项 → %d 项",
					raw.ID, len(want), len(got.OfStringArray))
			}
			if !bytes.Equal(bytes.TrimSpace(raw.OfStringArr), gotJSON) {
				byteDiff++
			}
		}
		if read%2048 < batch {
			t.Logf("已核对 %d/%d 行，用时 %.1f s", read, total, time.Since(startedAt).Seconds())
		}
	}
	t.Logf("逐行核对完成：%d 行，字节形态有差异的 %d 行（语义全部一致），用时 %.1f s",
		read, byteDiff, time.Since(startedAt).Seconds())
}

// 第三件事：拿真数据里的大行做一次完整的"压缩落库 → 读回"往返，
// 并量出这两列实际能压到多少。
//
// 用手写数据量压缩率是不算数的——真实响应体的重复度是它自己的性质。
func TestCompressSerializer_RealDatabasePayloadsRoundTrip(t *testing.T) {
	path := realDBCopy(t)

	src, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开副本失败：%v", err)
	}
	closeOnCleanup(t, src)

	// 取真库里最大的若干个响应体，它们才是空间所在
	type sample struct {
		ID          uint   `gorm:"column:id"`
		OfString    []byte `gorm:"column:of_string"`
		OfStringArr []byte `gorm:"column:of_string_array"`
	}
	var samples []sample
	if err := src.Raw(`SELECT id, of_string, of_string_array FROM chat_ios
		ORDER BY length(of_string_array) DESC LIMIT 200`).Scan(&samples).Error; err != nil {
		t.Fatalf("取样本失败：%v", err)
	}

	var rawBytes, storedBytes int64
	ctx := context.Background()
	dst := openCompressDB(t)

	for _, s := range samples {
		if len(bytes.TrimSpace(s.OfStringArr)) == 0 {
			continue
		}
		var chunks []string
		if err := json.Unmarshal(s.OfStringArr, &chunks); err != nil {
			t.Fatalf("行 %d 不是合法 JSON 数组：%v", s.ID, err)
		}
		io := ChatIO{LogId: s.ID, Input: "x", OutputUnion: OutputUnion{OfStringArray: chunks}}
		if err := gorm.G[ChatIO](dst).Create(ctx, &io); err != nil {
			t.Fatalf("写入失败：%v", err)
		}
		got, err := gorm.G[ChatIO](dst).Where("id = ?", io.ID).First(ctx)
		if err != nil {
			t.Fatalf("读回失败：%v", err)
		}
		gotJSON, _ := json.Marshal(got.OfStringArray)
		wantJSON, _ := json.Marshal(chunks)
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Fatalf("行 %d 往返后内容变了", s.ID)
		}
		if gotType := columnType(t, dst, io.ID, "of_string_array"); gotType != "blob" {
			t.Fatalf("行 %d 没存成帧，typeof=%s", s.ID, gotType)
		}
		rawBytes += int64(len(s.OfStringArr))
		storedBytes += int64(len(rawColumn(t, dst, io.ID, "of_string_array")))
	}

	if rawBytes == 0 {
		t.Fatal("没取到样本")
	}
	t.Logf("真数据样本 %d 行：明文 %.2f MiB → 帧 %.2f MiB（%.1fx）",
		len(samples), float64(rawBytes)/(1<<20), float64(storedBytes)/(1<<20),
		float64(rawBytes)/float64(storedBytes))
}
