package models

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/atopos31/llmio/pkg/compress"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// 本文件验证 body 列的压缩接入，盯的是三件事（按危害排序）：
//
//  1. **读得回来**。写进去的是二进制帧，读出来必须还是原来那串字符；
//     历史明文行（没有任何标记）也必须照常读。
//  2. **不碰表结构**。7 GB 的表上一次 AutoMigrate 重建就是灾难，
//     所以 chat_ios 的 DDL 必须一字不变（C1）。
//  3. **二进制帧不许经过 json.Marshal**。陷阱 B：非法 UTF-8 会被换成 U+FFFD，
//     帧一旦走了那条路就永久损坏。这条在改序列化器之前必然失败，是回归护栏。

// openCompressDB 起一个干净库，跑完整初始化，返回包级 DB。
func openCompressDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "compress.db")
	Init(context.Background(), path)
	closeOnCleanup(t, DB)
	return DB
}

// bigText 造一份有代表性的响应体：真实 SSE 那种高度重复的 JSON 骨架。
// 不可压的数据测不出压缩有没有生效，所以要"像真的一样的"内容。
func bigText(n int) string {
	chunk := `{"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1730000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"这是一段中文回复，夹杂 ASCII 与 emoji 🚀"}}]}`
	return strings.Repeat(chunk, n)
}

func bigChunks(n int) []string {
	one := `data: {"choices":[{"delta":{"content":"分片内容 with ASCII"}}]}`
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, one)
	}
	return out
}

// rawColumn 读一列的**原始字节**，绕过模型层的任何解码。
// 这是"库里到底存了什么"的唯一可信视角。
func rawColumn(t *testing.T, db *gorm.DB, id uint, col string) []byte {
	t.Helper()
	var b []byte
	if err := db.Raw("SELECT "+col+" FROM chat_ios WHERE id = ?", id).Row().Scan(&b); err != nil {
		t.Fatalf("读 %s 原始字节失败：%v", col, err)
	}
	return b
}

func columnType(t *testing.T, db *gorm.DB, id uint, col string) string {
	t.Helper()
	var v string
	if err := db.Raw("SELECT typeof("+col+") FROM chat_ios WHERE id = ?", id).Row().Scan(&v); err != nil {
		t.Fatalf("读 %s 的 typeof 失败：%v", col, err)
	}
	return v
}

// 写进去是帧、读回来是原文，而且原始字节确实是**未经 JSON 转义**的帧。
func TestChatIOCompress_RoundTripThroughFrames(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()

	ofString := bigText(2000)
	ofArray := bigChunks(2000)
	io := ChatIO{
		LogId: 1,
		Input: `{"model":"gpt-4o"}`,
		OutputUnion: OutputUnion{
			OfString:      ofString,
			OfStringArray: ofArray,
		},
	}
	if err := gorm.G[ChatIO](db).Create(ctx, &io); err != nil {
		t.Fatalf("写入失败：%v", err)
	}

	for _, col := range []string{"of_string", "of_string_array"} {
		if got := columnType(t, db, io.ID, col); got != "blob" {
			t.Errorf("%s 应当以 BLOB 形态落库（blob 即帧），得到 %s", col, got)
		}
		raw := rawColumn(t, db, io.ID, col)
		if !bytes.HasPrefix(raw, compress.Magic[:]) {
			t.Fatalf("%s 的原始字节不是帧：%x", col, raw[:min(16, len(raw))])
		}
		// 帧本身必须能原样解开。若它中途过了 json.Marshal，非法 UTF-8 会变成
		// U+FFFD，这一行就会在这里报错——这就是陷阱 B 的回归护栏。
		plain, wasFrame, err := compress.DecompressBytes(raw)
		if err != nil {
			t.Fatalf("%s 的帧解不开（很可能经过了 json.Marshal）：%v", col, err)
		}
		if !wasFrame {
			t.Fatalf("%s 存的不是帧", col)
		}
		want, err := json.Marshal(ofArray)
		if col == "of_string" {
			want = []byte(ofString)
		}
		if err != nil {
			t.Fatalf("算期望值失败：%v", err)
		}
		if !bytes.Equal(plain, want) {
			t.Fatalf("%s 解开后的内容与写入的不一致", col)
		}
	}

	// 模型层读回来必须是原本的 Go 值（前端契约：OfStringArray 是真数组）
	got, err := gorm.G[ChatIO](db).Where("id = ?", io.ID).First(ctx)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if got.OfString != ofString {
		t.Error("of_string 往返后内容变了")
	}
	if len(got.OfStringArray) != len(ofArray) {
		t.Fatalf("of_string_array 往返后长度变了：%d vs %d", len(got.OfStringArray), len(ofArray))
	}
	for i := range ofArray {
		if got.OfStringArray[i] != ofArray[i] {
			t.Fatalf("of_string_array 第 %d 项变了", i)
		}
	}
	if got.Input != `{"model":"gpt-4o"}` {
		t.Errorf("input 不该被动过，得到 %q", got.Input)
	}
}

