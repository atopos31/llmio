package models

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 本文件是**语料抽取工具**，不是测试。它把一份真实生产库副本里的若干行
// 脱敏后重新分块，产出一份可以入库的最小语料。
//
//	$env:LLMIO_COMPRESSED_CORPUS="E:\tmp\bak-probe.db"
//	$env:LLMIO_CORPUS_OUT="E:\Projects\llmio\models\testdata\cdc_corpus.db"
//	go test ./models/ -run ExtractSanitizedCorpus -v -timeout 60m
//
// 为什么必须脱敏：真实库里是用户 prompt（解压即明文），而这个仓库是公开的。
//
// 脱敏怎么做，以及它的**信任边界**：把明文的每个字节映射到 'a'..'z'。
// 这一步保住了长度，却丢掉了字节分布的熵差，所以产出的块边界必然与真实库
// 不同——**它不是真实分块产物的复刻**。它能验的仍然是最要紧的那一层：
// 这份库是"某一版代码分块出来的"，冻结之后，谁再改帧头、codec 或引用编码，
// 读它就会失败。它验不了"真实数据上的长度分布"，那个只有完整副本能验，
// 见 compressed_corpus_test.go（走环境变量，不入库）。
func TestExtractSanitizedCorpus(t *testing.T) {
	src := os.Getenv("LLMIO_COMPRESSED_CORPUS")
	dst := os.Getenv("LLMIO_CORPUS_OUT")
	if src == "" || dst == "" {
		t.Skip("需要 LLMIO_COMPRESSED_CORPUS（源副本）与 LLMIO_CORPUS_OUT（产出路径）")
	}
	if !strings.Contains(strings.ToLower(filepath.ToSlash(dst)), "/testdata/") {
		t.Fatalf("产出必须落在 testdata 下（会被提交），得到 %s", dst)
	}

	ctx := context.Background()
	rows := readSanitizedRows(t, src, 60)
	if len(rows) == 0 {
		t.Fatal("源库里没抽到行")
	}

	// 建目标库并走**生产写路径**写入：BeforeCreate → PackBodies → 块表，
	// 与线上一个新请求的落库形态完全一致。这份产物才是"某个版本的代码
	// 会写出来什么"的实物证据。
	_ = os.Remove(dst)
	prev, prevPath := DB, DBPath
	Init(ctx, dst)
	t.Cleanup(func() {
		if DB != nil {
			if sqlDB, err := DB.DB(); err == nil {
				_ = sqlDB.Close()
			}
		}
		DB, DBPath = prev, prevPath
	})

	manifest := corpusManifest{Rows: map[int64]string{}}
	for i, plain := range rows {
		// LogId 重新编号：原 log_id 连着真实请求，没必要带进来。
		io := ChatIO{LogId: uint(i + 1), Input: BodyBytes(plain)}
		if err := gorm.G[ChatIO](DB).Create(ctx, &io); err != nil {
			t.Fatalf("写入第 %d 行失败：%v", i+1, err)
		}
		manifest.Rows[int64(io.ID)] = bodyFingerprint(plain)
	}

	// 压一压：AutoMigrate 建的表与刚写的页会留下空洞，VACUUM 之后入库体积才是真的。
	if err := DB.Exec("VACUUM").Error; err != nil {
		t.Fatalf("VACUUM 失败：%v", err)
	}

	var (
		nBlocks, blockBytes int64
		nGroups, groupBytes int64
		nRows               int64
	)
	if err := DB.Raw(`SELECT count(*), COALESCE(sum(size),0) FROM blocks`).
		Row().Scan(&nBlocks, &blockBytes); err != nil {
		t.Fatalf("统计块失败：%v", err)
	}
	if err := DB.Raw(`SELECT count(*), COALESCE(sum(length(data)),0) FROM block_groups`).
		Row().Scan(&nGroups, &groupBytes); err != nil {
		t.Fatalf("统计组失败：%v", err)
	}
	if err := DB.Raw(`SELECT count(*) FROM chat_ios`).Row().Scan(&nRows); err != nil {
		t.Fatalf("统计行失败：%v", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("读产出文件失败：%v", err)
	}

	// manifest 与语料同目录、文件名按约定，测试侧据此找。
	mraw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("序列化 manifest 失败：%v", err)
	}
	if err := os.WriteFile(dst+".manifest.json", mraw, 0o644); err != nil {
		t.Fatalf("写 manifest 失败：%v", err)
	}

	t.Logf("产出 %s", dst)
	t.Logf("行 %d｜块 %d（内容 %s）｜组 %d（%s）｜文件 %s",
		nRows, nBlocks, humanBytes(blockBytes), nGroups, humanBytes(groupBytes),
		humanBytes(fi.Size()))
	if nBlocks == 0 {
		t.Fatal("一个块都没产出：脱敏后的内容可能太小或不够多样，块表路径没被覆盖")
	}
}

