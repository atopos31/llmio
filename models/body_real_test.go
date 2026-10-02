package models

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atopos31/llmio/pkg/compress"
	"gorm.io/gorm"
)

// 阶段 3 的真机验收：生产库副本里**每一行** input 都走一遍生产写路径
// （BeforeCreate → 分块 → 块表 → 引用帧），再走一遍生产读路径逐字节读回来。
//
// 为什么必须上真库：手写种子只能证明"代码按我想的方式跑"，证明不了"真实数据
// 里每一个字节都还在"。生产数据的 input 里有 7 GB 的请求体，包含流式数组、
// 非法 UTF-8、内嵌 NUL，以及从 0 字节到几 MB 的长度分布——任意一条都能让
// 分块器或帧格式在边界上出错。而这里错一行就是一次**静默的数据损坏**：
// 体积照省、读回来的长度也对得上，只是内容变了。
//
// 顺带量出线上组的真实代价。每次 `Put` 只封自己这一批（写入必须与行插入同
// 事务，INV-1），所以线上组偏小、组间冗余吃不到。这不是猜的：真数据写完之后
// 再按 256 KiB 重打一遍，两个数相减就是压实的收益上限。
//
// 默认跳过（要一份 7 GB 的库）。跑法：
//
//	$env:LLMIO_REAL_DB_COPY="D:\llmio-test\work-phase3\real.db"
//	go test ./models/ -run InputRealDatabase -v -timeout 120m
//
// 全程只读源库；中转库建在源库旁边（它会有几百 MiB，别塞进 C 盘临时目录）。
func TestChatIOInput_RealDatabaseEveryRowRoundTrips(t *testing.T) {
	src := realDBCopy(t)
	ctx := context.Background()

	srcDB := openRawSQL(t, src)
	defer srcDB.Close()

	var (
		srcRows  int64
		srcBytes int64
	)
	// `length()` 对 TEXT 数的是**字符**不是字节，而这一列是 JSON（除 ASCII 还有
	// 中文与 emoji），两个数能差 8%。整套口径都建立在字节上，所以每次都要
	// 先 `CAST(... AS BLOB)` 把它打回原始字节——BLOB 的 length 才是字节数。
	if err := srcDB.QueryRow(
		`SELECT count(*), COALESCE(sum(length(CAST(input AS BLOB))), 0) FROM chat_ios`).
		Scan(&srcRows, &srcBytes); err != nil {
		t.Fatalf("统计源库失败：%v", err)
	}
	if srcRows == 0 {
		t.Fatal("源库里一行 chat_ios 都没有，这份副本不对劲")
	}
	t.Logf("源库 input：%d 行 / %s", srcRows, humanBytes(srcBytes))

	dstPath := filepath.Join(filepath.Dir(src), "phase3-input.db")
	_ = os.Remove(dstPath)
	t.Cleanup(func() { _ = os.Remove(dstPath) })
	Init(ctx, dstPath)
	closeOnCleanup(t, DB)
	dst := DB
	// 组缓存只按组 id 索引，而新库的组 id 从 1 重新开始——上一个库里同号的组
	// 是完全不同的内容。Init 里已经 Reset 过一次，这里再显式写一遍：这条测试
	// 的每一条结论都建立在"取到的确实是这个库里的组"之上。
	blockStore.Reset()

	rows, err := srcDB.Query(`SELECT id, input FROM chat_ios ORDER BY id`)
	if err != nil {
		t.Fatalf("扫描源库失败：%v", err)
	}
	defer rows.Close()

	const batch = 64
	type srcRow struct {
		id   int64
		body []byte
	}
	pending := make([]srcRow, 0, batch)

	var (
		seen      int64
		checkByte int64
		storedCol int64
		nBlockRow int64
		nRowFrame int64
		nPlain    int64
		nEmpty    int64
		nRefs     int64
		writeDur  time.Duration
		readDur   time.Duration
		startedAt = time.Now()
	)
	// 陷阱 A 的样本：走块表的真行 id。之后拿它们验一遍"补响应体不会碰 input"。
	blockRowIDs := make([]uint, 0, 20)

	flush := func(bs []srcRow) {
		if len(bs) == 0 {
			return
		}

		// ── 写：生产写路径。每次 Create 自己一个事务，与线上一个请求一次写入同形 ──
		created := make([]ChatIO, 0, len(bs))
		w0 := time.Now()
		for _, s := range bs {
			io := ChatIO{LogId: uint(s.id), Input: BodyBytes(s.body)}
			if err := gorm.G[ChatIO](dst).Create(ctx, &io); err != nil {
				t.Fatalf("源行 %d 写入失败：%v", s.id, err)
			}
			created = append(created, io)
		}
		writeDur += time.Since(w0)

		// ── 读：生产读路径（AfterFind → 按引用还原）──
		ids := make([]uint, 0, len(created))
		for _, c := range created {
			ids = append(ids, c.ID)
		}
		r0 := time.Now()
		back, err := gorm.G[ChatIO](dst).Where("id IN ?", ids).Find(ctx)
		if err != nil {
			t.Fatalf("批量读回失败（源 id %d..%d）：%v", bs[0].id, bs[len(bs)-1].id, err)
		}
		readDur += time.Since(r0)
		byID := make(map[uint]ChatIO, len(back))
		for _, b := range back {
			byID[b.ID] = b
		}

		for i, s := range bs {
			got, ok := byID[created[i].ID]
			if !ok {
				t.Fatalf("源行 %d 读不回来（目标 id %d）", s.id, created[i].ID)
			}
			if !bytes.Equal(got.Input, s.body) {
				t.Fatalf("源行 %d 的 input 还原不一致：%d 字节 → %d 字节，%s",
					s.id, len(s.body), len(got.Input), firstDiff(s.body, got.Input))
			}
			checkByte += int64(len(s.body))
			seen++
		}

		// ── 库里到底存成了什么 ──
		//
		// typeof 必须与形态一一对上。这是迁移挑候选行（`typeof(input)='text'`）
		// 的依据，也是"帧按 BLOB 落进 TEXT 亲和列"这条不变量的唯一证明——
		// 亲和性一旦把帧转成了 TEXT，帧就永久损坏，而上面那次 bytes.Equal
		// 会照过（它读的是模型层解出来的明文）。
		shapeQ := `SELECT id, COALESCE(typeof(input), 'null'),
				COALESCE(length(CAST(input AS BLOB)), 0), input
			FROM chat_ios WHERE id IN (?` + repeatComma(len(ids)-1) + `)`
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		srows, err := dst.Raw(shapeQ, args...).Rows()
		if err != nil {
			t.Fatalf("读落库形态失败：%v", err)
		}
		for srows.Next() {
			var (
				id  uint
				typ string
				ln  int
				raw []byte
			)
			if err := srows.Scan(&id, &typ, &ln, &raw); err != nil {
				srows.Close()
				t.Fatalf("读落库形态失败：%v", err)
			}
			storedCol += int64(ln)
			if ln == 0 {
				// 空 body 保持原样：NULL 仍是 NULL、空串仍是空串。别为了
				// "顺便压一下"改动历史行的 typeof 分布。
				if typ != "null" && typ != "text" {
					srows.Close()
					t.Fatalf("行 %d 的空 input 落成了 %s", id, typ)
				}
				nEmpty++
				continue
			}
			f, err := compress.Unmarshal(raw)
			switch {
			case errors.Is(err, compress.ErrNotFrame):
				if typ != "text" {
					srows.Close()
					t.Fatalf("行 %d 存的是明文，typeof 却是 %s", id, typ)
				}
				nPlain++
			case err != nil:
				srows.Close()
				t.Fatalf("行 %d 的帧头不可信：%v", id, err)
			default:
				if typ != "blob" {
					srows.Close()
					t.Fatalf("行 %d 存的是 %s 帧，typeof 却是 %s——列亲和性把帧转坏了",
						id, f.Type, typ)
				}
				switch f.Type {
				case compress.TypeBlockRefs:
					refs, err := DecodeRefsFrame(f)
					if err != nil {
						srows.Close()
						t.Fatalf("行 %d 的引用序列坏了：%v", id, err)
					}
					if len(refs) == 0 {
						srows.Close()
						t.Fatalf("行 %d 存成了块表形态却一条引用都没有", id)
					}
					nRefs += int64(len(refs))
					nBlockRow++
					if len(blockRowIDs) < 20 {
						blockRowIDs = append(blockRowIDs, id)
					}
				case compress.TypeRowFrame:
					nRowFrame++
				default:
					srows.Close()
					t.Fatalf("行 %d 的 input 里装着 %s 帧，这一列不装这种", id, f.Type)
				}
			}
		}
		if err := srows.Err(); err != nil {
			srows.Close()
			t.Fatalf("读落库形态中断：%v", err)
		}
		srows.Close()

		if seen%2048 < batch {
			t.Logf("已过 %d/%d 行，用时 %.1f s（写完 %.1f s / 读回 %.1f s）",
				seen, srcRows, time.Since(startedAt).Seconds(),
				writeDur.Seconds(), readDur.Seconds())
		}
	}

	for rows.Next() {
		var s srcRow
		if err := rows.Scan(&s.id, &s.body); err != nil {
			t.Fatalf("读源库第 %d 行失败：%v", seen+1, err)
		}
		// 源库里出现帧，说明这份副本不是没跑过压缩的原始生产库。那样下面的
		// 逐字节比对就是拿帧去比明文，全部不作数——当场停下，别给假结论。
		if compress.LooksLikeFrame(s.body) {
			t.Fatalf("源库行 %d（id=%d）的 input 已经是帧了——这份副本跑过压缩，不是原始生产库",
				seen+1, s.id)
		}
		pending = append(pending, s)
		if len(pending) >= batch {
			flush(pending)
			pending = pending[:0]
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("源库扫描中断：%v", err)
	}
	flush(pending)

	if seen != srcRows {
		t.Fatalf("只过了 %d 行，源库有 %d 行", seen, srcRows)
	}
	if checkByte != srcBytes {
		t.Fatalf("逐字节核对的字节数 %d 与源库统计的 %d 不符", checkByte, srcBytes)
	}

	// ── 口径 A / 口径 B ──
	//
	// 两个口径必须分开报，混着说会得出相反的结论：A 是唯一内容原字节
	// （未压、未计引用），B 才是真正落库的字节。
	var (
		nBlocks    int64
		uniqBytes  int64
		nGroups    int64
		groupBytes int64
	)
	if err := dst.Raw(`SELECT count(*), COALESCE(sum(size), 0) FROM blocks`).
		Row().Scan(&nBlocks, &uniqBytes); err != nil {
		t.Fatalf("数块表失败：%v", err)
	}
	if err := dst.Raw(`SELECT count(*), COALESCE(sum(length(data)), 0) FROM block_groups`).
		Row().Scan(&nGroups, &groupBytes); err != nil {
		t.Fatalf("数组表失败：%v", err)
	}
	// 块表每行的开销沿用 Phase 0 的估算常数（block id + 摘要 + 长度 + 索引）。
	// 是估的：这个常数翻一倍也只多几 MiB，不影响任何结论。
	metaBytes := nBlocks * rpBlockRowBytes
	storedTotal := storedCol + groupBytes + metaBytes

	t.Logf("")
	t.Logf("全部 %d 行逐字节还原一致（%s）", seen, humanBytes(checkByte))
	t.Logf("行形态：块表 %d 行（共 %d 条引用，平均 %.1f 块/行）｜逐行帧 %d 行｜存明文 %d 行｜空 %d 行",
		nBlockRow, nRefs, float64(nRefs)/float64(max(nBlockRow, 1)), nRowFrame, nPlain, nEmpty)
	t.Logf("唯一块 %d 个 / %s，平均块 %s；组 %d 个，平均一组 %s",
		nBlocks, humanBytes(uniqBytes), humanBytes(uniqBytes/max(nBlocks, 1)),
		nGroups, humanBytes(groupBytes/max(nGroups, 1)))
	t.Logf("口径 A（唯一内容，未压、未计引用）：%s → %s（%.2fx）",
		humanBytes(srcBytes), humanBytes(uniqBytes), float64(srcBytes)/float64(uniqBytes))
	t.Logf("口径 B（真正落库）= input 列 %s + 组 %s + 块表 %s = %s ⇒ %s → %s（%.2fx，仅 input 一列）",
		humanBytes(storedCol), humanBytes(groupBytes), humanBytes(metaBytes),
		humanBytes(storedTotal), humanBytes(srcBytes), humanBytes(storedTotal),
		float64(srcBytes)/float64(storedTotal))
	t.Logf("写入 %s（摊到每行 %.2f ms，含分块、块表查插、行插入）；读回 %s（每行 %.2f ms）",
		writeDur.Round(time.Millisecond), writeDur.Seconds()*1000/float64(seen),
		readDur.Round(time.Millisecond), readDur.Seconds()*1000/float64(seen))

	// ── 线上组 vs 256 KiB 打包：压实值不值得做，看这一个数 ──
	p := measureOnlineVsPacked(t, dst)
	t.Logf("线上组：%d 组 / %s（最大一组原始 %s）",
		p.onlineGroups, humanBytes(p.online), humanBytes(p.maxOnline))
	delta := p.online - p.packed
	verdict := fmt.Sprintf("⇒ 压实能再省 %s（-%.1f%%）",
		humanBytes(delta), 100*float64(delta)/float64(p.online))
	if delta < 0 {
		verdict = fmt.Sprintf("⇒ 压实反而更大 %s（+%.1f%%）——组大小不是瓶颈，别做",
			humanBytes(-delta), -100*float64(delta)/float64(p.online))
	}
	t.Logf("同一批内容按 %s 重打：%d 组 / %s %s",
		humanBytes(BlockGroupTarget), p.packedGroups, humanBytes(p.packed), verdict)

	// ── 真实写路径的第二半：Updates(OutputUnion) 不许碰 input（陷阱 A）──
	assertOutputUpdateKeepsInput(t, dst, ctx, blockRowIDs)
}

// assertOutputUpdateKeepsInput 拿真行验陷阱 A。
//
// `service/chat.go` 记完响应体写的就是这一句：
//
//	Updates(ctx, models.ChatIO{OutputUnion: *output})
//
// 它只为补上输出列。若钩子挂在 BeforeSave 上，这次更新会带着零值 Input 走一遍
// 钩子，把已经存好的请求体覆盖掉——**静默丢数据，而且体积上还"更省"**。
// 单测里验过，但那是手写数据；这里拿真行、真字节再验一遍，因为这是这套方案里
// 最贵的一种错。
func assertOutputUpdateKeepsInput(t *testing.T, db *gorm.DB, ctx context.Context, ids []uint) {
	t.Helper()
	if len(ids) == 0 {
		t.Fatal("没有一行走块表，陷阱 A 这条断言等于没验")
	}
	beforeBlocks := blockCount(t, db)

	for _, id := range ids {
		want, err := gorm.G[ChatIO](db).Where("id = ?", id).First(ctx)
		if err != nil {
			t.Fatalf("行 %d 读不出来：%v", id, err)
		}
		// 库里此刻的**原始列**（引用帧），不是模型层解出来的明文
		stored := rawColumn(t, db, id, "input")
		if !compress.LooksLikeFrame(stored) {
			t.Fatalf("行 %d 的 input 不是帧，这条断言没验到块表行", id)
		}

		if _, err := gorm.G[ChatIO](db).Where("id = ?", id).
			Updates(ctx, ChatIO{OutputUnion: OutputUnion{OfString: "补上的响应体"}}); err != nil {
			t.Fatalf("行 %d 补响应体失败：%v", id, err)
		}

		if after := rawColumn(t, db, id, "input"); !bytes.Equal(stored, after) {
			t.Fatalf("行 %d 的 input 被 Updates(OutputUnion) 改动了：%d 字节 → %d 字节",
				id, len(stored), len(after))
		}
		got, err := gorm.G[ChatIO](db).Where("id = ?", id).First(ctx)
		if err != nil {
			t.Fatalf("行 %d 更新后读不回来：%v", id, err)
		}
		if !bytes.Equal(got.Input, want.Input) {
			t.Fatalf("行 %d 补响应体后 input 变了：%d 字节 → %d 字节，%s",
				id, len(want.Input), len(got.Input), firstDiff(want.Input, got.Input))
		}
		if got.OfString != "补上的响应体" {
			t.Fatalf("行 %d 的响应体没补上：%q", id, got.OfString)
		}
	}

	if after := blockCount(t, db); after != beforeBlocks {
		t.Fatalf("补响应体顺带写了块：%d → %d", beforeBlocks, after)
	}
	t.Logf("陷阱 A：%d 行真行补响应体后，input 的原始字节与还原结果都没变，块表也没长", len(ids))
}

// rpPack 是"线上组"与"256 KiB 打包"的对照结果。
type rpPack struct {
	online       int64 // 线上组帧的总字节
	onlineGroups int64
	maxOnline    int64 // 线上最大一组的原始字节
	packed       int64 // 同内容按 BlockGroupTarget 重打的字节
	packedGroups int64
}

// measureOnlineVsPacked 量出线上组相对 256 KiB 打包贵多少。
//
// 为什么必须量而不是猜：线上每次 `Put` 只封自己这一批（写入必须与行插入同事务，
// INV-1），新写的组因此偏小、组间冗余吃不到。代价有多大，直接决定 Phase 6 的
// 压实（CompactGroups）值不值得做——而这个数只能在这份真数据上量。
//
// 重打走的是与生产压实同一条路：按 `blocks.id` 顺序（就是块的**首次出现顺序**）
// 逐块取出来，够 BlockGroupTarget 就封一组。组边界永远落在块边界上，
// 规则与 insertBlockGroups、Phase 0 的 regroup 完全一致。
func measureOnlineVsPacked(t *testing.T, db *gorm.DB) rpPack {
	t.Helper()
	var p rpPack
	if err := db.Raw(`SELECT count(*), COALESCE(sum(length(data)), 0), COALESCE(max(raw_len), 0)
		FROM block_groups`).Row().Scan(&p.onlineGroups, &p.online, &p.maxOnline); err != nil {
		t.Fatalf("读线上组失败：%v", err)
	}

	rows, err := db.Raw(`SELECT group_id, off, size FROM blocks ORDER BY id`).Rows()
	if err != nil {
		t.Fatalf("读块表失败：%v", err)
	}
	defer rows.Close()

	var (
		curGID int64 = -1
		curGrp []byte
		buf    []byte
	)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		frame, ok := compress.EncodeVerified(buf, compress.TypeGroup, compress.LevelGroup)
		if !ok {
			t.Fatal("重打包自检没过——压缩代码有 bug")
		}
		p.packed += int64(len(frame))
		p.packedGroups++
		buf = buf[:0]
	}
	for rows.Next() {
		var (
			gid  int64
			off  int
			size int
		)
		if err := rows.Scan(&gid, &off, &size); err != nil {
			t.Fatalf("读块表失败：%v", err)
		}
		if gid != curGID {
			curGrp = loadGroupPlain(t, db, gid)
			curGID = gid
		}
		if off < 0 || size < 0 || off+size > len(curGrp) {
			t.Fatalf("块落在组 %d 的 %d..%d 之外（组长 %d）", gid, off, off+size, len(curGrp))
		}
		blk := curGrp[off : off+size]
		if len(buf) > 0 && len(buf)+len(blk) > BlockGroupTarget {
			flush()
		}
		buf = append(buf, blk...)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("块表遍历中断：%v", err)
	}
	flush()
	return p
}

func loadGroupPlain(t *testing.T, db *gorm.DB, id int64) []byte {
	t.Helper()
	var raw []byte
	if err := db.Raw(`SELECT data FROM block_groups WHERE id = ?`, id).Row().Scan(&raw); err != nil {
		t.Fatalf("读组 %d 失败：%v", id, err)
	}
	plain, wasFrame, err := compress.DecompressBytes(raw)
	if err != nil || !wasFrame {
		t.Fatalf("组 %d 解不开（wasFrame=%v）：%v", id, wasFrame, err)
	}
	return plain
}

// firstDiff 给出第一处差异。长度对得上、只有一个字节错是最难查的一类
// ——体积、行数、长度全对——所以位置与两侧的值都要打出来。
func firstDiff(a, b []byte) string {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("首个差异在第 %d 字节（源 0x%02x / 读回 0x%02x）", i, a[i], b[i])
		}
	}
	return fmt.Sprintf("前 %d 字节相同，长度不同", n)
}
