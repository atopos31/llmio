package compress

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

func randBytes(n int, seed int64) []byte {
	b := make([]byte, n)
	// math/rand 的 Read 在固定种子下是确定的，测试要的就是确定性
	r := rand.New(rand.NewSource(seed))
	if _, err := r.Read(b); err != nil {
		panic(err)
	}
	return b
}

func digestSet(b []byte) map[[16]byte]bool {
	out := make(map[[16]byte]bool)
	for _, c := range Chunk(b, DefaultChunkAvg) {
		out[Digest(c)] = true
	}
	return out
}

// 切点必须完整覆盖输入——漏一个字节就是丢数据。
func TestChunkCoversInputExactly(t *testing.T) {
	for _, n := range []int{0, 1, 63, 64, 1023, 4 << 10, 100000, 1 << 20} {
		b := randBytes(n, int64(n)+1)
		chunks := Chunk(b, DefaultChunkAvg)
		var joined []byte
		for _, c := range chunks {
			joined = append(joined, c...)
		}
		if !bytes.Equal(joined, b) {
			t.Fatalf("n=%d：拼回来和原文不一致（得到 %d 字节）", n, len(joined))
		}
	}
}

func TestChunkSizeBounds(t *testing.T) {
	const avg = DefaultChunkAvg
	chunks := Chunk(randBytes(1<<20, 7), avg)
	minSize := avg / 4
	maxSize := avg * 4
	for i, c := range chunks {
		if len(c) > maxSize {
			t.Fatalf("第 %d 块 %d 字节，超过上限 %d", i, len(c), maxSize)
		}
		// 最后一块是余数，可以短；其余都不该短于下限
		if i < len(chunks)-1 && len(c) < minSize {
			t.Fatalf("第 %d 块 %d 字节，短于下限 %d", i, len(c), minSize)
		}
	}
}

// 这是整个方案成立的**前提**。
//
// agent 每一轮都把上一轮正文原样重发、尾部加一点。如果插入几个字节会让
// 后面所有切点错位，那些重复内容就一个块都命不中，跨行去重整体归零。
// 固定长度分块在这里会拿到接近 0% 的对齐率，CDC 必须拿到 85% 以上。
func TestChunkInsertionResilience(t *testing.T) {
	base := randBytes(400<<10, 11)
	const at = 200 << 10
	ins := []byte("<<<INSERTED>>>")

	mod := make([]byte, 0, len(base)+len(ins))
	mod = append(mod, base[:at]...)
	mod = append(mod, ins...)
	mod = append(mod, base[at:]...)

	baseSet := digestSet(base)
	modSet := digestSet(mod)
	shared := 0
	for d := range modSet {
		if baseSet[d] {
			shared++
		}
	}
	ratio := float64(shared) / float64(len(modSet))
	if ratio < 0.85 {
		t.Fatalf("插入 %d 字节后只有 %.1f%% 的块还能对上（%d/%d）——切点没有重新对齐",
			len(ins), ratio*100, shared, len(modSet))
	}
	t.Logf("插入后对齐率 %.1f%%（%d/%d）", ratio*100, shared, len(modSet))
}

// 同内容必须切出同一串切点。
func TestChunkDeterministic(t *testing.T) {
	b := randBytes(64<<10, 3)
	a1 := CutPoints(b, DefaultChunkAvg, nil)
	a2 := CutPoints(b, DefaultChunkAvg, nil)
	if len(a1) != len(a2) {
		t.Fatalf("两次切点数量不同：%d vs %d", len(a1), len(a2))
	}
	for i := range a1 {
		if a1[i] != a2[i] {
			t.Fatalf("第 %d 个切点不同：%d vs %d", i, a1[i], a2[i])
		}
	}
}

// 冻住切点：gear 表或切点规则被改动时，历史数据的切点会全部错位、块表作废。
// 这条守卫**故意**在有人改表时报错，提醒"这是不兼容改动，需要迁移/重建"。
func TestCutPointsGolden(t *testing.T) {
	b := randBytes(64<<10, 3)
	got := CutPoints(b, DefaultChunkAvg, nil)
	want := []int{1956, 3125, 5748, 7082, 9039, 14193, 16026, 32410, 34304, 37430, 39994, 43133, 46893, 56987, 62909, 65536}
	if len(got) != len(want) {
		t.Fatalf("切点数量变了：%d（原 %d）\n got: %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个切点变了：%d（原 %d）\n got: %v", i, got[i], want[i], got)
		}
	}
}

