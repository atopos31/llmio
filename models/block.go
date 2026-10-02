package models

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/atopos31/llmio/pkg/compress"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 块表：内容寻址的跨行去重。
//
// 为什么需要它：编码类 agent 每轮把整段对话历史原样重发，`input` 列里 97% 的
// 字节是同一批内容重复出现了几百遍（实测 5.64 GiB → 唯一内容 192 MiB）。
// 省字节的唯一办法不是"压得更狠"，而是"同一段内容只存一份"。
//
// 与逐行压缩的本质区别：逐行压缩每行是一个独立的压缩窗口，看不见别的行，
// 重复内容一份都省不到。实测逐行最强也只到 3.83x，而这里是 94.9x（见
// docs/db-compression-phase0.md）。
const (
	// BlockGroupTarget 是一组攒到多少原始字节就封口。256 KiB 是实测选的：
	// 它比 64 KiB 省 11% 的体积，比 1 MiB 读起来快 73%（读 P95 22.6ms vs 39.2ms）。
	BlockGroupTarget = 256 << 10
	// BlockChunkAvg 是内容定义分块的平均块大小。
	// **这个值一旦有数据落库就不能改**：改了切点全错位，块表当场作废。
	BlockChunkAvg = compress.DefaultChunkAvg
)

// Block 是一个唯一块。同一份内容全局只有一行。
type Block struct {
	ID uint `gorm:"primarykey"`
	// Digest 是 SHA-256 截断到 128 位的内容标识。唯一索引既是去重的依据，
	// 也是并发写入的安全网：两个事务同时插入同一个块时，后来的那个撞索引，
	// 回查即可，不会写出两份。
	Digest  []byte `gorm:"uniqueIndex;size:16"`
	Size    int
	GroupID uint `gorm:"index"` // 所在的组
	Off     int  // 在组内的偏移
}

// BlockGroup 是一批块拼起来压成的一个帧。
//
// 为什么块要攒成组再压，而不是每块各压各的：块与块之间还有短程重复
// （同一段 JSON 骨架的变体），逐块各压就吃不到；而攒成一组一个帧之后，
// 随机读也只需要解一组，不必解全部。
//
// 组是**不可变**的：写下去就不再改。压实（CompactGroups）会另建新组，
// 旧组留到 GC 确认没人引用为止。这样并发读要么看到旧组、要么看到新组，
// 不会读到改到一半的东西。
type BlockGroup struct {
	ID     uint `gorm:"primarykey"`
	Data   []byte
	RawLen int
}

func (BlockGroup) TableName() string { return "block_groups" }

// ── 引用序列 ──────────────────────────────────────────────────────────────

// EncodeRefs 把引用序列编成可以直接落库的帧。
//
// 存的是块 id 而不是摘要：一行平均 92.7 条引用，id 是 4 字节、摘要 16 字节，
// 差了 4 倍。id 只在库内有意义，而引用本来就只在库内用。
//
// 自检不过返回 nil（而不是半个帧），调用方据此退回明文存储——
// 与 compress 包"压不了就存明文"的规矩一致。
func EncodeRefs(refs []uint32) []byte {
	buf := make([]byte, 4*len(refs))
	for i, r := range refs {
		binary.LittleEndian.PutUint32(buf[4*i:], r)
	}
	frame, ok := compress.EncodeVerified(buf, compress.TypeBlockRefs, compress.LevelWrite)
	if !ok {
		return nil
	}
	return frame
}

// DecodeRefs 解出引用序列。非帧返回 compress.ErrNotFrame，坏帧返回 compress.ErrCorrupt。
func DecodeRefs(frame []byte) ([]uint32, error) {
	f, err := compress.Unmarshal(frame)
	if err != nil {
		return nil, err
	}
	return DecodeRefsFrame(f)
}

