package compress

import "crypto/sha256"

// DefaultChunkAvg 是内容定义分块的平均块大小。
//
// 4 KiB 是在 llmio 的真实语料上扫出来的平衡点：更小则唯一块数与引用开销
// 一起爆炸（512 B 时元数据吃掉 54 MiB），更大则去重命中率崩塌
// （64 KiB 反而涨到 98.70 MiB）。它是两条曲线的交点，不是拍的。
const DefaultChunkAvg = 4 << 10

// gearTable 是 gear 滚动哈希的字节表。
//
// **这张表一旦有数据落库就不能再改。** 切点由它决定，表一变，历史数据的
// 切点全部错位，块表当场作废（每一行都指向对不上的块）。所以这里用固定
// 种子加纯整数运算：跨进程、跨平台、跨版本都必须得到同一张表。
var gearTable [256]uint64

func init() {
	var x uint64 = 0x9E3779B97F4A7C15
	for i := range gearTable {
		x += 0x9E3779B97F4A7C15
		z := x
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		gearTable[i] = z ^ (z >> 31)
	}
}

// CutPoints 把切点（块结束位置，最后一个是 len(b)）追加到 dst 并返回。
//
// 为什么必须按内容切、不能按固定长度切：固定长度下，正文中间插入几个字节
// 会让后面**所有**切点错位，一整条链全部失配，去重率归零。而 agent 每一轮
// 都会把上一轮的正文重发、尾部加一点，插入是常态而不是例外。
//
// 哈希是连续滚动的（切点处不归零）：`h>>1` 会把 64 字节以前的信息移空，
// 所以窗口天然有界，插入只影响局部，后面的切点会自动重新对齐。
//
// 方向是 `>>1` 而不是 `<<1`，这一点不是风格问题：`<<1` 时低位几乎完全由
// **最后一个字节**决定（h<<1 的末位恒为 0，加上 g[b] 后末 12 位就被 g[b]
// 主导），切点概率因此显著偏低——实测标称 4 KiB 只切出平均 6.5 KiB。
// `>>1` 把上一个状态的高位（已被充分打散）移到低位来判定，分布才均匀，
// avg 也才真的是 avg。见 TestChunkAverageMatchesTarget。
func CutPoints(b []byte, avg int, dst []int) []int {
	if len(b) == 0 {
		return dst
	}
	if avg <= 0 {
		avg = DefaultChunkAvg
	}
	minSize := avg / 4
	maxSize := avg * 4
	if minSize < 64 {
		minSize = 64
	}
	bits := 0
	for (1 << bits) < avg {
		bits++
	}
	mask := uint64(1)<<bits - 1

	var h uint64
	start := 0
	for i := 0; i < len(b); i++ {
		h = (h >> 1) + gearTable[b[i]]
		n := i - start + 1
		if n >= maxSize || (n >= minSize && h&mask == 0) {
			dst = append(dst, i+1)
			start = i + 1
		}
	}
	if start < len(b) {
		dst = append(dst, len(b))
	}
	return dst
}

// Chunk 把 b 切成块，返回的每块都与 b 共享底层数组（不拷贝）。
// 调用方若要留存或改动，必须自己拷一份。
func Chunk(b []byte, avg int) [][]byte {
	cuts := CutPoints(b, avg, nil)
	if len(cuts) == 0 {
		return nil
	}
	out := make([][]byte, 0, len(cuts))
	prev := 0
	for _, c := range cuts {
		out = append(out, b[prev:c])
		prev = c
	}
	return out
}

// Digest 是块的内容标识：SHA-256 截断到 128 位。
//
// 为什么不用 hash/maphash：它的种子每个进程都不同，做不了跨进程稳定的
// 内容寻址——重启一次，所有块的标识就都变了。
//
// 为什么敢截断：128 位下 14.7 万个块的生日碰撞概率约 1e-27。而引用要逐行
// 存进库，标识长度直接等于每行的引用开销，截到 128 位是"够安全"与"不太贵"
// 的交点。截断不改变碰撞的**安全性结论**：这里防的是意外碰撞，不是攻击者。
func Digest(b []byte) [16]byte {
	sum := sha256.Sum256(b)
	var d [16]byte
	copy(d[:], sum[:16])
	return d
}
