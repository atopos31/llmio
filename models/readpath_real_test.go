package models

import (
	"bytes"
	"container/list"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/atopos31/llmio/pkg/compress"
	_ "github.com/glebarez/sqlite"
)

// 阶段 0：读路径基准 —— 整个方案的**决策门**。
//
// 计划里写得明确：「这一关不过，后面全部不动。」理由是这套方案把
// 「读一行 = 一次 BLOB 读」换成了「读一行 = 取出约 87 个块，每块还得
// 解开它所在的整组」。体积上省了一个数量级，代价全在读这一侧。
//
// 与 D 盘那套旧 readpath.exe 相比，这版有三处不同，缺任何一条结论都不作数：
//
//  1. 用**生产分块器**（pkg/compress 的 gear 表 + CutPoints + SHA-256 截断），
//     不是探针版的 cdcCut/fnv64。切点规则不同，块数与读放大就不是同一回事。
//  2. 组是真**写进 SQLite 文件再读回来**的（真实的页缓存、真实的随机读）。
//     旧版把组揣在 Go 内存里，I/O 那一面被整个抹掉了。
//  3. 每一行都**与源库里的原字节逐字节比对**。读得快但读错了毫无意义。
//
// 诚实的边界，写在结论里也写在脸上：
//   - 这份语料只有 12,483 行，压出来的组表 55–64 MiB，会被操作系统文件缓存
//     整份装下。所以下面的耗时是**热缓存下的下界**，冷盘要另算。
//   - 与磁盘无关的两个数——「命中组数」和「解压字节」——才是硬结论：
//     前者就是这一行要发多少次随机读，后者就是这一行的 CPU 代价。
//   - **两个容量口径必须分开看**，混着报会得出相反的结论。这里两个都打：
//     口径 A 是唯一内容原字节（未压、未计引用），口径 B 是真正落库的字节
//     （组帧 + 引用帧 + 块表）。计划里那句"input 列 61.84 MiB（91x）"说的是 B。
//
// 抽样规模：随机读与顺序读各抽多少行。这两条曲线要跑 3 档分组 × 开/关缓存，
// 无缓存那几轮每行都要解 5–30 MiB，再大就跑不完了。P95 用这个量级足够稳。
const (
	rpRandRows = 200
	rpSeqRows  = 300
	// rpBlockRowBytes 是块表每块的开销（块 id + 摘要 + 长度 + 元数据）。
	// 沿用探针报告里的 40 B 估算，**是估的**：这个常数翻一倍也只多约 1.3 MiB，
	// 不影响任何结论。
	rpBlockRowBytes = 40
)

