package compress

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"sync"
)

// 写路径等级与组压缩等级分开：
// 写路径每来一个请求就要跑一次，等级 6 够用；
// 块组是一次性攒起来压的冷数据，值得用 9 换体积。
const (
	// LevelWrite 直接写 6，而不是用 flate.DefaultCompression——后者是 -1 这个
	// 哨兵值，被当成池下标会越界（踩过一次了）。flate 里 -1 本来就等于 6。
	LevelWrite = 6
	// LevelGroup 是块组（冷数据）的等级。
	LevelGroup = flate.BestCompression // 9
)

// MaxRawLen 是解码侧的上限。rawLen 是帧里的一个 uint32，若不加限，
// 一个伪造的帧头就能让解码器去分配 4 GiB。正常 body 远小于此。
var MaxRawLen uint32 = 1 << 30

// flate.Writer 不是并发安全的，也不适合每次现建（要初始化哈希表）。
// 按等级各留一个池。
var writerPools [10]sync.Pool

// newFlateWriter 只被 writerPools 的 New 调用。
//
// 这里的 panic 是刻意的：池下标来自 compressLevel 校验过的等级（1..9），
// flate 对合法等级不会报错，所以真走到 panic 说明有人绕过校验直接碰了池。
// 此时宁可当场崩，也不能返回 nil writer 让空指针崩在写路径深处。
func newFlateWriter(level int) *flate.Writer {
	w, err := flate.NewWriter(io.Discard, level)
	if err != nil {
		panic(err)
	}
	return w
}

func init() {
	for lv := 1; lv <= 9; lv++ {
		level := lv
		writerPools[level].New = func() any { return newFlateWriter(level) }
	}
}

// encodeSink 是压缩结果的落点：既要能写，也要能把字节取回来。
type encodeSink interface {
	io.Writer
	Grow(int)
	Bytes() []byte
}

// newEncodeSink 抽成变量只为一件事：让测试注入一个"写到一半就失败"的落点，
// 验证编码失败时**返回错误**（调用方据此退回明文存储），而不是 panic，
// 也不是把半截数据当成压缩结果。生产路径上恒为内存缓冲。
var newEncodeSink = func() encodeSink { return new(bytes.Buffer) }

// Compress 用写路径等级把 raw 编成帧。
//
// raw 为空同样返回合法帧（RawLen=0）而不是 nil——这样"空值"与"没存过"
// 在存储层仍然是两件不同的事，语义不在压缩层被抹掉。
func Compress(raw []byte, typ Type) ([]byte, error) {
	return compressLevel(raw, typ, LevelWrite)
}

func compressLevel(raw []byte, typ Type, level int) ([]byte, error) {
	if len(raw) > int(MaxRawLen) {
		return nil, fmt.Errorf("compress: 值过大 %d 字节", len(raw))
	}
	if level < 1 || level > 9 {
		level = LevelWrite
	}
	f := Frame{
		Codec:  CodecDeflate,
		Type:   typ,
		RawLen: uint32(len(raw)),
	}
	payload, err := deflateEncode(raw, level)
	if err != nil {
		return nil, err
	}
	// 压不动就明说，别硬塞一个更大的载荷。空载荷 + CodecRaw 最省。
	if len(payload) >= len(raw) {
		f.Codec = CodecRaw
		f.Payload = raw
	} else {
		f.Payload = payload
	}
	return Marshal(f), nil
}

// encodeBody 是 EncodeVerified 的编码入口。抽成变量只为一件事：
// 让测试能注入一个"产出的不是合法帧"的编码器，验证 INV-3 真的会拦住——
// 自检失败必须返回 ok=false 让调用方退回明文，绝不能把坏数据写进库。
// 生产路径上恒为 compressLevel。
var encodeBody = compressLevel

// EncodeVerified 编码后立刻解回来逐字节比对（不变量 INV-3）。
//
// 不一致时返回 ok=false，调用方**必须退回明文存储**。
// 代价约 0.4 ms / 470 KiB，买的是"压缩代码有 bug 也不可能变成数据损坏"——
// 这是本方案里性价比最高的一道保险。
func EncodeVerified(raw []byte, typ Type, level int) (frame []byte, ok bool) {
	f, err := encodeBody(raw, typ, level)
	if err != nil {
		return nil, false
	}
	parsed, err := Unmarshal(f)
	if err != nil {
		return nil, false
	}
	back, err := Decompress(parsed)
	if err != nil {
		return nil, false
	}
	if !bytes.Equal(back, raw) {
		return nil, false
	}
	return f, true
}

