package compress

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	payloads := []struct {
		name string
		raw  []byte
	}{
		{"空", []byte{}},
		{"小 JSON", []byte(`{"a":1}`)},
		{"含内嵌 NUL", append([]byte("abc"), 0x00, 'd')},
		{"非法 UTF-8", []byte{0xff, 0xfe, 0xfd, 0x00, 0x80, 0xc0}},
		{"不可压（随机）", randBytes(64<<10, 1)},
		{"高度重复", bytes.Repeat([]byte("abcdefgh"), 8192)},
		{"真实形态的 SSE 数组", bytes.Repeat([]byte(`["data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}"]`), 200)},
	}
	for _, p := range payloads {
		for _, typ := range []Type{TypeRowFrame, TypeBlockRefs} {
			frame, err := Compress(p.raw, typ)
			if err != nil {
				t.Fatalf("%s/%v: Compress: %v", p.name, typ, err)
			}
			if !LooksLikeFrame(frame) {
				t.Fatalf("%s/%v: 压出来的东西不像帧", p.name, typ)
			}
			f, err := Unmarshal(frame)
			if err != nil {
				t.Fatalf("%s/%v: Unmarshal: %v", p.name, typ, err)
			}
			if f.Type != typ {
				t.Fatalf("%s: type 丢了，得到 %v", p.name, f.Type)
			}
			got, err := Decompress(f)
			if err != nil {
				t.Fatalf("%s/%v: Decompress: %v", p.name, typ, err)
			}
			if !bytes.Equal(got, p.raw) {
				t.Fatalf("%s/%v: 往返不一致（原 %d 字节，回 %d 字节）", p.name, typ, len(p.raw), len(got))
			}
		}
	}
}

// 压不动时必须退回 CodecRaw，而不是硬塞一个更大的载荷。
func TestIncompressibleFallsBackToRaw(t *testing.T) {
	raw := randBytes(4096, 42)
	frame, err := Compress(raw, TypeRowFrame)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	f, err := Unmarshal(frame)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if f.Codec != CodecRaw {
		t.Fatalf("随机数据不该被判定为可压缩，得到 codec=%v", f.Codec)
	}
	if len(frame) > len(raw)+HeaderLen {
		t.Fatalf("退化成 raw 后仍比原文大：%d vs %d", len(frame), len(raw)+HeaderLen)
	}
}

// rawLen 与实际内容不符时**必须报错**，不能静默截断或填充。
// 这条保证"Decompress 成功"等价于"这一帧可信"。
func TestDecompressRejectsLengthMismatch(t *testing.T) {
	raw := bytes.Repeat([]byte("hello world "), 500)
	frame, err := Compress(raw, TypeRowFrame)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	f, _ := Unmarshal(frame)

	short := f
	short.RawLen--
	if _, err := Decompress(short); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("rawLen 少 1 应当报 ErrCorrupt，得到 %v", err)
	}

	long := f
	long.RawLen++
	if _, err := Decompress(long); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("rawLen 多 1 应当报 ErrCorrupt，得到 %v", err)
	}
}

func TestTruncatedPayloadIsCorrupt(t *testing.T) {
	raw := bytes.Repeat([]byte("abcdefghij"), 1000)
	frame, err := Compress(raw, TypeRowFrame)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	f, _ := Unmarshal(frame)
	if f.Codec != CodecDeflate {
		t.Skip("这份载荷没被压，测不到截断")
	}
	f.Payload = f.Payload[:len(f.Payload)/2]
	if _, err := Decompress(f); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("截断的载荷应当报 ErrCorrupt，得到 %v", err)
	}
}

// rawLen 超过上限时必须挡住——否则一个伪造的帧头就能让解码器去分配 4 GiB。
func TestDecompressRejectsOversizedRawLen(t *testing.T) {
	f := Frame{Codec: CodecRaw, RawLen: MaxRawLen + 1, Payload: []byte("x")}
	if _, err := Decompress(f); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("超限 rawLen 应当报 ErrCorrupt，得到 %v", err)
	}
}

// DecompressBytes 是接入层唯一入口：明文原样过，帧才解。
func TestDecompressBytes(t *testing.T) {
	plain := []byte(`{"legacy":true}`)
	got, was, err := DecompressBytes(plain)
	if err != nil {
		t.Fatalf("明文不该报错：%v", err)
	}
	if was {
		t.Fatal("明文被当成帧")
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("明文被改动了：%q", got)
	}

	raw := bytes.Repeat([]byte("payload "), 300)
	frame, err := Compress(raw, TypeRowFrame)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	got, was, err = DecompressBytes(frame)
	if err != nil {
		t.Fatalf("解帧失败：%v", err)
	}
	if !was {
		t.Fatal("帧没被认出来")
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("解出来的内容不对")
	}

	// 损坏的帧必须报错，**不能**退回明文
	broken := make([]byte, HeaderLen+4)
	copy(broken, frame[:HeaderLen])
	broken[5] = 99
	if _, _, err := DecompressBytes(broken); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("损坏的帧必须报错，得到 %v", err)
	}
}

