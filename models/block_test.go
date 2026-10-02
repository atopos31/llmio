package models

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/atopos31/llmio/pkg/compress"
	"gorm.io/gorm"
)

// 块表的单测盯四件事：
//
//  1. **还原得回来**，而且是逐字节。这是整个方案的地基。
//  2. **同一份内容全局只存一份**。去重是收益的全部来源，退化成"每行各存各的"
//     就等于白干（体积还比逐行压缩更差）。
//  3. **块与行同事务**（INV-1）。行插入失败时块必须跟着回滚，否则块表会被
//     孤儿撑爆——而孤儿是无法与"还没被引用"区分的。
//  4. **落库的确实是帧**，不是被转义过的文本（陷阱 B）。

// blockCount / groupCount 直接数表，绕过任何缓存与模型层。
func blockCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	n, err := gorm.G[Block](db).Count(context.Background(), "*")
	if err != nil {
		t.Fatalf("数块表失败：%v", err)
	}
	return n
}

func groupCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	n, err := gorm.G[BlockGroup](db).Count(context.Background(), "*")
	if err != nil {
		t.Fatalf("数组表失败：%v", err)
	}
	return n
}

// putGet 走一遍完整的存/取，并断言逐字节一致。
func putGet(t *testing.T, db *gorm.DB, s *BlockStore, plain []byte) []uint32 {
	t.Helper()
	ctx := context.Background()
	refs, err := s.Put(ctx, db, plain)
	if err != nil {
		t.Fatalf("Put 失败：%v", err)
	}
	back, err := s.Get(ctx, db, refs)
	if err != nil {
		t.Fatalf("Get 失败：%v", err)
	}
	if !bytes.Equal(back, plain) {
		t.Fatalf("还原不一致：%d 字节 vs %d 字节", len(back), len(plain))
	}
	return refs
}

// 主体：多行长文本（含非法 UTF-8 与内嵌 NUL），存进去再取回来必须分毫不差。
func TestBlockStore_RoundTrip(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(1 << 20)
	ctx := context.Background()

	// 非法 UTF-8 与内嵌 0x00 是刻意加的：body 列里真有二进制 base64 之外的脏字节，
	// 而 0x00 又恰好是帧 magic 的首字节，是最容易出事的一类输入。
	payloads := [][]byte{
		[]byte(""),
		[]byte("x"),
		[]byte(strings.Repeat("hello world ", 100)),
		[]byte(fmt.Sprintf("%s\x00\xff\xfe%s", bigText(20), strings.Repeat("尾巴", 900))),
		[]byte(strings.Repeat(bigText(50), 30)),
	}
	for i, p := range payloads {
		refs, err := s.Put(ctx, db, p)
		if err != nil {
			t.Fatalf("第 %d 份 Put 失败：%v", i, err)
		}
		if len(p) == 0 {
			if refs != nil {
				t.Fatalf("空 body 应当没有引用，得到 %d 条", len(refs))
			}
			continue
		}
		if len(refs) == 0 {
			t.Fatalf("第 %d 份 body 非空却没有引用", i)
		}
		back, err := s.Get(ctx, db, refs)
		if err != nil {
			t.Fatalf("第 %d 份 Get 失败：%v", i, err)
		}
		if !bytes.Equal(back, p) {
			t.Fatalf("第 %d 份还原不一致：%d 字节 vs %d 字节", i, len(back), len(p))
		}
	}
}

