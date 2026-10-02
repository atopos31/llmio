package service

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/pkg/compress"
	"gorm.io/gorm"
)

// 阶段 4 的真机验收：在一份**生产库副本**上做一次真正的**原地迁移**。
//
// 它和阶段 3 那条真机测试是两件事，别混。阶段 3 把 12,483 行重新写进一个
// **新库**，量的是"生产写路径对不对、代价多大"——它刻意绕开了"迁完之后库
// 真的变小了吗"这个问题，因为它压根没在原来的库上动过一行。那个问题留在这里。
//
// 这条测试要回答四件事：
//
//  1. **原地迁移之后，每一行的字节都还在。** 走生产读路径（AfterFind → 按引用
//     还原）逐行读回来，与源库的原字节比对。这是地基。
//  2. **库真的变小了。** 迁移只把页还进 freelist；文件要缩下去得 VACUUM。
//     两个数都报——只报前者会得出"迁移白做了"的错论，只报后者会掩盖
//     "不 VACUUM 就一字节都不缩"这个事实。
//  3. **迁移耗时**，以及**线上打的组有多大**。后者决定这批数据最终停在
//     62x 还是 94x：阶段 3 量出线上逐行写入的组平均只有 7.70 KiB，
//     而迁移是**批量**打包的，这两个数必须能对上。
//  4. **回滚闭环**：decompress 整库之后，与原始字节**再次**逐行相等。
//     这是 §四.7，也是 L2 降级的唯一实现。它顺带是对整个压缩链路的一次
//     全库往返验证——比任何抽样都硬。
//
// 默认跳过（要一份 7 GB 的库，跑一轮十几分钟）。跑法：
//
//	$env:LLMIO_REAL_DB_COPY="D:\llmio-test\work-phase3\real.db"
//	go test ./service/ -run RealDatabaseMigration -v -timeout 180m
//
// 源库全程只读（`PRAGMA query_only`），测试在它旁边复制一份可写的工作库再动。
func TestLogCompress_RealDatabaseInPlaceMigration(t *testing.T) {
	srcPath := realDBForMigration(t)
	ctx := context.Background()

	src := openReadOnlySQL(t, srcPath)
	defer src.Close()
	srcRows := assertSourceIntegrity(t, src)

	work := copyForMigration(t, srcPath)
	models.Init(ctx, work)
	t.Cleanup(func() {
		if sqlDB, err := models.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
		compressInFlight.Store(false)
		compressPauseRequested.Store(false)
	})

	before := measureRealDB(t, ctx, work)
	t.Logf("")
	t.Logf("【迁移前】%d 行 / input 列 %s ｜ 块表 %d 行 / 组 %d 个 ｜ 文件 %s",
		before.rows, humanBytesReal(before.colBytes), before.blocks, before.groups,
		humanBytesReal(before.fileSize))
	if before.blobRows != 0 {
		t.Fatalf("源库里已经有 %d 行是 BLOB——这份副本跑过压缩，不是原始生产库", before.blobRows)
	}
	if before.rows != srcRows {
		t.Fatalf("工作库 %d 行、源库 %d 行，复制没拷全", before.rows, srcRows)
	}

	// ── 迁移 ──
	started := time.Now()
	state, err := RunLogCompress(ctx, false)
	if err != nil {
		t.Fatalf("原地迁移失败：%v", err)
	}
	elapsed := time.Since(started)
	if state.Status != compressDone {
		t.Fatalf("迁移结束后状态是 %q，期望 %q（%s）", state.Status, compressDone, state.LastError)
	}
	t.Logf("【迁移】%s ｜ 扫 %d 行、改 %d 行、跳过 %d 行 ｜ 行内字节 %s → %s（%.2fx）",
		elapsed.Round(time.Millisecond), state.Scanned, state.Packed, state.Skipped,
		humanBytesReal(state.BytesBefore), humanBytesReal(state.BytesAfter),
		float64(state.BytesBefore)/float64(max(state.BytesAfter, 1)))
	if state.Scanned != before.rows {
		t.Fatalf("只扫了 %d 行，库里有 %d 行——有行被漏掉了", state.Scanned, before.rows)
	}

	// ── 逐行核对（走生产读路径）──
	n, checked := verifyAgainstSource(t, ctx, src)
	t.Logf("【核对】%d 行逐字节还原一致，共 %s", n, humanBytesReal(checked))

	after := measureRealDB(t, ctx, work)
	reportCapacity(t, before, after, state)

	// ── 文件真的小了吗：freelist 与 VACUUM ──
	vacuumRealDB(t, ctx, work, before.fileSize)
	vacuumed := measureRealDB(t, ctx, work)
	t.Logf("【文件】VACUUM 后 %s（迁移前 %s，省 %s / %.1f%%）",
		humanBytesReal(vacuumed.fileSize), humanBytesReal(before.fileSize),
		humanBytesReal(before.fileSize-vacuumed.fileSize),
		100*float64(before.fileSize-vacuumed.fileSize)/float64(before.fileSize))

	// ── 回滚闭环 ──
	rollStart := time.Now()
	rollState, err := RunLogDecompress(ctx, true)
	if err != nil {
		t.Fatalf("回滚失败：%v", err)
	}
	if rollState.Status != compressDone {
		t.Fatalf("回滚后状态是 %q（%s）", rollState.Status, rollState.LastError)
	}
	// 回滚要还原的是**全部 BLOB 行**：走块表的引用帧**加上**逐行帧。
	// `after.blobRows` 在 measureRealDB 里已经把逐行帧减掉了（它要单独报那个数），
	// 所以这里必须加回来——拿它直接比会漏掉所有逐行帧。
	framed := after.blobRows + after.rowFrameRows
	if rollState.Packed != framed {
		t.Fatalf("只还原了 %d 行，库里本来有 %d 行是帧（引用帧 %d + 逐行帧 %d）",
			rollState.Packed, framed, after.blobRows, after.rowFrameRows)
	}
	n2, checked2 := verifyAgainstSource(t, ctx, src)
	t.Logf("【回滚】%s ｜ 还原 %d 行 ｜ %d 行逐字节还原一致，共 %s",
		time.Since(rollStart).Round(time.Millisecond), rollState.Packed, n2, humanBytesReal(checked2))

	restored := measureRealDB(t, ctx, work)
	if restored.blobRows != 0 {
		t.Fatalf("回滚之后还有 %d 行是 BLOB", restored.blobRows)
	}
	if restored.colBytes != before.colBytes {
		t.Fatalf("回滚后 input 列 %s，原始是 %s——往返不闭合",
			humanBytesReal(restored.colBytes), humanBytesReal(before.colBytes))
	}
}

