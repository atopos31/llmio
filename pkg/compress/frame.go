// Package compress 提供 llmio 数据库 body 列的压缩帧。
//
// 三条前提决定了这里的每个设计（详见 docs/db-compression-safety.md）：
//
//  1. 压缩是纯优化。任何"判不准"的情形都必须退回明文，永不静默返回垃圾。
//  2. 帧必须自描述：读路径只认帧头，不依赖任何数据库标志位。
//     标志位与字节不一致时，必须偏安全的那一侧。
//  3. 编解码器是帧头里的显式字段，所以换编解码器不会让旧数据读不出来。
//
// 同一列里可以混装明文与帧，这是历史数据不必一次性迁移的前提。
package compress

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// HeaderLen 是帧头固定长度。
	HeaderLen = 16
	// Version 是当前帧格式版本。改布局必须同时改它，否则旧帧会被误读。
	Version = 1
)

// Magic 的首字节是 0x00，这不是随手挑的：RFC 8259 规定 JSON 字符串之外
// 唯一允许的控制字符是 \x20 \x09 \x0A \x0D，0x00 在任何位置都非法。
// 因此合法 JSON body 的首字节不可能是 0x00，混装不会误判。
var Magic = [4]byte{0x00, 'L', 'C', 'Z'}

// Codec 标识载荷的编码方式。
//
// 它是显式字段而不是"代码里写死用哪个"：将来把 flate 换成 zstd/brotli
// 只需要新增一个常量，历史帧照旧可读。**这就是"编解码器可替换"的全部实现。**
type Codec uint8

const (
	CodecRaw     Codec = 0
	CodecDeflate Codec = 1
	// 2 起预留给 zstd / brotli。启用前它们与未知值同等对待：
	// 明确报错，绝不静默退回明文。
)

func (c Codec) known() bool { return c == CodecRaw || c == CodecDeflate }

func (c Codec) String() string {
	switch c {
	case CodecRaw:
		return "raw"
	case CodecDeflate:
		return "deflate"
	default:
		return fmt.Sprintf("codec(%d)", uint8(c))
	}
}

// Type 标识这一帧装的是什么。
type Type uint8

const (
	// TypeRowFrame 是一整列的值（of_string / of_string_array）。
	TypeRowFrame Type = 1
	// TypeBlockRefs 是 input 列的块引用序列。
	TypeBlockRefs Type = 2
	// TypeGroup 是块表里的一组块（多个唯一块拼在一起压成的冷数据）。
	//
	// 它与前两者的区别值得单独一个类型而不是复用：块组是**全局共享**的，
	// 而前两者是某一行的私有值。将来若要"把块组也塞进 chat_ios 的同一列"
	// 或做按类型的统计/校验，这个字段就是唯一的判据，而它已经写进了
	// 每一个历史帧头里——后补不回来。
	TypeGroup Type = 3
)

func (t Type) known() bool {
	return t == TypeRowFrame || t == TypeBlockRefs || t == TypeGroup
}

func (t Type) String() string {
	switch t {
	case TypeRowFrame:
		return "row"
	case TypeBlockRefs:
		return "blockrefs"
	case TypeGroup:
		return "group"
	default:
		return fmt.Sprintf("type(%d)", uint8(t))
	}
}

var (
	// ErrNotFrame 表示这段字节根本不是帧，调用方应当按明文处理。
	ErrNotFrame = errors.New("compress: 不是帧")
	// ErrCorrupt 表示帧头合法但内容不可信——编解码器未知、解压失败、长度不符。
	//
	// 这一类比 ErrNotFrame 严重一个量级：**必须报错，绝不能退回明文**。
	// 退回明文等于把二进制垃圾当成用户数据吐出去。
	ErrCorrupt = errors.New("compress: 帧已损坏")
)

// Frame 是解出来的帧。Payload 与输入切片共享底层数组，调用方不得改写。
type Frame struct {
	Codec   Codec
	Type    Type
	Flags   uint8 // 当前未用，留给将来（如"引用序列是差分编码"）
	DictID  uint32
	RawLen  uint32 // 解压后长度
	Payload []byte
}

// 帧头布局（小端）：
//
//	 0  4  magic   = 00 'L' 'C' 'Z'
//	 4  1  version
//	 5  1  codec
//	 6  1  type
//	 7  1  flags
//	 8  4  dictID
//	12  4  rawLen
//	16  …  payload
//
// dictID **恒为 0，本方案不使用任何字典**——不训练、不分发、不随库迁移。
// 留这 4 字节只是为了将来若真要引入时不必再改版本号；在此之前它没有任何含义。
// （字典已被实测否决：纯 Go 自造正文只拿到 +2.33%，而把压缩等级提到最高档
// 免费赚 8.02%，比带字典还多。）
const (
	offCodec  = 5
	offType   = 6
	offFlags  = 7
	offDictID = 8
	offRawLen = 12
)

// LooksLikeFrame 是廉价的预筛：只看帧头，不解压。
//
// 它**故意不校验 codec**——codec 未知说明"这是我们的帧，但我们解不了"，
// 属于必须报错的情形，不能被降级成"不是帧、按明文处理"。
func LooksLikeFrame(b []byte) bool {
	if len(b) < HeaderLen {
		return false
	}
	if !bytes.Equal(b[:4], Magic[:]) {
		return false
	}
	if b[4] != Version {
		return false
	}
	return Type(b[offType]).known()
}

// Unmarshal 拆帧。b 不是帧时返回 ErrNotFrame；是帧但不可信时返回 ErrCorrupt。
//
// 调用方按这两个错误分开处理：
//
//	f, err := Unmarshal(b)
//	switch {
//	case errors.Is(err, ErrNotFrame): 按明文用 b
//	case err != nil:                  报错，不要用 b
//	}
func Unmarshal(b []byte) (Frame, error) {
	if !LooksLikeFrame(b) {
		return Frame{}, ErrNotFrame
	}
	f := Frame{
		Codec:   Codec(b[offCodec]),
		Type:    Type(b[offType]),
		Flags:   b[offFlags],
		DictID:  binary.LittleEndian.Uint32(b[offDictID:]),
		RawLen:  binary.LittleEndian.Uint32(b[offRawLen:]),
		Payload: b[HeaderLen:],
	}
	if !f.Codec.known() {
		return Frame{}, fmt.Errorf("%w: 未知编解码器 %s", ErrCorrupt, f.Codec)
	}
	return f, nil
}

// Marshal 组帧。marshalHeader 与它分开，是为了让调用方能自己拼大载荷而不多拷一次。
func Marshal(f Frame) []byte {
	out := make([]byte, HeaderLen+len(f.Payload))
	marshalHeader(out[:HeaderLen], f)
	copy(out[HeaderLen:], f.Payload)
	return out
}

func marshalHeader(dst []byte, f Frame) {
	copy(dst[:4], Magic[:])
	dst[4] = Version
	dst[offCodec] = byte(f.Codec)
	dst[offType] = byte(f.Type)
	dst[offFlags] = f.Flags
	binary.LittleEndian.PutUint32(dst[offDictID:], f.DictID)
	binary.LittleEndian.PutUint32(dst[offRawLen:], f.RawLen)
}