// 去重：第 N 轮请求体 = 第 N−1 轮 + 尾部增量，多存几轮，块表不该跟着线性涨。
func TestBlockStore_DedupsAcrossRows(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(0) // 关缓存：这条测的是块表，不是缓存
	ctx := context.Background()

	base := strings.Repeat(`{"role":"user","content":"请解释这段代码的行为与边界条件。"}`, 400)
	var (
		refsAll [][]uint32
		last    = base
	)
	for i := 0; i < 8; i++ {
		last = last + fmt.Sprintf("\n{\"turn\":%d, \"content\":\"第 %d 轮的增量\"}", i, i)
		refs, err := s.Put(ctx, db, []byte(last))
		if err != nil {
			t.Fatalf("第 %d 轮 Put 失败：%v", i, err)
		}
		refsAll = append(refsAll, refs)
		back, err := s.Get(ctx, db, refs)
		if err != nil {
			t.Fatalf("第 %d 轮 Get 失败：%v", i, err)
		}
		if !bytes.Equal(back, []byte(last)) {
			t.Fatalf("第 %d 轮还原不一致", i)
		}
	}

	distinct := map[uint32]bool{}
	total := 0
	for _, r := range refsAll {
		for _, id := range r {
			distinct[id] = true
			total++
		}
	}
	// 8 轮里前 7 轮的正文是完全重叠的：总引用数应当远多于唯一块数，
	// 而且唯一块数不应随轮数线性增长（那说明去重根本没生效）。
	if len(distinct) >= total {
		t.Fatalf("引用 %d 条、唯一块 %d 个：完全没有去重", total, len(distinct))
	}
	if got := blockCount(t, db); int(got) != len(distinct) {
		t.Fatalf("块表 %d 行，引用里出现 %d 个不同块 id：两者必须相等", got, len(distinct))
	}
	// 最后一轮的引用应当绝大部分是前三轮已经写过的老块。
	lastRefs := refsAll[len(refsAll)-1]
	fresh := 0
	for _, id := range lastRefs {
		if !sawIn(refsAll[:len(refsAll)-1], id) {
			fresh++
		}
	}
	if fresh > 4 {
		t.Fatalf("最后一轮新增了 %d 个块（共 %d 条引用）——尾部增量不该产生这么多新块",
			fresh, len(lastRefs))
	}
}

func sawIn(all [][]uint32, id uint32) bool {
	for _, r := range all {
		for _, x := range r {
			if x == id {
				return true
			}
		}
	}
	return false
}

// 幂等：同一份内容存两遍，块表一行都不该多。
func TestBlockStore_PutIsIdempotent(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(0)
	ctx := context.Background()
	body := []byte(strings.Repeat(bigText(30), 20))

	first, err := s.Put(ctx, db, body)
	if err != nil {
		t.Fatalf("第一次 Put 失败：%v", err)
	}
	before := blockCount(t, db)
	second, err := s.Put(ctx, db, body)
	if err != nil {
		t.Fatalf("第二次 Put 失败：%v", err)
	}
	if after := blockCount(t, db); after != before {
		t.Fatalf("重复写入让块表从 %d 涨到 %d", before, after)
	}
	if len(first) != len(second) {
		t.Fatalf("两次引用数不同：%d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("第 %d 条引用不同：%d vs %d", i, first[i], second[i])
		}
	}
}

// INV-1：行事务回滚，块必须跟着回滚。
//
// 这条不成立的话，任何一次失败的请求都会在块表里留下永远没人引用的孤儿，
// 而孤儿和"已被未提交事务写入的块"在库面上长得一模一样，事后无从分辨。
func TestBlockStore_RollsBackWithTransaction(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(0)
	ctx := context.Background()

	boom := errors.New("行插入失败")
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.Put(ctx, tx, []byte(strings.Repeat(bigText(30), 20))); err != nil {
			return err
		}
		if blockCount(t, tx) == 0 {
			t.Fatal("事务内应当已经写进了块")
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("期望事务因 %v 回滚，实际 %v", boom, err)
	}
	if n := blockCount(t, db); n != 0 {
		t.Fatalf("事务回滚后块表还剩 %d 行——块没有跟行同事务（INV-1 破了）", n)
	}
	if n := groupCount(t, db); n != 0 {
		t.Fatalf("事务回滚后组表还剩 %d 行", n)
	}
}

