package compress

import (
	"bytes"
	"errors"
	"testing"
)

// 合法明文不得被误判成帧——这是"同一列混装新旧两种形态"的全部依据。
func TestLooksLikeFrameRejectsPlaintext(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"空", nil},
		{"比帧头短", []byte{0x00, 'L', 'C', 'Z'}},
		{"JSON 对象", []byte(`{"model":"claude-3"}`)},
		{"JSON 数组", []byte(`["data: {\"a\":1}"]`)},
		{"XML", []byte(`<?xml version="1.0"?><a/>`)},
		{"纯文本", []byte("hello world")},
		{"首字节 0x00 但 magic 不对", append([]byte{0x00, 'X', 'Y', 'Z', 1, 1, 1, 0}, make([]byte, 9)...)},
		{"版本未知", append([]byte{0x00, 'L', 'C', 'Z', 99, 1, 1, 0}, make([]byte, 9)...)},
		{"type 未知", append([]byte{0x00, 'L', 'C', 'Z', 1, 1, 9, 0}, make([]byte, 9)...)},
	}
	for _, c := range cases {
		if LooksLikeFrame(c.in) {
			t.Errorf("%s：明文被误判成帧 %q", c.name, c.in)
		}
		if _, err := Unmarshal(c.in); !errors.Is(err, ErrNotFrame) {
			t.Errorf("%s：Unmarshal 应当是 ErrNotFrame，得到 %v", c.name, err)
		}
	}
}

// 最危险的一类输入：帧头完全合法，但编解码器不认识。
//
// 它必须报错，**绝不能**被降级成"不是帧、按明文用"——那等于把二进制垃圾
// 当成用户数据吐出去。所以预筛故意不看 codec，把判断留给 Unmarshal。
func TestUnknownCodecIsCorruptNotPlaintext(t *testing.T) {
	b := make([]byte, HeaderLen+8)
	copy(b[:4], Magic[:])
	b[4] = Version
	b[5] = 99 // 未知 codec
	b[6] = byte(TypeRowFrame)

	if !LooksLikeFrame(b) {
		t.Fatal("预筛不该看 codec：这是我们造出来的帧，只是解不了")
	}
	_, err := Unmarshal(b)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("未知 codec 必须报 ErrCorrupt，得到 %v", err)
	}
	if errors.Is(err, ErrNotFrame) {
		t.Fatal("绝不能是 ErrNotFrame——那等于按明文返回")
	}
}

// String 只用于日志与报错，但它必须是**穷尽**的：未知值要能被打印成
// codec(42) 这种带数字的形式，否则排查现场只会看到空字符串。
func TestCodecAndTypeString(t *testing.T) {
	codecs := map[Codec]string{
		CodecRaw:     "raw",
		CodecDeflate: "deflate",
		Codec(7):     "codec(7)",
		Codec(255):   "codec(255)",
	}
	for c, want := range codecs {
		if got := c.String(); got != want {
			t.Errorf("Codec(%d).String() = %q，期望 %q", uint8(c), got, want)
		}
	}
	types := map[Type]string{
		TypeRowFrame:  "row",
		TypeBlockRefs: "blockrefs",
		Type(9):       "type(9)",
	}
	for ty, want := range types {
		if got := ty.String(); got != want {
			t.Errorf("Type(%d).String() = %q，期望 %q", uint8(ty), got, want)
		}
	}
	if CodecRaw.String() == CodecDeflate.String() {
		t.Error("两个已知 Codec 打印成同一个名字")
	}
	if TypeRowFrame.String() == TypeBlockRefs.String() {
		t.Error("两个已知 Type 打印成同一个名字")
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	f := Frame{
		Codec:   CodecDeflate,
		Type:    TypeBlockRefs,
		Flags:   0x5A,
		DictID:  0xDEADBEEF,
		RawLen:  1234,
		Payload: []byte("payload-bytes"),
	}
	b := Marshal(f)
	if !LooksLikeFrame(b) {
		t.Fatal("Marshal 出来的东西不像帧")
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Codec != f.Codec || got.Type != f.Type || got.Flags != f.Flags ||
		got.DictID != f.DictID || got.RawLen != f.RawLen {
		t.Fatalf("帧头往返不一致：%+v vs %+v", got, f)
	}
	if !bytes.Equal(got.Payload, f.Payload) {
		t.Fatalf("载荷往返不一致：%q", got.Payload)
	}
}