// Decompress 还原载荷。
//
// 它执行帧头之外的第四道门槛：解出来的长度必须**恰好**等于 rawLen。
// 多一个字节、少一个字节都算损坏——只有这样才能保证 Decode 的成功
// 等价于"这一帧可信"，而不是"恰好没崩"。
func Decompress(f Frame) ([]byte, error) {
	if f.RawLen > MaxRawLen {
		return nil, fmt.Errorf("%w: rawLen %d 超过上限 %d", ErrCorrupt, f.RawLen, MaxRawLen)
	}
	switch f.Codec {
	case CodecRaw:
		if len(f.Payload) != int(f.RawLen) {
			return nil, fmt.Errorf("%w: raw 载荷 %d 字节与 rawLen %d 不符",
				ErrCorrupt, len(f.Payload), f.RawLen)
		}
		out := make([]byte, len(f.Payload))
		copy(out, f.Payload)
		return out, nil
	case CodecDeflate:
		// rawLen 已在上面被 MaxRawLen 封顶（默认 1 GiB，远小于 32 位 int 的上限），
		// 转成 int 不可能为负，所以这里不必再查一次。
		return deflateDecode(f.Payload, int(f.RawLen))
	default:
		return nil, fmt.Errorf("%w: 未知编解码器 %s", ErrCorrupt, f.Codec)
	}
}

// DecompressBytes 处理"可能是帧、也可能是历史明文"的字节。
//
// 这是接入层唯一需要的入口：serializer 的 Scan、模型的 AfterFind 都走它。
// 返回值 wasFrame 用于观测（有多少历史行还没迁），不影响正确性。
func DecompressBytes(b []byte) (out []byte, wasFrame bool, err error) {
	f, err := Unmarshal(b)
	if errors.Is(err, ErrNotFrame) {
		return b, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out, err = Decompress(f)
	if err != nil {
		return nil, true, err
	}
	return out, true, nil
}

// deflateEncode 出错时**不 panic**：写路径上的失败必须能被调用方接住、
// 退回明文存储，而不是把整个代理带崩。
func deflateEncode(src []byte, level int) ([]byte, error) {
	w := writerPools[level].Get().(*flate.Writer)
	defer writerPools[level].Put(w)

	buf := newEncodeSink()
	buf.Grow(len(src)/2 + 64)
	w.Reset(buf)
	if _, err := w.Write(src); err != nil {
		return nil, fmt.Errorf("compress: 编码失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("compress: 收尾失败: %w", err)
	}
	return buf.Bytes(), nil
}

// flateReader 是解码器池里的东西：既能读，也能重置到新的输入源。
//
// 用接口而不是具体的 *flate.Reader，是为了让测试能注入一个"重置就失败"
// 的假解码器：这条路径在 stdlib 上跑不到（flate 的 Reset 恒返回 nil），
// 但接口签名要求处理它，而它一旦真的出错就意味着"输入源没接上"——
// 那时必须报 ErrCorrupt，绝不能把上一次的残留数据当成解码结果。
type flateReader interface {
	io.ReadCloser
	flate.Resetter
}

// newFlateReader 造一个新解码器，只在池空时被调用。
// 抽成变量同样只为测试注入，理由见 flateReader 的注释。
// flate.NewReader 的静态返回类型是 io.ReadCloser，这里断言回具体的解码器能力。
// 断言不会失败：stdlib 的 flate 解码器本来就实现了 Resetter。
var newFlateReader = func() flateReader { return flate.NewReader(nil).(flateReader) }

var readerPool = sync.Pool{New: func() any { return newFlateReader() }}

func deflateDecode(src []byte, rawLen int) ([]byte, error) {
	r := readerPool.Get().(flateReader)
	defer readerPool.Put(r)

	if err := r.Reset(bytes.NewReader(src), nil); err != nil {
		return nil, fmt.Errorf("%w: 重置解码器失败: %v", ErrCorrupt, err)
	}

	out := make([]byte, rawLen)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("%w: 解压不足 %d 字节: %v", ErrCorrupt, rawLen, err)
	}
	// 恰好结束才算数：多出任何内容都说明 rawLen 与载荷不符
	var tail [1]byte
	if n, err := r.Read(tail[:]); n != 0 || !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: 解压后仍有剩余，rawLen 与载荷不符", ErrCorrupt)
	}
	return out, nil
}