// 落库的组必须是**真帧**，不是被 JSON 转义过的文本（陷阱 B）。
func TestBlockStore_GroupsAreFramesInDB(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(0)
	putGet(t, db, s, []byte(strings.Repeat(bigText(40), 25)))

	// 字段名要能被 GORM 映射回列名（RawLen → raw_len），否则扫出来恒为零。
	var rows []struct {
		ID     uint
		Data   []byte
		RawLen int
	}
	if err := db.Raw(`SELECT id, data, raw_len FROM block_groups`).Scan(&rows).Error; err != nil {
		t.Fatalf("读组表失败：%v", err)
	}
	if len(rows) == 0 {
		t.Fatal("组表是空的")
	}
	for _, r := range rows {
		if !compress.LooksLikeFrame(r.Data) {
			t.Fatalf("组 %d 落库的不是帧（前 4 字节 %x）", r.ID, r.Data[:min(4, len(r.Data))])
		}
		if got := compress.Type(r.Data[6]); got != compress.TypeGroup {
			t.Fatalf("组 %d 的类型是 %s，应为 group", r.ID, got)
		}
		plain, wasFrame, err := compress.DecompressBytes(r.Data)
		if err != nil || !wasFrame {
			t.Fatalf("组 %d 解不开：%v", r.ID, err)
		}
		if len(plain) != r.RawLen {
			t.Fatalf("组 %d 解出 %d 字节，raw_len 记的是 %d", r.ID, len(plain), r.RawLen)
		}
	}
}

// 组超过 BlockGroupTarget 要拆开，别攒成一个巨大的 BLOB——
// 那会让"随机读只解一组"的代价失去上界。
func TestBlockStore_SplitsLargeGroups(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(0)
	// 随机内容不可压，能确保原始字节真的超过一个组的目标。
	body := randomBytes(3 * BlockGroupTarget)
	putGet(t, db, s, body)
	if n := groupCount(t, db); n < 2 {
		t.Fatalf("3×目标大小的 body 只写了 %d 组，没有按 BlockGroupTarget 拆开", n)
	}
	var maxRaw int
	if err := db.Raw(`SELECT COALESCE(MAX(raw_len),0) FROM block_groups`).Scan(&maxRaw).Error; err != nil {
		t.Fatalf("读组大小失败：%v", err)
	}
	// 一块最大 avg*4 = 16 KiB，所以一组的原始字节不会超过目标 + 一块。
	if maxRaw > BlockGroupTarget+BlockChunkAvg*4 {
		t.Fatalf("最大的组有 %d 原始字节，超出目标 %d 太多", maxRaw, BlockGroupTarget)
	}
}

// 引用指向不存在的块必须**明确报错**，不能悄悄返回半截数据。
func TestBlockStore_DanglingRefErrors(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(0)
	ctx := context.Background()
	if _, err := s.Get(ctx, db, []uint32{999999}); err == nil {
		t.Fatal("引用指向不存在的块，Get 却成功了")
	}
	// 组被删掉（GC 出错）也要报错，而不是拼出短一截的 body。
	refs := putGet(t, db, s, []byte(strings.Repeat(bigText(30), 20)))
	if err := db.Exec(`DELETE FROM block_groups`).Error; err != nil {
		t.Fatalf("删组失败：%v", err)
	}
	fresh := NewBlockStore(0) // 换个没缓存的实例，避免旧缓存把删掉的组挡住
	if _, err := fresh.Get(ctx, db, refs); err == nil {
		t.Fatal("组已被删，Get 却成功了")
	}
}

// ── 引用序列编码 ──────────────────────────────────────────────────────────

func TestEncodeRefs_RoundTrip(t *testing.T) {
	for _, refs := range [][]uint32{
		nil,
		{},
		{0},
		{1, 2, 3},
		{0xFFFFFFFF, 0, 1 << 31},
	} {
		frame := EncodeRefs(refs)
		if len(refs) == 0 {
			if frame == nil {
				continue // 空序列允许编码成 nil
			}
		}
		got, err := DecodeRefs(frame)
		if err != nil {
			t.Fatalf("解引用失败：%v", err)
		}
		if len(got) != len(refs) {
			t.Fatalf("引用数 %d，解出 %d", len(refs), len(got))
		}
		for i := range refs {
			if got[i] != refs[i] {
				t.Fatalf("第 %d 条引用 %d，解出 %d", i, refs[i], got[i])
			}
		}
	}
}