// ── 容量报告 ──────────────────────────────────────────────────────────────

// rpBlockRowBytes 是块表每行的开销估算，沿用阶段 0 的常数。
// 是估的：它翻一倍也只多几 MiB，不影响任何结论。
const rpBlockRowBytes = 40

// phase3PackedCeiling 是阶段 3 离线量出的压实上界：把整库唯一内容按 256 KiB
// 重新打组，组表是 56.53 MiB。它是"不按批、想怎么打就怎么打"的成绩，
// 所以**只会优于**迁移（迁移每批的最后一组必然是半组）。
const phase3PackedCeiling = 59_275_617

// reportCapacity 报两个**必须分开说**的口径，以及组打得够不够大。
func reportCapacity(t *testing.T, before, after dbMeasure, state *models.LogCompressState) {
	t.Helper()
	meta := after.blocks * rpBlockRowBytes
	storedTotal := after.colBytes + after.groupBytes + meta

	t.Logf("")
	t.Logf("【迁移后】行形态：块表 %d 行 ｜ 逐行帧 %d 行 ｜ 明文 %d 行 ｜ 空 %d 行",
		after.blobRows, after.rowFrameRows, after.textRows, after.nullRows)
	t.Logf("【迁移后】唯一块 %d 个 / %s，平均块 %s",
		after.blocks, humanBytesReal(after.blockRawBytes),
		humanBytesReal(after.blockRawBytes/max(after.blocks, 1)))
	t.Logf("【迁移后】组 %d 个 / %s，**平均一组 %s**（线上逐行写入是 7.70 KiB）",
		after.groups, humanBytesReal(after.groupBytes),
		humanBytesReal(after.groupRawBytes/max(after.groups, 1)))

	t.Logf("")
	t.Logf("口径 A（唯一内容，未压、未计引用）：%s → %s（%.2fx）",
		humanBytesReal(before.colBytes), humanBytesReal(after.blockRawBytes),
		float64(before.colBytes)/float64(max(after.blockRawBytes, 1)))
	t.Logf("口径 B（真正落库）= input 列 %s + 组 %s + 块表 %s = %s ⇒ **%.2fx**（仅 input 一列）",
		humanBytesReal(after.colBytes), humanBytesReal(after.groupBytes),
		humanBytesReal(meta), humanBytesReal(storedTotal),
		float64(before.colBytes)/float64(max(storedTotal, 1)))

	// 阶段 3 用"同一批内容按 256 KiB 重打"算出压实上界是 56.53 MiB。
	// 迁移只按**批**打包（写入必须与行同事务，INV-1），所以每批的最后一组
	// 必然是半组——这条报的就是那个代价有多大，也是"batch_bytes 该调多大"
	// 这个唯一还没定的参数的经验依据。
	if after.groups > 0 {
		avg := after.groupRawBytes / after.groups
		fill := 100 * float64(avg) / float64(models.BlockGroupTarget)
		verdict := "打包到位"
		switch {
		case avg < 32<<10:
			verdict = "**几乎没打包**——批量那条路没走上？"
		case avg < 128<<10:
			verdict = "打包了，但每批的尾巴占比不小；调大 batch_rows/batch_bytes 能再省"
		}
		t.Logf("【组】平均一组 %s / 目标 %s（填充率 %.1f%%）⇒ %s",
			humanBytesReal(avg), humanBytesReal(models.BlockGroupTarget), fill, verdict)
		// 阶段 3 离线把同一批唯一内容按 256 KiB 重打，得到 56.53 MiB。
		// 迁移只按批打包，理论上必然略差于它（每批的尾巴是半组）——
		// 差多少直接说明"批"这个粒度损失了多少。
		t.Logf("【组】对照阶段 3 离线上界 %s（同内容按 256 KiB 重打）：现为 %s，多 %s（%.1f%%）",
			humanBytesReal(phase3PackedCeiling), humanBytesReal(after.groupBytes),
			humanBytesReal(after.groupBytes-phase3PackedCeiling),
			100*float64(after.groupBytes-phase3PackedCeiling)/float64(phase3PackedCeiling))
	}

	// 这一行是整套方案的成本结构本身，所以要说得毫不含糊：
	// 省下来的 5.6 GiB 是"重复"被消掉的部分；最后真正占地方的是"唯一内容压完
	// 之后的净占"，而它由组表（+块表）构成。只看行内字节会得出 1714x 这种数，
	// 那个数不假，但它不是"库变小了多少倍"。
	t.Logf("【账】原始 input 列 %s ⇒ 真正落库 %s（%.2fx）：省掉的是重复（%s），"+
		"剩下的是唯一内容压完的净占（组 %s + 块表 %s + 行内 %s）",
		humanBytesReal(before.colBytes), humanBytesReal(storedTotal),
		float64(before.colBytes)/float64(max(storedTotal, 1)),
		humanBytesReal(state.BytesBefore-state.BytesAfter),
		humanBytesReal(after.groupBytes), humanBytesReal(meta), humanBytesReal(after.colBytes))
	t.Logf("【账】行内那一列的 %s⇒%s 只是中间态：真正省下的是组表",
		humanBytesReal(state.BytesBefore), humanBytesReal(state.BytesAfter))
}