func TestReadPathRealDatabase(t *testing.T) {
	src := realDBCopy(t)
	scratchDir := filepath.Dir(src)

	idx := buildRPBlockIndex(t, src)
	avgRefs := float64(idx.nRefs) / float64(len(idx.rowIDs))
	avgRow := float64(idx.rowBytes) / float64(len(idx.rowIDs))
	uniqBytes := int64(len(idx.uniq))
	uniqChunks := len(idx.uniqOff)
	avgChunk := int64(0)
	if uniqChunks > 0 {
		avgChunk = uniqBytes / int64(uniqChunks)
	}

	t.Logf("语料：%d 行 / %s；唯一块 %d 个 / %s（平均 %s）；引用 %d 条，平均每行 %.1f 块，平均行 %s",
		len(idx.rowIDs), humanBytes(idx.rowBytes), uniqChunks, humanBytes(uniqBytes),
		humanBytes(avgChunk), idx.nRefs, avgRefs, humanBytes(int64(avgRow)))
	// 两个口径必须分开写，混着报会得出相反的结论：这里 192 MiB 与后来的 62 MiB
	// 说的是同一件事（前者是唯一内容原字节，后者是它压完再加引用与块表）。
	t.Logf("口径 A（唯一内容，未压、未计引用）：%s → %s（%.2fx）",
		humanBytes(idx.rowBytes), humanBytes(uniqBytes),
		float64(idx.rowBytes)/float64(uniqBytes))
	t.Logf("写入侧：整库分块（滚动哈希 + %d 次摘要）共 %.1f s，摊到每行 %.2f ms —— 这是每请求新增的 CPU",
		idx.nRefs, idx.chunked.Seconds(), idx.chunked.Seconds()*1000/float64(len(idx.rowIDs)))

	// 引用序列也要落库，且同样是压缩帧（这就是 input 列将来存的东西）。
	// 它是"口径 B"里不可省的一项：1,157,485 条引用若原样存是 4.4 MiB。
	refsStored := measureRefsStored(t, idx)

	sample := rpSampleRows(len(idx.rowIDs), rpRandRows)

	t.Logf("")
	t.Logf("%-9s %-13s %7s %8s %10s %10s %10s %10s %9s",
		"组大小", "缓存", "组数", "组压后", "命中组P50", "命中组P95", "解压P50", "解压P95", "延迟P95")

	for _, size := range []int{64 << 10, 256 << 10, 1 << 20} {
		g := idx.regroup(t, size)
		var packed int64
		for _, f := range g.frames {
			packed += int64(len(f))
		}
		dbPath := filepath.Join(scratchDir, fmt.Sprintf("phase0-group-%d.db", size))
		writeRPGroups(t, dbPath, g)
		t.Cleanup(func() { _ = os.Remove(dbPath) })

		// 口径 B：真正落库多少 = 组帧 + 引用帧 + 块表行。这才是能与"现状裸存"
		// 直接相除的数，也是计划里那个 91x 的口径。
		tableEst := int64(uniqChunks) * rpBlockRowBytes
		stored := packed + refsStored + tableEst
		t.Logf("口径 B（落库）= 组压后 %s + 引用帧 %s + 块表 %s = %s ⇒ %s → %s（%.1fx，仅 input 一列）",
			humanBytes(packed), humanBytes(refsStored), humanBytes(tableEst), humanBytes(stored),
			humanBytes(idx.rowBytes), humanBytes(stored), float64(idx.rowBytes)/float64(stored))

		for _, lruBytes := range []int{0, 32 << 20} {
			r := measureRP(t, src, dbPath, idx, g, sample, lruBytes)
			label := "关"
			if lruBytes > 0 {
				label = humanBytes(int64(lruBytes))
			}
			rate := ""
			if tot := r.hit + r.miss; tot > 0 && lruBytes > 0 {
				rate = fmt.Sprintf(" 命中%.0f%%", 100*float64(r.hit)/float64(tot))
			}
			t.Logf("%-9s %-13s %7d %8s %10d %10d %10s %10s %9s%s",
				humanBytes(int64(size)), label, g.nGroups, humanBytes(packed),
				pctInt(r.groups, 0.50), pctInt(r.groups, 0.95),
				humanBytes(pctI64(r.bytesIn, 0.50)), humanBytes(pctI64(r.bytesIn, 0.95)),
				pctDur(r.lat, 0.95).Round(100*time.Microsecond), rate)
		}
	}

	// 顺序浏览：日志界面真正在做的事——顺着一个会话往下翻。相邻行的块高度重叠，
	// 缓存命中率应当很高，这正是「LRU 不是可选项」的论据。
	t.Logf("")
	t.Logf("== 顺序浏览（按 id 原序取前 %d 行，模拟顺着会话往下翻）==", rpSeqRows)
	seq := rpFirstRows(len(idx.rowIDs), rpSeqRows)
	for _, size := range []int{64 << 10, 256 << 10, 1 << 20} {
		g := idx.regroup(t, size)
		dbPath := filepath.Join(scratchDir, fmt.Sprintf("phase0-group-%d.db", size))
		for _, lruBytes := range []int{0, 32 << 20} {
			r := measureRP(t, src, dbPath, idx, g, seq, lruBytes)
			rate := "-"
			if tot := r.hit + r.miss; tot > 0 && lruBytes > 0 {
				rate = fmt.Sprintf("%.1f%%", 100*float64(r.hit)/float64(tot))
			}
			t.Logf("  组 %-7s 缓存 %-8s 命中 %-7s 命中组P50 %4d  解压P50 %9s  延迟P50 %9s  延迟P95 %9s",
				humanBytes(int64(size)), rpLRULabel(lruBytes), rate,
				pctInt(r.groups, 0.50), humanBytes(pctI64(r.bytesIn, 0.50)),
				pctDur(r.lat, 0.50).Round(100*time.Microsecond),
				pctDur(r.lat, 0.95).Round(100*time.Microsecond))
		}
	}
}

// ── 块表：扫一遍整库，攒出「唯一块顺序表」与「每行的块引用序列」 ──────────────

type rpBlockIndex struct {
	uniq     []byte  // 唯一块内容，按**首次出现**顺序拼接（分组归属由这个顺序决定）
	uniqOff  []int32 // 每块在 uniq 里的起始偏移
	uniqLen  []int32 // 每块长度
	index    map[[16]byte]int32
	rowIDs   []int64
	rowRefs  [][]int32
	rowBytes int64
	nRefs    int64
	chunked  time.Duration
}

