package service

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/pkg/compress"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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
			// 比的是**两边各自读出来的值**，而不是列里的字节。源库那一列本身
			// 就可能是帧（input 列先迁完的库就是这样），拿它跟明文比必然不等，
			// 而那个"不等"恰恰是压缩生效的证据。明文走 DecompressBytes 也是
			// 原样返回，所以这一句对两种源库形态都成立。
			want, _, err := compress.DecompressBytes(s.body)
			if err != nil {
				t.Fatalf("源库行 %d 的 input 解不开：%v", s.id, err)
			}
			if !bytes.Equal(g.Input, want) {
				t.Fatalf("行 %d 还原不一致：%d 字节 → %d 字节",
					s.id, len(want), len(g.Input))
			}
			seen++
			checked += int64(len(want))
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
	return copyForMigrationNamed(t, src, "work-phase4", "migrate.db")
}

// copyForMigrationNamed 同上，只是工作目录与文件名可指定——两个阶段的真机验收
// 各留各的副本，重跑其中一条不会把另一条的中间态覆盖掉。
func copyForMigrationNamed(t *testing.T, src, dirName, fileName string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(src), "..", dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建工作目录失败：%v", err)
	}
	// 每次都重建：上一次跑剩下来的库已经不是"迁移前"的形态了，
	// 拿它接着跑会得到一堆"0 行改动"的假绿灯。
	dst := filepath.Join(dir, fileName)
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