// 明文（历史行）不能被当成引用序列；坏帧必须报错而不是回退。
func TestDecodeRefs_RejectsPlaintextAndCorrupt(t *testing.T) {
	if _, err := DecodeRefs([]byte(`{"model":"claude-3-5-sonnet"}`)); !errors.Is(err, compress.ErrNotFrame) {
		t.Fatalf("明文应当报 ErrNotFrame，得到 %v", err)
	}
	frame := EncodeRefs([]uint32{1, 2, 3})
	broken := append([]byte(nil), frame...)
	broken[5] = 99 // 未知编解码器：这是"我们的帧但解不了"，必须报错
	if _, err := DecodeRefs(broken); !errors.Is(err, compress.ErrCorrupt) {
		t.Fatalf("未知编解码器应当报 ErrCorrupt，得到 %v", err)
	}
	// 引用序列的长度必须是 4 的倍数，否则会解出一串错位的 id 去查一堆不相干的块。
	// 手搓一个长度 5 的载荷来喂这条检查（正常的编码器产不出这种帧）。
	odd := compress.Marshal(compress.Frame{
		Codec: compress.CodecRaw, Type: compress.TypeBlockRefs, RawLen: 5, Payload: []byte("abcde"),
	})
	if _, err := DecodeRefs(odd); !errors.Is(err, compress.ErrCorrupt) {
		t.Fatalf("长度不是 4 的倍数的引用序列应当报 ErrCorrupt，得到 %v", err)
	}
}

// ── 组缓存 ────────────────────────────────────────────────────────────────

func TestGroupCache_LRUAndStats(t *testing.T) {
	c := newGroupCache(250)
	data := func(n int, b byte) []byte { return bytes.Repeat([]byte{b}, n) }
	c.put(1, data(100, 'a'))
	c.put(2, data(100, 'b'))
	if _, ok := c.get(1); !ok {
		t.Fatal("刚放进去的组取不到")
	}
	c.put(3, data(100, 'c')) // 300 字节超过 250 的容量，触发淘汰
	if _, ok := c.get(2); ok {
		t.Fatal("最久未用的组没有被淘汰")
	}
	if _, ok := c.get(1); !ok {
		t.Fatal("刚访问过的组被误淘汰")
	}
	hits, misses := c.stats()
	if hits != 2 || misses != 1 {
		t.Fatalf("命中 %d 次 / 未命中 %d 次，期望 2 / 1", hits, misses)
	}
}

func TestGroupCache_Disabled(t *testing.T) {
	for _, c := range []*groupCache{nil, newGroupCache(0), newGroupCache(-1)} {
		c.put(1, []byte("x"))
		if _, ok := c.get(1); ok {
			t.Fatal("关掉的缓存不该命中")
		}
		if hits, misses := c.stats(); hits != 0 || misses != 0 {
			t.Fatalf("关掉的缓存不该计数：%d / %d", hits, misses)
		}
	}
}

// 单个组比容量还大时不能死循环，也不能把缓存撑爆。
func TestGroupCache_OversizeEntryDoesNotLoop(t *testing.T) {
	c := newGroupCache(10)
	c.put(1, bytes.Repeat([]byte{'x'}, 100))
	if c.cur > c.cap {
		t.Fatalf("缓存占用 %d 超过容量 %d", c.cur, c.cap)
	}
}