// 历史行是明文，没有任何标记。同一列混装两种形态，靠帧头区分。
func TestChatIOCompress_LegacyPlaintextStillReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	openLegacyDB(t, path)
	Init(context.Background(), path)
	closeOnCleanup(t, DB)

	ctx := context.Background()
	cases := []struct {
		id       uint
		input    string
		ofString string
		ofArray  []string
	}{
		// 上游形态：of_string_array 是空文本而不是 NULL
		{1, `{"model":"gpt-4o"}`, `{"id":"chatcmpl-1"}`, nil},
		// of_string 空、of_string_array 是 "[]"
		{2, `{"model":"claude-3"}`, "", []string{}},
	}
	for _, c := range cases {
		got, err := gorm.G[ChatIO](DB).Where("id = ?", c.id).First(ctx)
		if err != nil {
			t.Fatalf("读旧行 %d 失败：%v", c.id, err)
		}
		if got.Input != c.input {
			t.Errorf("行 %d 的 input 变了：%q", c.id, got.Input)
		}
		if got.OfString != c.ofString {
			t.Errorf("行 %d 的 of_string 变了：%q", c.id, got.OfString)
		}
		if len(got.OfStringArray) != len(c.ofArray) {
			t.Errorf("行 %d 的 of_string_array 变了：%v", c.id, got.OfStringArray)
		}
	}
}

