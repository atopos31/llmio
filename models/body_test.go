package models

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/atopos31/llmio/pkg/compress"
	"gorm.io/gorm"
)

// 本文件验证 `input` 列的接入层（models/body.go）。
//
// 与 block_test.go 的分工：那边测存储层本身（能不能还原、去重、事务），
// 这边测**接得对不对**——钩子有没有被触发、落库形态对不对、关掉开关会不会
// 把已压的行读坏。存储层全绿而接入错了，数据一样是丢的。

// inputFrameType 读 input 列的原始字节并拆出帧类型。
// expectFrame=false 表示期望这一列是明文（拿不到帧）。
func inputFrameType(t *testing.T, db *gorm.DB, id uint) (compress.Type, bool) {
	t.Helper()
	raw := rawColumn(t, db, id, "input")
	f, err := compress.Unmarshal(raw)
	if err != nil {
		return 0, false
	}
	return f.Type, true
}

// 大 body 必须走块表：库里躺着的是**引用序列**，不是原文，也不是逐行帧。
func TestChatIOInput_LargeBodyGoesToBlockTable(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()
	body := BodyBytes(strings.Repeat(bigText(40), 30))

	io := ChatIO{LogId: 1, Input: body}
	if err := gorm.G[ChatIO](db).Create(ctx, &io); err != nil {
		t.Fatalf("写入失败：%v", err)
	}

	typ, ok := inputFrameType(t, db, io.ID)
	if !ok {
		t.Fatalf("input 没有落成帧：%x", rawColumn(t, db, io.ID, "input")[:16])
	}
	if typ != compress.TypeBlockRefs {
		t.Fatalf("大 body 落的是 %s 帧，应当走块表（blockrefs）", typ)
	}
	if got := columnType(t, db, io.ID, "input"); got != "blob" {
		t.Fatalf("帧必须以 BLOB 落库（blob 即帧），得到 %s", got)
	}
	// 库里存的不该是原文——那说明根本没压。
	raw := rawColumn(t, db, io.ID, "input")
	if bytes.Contains(raw, []byte("chatcmpl-abc")) {
		t.Fatal("input 列里还能直接搜到正文，压缩没生效")
	}
	if n := blockCount(t, db); n == 0 {
		t.Fatal("走了块表却没有块")
	}

	got, err := gorm.G[ChatIO](db).Where("id = ?", io.ID).First(ctx)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if !bytes.Equal(got.Input, body) {
		t.Fatalf("还原不一致：%d 字节 vs %d 字节", len(got.Input), len(body))
	}
}

// 小 body 走逐行帧或原样明文，反正不进块表——为几百字节建一行块表是纯亏。
func TestChatIOInput_SmallBodyStaysOutOfBlockTable(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()

	// 高度可压的一段，长度介于"比帧头大"与 inputBlockMin 之间。
	body := BodyBytes(strings.Repeat(`{"role":"user","content":"你好"}`, 40))
	if len(body) >= inputBlockMin {
		t.Fatalf("这条用例的前提是 body 小于 %d 字节，实际 %d", inputBlockMin, len(body))
	}
	io := ChatIO{LogId: 2, Input: body}
	if err := gorm.G[ChatIO](db).Create(ctx, &io); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	if n := blockCount(t, db); n != 0 {
		t.Fatalf("小 body 不该产生块，块表有 %d 行", n)
	}
	if typ, ok := inputFrameType(t, db, io.ID); ok && typ != compress.TypeRowFrame {
		t.Fatalf("小 body 落的是 %s 帧，只该是逐行帧或明文", typ)
	}
	got, err := gorm.G[ChatIO](db).Where("id = ?", io.ID).First(ctx)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if !bytes.Equal(got.Input, body) {
		t.Fatal("还原不一致")
	}
}