// 缓存反复读同一个组，必须拿到同一份字节（不是被谁改写过的）。
func TestGroupCache_StableUnderConcurrency(t *testing.T) {
	c := newGroupCache(1 << 20)
	want := []byte("块组内容")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				id := uint32((i + j) % 4)
				c.put(id, want)
				if got, ok := c.get(id); ok && !bytes.Equal(got, want) {
					t.Errorf("组 %d 的内容被改写了", id)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// randomBytes 造不可压的字节。用固定种子的 LCG 而不是 math/rand——
// 这里要的是"内容不重复以便块真的切得开"，不需要统计性质。
func randomBytes(n int) []byte {
	out := make([]byte, n)
	var x uint64 = 0x2545F4914F6CDD1D
	for i := range out {
		x = x*6364136223846793005 + 1442695040888963407
		out[i] = byte(x >> 33)
	}
	return out
}

// ── 批量写入（历史行迁移走的就是这条路）────────────────────────────────────

// distinctRow 造第 i 行的内容：n 字节不可压数据，掺上序号。
//
// 必须每行都不一样：randomBytes 是固定种子的，同一个 n 调两次拿到的是**同一份
// 内容**，那样多行会互相去重成一块，"一行一个组"就测不出来了。差异散布在整行上
// （步长取质数，避免跟分块节奏共振），保证每一块都不同。
func distinctRow(i, n int) []byte {
	out := randomBytes(n)
	for j := 0; j < n; j += 977 {
		out[j] ^= byte(i*31 + j)
	}
	return out
}

// 这一条是阶段 3 真机发现（线上组平均只有 **7.70 KiB**，按 256 KiB 打包能再省
// **35.9%**）在单测里的缩影：**逐行写是一行一个组，批量写是一批几个组。**
//
// 组越小，每组 16 字节帧头的占比越高，组间的短程重复也越吃不到。历史行迁移
// 必须走批量那条，否则迁完还是这个 7.70 KiB 的形态，等于白迁。
func TestBlockStore_PutBatchPacksInsteadOfPerRow(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(0)
	ctx := context.Background()

	const rows, each = 8, 100 << 10

	// 逐行写：每行各封各的组
	for i := range rows {
		putGet(t, db, s, distinctRow(i, each))
	}
	perRow := groupCount(t, db)
	if perRow != rows {
		t.Fatalf("逐行写 %d 行应当是 %d 个组，得到 %d——分组规则变了？", rows, rows, perRow)
	}

	// 同样规模、不同内容，一次批量写
	batch := make([][]byte, rows)
	for i := range batch {
		batch[i] = distinctRow(100+i, each)
	}
	refs, err := s.PutBatch(ctx, db, batch)
	if err != nil {
		t.Fatalf("PutBatch 失败：%v", err)
	}
	if len(refs) != rows {
		t.Fatalf("返回 %d 行的引用，输入是 %d 行", len(refs), rows)
	}

	batched := groupCount(t, db) - perRow
	// 8 × 100 KiB = 800 KiB，按 256 KiB 封口至少 4 个、最多 5 个
	if batched > int64(rows*each/BlockGroupTarget)+1 {
		t.Fatalf("批量写 %d 行用了 %d 个组，按 %s 封口不该超过 %d 个",
			rows, batched, humanBytes(BlockGroupTarget), rows*each/BlockGroupTarget+1)
	}
	if batched >= perRow {
		t.Fatalf("批量 %d 个组、逐行 %d 个组——批量没有起到打包作用", batched, perRow)
	}

	// 每一行都要能原样取回来
	for i, b := range batch {
		back, err := s.Get(ctx, db, refs[i])
		if err != nil {
			t.Fatalf("第 %d 行取回失败：%v", i, err)
		}
		if !bytes.Equal(back, b) {
			t.Fatalf("第 %d 行还原不一致：%d 字节 vs %d 字节", i, len(back), len(b))
		}
	}
}

// 空行必须保持下标对齐：返回的引用序列与输入一一对应，否则调用方会把 A 行的
// 引用写到 B 行身上——那是静默的数据错位，两条路径都不会报错。
//
// 注意"分不出块"这件事在这一层不存在：CutPoints 对任何非空输入都会切出至少
// 一块（末尾那一刀是补的）。"小于 4 KiB 就不进块表"是钩子层（inputBlockMin）
// 的决定，不是存储层的。
func TestBlockStore_PutBatchKeepsAlignment(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(0)
	ctx := context.Background()

	a := distinctRow(21, 64<<10)
	b := distinctRow(22, 40<<10)
	plains := [][]byte{nil, a, {}, b, nil}

	refs, err := s.PutBatch(ctx, db, plains)
	if err != nil {
		t.Fatalf("PutBatch 失败：%v", err)
	}
	if len(refs) != len(plains) {
		t.Fatalf("返回 %d 条，输入 %d 条", len(refs), len(plains))
	}
	for i, want := range []bool{false, true, false, true, false} {
		if got := len(refs[i]) > 0; got != want {
			t.Fatalf("第 %d 行（%d 字节）该不该有引用：期望 %v，得到 %v",
				i, len(plains[i]), want, got)
		}
	}
	// 两个大行各取一次、分别比对：引用一旦错位，这里必炸
	for _, tc := range []struct {
		i    int
		want []byte
	}{{1, a}, {3, b}} {
		back, err := s.Get(ctx, db, refs[tc.i])
		if err != nil {
			t.Fatalf("第 %d 行取回失败：%v", tc.i, err)
		}
		if !bytes.Equal(back, tc.want) {
			t.Fatalf("第 %d 行的引用指向了别的行：取回 %d 字节，该行是 %d 字节",
				tc.i, len(back), len(tc.want))
		}
	}
}

// 一批里两行内容相同：只该插一份块，两行的引用序列完全相同。
//
// 这是批量相对逐行多出来的那份收益（批内去重），也是迁移里最常见的情形——
// 历史行彼此高度重叠。
func TestBlockStore_PutBatchDedupsWithinBatch(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(0)
	ctx := context.Background()

	one := distinctRow(3, 80<<10)
	refs, err := s.PutBatch(ctx, db, [][]byte{one, one, distinctRow(4, 80<<10)})
	if err != nil {
		t.Fatalf("PutBatch 失败：%v", err)
	}
	if len(refs[0]) != len(refs[1]) {
		t.Fatalf("两行内容相同，引用数却不同：%d vs %d", len(refs[0]), len(refs[1]))
	}
	for i := range refs[0] {
		if refs[0][i] != refs[1][i] {
			t.Fatalf("第 %d 条引用不同：%d vs %d", i, refs[0][i], refs[1][i])
		}
	}
	// 块表行数 = 两行内容合起来的唯一块数，不能是两份
	var want int64
	seen := map[uint32]bool{}
	for _, r := range refs {
		for _, id := range r {
			if !seen[id] {
				seen[id] = true
				want++
			}
		}
	}
	if got := blockCount(t, db); got != want {
		t.Fatalf("块表 %d 行，引用里只出现 %d 个不同块——批内没有去重", got, want)
	}
}

// INV-1 对批量路径同样成立：事务回滚，这一批写的块与组一个都不许留。
func TestBlockStore_PutBatchRollsBackWithTransaction(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(0)
	ctx := context.Background()

	batch := make([][]byte, 6)
	for i := range batch {
		batch[i] = distinctRow(200+i, 60<<10)
	}

	boom := errors.New("这一批里有一行写失败")
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.PutBatch(ctx, tx, batch); err != nil {
			return err
		}
		if blockCount(t, tx) == 0 {
			t.Fatal("事务内应当已经写进了块")
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("期望事务因 %v 回滚，实际 %v", boom, err)
	}
	if n := blockCount(t, db); n != 0 {
		t.Fatalf("事务回滚后块表还剩 %d 行（INV-1 破了）", n)
	}
	if n := groupCount(t, db); n != 0 {
		t.Fatalf("事务回滚后组表还剩 %d 行", n)
	}
}

// 单行 Put 必须就是"一批只有一行"的 PutBatch，两条路的引用序列要逐个相同。
//
// 这条守的是**线上写路径没有被批量那条路带偏**：线上每个请求一次 Put，
// 它打出来的 7.70 KiB 小组是阶段 3 真机量过的既成事实，不能被顺手改掉。
func TestBlockStore_PutMatchesPutBatchOfOne(t *testing.T) {
	db := openCompressDB(t)
	s := NewBlockStore(0)
	ctx := context.Background()

	body := distinctRow(9, 120<<10)
	putRefs := putGet(t, db, s, body)
	batchRefs, err := s.PutBatch(ctx, db, [][]byte{body})
	if err != nil {
		t.Fatalf("PutBatch 失败：%v", err)
	}
	if len(putRefs) != len(batchRefs[0]) {
		t.Fatalf("两条路的引用数不同：%d vs %d", len(putRefs), len(batchRefs[0]))
	}
	for i := range putRefs {
		if putRefs[i] != batchRefs[0][i] {
			t.Fatalf("第 %d 条引用不同：%d vs %d", i, putRefs[i], batchRefs[0][i])
		}
	}
}