// DecodeRefsFrame 是已经拆过帧头的版本。读路径先看帧头再决定怎么办，
// 这里省掉一次重复的拆头。
func DecodeRefsFrame(f compress.Frame) ([]uint32, error) {
	if f.Type != compress.TypeBlockRefs {
		return nil, fmt.Errorf("%w: 拿 %s 帧当引用序列解", compress.ErrCorrupt, f.Type)
	}
	plain, err := compress.Decompress(f)
	if err != nil {
		return nil, err
	}
	if len(plain)%4 != 0 {
		return nil, fmt.Errorf("%w: 引用序列长度 %d 不是 4 的倍数", compress.ErrCorrupt, len(plain))
	}
	refs := make([]uint32, len(plain)/4)
	for i := range refs {
		refs[i] = binary.LittleEndian.Uint32(plain[4*i:])
	}
	return refs, nil
}

// ── 组缓存 ────────────────────────────────────────────────────────────────

// groupCache 缓存**解压后的**组。
//
// 它不是可选项，是读路径能不能用的关键：Phase 0 实测，顺序浏览一个会话
// （日志界面真正在做的事）开 32 MiB 缓存命中率 98.7%、P50 延迟直接归零；
// 关掉缓存则每行都要重新解 2.9–6.4 MiB。
//
// 但要有清醒认识：随机访问时它只覆盖得了一半（命中 47–49%），因为组表本身
// 就有 57–64 MiB。**它保的是顺序浏览，不是随机访问。**
type groupCache struct {
	mu    sync.Mutex
	cap   int
	cur   int
	items map[uint32]*groupCacheEnt
	// 手写双向链表，不用 container/list：这里的热路径在锁内，
	// 少一层接口断言与分配。节点跟着条目一起回收。
	head, tail *groupCacheEnt
	hits       int64
	misses     int64
}

type groupCacheEnt struct {
	id         uint32
	data       []byte
	prev, next *groupCacheEnt
}

func newGroupCache(capBytes int) *groupCache {
	if capBytes <= 0 {
		return nil
	}
	return &groupCache{cap: capBytes, items: make(map[uint32]*groupCacheEnt)}
}

func (c *groupCache) get(id uint32) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[id]
	if !ok {
		c.misses++
		return nil, false
	}
	c.hits++
	c.moveToFront(e)
	return e.data, true
}

func (c *groupCache) put(id uint32, data []byte) {
	if c == nil || c.cap <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[id]; ok {
		return
	}
	e := &groupCacheEnt{id: id, data: data}
	c.items[id] = e
	c.pushFront(e)
	c.cur += len(data)
	for c.cur > c.cap && c.tail != nil {
		old := c.tail
		c.unlink(old)
		delete(c.items, old.id)
		c.cur -= len(old.data)
	}
}

func (c *groupCache) pushFront(e *groupCacheEnt) {
	e.prev, e.next = nil, c.head
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
}

func (c *groupCache) unlink(e *groupCacheEnt) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		c.tail = e.prev
	}
	e.prev, e.next = nil, nil
}

func (c *groupCache) moveToFront(e *groupCacheEnt) {
	if c.head == e {
		return
	}
	c.unlink(e)
	c.pushFront(e)
}

func (c *groupCache) stats() (hits, misses int64) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}

// ── 存储层 ────────────────────────────────────────────────────────────────

// BlockStore 把 body 存成"块表 + 引用序列"，并负责还原。
//
// 它必须拿得到 **gorm 的事务句柄**：写块与写行要在同一个事务里，
// 否则行插入失败时块会变成孤儿。这正是本方案不能用 GORM serializer 的原因
// （serializer 的接口里根本没有 DB 句柄）。
type BlockStore struct {
	cacheBytes int
	cache      *groupCache
}

// NewBlockStore 造一个带组缓存的存储层。cacheBytes<=0 表示不要缓存。
func NewBlockStore(cacheBytes int) *BlockStore {
	return &BlockStore{cacheBytes: cacheBytes, cache: newGroupCache(cacheBytes)}
}

// Reset 丢掉整个组缓存。
//
// **换库必须调用它。** 缓存只按组 id 索引，而组 id 只在单个库文件内有意义：
// 另一个库的第 1 组是完全不同的内容。库一换，缓存里的每一条都可能对错门。
//
// 生产上库不会中途换，所以这条主要服务于两件事：进程启动（models.Init，
// 可能是从一个备份恢复的库，rowid 回退了），以及将来重建存储层的那一步
// （组会被重新编号）。
func (s *BlockStore) Reset() {
	s.cache = newGroupCache(s.cacheBytes)
}