// 钩子必须在这四条路径上都触发。**这是整个接入方式的前提**：
// 计划里"用模型钩子而不是 serializer"的结论，全靠"泛型 API 也走同一套回调"。
// 万一哪天 GORM 改了这一点，缺了这条测试就是静默的数据损坏。
func TestChatIOInput_HooksFireOnBothAPIs(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()
	body := BodyBytes(strings.Repeat(bigText(40), 30))

	// 写：经典 API 与泛型 API 各一次
	var classic ChatIO
	classicInput := append(BodyBytes(nil), body...)
	classicInput[len(classicInput)-1] ^= 0 // 保持内容不变，只是另起一份切片
	classic = ChatIO{LogId: 1, Input: classicInput}
	if err := db.Create(&classic).Error; err != nil {
		t.Fatalf("经典 API 写入失败：%v", err)
	}
	generic := ChatIO{LogId: 2, Input: append(BodyBytes(nil), body...)}
	if err := gorm.G[ChatIO](db).Create(ctx, &generic); err != nil {
		t.Fatalf("泛型 API 写入失败：%v", err)
	}

	for _, id := range []uint{classic.ID, generic.ID} {
		if typ, ok := inputFrameType(t, db, id); !ok || typ != compress.TypeBlockRefs {
			t.Fatalf("行 %d 的 input 没被 BeforeCreate 处理（typ=%v ok=%v）", id, typ, ok)
		}
	}

	// 读：First / Find / 经典 API 三条路都要还原
	one, err := gorm.G[ChatIO](db).Where("id = ?", generic.ID).First(ctx)
	if err != nil {
		t.Fatalf("泛型 First 失败：%v", err)
	}
	if !bytes.Equal(one.Input, body) {
		t.Fatal("泛型 First 读回的内容不对")
	}
	many, err := gorm.G[ChatIO](db).Where("id IN ?", []uint{generic.ID, classic.ID}).Find(ctx)
	if err != nil {
		t.Fatalf("泛型 Find 失败：%v", err)
	}
	if len(many) != 2 {
		t.Fatalf("Find 出了 %d 行", len(many))
	}
	for _, r := range many {
		if !bytes.Equal(r.Input, body) {
			t.Fatalf("Find 读回的行 %d 内容不对", r.ID)
		}
	}
	var viaClassic ChatIO
	if err := db.First(&viaClassic, generic.ID).Error; err != nil {
		t.Fatalf("经典 First 失败：%v", err)
	}
	if !bytes.Equal(viaClassic.Input, body) {
		t.Fatal("经典 First 读回的内容不对")
	}
}

// 钩子必须幂等：GORM 的默认 logger 在慢查询/报错时会把值再取一遍。
// 重复跑一次 packBody / unpackBody 不能改变结果。
func TestChatIOInput_HooksAreIdempotent(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()
	body := BodyBytes(strings.Repeat(bigText(40), 30))

	io := ChatIO{LogId: 1, Input: append(BodyBytes(nil), body...)}
	if err := io.BeforeCreate(db.WithContext(ctx)); err != nil {
		t.Fatalf("第一次 packBody 失败：%v", err)
	}
	packed := append(BodyBytes(nil), io.Input...)
	if err := io.BeforeCreate(db.WithContext(ctx)); err != nil {
		t.Fatalf("第二次 packBody 失败：%v", err)
	}
	if !bytes.Equal(packed, io.Input) {
		t.Fatal("packBody 不幂等：第二次把已经压好的值又动了一遍")
	}
	if err := io.AfterFind(db.WithContext(ctx)); err != nil {
		t.Fatalf("AfterFind 失败：%v", err)
	}
	if !bytes.Equal(io.Input, body) {
		t.Fatal("AfterFind 没还原出原文")
	}
	// 再跑一遍 AfterFind：此时 Input 已是明文，必须原样放行
	if err := io.AfterFind(db.WithContext(ctx)); err != nil {
		t.Fatalf("对明文再跑一次 AfterFind 失败：%v", err)
	}
	if !bytes.Equal(io.Input, body) {
		t.Fatal("对明文再跑一次 AfterFind 把内容改了")
	}
}