// ── 响应体两列的真机验收（阶段 6）────────────────────────────────────────
//
// 与上面那条**不是一件事**，别混：
//
//   - 上面那条从"整库明文"起跑，验的是请求体列的迁移能不能把 5.64 GiB 压下去；
//   - 这条从"请求体列已经迁完、响应体两列还是明文"起跑。真机上那份 1.47 GiB 的
//     库就是这个形状：input 列早就是引用帧了，而 of_string_array 那一列的明文
//     占 1.36 GiB（8,890 行），界面却一直报"已完成"。
//
// 它要回答三件事：
//
//  1. **三列都迁完之后逐行读得回来。** 走生产读路径，与源库**三列**逐一比对。
//     响应体那两列不能拿"列里字节相等"来验：迁移之后列里存的是帧，本来就
//     不该相等；要比的是读路径解出来的值。
//  2. **库真的小下来了。** VACUUM 前后的文件大小都报——1.36 GiB 的明文压完
//     应该只剩几十 MiB，这个落差就是这次改动的全部意义。
//  3. **回滚闭环**：decompress 之后三列都回到明文，与源库**再次**逐行相等。
//
// 跑法（源库只读，测试在它旁边复制一份工作库再动）：
//
//	$env:LLMIO_REAL_DB_COPY="D:\llmio-test\work-phase4\real.db"
//	go test ./service/ -run RealDatabaseOutputColumns -v -timeout 180m
func TestLogCompress_RealDatabaseOutputColumnsInPlace(t *testing.T) {
	srcPath := realDBForMigration(t)
	ctx := context.Background()

	src := openReadOnlySQL(t, srcPath)
	defer src.Close()
	srcRows := assertSourceIntegrity(t, src)
	// 核对基线走**生产读路径**，理由见 openSourceModel。
	srcDB := openSourceModel(t, srcPath)

	work := copyForMigrationNamed(t, srcPath, "work-phase6", "output.db")
	models.Init(ctx, work)
	t.Cleanup(func() {
		if sqlDB, err := models.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
		compressInFlight.Store(false)
		compressPauseRequested.Store(false)
	})

	before := measureRealDB(t, ctx, work)
	outBefore := measureOutputColumns(t, ctx)
	if before.rows != srcRows {
		t.Fatalf("工作库 %d 行、源库 %d 行，复制没拷全", before.rows, srcRows)
	}
	if outBefore.plainRows == 0 {
		t.Skip("这份副本的响应体两列已经是压缩形态——这条验收要一份还没迁过的副本")
	}
	t.Logf("")
	t.Logf("【迁移前】input 列 %s（明文 %d 行 / 帧 %d 行）｜文件 %s",
		humanBytesReal(before.colBytes), before.textRows, before.blobRows,
		humanBytesReal(before.fileSize))
	t.Logf("【迁移前】响应体两列 %s：其中 %d 行还有明文（原始 %s）｜组 %d 个 / %s",
		humanBytesReal(outBefore.storedBytes), outBefore.plainRows,
		humanBytesReal(outBefore.plainBytes), before.groups, humanBytesReal(before.groupBytes))

	// ── 迁移 ──
	//
	// full=true 是**必须**的，而且它正是这次改动顺带修掉的那个缺口：
	// 水位停在表尾，续跑一行都扫不到，响应体那两列永远迁不动。
	started := time.Now()
	state, err := RunLogCompress(ctx, true)
	if err != nil {
		t.Fatalf("原地迁移失败：%v", err)
	}
	elapsed := time.Since(started)
	if state.Status != compressDone {
		t.Fatalf("迁移结束状态是 %q（last_error=%q）", state.Status, state.LastError)
	}
	t.Logf("【迁移】用了 %s：扫 %d 行 / 改 %d 行 / 跳过 %d 行（原始 %s ⇒ %s）",
		elapsed.Round(time.Millisecond), state.Scanned, state.Packed, state.Skipped,
		humanBytesReal(state.BytesBefore), humanBytesReal(state.BytesAfter))

	outAfter := measureOutputColumns(t, ctx)
	// 这里**不能**断言"零明文行"，理由见 assertRemainingPlainUnpackable。
	left := assertRemainingPlainUnpackable(t, ctx)
	// 归一化：真机上那 8,932 行 `of_string` 是空串，到这一步应当全部变成 NULL。
	assertNoEmptyText(t, ctx)
	if left > max(outBefore.plainBytes/100, 1) {
		t.Fatalf("残留明文 %s 超过原明文的 1%%（%s）——这个量级不像是压不动，像是漏迁",
			humanBytesReal(left), humanBytesReal(outBefore.plainBytes))
	}
	t.Logf("【迁移后】响应体两列 %s（原始 %s ⇒ **%.1fx**）",
		humanBytesReal(outAfter.storedBytes), humanBytesReal(outBefore.plainBytes),
		float64(outBefore.plainBytes)/float64(max(outAfter.storedBytes, 1)))

	// ── 逐行核对（三列）──
	checked, checkedOut := verifyAllColumnsAgainstSource(t, ctx, srcDB)
	t.Logf("【核对】%d 行三列逐一相等（响应体两列解出来 %s）", checked,
		humanBytesReal(checkedOut))

	// ── 文件 ──
	after := measureRealDB(t, ctx, work)
	t.Logf("【文件】迁移后 %s（freelist %d 页）；不 VACUUM 文件不会缩",
		humanBytesReal(after.fileSize), after.freelistCount)
	vacuumRealDB(t, ctx, work, after.fileSize)
	vacuumed := measureRealDB(t, ctx, work)
	t.Logf("【文件】VACUUM 后 %s：整库 %s ⇒ %s（%.1f%%）",
		humanBytesReal(vacuumed.fileSize), humanBytesReal(before.fileSize),
		humanBytesReal(vacuumed.fileSize),
		100*float64(vacuumed.fileSize)/float64(max(before.fileSize, 1)))

	// ── 回滚闭环 ──
	started = time.Now()
	dstate, err := RunLogDecompress(ctx, true)
	if err != nil {
		t.Fatalf("回滚失败：%v", err)
	}
	if dstate.Status != compressDone {
		t.Fatalf("回滚结束状态是 %q（last_error=%q）", dstate.Status, dstate.LastError)
	}
	t.Logf("【回滚】用了 %s：还原 %d 行", time.Since(started).Round(time.Millisecond), dstate.Packed)

	assertColumnsPlain(t, ctx)
	assertNoEmptyText(t, ctx)
	verifyAllColumnsAgainstSource(t, ctx, srcDB)
	rolled := measureRealDB(t, ctx, work)
	t.Logf("【回滚后】input 列 %s（明文 %d 行 / 帧 %d 行）｜文件 %s",
		humanBytesReal(rolled.colBytes), rolled.textRows, rolled.blobRows,
		humanBytesReal(rolled.fileSize))
}