// readSanitizedRows 按**明文长度**分档抽 n 行，脱敏后返回。
//
// 为什么不能按 `length(input)` 排序：库里这一列是帧，引用帧只有几十到几百
// 字节，与它代表的明文长度毫无关系。按帧长分档会抽出一堆"帧很小、内容很小"
// 的行，块表那条路就覆盖不到。所以先均匀抽一批候选、全部解压、按真实明文
// 长度排序，再从排序结果取档位。
//
// 候选上限 3000：全解 69,000 行要 40 秒，而长度分布抽 3000 行已经足够准。
func readSanitizedRows(t *testing.T, src string, n int) [][]byte {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(src+"?_pragma=query_only(1)"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开源副本失败：%v", err)
	}
	closeOnCleanup(t, db)
	// 组缓存只按组 id 索引，换库必须作废。
	blockStore.Reset()
	ctx := context.Background()

	var ids []int64
	if err := db.Raw(`SELECT id FROM chat_ios
		WHERE input IS NOT NULL AND length(input) > 0 ORDER BY id`).Scan(&ids).Error; err != nil {
		t.Fatalf("读 id 失败：%v", err)
	}
	if len(ids) == 0 {
		return nil
	}

	const maxProbe = 3000
	step := len(ids) / maxProbe
	if step < 1 {
		step = 1
	}
	type plainRow struct {
		plain []byte
	}
	probed := make([]plainRow, 0, maxProbe)
	for i := 0; i < len(ids); i += step {
		var raw []byte
		if err := db.Raw(`SELECT input FROM chat_ios WHERE id = ?`, ids[i]).Row().Scan(&raw); err != nil {
			t.Fatalf("读行 %d 失败：%v", ids[i], err)
		}
		plain, err := UnpackBody(ctx, db, raw)
		if err != nil {
			t.Fatalf("行 %d 解不开：%v", ids[i], err)
		}
		if len(plain) == 0 {
			continue
		}
		probed = append(probed, plainRow{plain: plain})
	}
	if len(probed) == 0 {
		return nil
	}
	sort.Slice(probed, func(i, j int) bool { return len(probed[i].plain) < len(probed[j].plain) })

	out := make([][]byte, 0, n)
	for i := 0; i < n && i < len(probed); i++ {
		p := probed[i*len(probed)/n].plain
		out = append(out, sanitize(p))
		t.Logf("第 %2d 档：明文 %s", i+1, humanBytes(int64(len(p))))
	}
	return out
}

// sanitize 把明文的每个字节映射进 'a'..'z'。
//
// 为什么是"映射"而不是"随机填充"：随机字节压不动，PackBodies 会把它们
// 退回明文存储（压完比原文大），块表路径就整条走不到——那样产出的语料
// 验不了它该验的东西。映射保留了长度，又让内容高度可压。
//
// 确定性也是必须的：同一份源库重跑必须得到同一份语料，否则 manifest
// 每次都变，CI 里就成了噪声源。
func sanitize(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = 'a' + c%26
	}
	return out
}