// 关掉总开关**只停新写入**：已经压过的行照常读得回来。
//
// 这是运维事故时的回退手段，必须真的管用——所以它是一条测试，不是一句承诺。
func TestChatIOInput_KillSwitchStopsNewWritesOnly(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()
	body := BodyBytes(strings.Repeat(bigText(40), 30))

	// 先开着写一行
	before := ChatIO{LogId: 1, Input: append(BodyBytes(nil), body...)}
	if err := gorm.G[ChatIO](db).Create(ctx, &before); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	if typ, ok := inputFrameType(t, db, before.ID); !ok || typ != compress.TypeBlockRefs {
		t.Fatal("开关打开时没有走块表")
	}

	t.Setenv("DB_COMPRESS", "false")

	// 关掉之后再写一行：必须是明文
	after := ChatIO{LogId: 2, Input: append(BodyBytes(nil), body...)}
	if err := gorm.G[ChatIO](db).Create(ctx, &after); err != nil {
		t.Fatalf("关掉开关后写入失败：%v", err)
	}
	if _, ok := inputFrameType(t, db, after.ID); ok {
		t.Fatal("开关关掉后新行仍被压了")
	}
	if got := columnType(t, db, after.ID, "input"); got != "text" {
		t.Fatalf("开关关掉后新行应当是明文 text，得到 %s", got)
	}

	// 两条都要读得回来——尤其是关掉之后写的那条明文行。
	for _, id := range []uint{before.ID, after.ID} {
		got, err := gorm.G[ChatIO](db).Where("id = ?", id).First(ctx)
		if err != nil {
			t.Fatalf("行 %d 读回失败：%v", id, err)
		}
		if !bytes.Equal(got.Input, body) {
			t.Fatalf("行 %d 内容不对", id)
		}
	}
}

// 历史明文行 + 新压缩行混在同一张表里，两种都要读对。
// 这是迁移可以分批、可以中断的前提。
func TestChatIOInput_MixedOldAndNewRows(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()
	body := BodyBytes(strings.Repeat(bigText(40), 30))

	// 造一条"历史行"：绕过钩子直接写原文（迁移前的库就是这个样子）
	if err := db.Exec(`INSERT INTO chat_ios (log_id, input, created_at, updated_at) VALUES (?, ?, datetime('now'), datetime('now'))`,
		1, string(body)).Error; err != nil {
		t.Fatalf("写历史行失败：%v", err)
	}
	if got := columnType(t, db, 1, "input"); got != "text" {
		t.Fatalf("历史行应当是 text，得到 %s", got)
	}

	// 再造一条新行（走钩子）
	fresh := ChatIO{LogId: 2, Input: append(BodyBytes(nil), body...)}
	if err := gorm.G[ChatIO](db).Create(ctx, &fresh); err != nil {
		t.Fatalf("写新行失败：%v", err)
	}
	if got := columnType(t, db, fresh.ID, "input"); got != "blob" {
		t.Fatalf("新行应当是 blob，得到 %s", got)
	}

	for _, id := range []uint{1, fresh.ID} {
		got, err := gorm.G[ChatIO](db).Where("id = ?", id).First(ctx)
		if err != nil {
			t.Fatalf("行 %d 读回失败：%v", id, err)
		}
		if !bytes.Equal(got.Input, body) {
			t.Fatalf("行 %d（%s）内容不对", id, columnType(t, db, id, "input"))
		}
	}
}