// Put 把 plain 分块、查表、把新块写进新组，返回这一行的引用序列。
//
// tx 必须是调用方正在用的那个事务。全程不另开连接——那会撞事务锁，
// 卡满 busy_timeout 之后报 database is locked。
func (s *BlockStore) Put(ctx context.Context, tx *gorm.DB, plain []byte) ([]uint32, error) {
	if len(plain) == 0 {
		return nil, nil
	}
	cuts := compress.CutPoints(plain, BlockChunkAvg, nil)
	if len(cuts) == 0 {
		return nil, nil
	}

	// 先把这一行的块都点出来：digest 与内容各留一份。
	digests := make([][16]byte, 0, len(cuts))
	chunks := make([][]byte, 0, len(cuts))
	prev := 0
	for _, cut := range cuts {
		ch := plain[prev:cut]
		prev = cut
		digests = append(digests, compress.Digest(ch))
		chunks = append(chunks, ch)
	}

	known, err := lookupBlocks(ctx, tx, digests)
	if err != nil {
		return nil, err
	}

	// 把还不存在的块攒成组写下去。组的边界按首次出现顺序切，
	// 与 Phase 0 基准车、与迁移路径完全一致——组归属由这个顺序决定。
	var (
		missing []blockPending
		seen    = make(map[[16]byte]bool, len(digests))
	)
	for i, d := range digests {
		if _, ok := known[d]; ok || seen[d] {
			continue
		}
		seen[d] = true
		missing = append(missing, blockPending{digest: d, data: chunks[i]})
	}
	if len(missing) > 0 {
		if err := insertBlockGroups(ctx, tx, missing); err != nil {
			return nil, err
		}
		// 回查拿 id：新插入的行在这一刻还不知道自己的 id，
		// 而并发写入（另一个进程刚插过同一个块）也只有回查才能拿到确定的结果。
		got, err := lookupBlocks(ctx, tx, digestsOf(missing))
		if err != nil {
			return nil, err
		}
		for d, loc := range got {
			known[d] = loc
		}
	}

	refs := make([]uint32, len(digests))
	for i, d := range digests {
		loc, ok := known[d]
		if !ok {
			return nil, fmt.Errorf("compress: 块 %x 写完后仍查不到，拒绝写入悬空引用", d[:4])
		}
		refs[i] = loc.id
	}
	return refs, nil
}

type blockLoc struct {
	id      uint32
	groupID uint32
	off     int
	size    int
}

// freshDB 把调用方给的句柄换成一个**干净 statement** 的同款句柄。
//
// 为什么块表的每一次查询都必须先过这一道：钩子拿到的 tx 是"正在插入 chat_ios"
// 的那个 statement，它的 Table 已经被外层的插入钉成 `chat_ios` 了。而 GORM 的
// `Statement.Parse` 是这么写的——
//
//	if stmt.Schema, err = schema.Parse(...); err == nil && stmt.Table == "" { stmt.Table = ... }
//
// 也就是说 Table 非空时它就**不推表名**。直接拿钩子的 tx 去 Model(&Block{})，
// 生成的 SQL 会是 `SELECT * FROM chat_ios WHERE digest IN (...)`，报
// "no such column: digest"。
//
// 这个 bug 只有在真正的钩子路径上才会出现：直接拿一个干净的 *gorm.DB 调用
// 存储层是看不出来的（Table 恰好是空的）。所以存储层自己不假定句柄干净。
//
// Session(NewDB) 换掉的只是 statement：ConnPool 与 Context 都照旧，
// 所以"块与行同事务"（INV-1）和"事务里读得到自己的未提交数据"都还在。
func freshDB(tx *gorm.DB, ctx context.Context) *gorm.DB {
	if ctx != nil {
		tx = tx.WithContext(ctx)
	}
	return tx.Session(&gorm.Session{NewDB: true})
}

// blockPending 是"这一行产生、库里还没有"的块，等着攒组落库。
type blockPending struct {
	digest [16]byte
	data   []byte
}