// outputMeasure 是响应体两列的现状。
type outputMeasure struct {
	plainRows   int64 // 至少一列还是明文的行数
	framedRows  int64 // 至少一列已是帧的行数
	plainBytes  int64 // 明文列的原文字节合计
	storedBytes int64 // 两列此刻真实占的字节（压过的算帧的大小）
}

// measureOutputColumns 量响应体两列。
//
// 判据直接复用迁移自己的 plaintextFilter 那一套常量（`ofStringPlain` /
// `ofArrayPlain`）：测试和生产要是各写一份，"还有没有货"就会有两个答案。
func measureOutputColumns(t *testing.T, ctx context.Context) outputMeasure {
	t.Helper()
	var m outputMeasure
	q := fmt.Sprintf(`SELECT
		   COALESCE(sum(CASE WHEN %s OR %s THEN 1 ELSE 0 END), 0),
		   COALESCE(sum(CASE WHEN %s OR %s THEN 1 ELSE 0 END), 0),
		   COALESCE(sum(CASE WHEN %s THEN COALESCE(length(CAST(of_string AS BLOB)), 0) ELSE 0 END
		                  + CASE WHEN %s THEN COALESCE(length(CAST(of_string_array AS BLOB)), 0) ELSE 0 END), 0),
		   COALESCE(sum(COALESCE(length(CAST(of_string AS BLOB)), 0)
		              + COALESCE(length(CAST(of_string_array AS BLOB)), 0)), 0)
		FROM chat_ios WHERE deleted_at IS NULL`,
		ofStringPlain, ofArrayPlain, ofStringFramed, ofArrayFramed,
		ofStringPlain, ofArrayPlain)
	if err := models.DB.WithContext(ctx).Raw(q).
		Row().Scan(&m.plainRows, &m.framedRows, &m.plainBytes, &m.storedBytes); err != nil {
		t.Fatalf("量响应体两列失败：%v", err)
	}
	return m
}

// assertRemainingPlainUnpackable 复核迁移之后的残留明文：每一段都必须是**真的压不动**。
//
// 为什么不直接断言"零明文行"——第一次跑真机就是这么写的，然后红在一件完全正确的
// 事上：这份库里有 43 行的 of_string 只有 19～615 字节（of_string_array 是 NULL），
// 短文本压完比原文还长，PackColumnValue 按规则 1（压不了就存明文）让它留在明文里。
// 那是设计，不是漏迁。钉零明文会把正确的行为判成失败，而真正的风险——"还有大块
// 明文没迁"——反倒从这个断言下面溜过去了：它只数行数，不看那些行有多大。
//
// 判据用**生产那一个函数**（models.PackColumnValue），而不是在测试里重算一遍压缩：
// 两处各写一份的话，"压不动"迟早会有两个答案。调用方另有一条不依赖压缩代码的
// 量级断言（残留不得超过原明文的 1%），两条合起来才既认得出"压不动"、又挡得住
// "PackColumnValue 自己有 bug 于是什么都压不动"。
//
// 返回残留明文的合计字节。
func assertRemainingPlainUnpackable(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	var (
		segs    int64
		left    int64
		longest int
	)
	for _, c := range []struct{ col, filter string }{
		{"of_string", ofStringPlain},
		{"of_string_array", ofArrayPlain},
	} {
		rows, err := models.DB.WithContext(ctx).Raw(fmt.Sprintf(
			`SELECT id, %s FROM chat_ios WHERE deleted_at IS NULL AND %s ORDER BY id`,
			c.col, c.filter)).Rows()
		if err != nil {
			t.Fatalf("扫 %s 的残留明文失败：%v", c.col, err)
		}
		for rows.Next() {
			var (
				id  uint
				raw []byte
			)
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				t.Fatalf("读 %s 的残留明文失败：%v", c.col, err)
			}
			if models.PackColumnValue(raw) != nil {
				rows.Close()
				t.Fatalf("行 %d 的 %s 有 %d 字节明文，而且它**压得动**——这一行被漏掉了",
					id, c.col, len(raw))
			}
			segs++
			left += int64(len(raw))
			longest = max(longest, len(raw))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("%s 的扫描中断：%v", c.col, err)
		}
		rows.Close()
	}
	t.Logf("【核对】残留明文 %d 段 / %s（最长 %d 字节）：都是压不动的短响应体，符合规则 1",
		segs, humanBytesReal(left), longest)
	return left
}