func buildRPBlockIndex(t *testing.T, srcPath string) *rpBlockIndex {
	t.Helper()
	db := openRawSQL(t, srcPath)
	defer db.Close()

	rows, err := db.Query(`SELECT id, input FROM chat_ios WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		t.Fatalf("扫描 chat_ios 失败：%v", err)
	}
	defer rows.Close()

	idx := &rpBlockIndex{index: make(map[[16]byte]int32, 1<<16)}
	var cuts []int
	started := time.Now()
	for rows.Next() {
		var (
			id  int64
			in  []byte
			err error
		)
		if err = rows.Scan(&id, &in); err != nil {
			t.Fatalf("读第 %d 行失败：%v", len(idx.rowIDs)+1, err)
		}
		idx.rowIDs = append(idx.rowIDs, id)
		idx.rowBytes += int64(len(in))
		if len(in) == 0 {
			idx.rowRefs = append(idx.rowRefs, nil)
			continue
		}
		cuts = compress.CutPoints(in, compress.DefaultChunkAvg, cuts[:0])
		refs := make([]int32, 0, len(cuts))
		prev := 0
		for _, cut := range cuts {
			ch := in[prev:cut]
			prev = cut
			d := compress.Digest(ch)
			ci, ok := idx.index[d]
			if !ok {
				ci = int32(len(idx.uniqLen))
				idx.index[d] = ci
				idx.uniqOff = append(idx.uniqOff, int32(len(idx.uniq)))
				idx.uniqLen = append(idx.uniqLen, int32(len(ch)))
				idx.uniq = append(idx.uniq, ch...) // 拷一份：in 下一轮就被复用
			}
			refs = append(refs, ci)
		}
		idx.rowRefs = append(idx.rowRefs, refs)
		idx.nRefs += int64(len(refs))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("扫描中断：%v", err)
	}
	if len(idx.rowIDs) == 0 {
		t.Fatal("源库里没有 chat_ios 行")
	}
	idx.chunked = time.Since(started)
	return idx
}

// measureRefsStored 量的是「每行的块引用序列压成帧要多少字节」。
//
// 这就是将来 input 列里真正躺着的东西（TypeBlockRefs 帧），所以它必须算进
// 落库口径里——1,157,485 条引用原样存是 4.4 MiB，压完小得多，但不能当它不存在。
func measureRefsStored(t *testing.T, b *rpBlockIndex) int64 {
	t.Helper()
	var (
		total int64
		buf   []byte
	)
	for _, refs := range b.rowRefs {
		if len(refs) == 0 {
			continue // 空行不写帧，与 serializer 的空值语义一致
		}
		buf = buf[:0]
		for _, r := range refs {
			buf = binary.LittleEndian.AppendUint32(buf, uint32(r))
		}
		frame, ok := compress.EncodeVerified(buf, compress.TypeBlockRefs, compress.LevelWrite)
		if !ok {
			t.Fatal("引用序列自检没过")
		}
		total += int64(len(frame))
	}
	return total
}

// ── 分组：与 fulldedup 同规则（顺序即首次出现顺序），只换组大小 ──────────────

type rpChunkLoc struct {
	group uint32
	off   uint32
	size  uint32
}

type rpGrouped struct {
	size    int
	frames  [][]byte
	rawLen  []int32
	loc     []rpChunkLoc
	nGroups int
}

func (b *rpBlockIndex) regroup(t *testing.T, size int) *rpGrouped {
	t.Helper()
	g := &rpGrouped{size: size, loc: make([]rpChunkLoc, len(b.uniqLen))}
	var (
		buf []byte
		off int
	)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		// 组用最高档压，并且**落库前自检**（INV-3）——冷数据压一次就长期躺着，
		// 值得多花这一次解码。生产侧同样走 EncodeVerified。
		frame, ok := compress.EncodeVerified(buf, compress.TypeGroup, compress.LevelGroup)
		if !ok {
			t.Fatalf("第 %d 组自检没过，压缩代码有 bug", g.nGroups)
		}
		g.frames = append(g.frames, frame)
		g.rawLen = append(g.rawLen, int32(len(buf)))
		g.nGroups++
		buf = buf[:0]
		off = 0
	}
	for i, ln := range b.uniqLen {
		if len(buf) > 0 && len(buf)+int(ln) > size {
			flush()
		}
		g.loc[i] = rpChunkLoc{group: uint32(g.nGroups), off: uint32(off), size: uint32(ln)}
		buf = append(buf, b.uniq[b.uniqOff[i]:b.uniqOff[i]+ln]...)
		off += int(ln)
	}
	flush()
	return g
}

func writeRPGroups(t *testing.T, path string, g *rpGrouped) {
	t.Helper()
	_ = os.Remove(path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("建组库失败：%v", err)
	}
	defer db.Close()
	// 基准车不关心耐久性，关掉 journal 省下无关开销
	if _, err := db.Exec(`PRAGMA journal_mode=OFF`); err != nil {
		t.Fatalf("设置 journal 失败：%v", err)
	}
	if _, err := db.Exec(`CREATE TABLE groups(id INTEGER PRIMARY KEY, data BLOB)`); err != nil {
		t.Fatalf("建表失败：%v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("开事务失败：%v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO groups(id, data) VALUES(?, ?)`)
	if err != nil {
		t.Fatalf("预编译失败：%v", err)
	}
	for i, f := range g.frames {
		if _, err := stmt.Exec(i, f); err != nil {
			t.Fatalf("写第 %d 组失败：%v", i, err)
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("提交失败：%v", err)
	}
}

// ── LRU：解压后的组 ────────────────────────────────────────────────────────
//
// 这层缓存在阶段 3 会搬进生产代码（连同它自己的单测）。放在这里是因为
// 阶段 0 要先用它量出「缓存到底值不值」，值了才值得搬。

type rpLRU struct {
	cap   int
	cur   int
	items map[uint32]*list.Element
	order *list.List
	hit   int64
	miss  int64
}

type rpLRUEnt struct {
	g uint32
	b []byte
}

func newRPLRU(capBytes int) *rpLRU {
	return &rpLRU{cap: capBytes, items: make(map[uint32]*list.Element), order: list.New()}
}

func (c *rpLRU) get(g uint32) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	if e, ok := c.items[g]; ok {
		c.order.MoveToFront(e)
		c.hit++
		return e.Value.(*rpLRUEnt).b, true
	}
	c.miss++
	return nil, false
}