// 边界输入：空、只有一个字节、非法 UTF-8、内嵌 NUL、纯随机二进制。
// 这些是帧格式最容易出事的地方（magic 的首字节就是 0x00）。
func TestChatIOInput_EdgeCases(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()

	cases := []struct {
		name string
		body BodyBytes
	}{
		{"空", BodyBytes("")},
		{"单字节", BodyBytes("x")},
		{"非法 UTF-8", BodyBytes(strings.Repeat("\xff\xfe 坏字节 ", 500))},
		{"内嵌 NUL", BodyBytes(fmt.Sprintf("头\x00%s\x00尾", strings.Repeat("正文", 3000)))},
		{"以帧 magic 开头但版本不符", BodyBytes("\x00LCZ\x09" + strings.Repeat("x", 9000))},
		{"以帧 magic 开头但类型未知", BodyBytes("\x00LCZ\x01\x01\x7f" + strings.Repeat("x", 9000))},
		{"纯随机二进制", randomBytes(70 << 10)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			io := ChatIO{LogId: 1, Input: append(BodyBytes(nil), c.body...)}
			if err := gorm.G[ChatIO](db).Create(ctx, &io); err != nil {
				t.Fatalf("写入失败：%v", err)
			}
			got, err := gorm.G[ChatIO](db).Where("id = ?", io.ID).First(ctx)
			if err != nil {
				t.Fatalf("读回失败：%v", err)
			}
			if !bytes.Equal(got.Input, c.body) {
				t.Fatalf("还原不一致：%d 字节 vs %d 字节", len(got.Input), len(c.body))
			}
		})
	}
}

// 首字节是 0x00、且前几个字节碰巧长得像帧 magic 的 body，**不能被误当成帧**。
//
// 合法 JSON 的首字节不可能是 0x00（RFC 8259），所以这一类输入现实中不该出现在
// input 列里。但"不该出现"不是"可以随便处理"：真出现了也必须读得回来。
//
// 注意这里断言的不是"留在明文"——它们和任何 body 一样会被正常压缩，
// 只是**不能走判帧那条路**。误判的后果不是"没压"，是读的时候拿一个假帧头
// 去解压，解出垃圾或者直接报错。
func TestChatIOInput_MagicLookalikesAreNotMistakenForFrames(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()
	bodies := []BodyBytes{
		append(BodyBytes("\x00LCZ"), []byte(" 只是碰巧长这样")...),
		BodyBytes("\x00LCZ\x09 版本号对不上" + strings.Repeat("x", 9000)),
		BodyBytes("\x00LCZ\x01\x01\x7f 类型不认识" + strings.Repeat("x", 9000)),
	}
	// 有意**不**包含的一类：`\x00LCZ` + 版本对 + 已知编解码器 + 已知类型。
	// 那条与"真帧损坏"在字节上完全无法区分，按设计必须报错而不是猜（见
	// TestChatIOInput_CorruptFrameFailsLoudly）。这个歧义是接受的，因为
	// input 列里只可能是 JSON，而 JSON 的首字节不可能是 0x00——见 frame.go 开头。
	for i, body := range bodies {
		if compress.LooksLikeFrame(body) {
			t.Fatalf("第 %d 条被误判成帧了", i)
		}
	}
	for i, body := range bodies {
		io := ChatIO{LogId: uint(i + 1), Input: append(BodyBytes(nil), body...)}
		if err := gorm.G[ChatIO](db).Create(ctx, &io); err != nil {
			t.Fatalf("第 %d 条写入失败：%v", i, err)
		}
		got, err := gorm.G[ChatIO](db).Where("id = ?", io.ID).First(ctx)
		if err != nil {
			t.Fatalf("第 %d 条读回失败：%v", i, err)
		}
		if !bytes.Equal(got.Input, body) {
			t.Fatalf("第 %d 条读回来变了", i)
		}
	}
}

// 反面：帧头**完全合法**但我们解不开（未知编解码器）时，必须报错，
// 绝不静默退回明文——那等于把二进制垃圾当成用户数据吐出去。
//
// 这一类是设计上无法与"真帧损坏"区分的（见 frame.go 的 ErrCorrupt 注释），
// 所以它的行为被钉成"报错"，并且这里把它写死成测试。
func TestChatIOInput_CorruptFrameFailsLoudly(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()

	// 手搓一个 codec=99 的帧（我们的 magic、我们的版本、已知的类型，但编解码器不存在）
	bad := compress.Marshal(compress.Frame{
		Codec: 99, Type: compress.TypeRowFrame, RawLen: 4, Payload: []byte("data"),
	})
	io := ChatIO{LogId: 1, Input: BodyBytes(bad)}
	if err := gorm.G[ChatIO](db).Create(ctx, &io); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	// 写进去时钩子认它为帧（LooksLikeFrame 故意不校验 codec），原样落库
	if got := columnType(t, db, io.ID, "input"); got != "blob" {
		t.Fatalf("期望原样落成 blob，得到 %s", got)
	}
	if _, err := gorm.G[ChatIO](db).Where("id = ?", io.ID).First(ctx); err == nil {
		t.Fatal("未知编解码器的帧必须报错，绝不能退回明文")
	}
}