func TestEncodeVerified(t *testing.T) {
	raw := bytes.Repeat([]byte("verified payload "), 1000)
	frame, ok := EncodeVerified(raw, TypeRowFrame, LevelGroup)
	if !ok {
		t.Fatal("正常载荷自检不该失败")
	}
	f, err := Unmarshal(frame)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got, err := Decompress(f)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("自检通过但内容不对")
	}
}

func TestCompressRejectsOversizedValue(t *testing.T) {
	// 用长度断言代替真的分配 1 GiB：MaxRawLen 是包级变量，这里临时调小
	saved := MaxRawLen
	MaxRawLen = 16
	defer func() { MaxRawLen = saved }()

	if _, err := Compress(make([]byte, 17), TypeRowFrame); err == nil {
		t.Fatal("超过 MaxRawLen 的值必须拒绝编码")
	}
}

// 编解码池是共享的，-race 下必须干净。
func TestConcurrentCodec(t *testing.T) {
	raw := bytes.Repeat([]byte("concurrent payload 0123456789 "), 400)
	var frames [][]byte
	for i := 0; i < 5; i++ {
		f, err := Compress(raw, TypeRowFrame)
		if err != nil {
			t.Fatalf("Compress: %v", err)
		}
		frames = append(frames, f)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				frame, err := Compress(raw, TypeRowFrame)
				if err != nil {
					t.Errorf("并发 Compress: %v", err)
					return
				}
				f, err := Unmarshal(frame)
				if err != nil {
					t.Errorf("并发 Unmarshal: %v", err)
					return
				}
				got, err := Decompress(f)
				if err != nil {
					t.Errorf("并发 Decompress: %v", err)
					return
				}
				if !bytes.Equal(got, raw) {
					t.Error("并发往返内容不一致")
					return
				}
				// 顺带解一下别的协程压出来的帧
				src := frames[(id+j)%len(frames)]
				df, err := Unmarshal(src)
				if err != nil {
					t.Errorf("并发 Unmarshal(共享): %v", err)
					return
				}
				if _, err := Decompress(df); err != nil {
					t.Errorf("并发 Decompress(共享): %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// 等级越界必须回落到写档，绝不能让池下标越界——池是按等级索引的数组，
// 而 flate.DefaultCompression 恰好是 -1 这个哨兵值（早先就是这么踩的）。
func TestCompressLevelFallsBackOnBadLevel(t *testing.T) {
	raw := bytes.Repeat([]byte("level fallback "), 200)
	for _, level := range []int{0, -1, 10, 99} {
		frame, err := compressLevel(raw, TypeRowFrame, level)
		if err != nil {
			t.Fatalf("level=%d: %v", level, err)
		}
		f, err := Unmarshal(frame)
		if err != nil {
			t.Fatalf("level=%d: Unmarshal: %v", level, err)
		}
		got, err := Decompress(f)
		if err != nil {
			t.Fatalf("level=%d: Decompress: %v", level, err)
		}
		if !bytes.Equal(got, raw) {
			t.Fatalf("level=%d：回落后的往返内容不对", level)
		}
	}
}

// Decompress 是最后的守门人：它不能默认"进来的 Frame 一定来自 Unmarshal"。
func TestDecompressDirectFrameGuards(t *testing.T) {
	if _, err := Decompress(Frame{Codec: CodecRaw, RawLen: 9, Payload: []byte("abc")}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("raw 载荷与 rawLen 不符应当报 ErrCorrupt，得到 %v", err)
	}
	if _, err := Decompress(Frame{Codec: Codec(42), RawLen: 0}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("未知编解码器应当报 ErrCorrupt，得到 %v", err)
	}
}

// "是帧、但解不开"必须把错误抛上去。这是接入层唯一能区分历史明文与坏帧的
// 地方——一旦这里退回明文，用户拿到的就是二进制垃圾。
func TestDecompressBytesCorruptFrame(t *testing.T) {
	raw := bytes.Repeat([]byte("payload "), 300)
	frame, err := Compress(raw, TypeRowFrame)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	f, err := Unmarshal(frame)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	f.RawLen += 7 // 帧头仍然合法，只是载荷对不上

	out, was, err := DecompressBytes(Marshal(f))
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("坏帧应当报 ErrCorrupt，得到 %v", err)
	}
	if !was {
		t.Error("坏帧也必须先被认出是帧")
	}
	if out != nil {
		t.Fatalf("出错时不能返回任何字节，得到 %q", out)
	}
}

// INV-3 是"压缩代码有 bug 也不可能变成数据损坏"的那道保险。
// 这里注入一个坏掉的编码器，验证四种失手方式**全部**返回 ok=false。
//
// 注意：第四种（解得开、但不是原文）是唯一能发现"编码器与解码器一起写错"
// 的情形——只验"能解开"是不够的。
func TestEncodeVerifiedRejectsBadEncoder(t *testing.T) {
	saved := encodeBody
	defer func() { encodeBody = saved }()

	raw := bytes.Repeat([]byte("payload "), 100)
	cases := []struct {
		name string
		body func([]byte, Type, int) ([]byte, error)
	}{
		{"编码器直接报错", func([]byte, Type, int) ([]byte, error) {
			return nil, errors.New("压不了")
		}},
		{"产出的不是帧", func([]byte, Type, int) ([]byte, error) {
			return []byte("这不是帧"), nil
		}},
		{"帧头合法但载荷对不上", func([]byte, Type, int) ([]byte, error) {
			return Marshal(Frame{
				Codec: CodecDeflate, Type: TypeRowFrame,
				RawLen: 999, Payload: []byte("坏载荷"),
			}), nil
		}},
		{"解得开但不是原文", func([]byte, Type, int) ([]byte, error) {
			return Compress([]byte("别的内容"), TypeRowFrame)
		}},
	}
	for _, c := range cases {
		encodeBody = c.body
		frame, ok := EncodeVerified(raw, TypeRowFrame, LevelWrite)
		if ok {
			t.Errorf("%s：自检居然通过了", c.name)
		}
		if frame != nil {
			t.Errorf("%s：自检失败时不能返回帧", c.name)
		}
	}
}

// ---- 以下四段测的是"在真实 stdlib 上跑不到"的分支 ----
//
// 跑不到不等于写错了：这几条恰恰守着数据安全的那一侧——压缩失败要能退回明文、
// 自检失败要能拦住、解码器没接上要报错而不是把上一次的残留当成结果。
// 用注入把"我认为它不会错"变成断言。

// failingSink 是写到 allow 字节之后就开始报错的落点。
type failingSink struct {
	allow int
	used  int
}

func (s *failingSink) Grow(int)      {}
func (s *failingSink) Bytes() []byte { return nil }

func (s *failingSink) Write(p []byte) (int, error) {
	if s.used+len(p) > s.allow {
		return 0, errors.New("落点写满了")
	}
	s.used += len(p)
	return len(p), nil
}

// 编码落点出错时必须**返回错误**让调用方退回明文存储，
// 而不是 panic，更不是把半截数据当成压缩结果写进库。
func TestEncodeSinkFailureIsReturned(t *testing.T) {
	saved := newEncodeSink
	defer func() { newEncodeSink = saved }()
	newEncodeSink = func() encodeSink { return &failingSink{} }

	// 1 MiB 不可压数据必然在 Write 内部就冲刷到落点
	if _, err := Compress(randBytes(1<<20, 8), TypeRowFrame); err == nil {
		t.Error("写过程中失败必须把错误传上来")
	}
	// 200 字节会在 Write 里攒着，Close 收尾时才冲刷
	if _, err := Compress(bytes.Repeat([]byte("a"), 200), TypeRowFrame); err == nil {
		t.Error("收尾失败必须把错误传上来")
	}
	// 自检路径同样要能把错误变成 ok=false
	if _, ok := EncodeVerified(bytes.Repeat([]byte("b"), 200), TypeRowFrame, LevelWrite); ok {
		t.Error("落点坏了，自检不可能通过")
	}
}

// 池下标来自校验过的等级，所以这个 panic 在生产路径上不可达。
// 但它必须仍然 panic：返回 nil writer 的话，空指针会崩在写路径深处。
func TestNewFlateWriterPanicsOnInvalidLevel(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("非法等级必须当场 panic，而不是返回 nil writer")
		}
	}()
	newFlateWriter(10)
}

// failingFlateReader 是"重置就失败"的假解码器。
type failingFlateReader struct{}

func (failingFlateReader) Read([]byte) (int, error) { return 0, errors.New("读不了") }
func (failingFlateReader) Close() error             { return nil }
func (failingFlateReader) Reset(io.Reader, []byte) error {
	return errors.New("输入源没接上")
}

// 解码器重置失败必须报 ErrCorrupt。若这里被忽略，读到的将是解码器里残留的
// 上一次数据——那是"静默返回错误内容"，比报错严重得多。
func TestFlateReaderResetFailureIsCorrupt(t *testing.T) {
	saved := newFlateReader
	newFlateReader = func() flateReader { return failingFlateReader{} }
	readerPool = sync.Pool{New: func() any { return newFlateReader() }}
	defer func() {
		newFlateReader = saved
		readerPool = sync.Pool{New: func() any { return newFlateReader() }}
	}()

	if _, err := deflateDecode([]byte("whatever"), 8); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("重置失败应当报 ErrCorrupt，得到 %v", err)
	}
}