// 标称 avg 必须真的是平均块大小。
//
// 这条测试是有来历的：gear 公式若写成 `h<<1`，低位被最后一个字节主导，
// 切点概率严重偏低——实测标称 4 KiB 只切出平均 6.5 KiB，块粒度偏大 1.6 倍。
// 而去重收益对块大小很敏感（512 B 时元数据爆炸、64 KiB 时命中率崩塌），
// 所以"avg 就是 avg"值得钉成一条断言。
func TestChunkAverageMatchesTarget(t *testing.T) {
	for _, avg := range []int{1 << 10, 4 << 10, 16 << 10} {
		b := randBytes(8<<20, int64(avg))
		chunks := Chunk(b, avg)
		mean := float64(len(b)) / float64(len(chunks))
		if mean < float64(avg)*0.7 || mean > float64(avg)*1.4 {
			t.Fatalf("avg=%d 时实测平均块 %.0f 字节，超出容差", avg, mean)
		}
		t.Logf("avg=%d 实测平均块 %.0f 字节（%d 块）", avg, mean, len(chunks))
	}
}

// 小块长下 minSize 会被抬到 64：avg=128 时按 avg/4 算下限只有 32 字节，
// 块小到去重元数据比正文还贵，所以宁可少切几刀。
func TestCutPointsSmallAvgClampsMinSize(t *testing.T) {
	const avg = 128
	b := randBytes(64<<10, 21)
	chunks := Chunk(b, avg)
	if len(chunks) < 2 {
		t.Fatalf("64 KiB 输入在 avg=%d 下只切出 %d 块", avg, len(chunks))
	}
	var joined []byte
	for i, c := range chunks {
		joined = append(joined, c...)
		if i < len(chunks)-1 && len(c) < 64 {
			t.Fatalf("第 %d 块只有 %d 字节，低于被抬高的下限 64", i, len(c))
		}
		if len(c) > avg*4 {
			t.Fatalf("第 %d 块 %d 字节，超过上限 %d", i, len(c), avg*4)
		}
	}
	if !bytes.Equal(joined, b) {
		t.Fatal("拼回来和原文不一致")
	}
}

func TestCutPointsEdge(t *testing.T) {
	if got := CutPoints(nil, DefaultChunkAvg, nil); len(got) != 0 {
		t.Fatalf("空输入应当没有切点，得到 %v", got)
	}
	if got := CutPoints([]byte{1, 2, 3}, DefaultChunkAvg, nil); len(got) != 1 || got[0] != 3 {
		t.Fatalf("短输入应当整块一个切点，得到 %v", got)
	}
	// avg=0 走默认值，不能除以零、不能死循环
	if got := CutPoints(randBytes(1000, 5), 0, nil); len(got) == 0 {
		t.Fatal("avg=0 应当回退到默认值")
	}
}

func TestDigestStableAndDistinct(t *testing.T) {
	var want = [16]byte{0x79, 0x6b, 0xc2, 0x49, 0x83, 0x6b, 0x7d, 0x28, 0xe6, 0xb5, 0x37, 0x8c, 0xe7, 0x79, 0xe7, 0x9e}
	if got := Digest([]byte("llmio")); got != want {
		t.Fatalf("Digest(\"llmio\") 变了：%x\n（截断算法一旦改动，所有历史块标识作废）", got)
	}
	if Digest([]byte("a")) == Digest([]byte("b")) {
		t.Fatal("不同内容得到同一个标识")
	}
	if Digest(nil) != Digest([]byte{}) {
		t.Fatal("nil 与空切片必须得到同一标识")
	}
}

func TestDigestIsNotSeedDependent(t *testing.T) {
	// 反例说明：hash/maphash 的种子每进程不同，跨进程做不了内容寻址。
	// 这里只能验证"同进程内一致"，跨进程稳定性靠的是 SHA-256 本身的确定性。
	b := randBytes(4096, 9)
	if Digest(b) != Digest(b) {
		t.Fatal("同内容两次摘要不同")
	}
	_ = fmt.Sprintf("%x", Digest(b))
}