// chatIOsShape 把 chat_ios 的表结构取成一个可比较的字符串：
// 建表语句 + 逐列定义 + 索引。任何一处变了都说明 AutoMigrate 动了表。
func chatIOsShape(t *testing.T, path string) string {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	var b strings.Builder
	var ddl string
	if err := db.Raw(`SELECT sql FROM sqlite_master WHERE type='table' AND name='chat_ios'`).Row().Scan(&ddl); err != nil {
		t.Fatalf("读建表语句失败：%v", err)
	}
	b.WriteString(ddl)

	rows, err := db.Raw(`PRAGMA table_info(chat_ios)`).Rows()
	if err != nil {
		t.Fatalf("读 table_info 失败：%v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, typ        string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("扫描 table_info 失败：%v", err)
		}
		b.WriteString("\n")
		b.WriteString(strings.Join([]string{name, typ, dflt.String}, "|"))
	}

	var indexes []string
	if err := db.Raw(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='chat_ios' ORDER BY name`).
		Scan(&indexes).Error; err != nil {
		t.Fatalf("读索引失败：%v", err)
	}
	b.WriteString("\n")
	b.WriteString(strings.Join(indexes, ","))
	return b.String()
}

// C1：给两列加序列化器**不许**改变 chat_ios 的表结构。
//
// 这一条必须单独测，因为 AutoMigrate 改列类型在 SQLite 上是
// "建新表-拷数据-删旧表-改名"。在 7 GB 的生产库上，那等于一次灾难。
// 真库副本上的同款断言在真机验收里再跑一遍（见 docs/db-compression-safety.md §四.4）。
func TestChatIOCompress_AutoMigrateLeavesChatIOsAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	openLegacyDB(t, path)

	before := chatIOsShape(t, path)
	Init(context.Background(), path)
	closeOnCleanup(t, DB)
	after := chatIOsShape(t, path)

	if before != after {
		t.Fatalf("AutoMigrate 改动了 chat_ios：\n之前：\n%s\n之后：\n%s", before, after)
	}
}

// 陷阱 A 的守卫：Updates 只带 OutputUnion 时，input 必须一个字节都不动。
//
// 现在 input 还是明文列，所以这条测的是 GORM 的"零值不进 SET"行为；
// 等 input 换成钩子接入（Phase 3），它会变成真正的护栏——
// 那时钩子里的块引用一旦被 GORM 带进 SET，行里已存好的请求体就被覆盖了。
func TestChatIOCompress_UpdatesOutputDoesNotTouchInput(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()

	const input = `{"model":"gpt-4o","messages":[{"role":"user","content":"别动我"}]}`
	io := ChatIO{LogId: 7, Input: input}
	if err := gorm.G[ChatIO](db).Create(ctx, &io); err != nil {
		t.Fatalf("写入失败：%v", err)
	}

	// 照抄 service/chat.go:314 的写法
	output := OutputUnion{OfString: bigText(500)}
	if _, err := gorm.G[ChatIO](db).Where("log_id = ?", 7).
		Updates(ctx, ChatIO{OutputUnion: output}); err != nil {
		t.Fatalf("更新响应体失败：%v", err)
	}

	if got := string(rawColumn(t, db, io.ID, "input")); got != input {
		t.Fatalf("input 被覆盖了：\n之前：%s\n之后：%s", input, got)
	}
	if got := columnType(t, db, io.ID, "input"); got != "text" {
		t.Errorf("本阶段 input 还应当是明文 text，得到 %s", got)
	}
	got, err := gorm.G[ChatIO](db).Where("id = ?", io.ID).First(ctx)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if got.OfString != output.OfString {
		t.Error("响应体没写进去")
	}
}

// Value 会被调两次（GORM 默认 logger 在慢查询/报错时对 driver.Valuer 再取一次值），
// 所以"同一份输入必得同一份输出"是结构性的要求，不是巧合。
func TestCompressSerializer_ValueIsIdempotent(t *testing.T) {
	ser, ofString, dst := parseChatIOField(t, "of_string")
	ctx := context.Background()
	plain := bigText(300)

	first, err := ser.Value(ctx, ofString, dst, plain)
	if err != nil {
		t.Fatalf("第一次编码失败：%v", err)
	}
	second, err := ser.Value(ctx, ofString, dst, plain)
	if err != nil {
		t.Fatalf("第二次编码失败：%v", err)
	}
	if !bytes.Equal(first.([]byte), second.([]byte)) {
		t.Fatal("同一份输入两次编码结果不同")
	}

	// 已经被压过的值再进一次：必须原样写回，绝不能二次压缩
	again, err := ser.Value(ctx, ofString, dst, string(first.([]byte)))
	if err != nil {
		t.Fatalf("编码已压过的值失败：%v", err)
	}
	if !bytes.Equal(first.([]byte), again.([]byte)) {
		t.Fatal("已经压过的值被二次压缩了")
	}
}

// parseChatIOField 解析出真实的 ChatIO schema 字段，用它直接驱动序列化器。
// 手搓 reflect 结构测不出真问题，用真模型的字段才有意义。
func parseChatIOField(t *testing.T, name string) (compressSerializer, *schema.Field, reflect.Value) {
	t.Helper()
	sch, err := schema.Parse(&ChatIO{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("解析 ChatIO 失败：%v", err)
	}
	field := sch.LookUpField(name)
	if field == nil {
		t.Fatalf("找不到字段 %s", name)
	}
	return compressSerializer{}, field, reflect.ValueOf(&ChatIO{}).Elem()
}

// 边界与兜底分支。这些路径在正常写入时走不到，但每一条都对应一种
// "宁可报错也不猜"的判断，值得逐条钉住。
func TestCompressSerializer_DefensiveBranches(t *testing.T) {
	ctx := context.Background()
	ser, ofString, _ := parseChatIOField(t, "of_string")
	_, ofArray, _ := parseChatIOField(t, "of_string_array")
	_, idField, _ := parseChatIOField(t, "id")

	fresh := func() reflect.Value { return reflect.ValueOf(&ChatIO{}).Elem() }

	// 空值的存储形态不许因为压缩而改变
	if v, err := ser.Value(ctx, ofString, fresh(), ""); err != nil || v != "" {
		t.Errorf("空字符串应当原样存 ''，得到 %#v / %v", v, err)
	}
	if v, err := ser.Value(ctx, ofArray, fresh(), []string(nil)); err != nil || v != nil {
		t.Errorf("nil 切片应当存 NULL，得到 %#v / %v", v, err)
	}
	// 空切片不是 nil，今天存的是 "[]"，压缩后也应当是它
	if v, err := ser.Value(ctx, ofArray, fresh(), []string{}); err != nil {
		t.Errorf("空切片编码失败：%v", err)
	} else if plain, _, derr := compress.DecompressBytes(v.([]byte)); derr != nil || string(plain) != "[]" {
		t.Errorf("空切片应当存成 []，得到 %q / %v", plain, derr)
	}

	// 不认识 Go 类型：宁可报错，也不猜着存
	if _, err := ser.Value(ctx, ofString, fresh(), int64(1)); err == nil {
		t.Error("不支持的类型必须报错")
	}

	// NULL → 字段归零
	dst := fresh()
	if err := ser.Scan(ctx, ofString, dst, nil); err != nil {
		t.Fatalf("扫描 NULL 失败：%v", err)
	}
	if got := dst.Interface().(ChatIO); got.OfString != "" || got.OfStringArray != nil {
		t.Errorf("NULL 应当把字段归零，得到 %+v", got.OutputUnion)
	}

	// 非常规驱动类型：按 JSON 文本兜底，而不是直接失败
	dst = fresh()
	if err := ser.Scan(ctx, ofString, dst, int64(7)); err != nil {
		t.Fatalf("非常规类型兜底失败：%v", err)
	}
	if got := dst.Interface().(ChatIO).OfString; got != "7" {
		t.Errorf("非常规类型应当兜底成 %q，得到 %q", "7", got)
	}

	// 字段类型不受支持：报错，而不是硬塞进去
	if err := ser.Scan(ctx, idField, fresh(), "x"); err == nil {
		t.Error("不受支持的字段类型必须报错")
	}

	// 明文不是合法 JSON 数组
	dst = fresh()
	if err := ser.Scan(ctx, ofArray, dst, `{"a":1}`); err == nil {
		t.Error("不是 JSON 数组的明文必须报错")
	}

	// 是帧、但解不开：必须报错。退回明文等于把二进制垃圾当用户数据吐出去
	frame, err := compress.Compress([]byte("目标内容"), compress.TypeRowFrame)
	if err != nil {
		t.Fatalf("造帧失败：%v", err)
	}
	head, err := compress.Unmarshal(frame)
	if err != nil {
		t.Fatalf("解帧失败：%v", err)
	}
	head.RawLen++ // 帧头仍合法，载荷对不上
	dst = fresh()
	if err := ser.Scan(ctx, ofString, dst, compress.Marshal(head)); err == nil {
		t.Error("坏帧必须报错，绝不能静默当明文")
	}
}