// ── 逐行核对 ──────────────────────────────────────────────────────────────

// verifyAgainstSource 把库里每一行**走生产读路径**读回来，与源库的原字节比对。
//
// 两边都按 id 有序，所以能并排走：源库一条条取出来攒成批，目标库按 id
// 批量 Find（触发 AfterFind 的解码），再逐个比。这样峰值内存只有一批的原文，
// 不必把 5.64 GiB 全塞进内存。
//
// **它读的是模型层解出来的明文，不是列里的字节。** 所以它验的是"整条读路径
// 能不能把原文还回来"，而不是"列里存了什么"——后者由 measureRealDB 的
// typeof 分布来验。两件事都要验：前者抓解码 bug，后者抓"帧被列亲和性转坏了"
// 这种连解码都救不回来的损坏。
func verifyAgainstSource(t *testing.T, ctx context.Context, src *sql.DB) (int64, int64) {
	t.Helper()
	rows, err := src.Query(`SELECT id, input FROM chat_ios WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		t.Fatalf("扫源库失败：%v", err)
	}
	defer rows.Close()

	const batch = 64
	type srcRow struct {
		id   uint
		body []byte
	}
	var (
		pending []srcRow
		seen    int64
		checked int64
	)

	flush := func() {
		if len(pending) == 0 {
			return
		}
		ids := make([]uint, 0, len(pending))
		for _, s := range pending {
			ids = append(ids, s.id)
		}
		got, err := gorm.G[models.ChatIO](models.DB).Where("id IN ?", ids).Find(ctx)
		if err != nil {
			t.Fatalf("读回行 %d..%d 失败：%v", ids[0], ids[len(ids)-1], err)
		}
		byID := make(map[uint]models.ChatIO, len(got))
		for _, g := range got {
			byID[g.ID] = g
		}
		for _, s := range pending {
			g, ok := byID[s.id]
			if !ok {
				t.Fatalf("行 %d 在目标库里不见了", s.id)
			}
			if !bytes.Equal(g.Input, s.body) {
				t.Fatalf("行 %d 还原不一致：%d 字节 → %d 字节",
					s.id, len(s.body), len(g.Input))
			}
			seen++
			checked += int64(len(s.body))
		}
		pending = pending[:0]
	}

	for rows.Next() {
		var s srcRow
		if err := rows.Scan(&s.id, &s.body); err != nil {
			t.Fatalf("读源库失败：%v", err)
		}
		pending = append(pending, s)
		if len(pending) >= batch {
			flush()
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("源库扫描中断：%v", err)
	}
	flush()

	var targetRows int64
	if err := models.DB.WithContext(ctx).
		Raw(`SELECT count(*) FROM chat_ios WHERE deleted_at IS NULL`).Row().Scan(&targetRows); err != nil {
		t.Fatalf("数目标库失败：%v", err)
	}
	if seen != targetRows {
		t.Fatalf("核对了 %d 行，目标库有 %d 行", seen, targetRows)
	}
	return seen, checked
}

// ── 度量 ──────────────────────────────────────────────────────────────────

type dbMeasure struct {
	rows          int64
	colBytes      int64 // input 一列此刻真实占的字节
	textRows      int64 // 明文（含"压不动所以留明文"的行）
	rowFrameRows  int64 // 逐行帧
	blobRows      int64 // 走块表的引用帧
	nullRows      int64
	blocks        int64
	blockRawBytes int64 // 块表里唯一块的原文字节（口径 A 的分子）
	groups        int64
	groupBytes    int64 // 组表落库字节（帧）
	groupRawBytes int64 // 组的原始字节，用来算"平均一组多大"
	fileSize      int64
	pageCount     int64
	freelistCount int64
	autoVacuum    int64
}

// measureRealDB 量一次库的现状。**全部走裸 SQL**：走模型读会把帧解成明文，
// 于是"迁没迁过"就看不出来了——而这一整套设计里，判别形态的唯一依据就是
// 列里此刻的字节。
func measureRealDB(t *testing.T, ctx context.Context, path string) dbMeasure {
	t.Helper()
	var m dbMeasure
	if err := models.DB.WithContext(ctx).Raw(`
		SELECT count(*),
		       COALESCE(sum(length(CAST(input AS BLOB))), 0),
		       COALESCE(sum(CASE WHEN typeof(input) = 'text' THEN 1 ELSE 0 END), 0),
		       COALESCE(sum(CASE WHEN typeof(input) = 'blob' THEN 1 ELSE 0 END), 0),
		       COALESCE(sum(CASE WHEN input IS NULL THEN 1 ELSE 0 END), 0)
		FROM chat_ios WHERE deleted_at IS NULL`).
		Row().Scan(&m.rows, &m.colBytes, &m.textRows, &m.blobRows, &m.nullRows); err != nil {
		t.Fatalf("量 input 列失败：%v", err)
	}

	// 引用帧与逐行帧都是 BLOB，只能靠帧头分开。**这一步要读载荷**，
	// 而这一列平均 470 KiB——所以它只发生在真机测试里，不在任何生产路径上。
	frameRows, err := models.DB.WithContext(ctx).Raw(`SELECT id, input FROM chat_ios
		WHERE deleted_at IS NULL AND typeof(input) = 'blob'`).Rows()
	if err != nil {
		t.Fatalf("读帧行失败：%v", err)
	}
	for frameRows.Next() {
		var (
			id  uint
			raw []byte
		)
		if err := frameRows.Scan(&id, &raw); err != nil {
			frameRows.Close()
			t.Fatalf("读帧行失败：%v", err)
		}
		f, err := compress.Unmarshal(raw)
		if err != nil {
			frameRows.Close()
			t.Fatalf("行 %d 是 BLOB 却解不出帧头：%v", id, err)
		}
		switch f.Type {
		case compress.TypeRowFrame:
			m.rowFrameRows++
		case compress.TypeBlockRefs:
			// blobRows 是"BLOB 总数"，引用帧稍后覆盖它。
		default:
			frameRows.Close()
			t.Fatalf("行 %d 的 input 里装着 %s 帧", id, f.Type)
		}
	}
	if err := frameRows.Err(); err != nil {
		frameRows.Close()
		t.Fatalf("读帧行中断：%v", err)
	}
	frameRows.Close()
	m.blobRows -= m.rowFrameRows // 走块表的引用帧

	if err := models.DB.WithContext(ctx).Raw(
		`SELECT count(*), COALESCE(sum(size), 0) FROM blocks`).
		Row().Scan(&m.blocks, &m.blockRawBytes); err != nil {
		t.Fatalf("数块表失败：%v", err)
	}
	if err := models.DB.WithContext(ctx).Raw(
		`SELECT count(*), COALESCE(sum(length(data)), 0), COALESCE(sum(raw_len), 0)
		 FROM block_groups`).
		Row().Scan(&m.groups, &m.groupBytes, &m.groupRawBytes); err != nil {
		t.Fatalf("数组表失败：%v", err)
	}

	if err := models.DB.WithContext(ctx).Raw(`PRAGMA page_count`).Row().Scan(&m.pageCount); err != nil {
		t.Fatalf("读 page_count 失败：%v", err)
	}
	if err := models.DB.WithContext(ctx).Raw(`PRAGMA freelist_count`).Row().Scan(&m.freelistCount); err != nil {
		t.Fatalf("读 freelist_count 失败：%v", err)
	}
	if err := models.DB.WithContext(ctx).Raw(`PRAGMA auto_vacuum`).Row().Scan(&m.autoVacuum); err != nil {
		t.Fatalf("读 auto_vacuum 失败：%v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("量文件大小失败：%v", err)
	}
	m.fileSize = info.Size()
	return m
}

// vacuumRealDB 把 freelist 里的页真正还给文件系统。
//
// **不 VACUUM 的话，迁移一字节都不会让库变小。** `auto_vacuum=0` 时删除/改写
// 释放的页只进 freelist，文件永远只涨不缩——这是这套方案里"省了空间"和
// "文件变小了"两件事之间那段最容易被含糊过去的距离，所以要分开报。
// （设置 auto_vacuum=INCREMENTAL 需要一次全库重建，是阶段 5 的事。）
func vacuumRealDB(t *testing.T, ctx context.Context, path string, fileSize int64) {
	t.Helper()
	_ = fileSize
	// 实测 7 GB 库 VACUUM 峰值约 2× 库大小，跑之前先确认盘上放得下。
	// 不预检直接跑，磁盘满会以一句 SQLite 的 I/O 报错收场，而那个报错
	// 看不出是"盘满了"还是"库坏了"。
	info, err := os.Stat(path)
	if err == nil && info.Size() > 0 {
		if free, ok := freeSpace(path); ok && free < 2*info.Size() {
			t.Logf("【文件】剩余空间 %s 不足 2× 库大小（%s），跳过 VACUUM",
				humanBytesReal(free), humanBytesReal(2*info.Size()))
			return
		}
	}
	started := time.Now()
	if err := models.DB.WithContext(ctx).Exec(`VACUUM`).Error; err != nil {
		t.Fatalf("VACUUM 失败：%v", err)
	}
	t.Logf("【文件】VACUUM 用了 %s", time.Since(started).Round(time.Millisecond))
}

// ── 准备 ──────────────────────────────────────────────────────────────────

// realDBForMigration 取一份生产库副本的路径。
//
// 两道硬拒绝照抄 models 那边的守卫，不是形式主义：源库是唯一一份原始数据，
// 而这条测试会**原地改写**它。路径写错一次的代价是不可逆的。
func realDBForMigration(t *testing.T) string {
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
	if strings.HasPrefix(strings.ToLower(filepath.Base(abs)), "llmio-repaired") {
		t.Fatalf("拒绝在只读参考副本上跑：%s", abs)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("副本不存在：%v", err)
	}
	return abs
}

// openReadOnlySQL 打开源库，并**把写权限关掉**。
//
// `PRAGMA query_only` 是硬保证：即便后面哪一行代码写错了，SQLite 也会直接
// 拒绝写，而不是默默把源库改了。（glebarez/sqlite 的 DSN 不吃 `?mode=ro`，
// 所以靠 pragma 而不是靠连接参数。）
func openReadOnlySQL(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开源库失败：%v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA query_only = ON`); err != nil {
		db.Close()
		t.Fatalf("关闭源库写权限失败：%v", err)
	}
	return db
}