func (c *rpLRU) put(g uint32, b []byte) {
	if c == nil || c.cap <= 0 {
		return
	}
	if _, ok := c.items[g]; ok {
		return
	}
	c.items[g] = c.order.PushFront(&rpLRUEnt{g, b})
	c.cur += len(b)
	for c.cur > c.cap {
		e := c.order.Back()
		if e == nil {
			return
		}
		c.order.Remove(e)
		ent := e.Value.(*rpLRUEnt)
		delete(c.items, ent.g)
		c.cur -= len(ent.b)
	}
}

// ── 测量：模拟 GetChatIO 的读路径 ──────────────────────────────────────────

type rpMeasure struct {
	groups  []int
	bytesIn []int64
	lat     []time.Duration
	hit     int64
	miss    int64
}

// measureRP 按 sample 里的行下标逐行模拟一次 GetChatIO：
// 取引用 → 定位组 → 取缺失的组 → 解压 → 拼回原文 → **与源库逐字节比对**。
//
// 只对「不在缓存里的组」发查询，这才是真实读路径的形状。
func measureRP(t *testing.T, srcPath, groupDBPath string, b *rpBlockIndex, g *rpGrouped, sample []int, lruBytes int) *rpMeasure {
	t.Helper()
	src := openRawSQL(t, srcPath)
	defer src.Close()
	gdb := openRawSQL(t, groupDBPath)
	defer gdb.Close()

	srcStmt, err := src.Prepare(`SELECT input FROM chat_ios WHERE id = ?`)
	if err != nil {
		t.Fatalf("预编译源查询失败：%v", err)
	}
	defer srcStmt.Close()

	// 「这一组本行是否已计入」用盖章数组而不是 map：生产读路径也会这么做，
	// 每行一次的 map 分配会污染耗时测量。
	stamp := make([]uint32, g.nGroups)
	var (
		round  uint32
		uniq   = make([]uint32, 0, 128)
		misses = make([]uint32, 0, 128)
		raw    = make([][]byte, g.nGroups)
	)
	cache := (*rpLRU)(nil)
	if lruBytes > 0 {
		cache = newRPLRU(lruBytes)
	}

	res := &rpMeasure{}
	for _, ri := range sample {
		refs := b.rowRefs[ri]
		round++
		uniq = uniq[:0]
		for _, c := range refs {
			gr := g.loc[c].group
			if stamp[gr] != round {
				stamp[gr] = round
				uniq = append(uniq, gr)
			}
		}
		// 按组号排序再取：把 87 次随机读压成近似顺序读（计划 §2.7 缓解手段 2）
		sort.Slice(uniq, func(i, j int) bool { return uniq[i] < uniq[j] })

		t0 := time.Now()
		misses = misses[:0]
		var need int64
		for _, gr := range uniq {
			if d, ok := cache.get(gr); ok {
				raw[gr] = d
				continue
			}
			misses = append(misses, gr)
			need += int64(g.rawLen[gr])
		}
		if len(misses) > 0 {
			q := `SELECT id, data FROM groups WHERE id IN (?` + repeatComma(len(misses)-1) + `)`
			args := make([]any, len(misses))
			for i, gr := range misses {
				args[i] = int64(gr)
			}
			rows, err := gdb.Query(q, args...)
			if err != nil {
				t.Fatalf("取组失败：%v", err)
			}
			for rows.Next() {
				var (
					id   int64
					data []byte
				)
				if err := rows.Scan(&id, &data); err != nil {
					rows.Close()
					t.Fatalf("读组失败：%v", err)
				}
				plain, wasFrame, err := compress.DecompressBytes(data)
				if err != nil || !wasFrame {
					rows.Close()
					t.Fatalf("组 %d 解不开（wasFrame=%v）：%v", id, wasFrame, err)
				}
				raw[id] = plain
				cache.put(uint32(id), plain)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatalf("遍历组失败：%v", err)
			}
		}
		out := make([]byte, 0, 512<<10)
		for _, c := range refs {
			l := g.loc[c]
			grp := raw[l.group]
			if int(l.off)+int(l.size) > len(grp) {
				t.Fatalf("块 %d 落在组 %d 的 %d..%d 之外（组长 %d）",
					c, l.group, l.off, int(l.off)+int(l.size), len(grp))
			}
			out = append(out, grp[l.off:l.off+l.size]...)
		}
		el := time.Since(t0)

		// 校验放在计时之后：它读的是源库，不属于被测路径
		var want []byte
		if err := srcStmt.QueryRow(b.rowIDs[ri]).Scan(&want); err != nil {
			t.Fatalf("读源行 %d 失败：%v", b.rowIDs[ri], err)
		}
		if !bytes.Equal(out, want) {
			t.Fatalf("行 %d（id=%d）按块表还原的结果与源库不一致：%d 字节 vs %d 字节",
				ri, b.rowIDs[ri], len(out), len(want))
		}

		res.groups = append(res.groups, len(uniq))
		res.bytesIn = append(res.bytesIn, need)
		res.lat = append(res.lat, el)
	}
	res.hit, res.miss = cache.hits(), cache.misses()
	return res
}