func digestsOf(ps []blockPending) [][16]byte {
	out := make([][16]byte, len(ps))
	for i := range ps {
		out[i] = ps[i].digest
	}
	return out
}

// lookupBlocks 按摘要回查已存在的块。分批查，避免 IN 列表过长。
func lookupBlocks(ctx context.Context, tx *gorm.DB, digests [][16]byte) (map[[16]byte]blockLoc, error) {
	out := make(map[[16]byte]blockLoc, len(digests))
	uniq := make([][]byte, 0, len(digests))
	seen := make(map[[16]byte]bool, len(digests))
	for _, d := range digests {
		if seen[d] {
			continue
		}
		seen[d] = true
		uniq = append(uniq, d[:])
	}
	db := freshDB(tx, ctx)
	const batch = 256
	for start := 0; start < len(uniq); start += batch {
		end := min(start+batch, len(uniq))
		var rows []Block
		if err := db.Model(&Block{}).
			Where("digest IN ?", uniq[start:end]).
			Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			var d [16]byte
			if len(r.Digest) != 16 {
				return nil, fmt.Errorf("compress: 块 %d 的摘要长度是 %d，应为 16", r.ID, len(r.Digest))
			}
			copy(d[:], r.Digest)
			out[d] = blockLoc{id: uint32(r.ID), groupID: uint32(r.GroupID), off: r.Off, size: r.Size}
		}
	}
	return out, nil
}

// insertBlockGroups 把 missing 里的块攒成组写下去。
//
// 攒到现在这一批就封口，不跨调用攒——因为**写入必须与行插入同一个事务**（INV-1）。
// 跨请求攒组就意味着组要先于引用落库，事务一失败就留下一堆没人引用的组。
// 代价是线上新写的组偏小、组间冗余吃不到；压实（CompactGroups）负责事后合并。
//
// 这个代价**在真数据上量过**（阶段 3，生产库副本 12,483 行）：线上组平均只有
// **7.70 KiB**，11,725 个组共 88.21 MiB；同一批内容按 BlockGroupTarget 重打是
// 777 个组 / 56.53 MiB。**压实能再省 35.9%**——也就是说光靠线上写入是 62x，
// 压实之后才到 ~94x。
//
// 为什么是 7.70 KiB 这么小：一个请求一轮对话只新增十几 KiB 的块（其余全是
// 重复的历史），而这一批就是这一轮的全部新内容。它远低于 flate 的 32 KiB 窗口，
// 所以线上组吃不到长程匹配。**这不是可调的参数问题，是 INV-1 的直接后果**——
// 想在线攒大组就必须让组先于行落库，那就等于放弃 INV-1。所以出路只能是压实。
//
// 每组一个压缩帧，压完**立刻解回来比对**（INV-3）：组是冷数据，压一次就长期
// 躺着，多花一次解码换"压缩代码有 bug 也变不成数据损坏"，是这套方案里
// 性价比最高的一道保险。
func insertBlockGroups(ctx context.Context, tx *gorm.DB, missing []blockPending) error {
	db := freshDB(tx, ctx)
	var (
		buf     []byte
		bufOff  []int
		bufSize []int
		bufDig  [][16]byte
	)
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		frame, ok := compress.EncodeVerified(buf, compress.TypeGroup, compress.LevelGroup)
		if !ok {
			return errors.New("compress: 块组自检没过，拒绝写入")
		}
		group := BlockGroup{Data: frame, RawLen: len(buf)}
		// 组表与块表各查各的，都得用没被外层污染过的 statement。
		// 注意 flush 会被调用多次（一组写满就写下一组），每次都重新取，
		// 因为上一条 Create 已经把这条 statement 的表名钉住了。
		if err := freshDB(db, nil).Create(&group).Error; err != nil {
			return err
		}
		blocks := make([]Block, 0, len(bufDig))
		for i := range bufDig {
			blocks = append(blocks, Block{
				Digest:  bufDig[i][:],
				Size:    bufSize[i],
				GroupID: group.ID,
				Off:     bufOff[i],
			})
		}
		// 撞唯一索引说明别的进程刚插过同一个块：忽略即可，回查会拿到它。
		if err := freshDB(db, nil).Clauses(clause.OnConflict{DoNothing: true}).
			Create(&blocks).Error; err != nil {
			return err
		}
		buf, bufOff, bufSize, bufDig = nil, nil, nil, nil
		return nil
	}

	for _, m := range missing {
		if len(buf) > 0 && len(buf)+len(m.data) > BlockGroupTarget {
			if err := flush(); err != nil {
				return err
			}
		}
		bufOff = append(bufOff, len(buf))
		bufSize = append(bufSize, len(m.data))
		bufDig = append(bufDig, m.digest)
		buf = append(buf, m.data...)
	}
	return flush()
}