// BodyBytes 的落库形态完全由内容决定：帧 → BLOB，明文 → TEXT，空 → NULL。
// 这条不变量是迁移期"一句 typeof 就挑出没迁的行"的依据。
func TestBodyBytes_ValuePicksType(t *testing.T) {
	frame := EncodeRefs([]uint32{1, 2, 3})
	cases := []struct {
		name string
		in   BodyBytes
		want any
	}{
		{"nil → NULL", nil, nil},
		// 空值落 NULL，不是 `''`：这三列里 **TEXT 只表示非空明文**，
		// 三者与 typeof 一一对应。见 serializer.go 里那条不变量的推导。
		{"空 → NULL", BodyBytes(""), nil},
		{"明文 → TEXT", BodyBytes(`{"model":"gpt-4o"}`), `{"model":"gpt-4o"}`},
		{"帧 → BLOB", BodyBytes(frame), []byte(frame)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.in.Value()
			if err != nil {
				t.Fatalf("Value 失败：%v", err)
			}
			switch want := c.want.(type) {
			case nil:
				if got != nil {
					t.Fatalf("期望 NULL，得到 %#v", got)
				}
			case string:
				if got != want {
					t.Fatalf("期望 TEXT %q，得到 %#v", want, got)
				}
			case []byte:
				b, ok := got.([]byte)
				if !ok || !bytes.Equal(b, want) {
					t.Fatalf("期望 BLOB %x，得到 %#v", want, got)
				}
			}
		})
	}
}

// Scan 要认齐三种驱动类型，并且不能与已有内容共享底层数组。
func TestBodyBytes_Scan(t *testing.T) {
	var b BodyBytes
	if err := b.Scan(nil); err != nil || b != nil {
		t.Fatalf("扫 NULL：%v %v", b, err)
	}
	if err := b.Scan("文本"); err != nil || string(b) != "文本" {
		t.Fatalf("扫 TEXT：%q %v", b, err)
	}
	src := []byte{0x00, 0xff, 0x10}
	if err := b.Scan(src); err != nil || !bytes.Equal(b, src) {
		t.Fatalf("扫 BLOB：%x %v", b, err)
	}
	src[0] = 0xAA
	if b[0] == 0xAA {
		t.Fatal("Scan 与驱动给的切片共享了底层数组")
	}
	if err := b.Scan(int64(7)); err == nil {
		t.Fatal("整数驱动值应当报错，而不是被悄悄转成别的东西")
	}
}

// 关掉块缓存（把单例换成无缓存的）也必须读得回来——缓存在设计上是可选的加速，
// 不是正确性的一环。存储层的单测覆盖过一次，这里再过一遍钩子路径。
func TestChatIOInput_WorksWithoutGroupCache(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()
	body := BodyBytes(strings.Repeat(bigText(40), 30))

	io := ChatIO{LogId: 1, Input: append(BodyBytes(nil), body...)}
	if err := gorm.G[ChatIO](db).Create(ctx, &io); err != nil {
		t.Fatalf("写入失败：%v", err)
	}

	original := blockStore
	blockStore = NewBlockStore(0)
	t.Cleanup(func() { blockStore = original })

	got, err := gorm.G[ChatIO](db).Where("id = ?", io.ID).First(ctx)
	if err != nil {
		t.Fatalf("无缓存读回失败：%v", err)
	}
	if !bytes.Equal(got.Input, body) {
		t.Fatal("无缓存时内容不对")
	}
}