// assertSourceIntegrity 是真机验收的**第 0 步**。
//
// 库里只要有一个坏页，后面所有"逐行核对"都会以 `database disk image is
// malformed` 收场，而那个报错**完全看不出**是"库坏了"还是"压缩代码读错了"
// ——这两件事的处置方式天差地别。第一次踩到这个坑的是阶段 0（scp 传坏的副本）。
func assertSourceIntegrity(t *testing.T, src *sql.DB) int64 {
	t.Helper()
	var integrity string
	if err := src.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatalf("完整性自检跑不起来：%v", err)
	}
	if integrity != "ok" {
		t.Fatalf("源库自身是坏的（%s）——先修库，再谈压缩", integrity)
	}
	var rows int64
	if err := src.QueryRow(
		`SELECT count(*) FROM chat_ios WHERE deleted_at IS NULL`).Scan(&rows); err != nil {
		t.Fatalf("数源库行数失败：%v", err)
	}
	if rows == 0 {
		t.Fatal("源库里一行 chat_ios 都没有，这份副本不对劲")
	}
	return rows
}

// copyForMigration 复制一份可写的工作库。源库只读，原地迁移必须有自己的副本。
func copyForMigration(t *testing.T, src string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(src), "..", "work-phase4")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建工作目录失败：%v", err)
	}
	// 每次都重建：上一次跑剩下来的库已经不是"迁移前"的形态了，
	// 拿它接着跑会得到一堆"0 行改动"的假绿灯。
	dst := filepath.Join(dir, "migrate.db")
	_ = os.Remove(dst)

	started := time.Now()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("打开源库失败：%v", err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("建工作库失败：%v", err)
	}
	buf := make([]byte, 8<<20)
	if _, err := io.CopyBuffer(out, in, buf); err != nil {
		out.Close()
		t.Fatalf("复制源库失败：%v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("关闭工作库失败：%v", err)
	}
	t.Logf("工作副本 %s（复制用了 %s）", dst, time.Since(started).Round(time.Millisecond))
	return dst
}

func freeSpace(path string) (int64, bool) {
	n, err := diskFree(filepath.Dir(path))
	return n, err == nil
}

// ── 小工具 ────────────────────────────────────────────────────────────────

func humanBytesReal(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