// Get 按引用序列还原原文。
//
// 只对"缓存里没有的组"发查询——这才是真实读路径的形状（见 Phase 0）。
// 取回后按组号排序一次，把 90 多次随机读压成近似顺序读。
func (s *BlockStore) Get(ctx context.Context, db *gorm.DB, refs []uint32) ([]byte, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	src := freshDB(db, ctx)
	locs := make([]Block, 0, len(refs))
	if err := src.Model(&Block{}).Where("id IN ?", refs).Find(&locs).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint32]Block, len(locs))
	for _, b := range locs {
		byID[uint32(b.ID)] = b
	}

	// 去重（一行平均 92.7 条引用，但只落在十几个组上）
	var (
		groups []uint32
		seen   = make(map[uint32]bool, len(refs))
	)
	for _, r := range refs {
		b, ok := byID[r]
		if !ok {
			return nil, fmt.Errorf("compress: 引用 %d 指向的块不存在（块表被删过？）", r)
		}
		gid := uint32(b.GroupID)
		if seen[gid] {
			continue
		}
		seen[gid] = true
		groups = append(groups, gid)
	}

	// resolved 是**本次调用**用到的组，它必须自己兜住数据，不能借缓存转手：
	// 缓存可以是关的（cacheBytes<=0），也可以小到刚放进去就被挤掉。缓存只做
	// 「省一次解压」的加速，不做正确性的一环——把两者混起来的话，
	// 关掉缓存就等于关掉读路径，而缓存本来就该是可选的。
	resolved := make(map[uint32][]byte, len(groups))
	var missing []uint32
	for _, g := range groups {
		if d, ok := s.cache.get(g); ok {
			resolved[g] = d
			continue
		}
		missing = append(missing, g)
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		var rows []BlockGroup
		if err := freshDB(db, ctx).Model(&BlockGroup{}).Where("id IN ?", missing).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			plain, wasFrame, err := compress.DecompressBytes(r.Data)
			if err != nil {
				return nil, fmt.Errorf("compress: 组 %d 解压失败: %w", r.ID, err)
			}
			if !wasFrame {
				return nil, fmt.Errorf("compress: 组 %d 存的不是帧", r.ID)
			}
			if len(plain) != r.RawLen {
				return nil, fmt.Errorf("compress: 组 %d 解出 %d 字节，记录的是 %d",
					r.ID, len(plain), r.RawLen)
			}
			resolved[uint32(r.ID)] = plain
			s.cache.put(uint32(r.ID), plain)
		}
		for _, g := range missing {
			if _, ok := resolved[g]; !ok {
				return nil, fmt.Errorf("compress: 组 %d 不存在（块表指向了已删的组）", g)
			}
		}
	}

	var out []byte
	for _, r := range refs {
		b := byID[r]
		gid := uint32(b.GroupID)
		grp, ok := resolved[gid]
		if !ok {
			return nil, fmt.Errorf("compress: 组 %d 没取到（引用与组表不一致）", gid)
		}
		if b.Off < 0 || b.Size < 0 || b.Off+b.Size > len(grp) {
			return nil, fmt.Errorf("compress: 块 %d 落在组 %d 的 %d..%d 之外（组长 %d）",
				b.ID, b.GroupID, b.Off, b.Off+b.Size, len(grp))
		}
		out = append(out, grp[b.Off:b.Off+b.Size]...)
	}
	return out, nil
}