// openSourceModel 按**生产读路径**打开源库，并把写权限关掉。
//
// 为什么不能拿裸 SQL 读来的字节当基线：这份副本的 input 列早就是帧了，其中
// 80 字节上下那些是**块引用序列**——只有 models.UnpackBody 解得开（要查块表），
// 而 compress.DecompressBytes 只认逐行帧，遇到引用序列会把那 80 字节原样当
// 明文还回来。第一次跑就红在这里：80 字节 vs 112,434 字节。
//
// 钩子用的是**查询时那个 tx**，所以源库与目标库各用自己的块表解包，两边读出来
// 的都是原始 body——比它才是"数据没坏"。单连接 + query_only 与 openReadOnlySQL
// 是同一套硬保证，只是这条连接要跑读路径。
func openSourceModel(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("按读路径打开源库失败：%v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取源库连接失败：%v", err)
	}
	// 连接不许被回收换新——query_only 是**挂在连接上**的，换一条连接就没了。
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)
	if _, err := sqlDB.Exec(`PRAGMA query_only = ON`); err != nil {
		t.Fatalf("关闭源库写权限失败：%v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// verifyAllColumnsAgainstSource 把**三列**都走生产读路径读出来，与源库逐行比对。
//
// 两侧都是"读出来的值"，不是"列里的字节"：迁移之后列里是帧，与源库的明文本来
// 就不该相等，而那个不等恰恰是压缩生效的证据。同理，源库的 input 列此刻也可能是
// 帧——这份副本的响应体还没迁，但 input 早迁完了。
//
// 分批是有代价上的必要的：整库三列的明文合起来 7 GB 上下，一次性 Find 会把它们
// 全拽进内存。每批 64 行 ~36 MB，稳。
func verifyAllColumnsAgainstSource(t *testing.T, ctx context.Context, srcDB *gorm.DB) (int64, int64) {
	t.Helper()
	var ids []uint
	if err := srcDB.WithContext(ctx).Raw(
		`SELECT id FROM chat_ios WHERE deleted_at IS NULL ORDER BY id`).Scan(&ids).Error; err != nil {
		t.Fatalf("读源库 id 列表失败：%v", err)
	}
	if len(ids) == 0 {
		t.Fatal("源库里一行都没有")
	}

	const batch = 64
	var (
		seen    int64
		outSize int64
	)
	for lo := 0; lo < len(ids); lo += batch {
		chunk := ids[lo:min(lo+batch, len(ids))]
		src, err := gorm.G[models.ChatIO](srcDB).Where("id IN ?", chunk).Find(ctx)
		if err != nil {
			t.Fatalf("读源库行 %d..%d 失败：%v", chunk[0], chunk[len(chunk)-1], err)
		}
		dst, err := gorm.G[models.ChatIO](models.DB).Where("id IN ?", chunk).Find(ctx)
		if err != nil {
			t.Fatalf("读目标库行 %d..%d 失败：%v", chunk[0], chunk[len(chunk)-1], err)
		}
		byID := make(map[uint]models.ChatIO, len(dst))
		for _, g := range dst {
			byID[g.ID] = g
		}
		for _, s := range src {
			g, ok := byID[s.ID]
			if !ok {
				t.Fatalf("行 %d 在目标库里不见了", s.ID)
			}
			if !bytes.Equal(g.Input, s.Input) {
				t.Fatalf("行 %d 的 input 还原不一致：%d 字节 → %d 字节",
					s.ID, len(s.Input), len(g.Input))
			}
			if g.OfString != s.OfString {
				t.Fatalf("行 %d 的 of_string 还原不一致：%d 字节 → %d 字节",
					s.ID, len(s.OfString), len(g.OfString))
			}
			// 两侧都已经是 []string（各有各的解码路径），比切片而不是比 JSON 字面
			// 形式——历史行不一定是同一个 marshal 写出来的。
			if !reflect.DeepEqual(g.OfStringArray, s.OfStringArray) {
				t.Fatalf("行 %d 的 of_string_array 还原不一致：%d 段 → %d 段",
					s.ID, len(s.OfStringArray), len(g.OfStringArray))
			}
			seen++
			outSize += int64(len(s.OfString)) + int64(len(s.OfStringArray))
		}
	}

	var targetRows int64
	if err := models.DB.WithContext(ctx).
		Raw(`SELECT count(*) FROM chat_ios WHERE deleted_at IS NULL`).Row().Scan(&targetRows); err != nil {
		t.Fatalf("数目标库失败：%v", err)
	}
	if seen != targetRows {
		t.Fatalf("核对了 %d 行，目标库有 %d 行", seen, targetRows)
	}
	return seen, outSize
}

// assertNoEmptyText 断言三列里**没有零长度的 TEXT**。
//
// 它盯的是那条不变量本身：**TEXT 只表示"非空明文"**，空值一律 NULL。这不是
// 洁癖——只要 TEXT 还兼着"空"这个含义，"挑出还没迁的行"就非得把载荷读出来量
// 长度不可，而那个 `length()` 在真机 7.6 GiB 的库上要 4.2 秒；`journal_mode=delete`
// 下读事务挡写，聊天请求等锁超时，整站 500。判据能瘦成免费的 `typeof`，全靠这条。
//
// 历史行里这个形态真实存在：真机上 `of_string` 有 8,932 行是空串（流式响应，
// 正文在 of_string_array 里），当年写入路径把空串写成零长度字符串而不是 NULL。
// 迁移顺手归一化（packColumn），回滚也一样（空值写 NULL 回去），所以迁完与
// 回滚完都不该再有。
//
// 这一句要读载荷（拿空串去比，得把 TEXT 取出来），但只对 TEXT 行读——迁完之后
// 剩下的 TEXT 只有几十行、几十字节，代价可以忽略。它**不在任何生产路径上**，
// 所以不违反"轮询那条路只许用 typeof"。
func assertNoEmptyText(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, col := range chatIOColumns {
		var n int64
		q := fmt.Sprintf(
			`SELECT count(*) FROM chat_ios WHERE deleted_at IS NULL AND typeof(%s) = 'text' AND %s = ''`,
			col, col)
		if err := models.DB.WithContext(ctx).Raw(q).Row().Scan(&n); err != nil {
			t.Fatalf("数 %s 的空 TEXT 行失败：%v", col, err)
		}
		if n != 0 {
			t.Fatalf("%s 还有 %d 行是零长度的 TEXT——空的该写 NULL。"+
				"TEXT 兼着「空」这个含义，状态页那条判据就非得读载荷量长度，"+
				"而那正是真机上把整站打成 500 的那 4.2 秒", col, n)
		}
	}
}

// assertColumnsPlain 断言三列都回到了明文形态（`typeof` 是 text 或 null）。
func assertColumnsPlain(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, col := range chatIOColumns {
		var n int64
		q := fmt.Sprintf(
			`SELECT count(*) FROM chat_ios WHERE deleted_at IS NULL AND typeof(%s) = 'blob'`, col)
		if err := models.DB.WithContext(ctx).Raw(q).Row().Scan(&n); err != nil {
			t.Fatalf("数 %s 的帧行失败：%v", col, err)
		}
		if n != 0 {
			t.Fatalf("回滚之后 %s 还有 %d 行是 BLOB——明文按 BLOB 写回去，读路径照样读得出来，"+
				"于是这个错会一直躺着，直到下一次迁移把它当成已迁过而漏掉", col, n)
		}
	}
}