func (c *rpLRU) hits() int64 {
	if c == nil {
		return 0
	}
	return c.hit
}

func (c *rpLRU) misses() int64 {
	if c == nil {
		return 0
	}
	return c.miss
}

// ── 小工具 ────────────────────────────────────────────────────────────────

func openRawSQL(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开 %s 失败：%v", path, err)
	}
	// 读多写少的基准车只需要一个连接：既避免驱动层的并发调度噪声，
	// 也让 SQLite 的页缓存行为可解释。
	db.SetMaxOpenConns(1)
	return db
}

// rpFirstRows 取前 want 行的下标，**保持 id 原序**。顺序浏览那一组必须用它：
// 随机抽样会把「顺着会话往下翻」这个前提本身抽掉，测出来的缓存命中率就没意义了。
func rpFirstRows(n, want int) []int {
	if want > n {
		want = n
	}
	idx := make([]int, want)
	for i := range idx {
		idx[i] = i
	}
	return idx
}

// rpSampleRows 随机抽 want 行。固定种子：同一份库上两次跑要能对上，
// 否则数字变了都分不清是代码改了还是抽样换了。
func rpSampleRows(n, want int) []int {
	idx := rpFirstRows(n, n)
	if n <= want {
		return idx
	}
	rnd := rand.New(rand.NewSource(20261002))
	rnd.Shuffle(n, func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	return idx[:want]
}

func rpLRULabel(b int) string {
	if b == 0 {
		return "关"
	}
	return humanBytes(int64(b))
}

func repeatComma(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, n*3)
	for i := 0; i < n; i++ {
		out = append(out, ',', '?')
	}
	return string(out)
}

func humanBytes(n int64) string {
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

func pctInt(v []int, p float64) int {
	if len(v) == 0 {
		return 0
	}
	c := append([]int(nil), v...)
	sort.Ints(c)
	return c[int(float64(len(c)-1)*p)]
}

func pctI64(v []int64, p float64) int64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]int64(nil), v...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	return c[int(float64(len(c)-1)*p)]
}

func pctDur(v []time.Duration, p float64) time.Duration {
	if len(v) == 0 {
		return 0
	}
	c := append([]time.Duration(nil), v...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	return c[int(float64(len(c)-1)*p)]
}
