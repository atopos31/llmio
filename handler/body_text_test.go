package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// GBK 形式的“你好，世界”。
// 写成字节而不是字符串字面量：源码文件是 UTF-8，
// 直接写中文字面量就不再是 GBK 字节了。
var gbkHello = []byte{0xc4, 0xe3, 0xba, 0xc3, 0xa3, 0xac, 0xca, 0xc0, 0xbd, 0xe7}

const gbkHelloText = "你好，世界"

func TestDecodeBodyText(t *testing.T) {
	// 图片头：GB18030 也能“解码”出来字符，但带控制字符，
	// 必须被 looksLikeText 拦下来标成 binary。
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d}

	tests := []struct {
		name     string
		raw      []byte
		wantText string
		wantEnc  string
	}{
		{name: "空 body", raw: nil, wantText: "", wantEnc: ""},
		{name: "utf-8 原样", raw: []byte(gbkHelloText), wantText: gbkHelloText, wantEnc: encodingUTF8},
		{name: "gbk 按 gb18030 解码", raw: gbkHello, wantText: gbkHelloText, wantEnc: encodingGB18030},
		{name: "二进制标成 binary", raw: png, wantEnc: encodingBinary},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text, enc := decodeBodyText(tc.raw)
			if enc != tc.wantEnc {
				t.Fatalf("encoding = %q, want %q", enc, tc.wantEnc)
			}
			if tc.wantText != "" && text != tc.wantText {
				t.Fatalf("text = %q, want %q", text, tc.wantText)
			}
			// 无论哪条分支，返回的必须是合法 UTF-8，
			// 否则 gin 序列化时仍会丢字节。
			if !utf8.ValidString(text) {
				t.Fatalf("text %q is not valid UTF-8", text)
			}
		})
	}
}

func TestGetChatIODecodesLegacyEncoding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chatio-encoding.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.ChatLog{}, &models.ChatIO{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	// 两条日志：一条单段响应，一条分段响应，输入都故意用 GBK 字节。
	logs := []models.ChatLog{
		{ID: 1, Name: "embed", ProviderName: "p1", ProviderModel: "bge-m3", Status: "success", Style: ""},
		{ID: 2, Name: "stream", ProviderName: "p1", ProviderModel: "bge-m3", Status: "success", Style: ""},
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}
	ios := []models.ChatIO{
		// of_string 走"string → 原始字节"存列，GBK 字节能原样留下，可被读路径解码；
		// of_string_array 是 []string → json.Marshal 后存列，非法 UTF-8 字节在**写入时**
		// 就已被替换成 U+FFFD（不可逆），所以这里只用正常 UTF-8 片段验证分段响应不被打标。
		{LogId: 1, Input: models.BodyBytes(gbkHello), OutputUnion: models.OutputUnion{OfString: string(gbkHello)}},
		{LogId: 2, Input: models.BodyBytes(gbkHello), OutputUnion: models.OutputUnion{OfStringArray: []string{`{"object":"list"}`, `{"object":"list"}`}}},
	}
	if err := db.Create(&ios).Error; err != nil {
		t.Fatalf("seed io: %v", err)
	}
	prev := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prev
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/logs/:id/io", GetChatIO)

	type ioResp struct {
		Code int
		Data struct {
			Input          string
			InputEncoding  string
			OfString       string
			OfStringArray  []string
			OutputEncoding string
		}
	}
	get := func(t *testing.T, id string) ioResp {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/logs/"+id+"/io", nil)
		router.ServeHTTP(w, req)
		var body ioResp
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
		}
		return body
	}

	t.Run("单段响应", func(t *testing.T) {
		body := get(t, "1")
		if body.Code != http.StatusOK {
			t.Fatalf("code = %d", body.Code)
		}
		if body.Data.Input != gbkHelloText || body.Data.InputEncoding != encodingGB18030 {
			t.Fatalf("input = %q (%s)", body.Data.Input, body.Data.InputEncoding)
		}
		if body.Data.OfString != gbkHelloText || body.Data.OutputEncoding != encodingGB18030 {
			t.Fatalf("ofString = %q (%s)", body.Data.OfString, body.Data.OutputEncoding)
		}
		if body.Data.OfStringArray != nil {
			t.Fatalf("ofStringArray = %v, want nil", body.Data.OfStringArray)
		}
	})

	t.Run("分段响应", func(t *testing.T) {
		body := get(t, "2")
		if body.Data.InputEncoding != encodingGB18030 {
			t.Fatalf("input encoding = %q", body.Data.InputEncoding)
		}
		if len(body.Data.OfStringArray) != 2 {
			t.Fatalf("ofStringArray = %v", body.Data.OfStringArray)
		}
		for _, chunk := range body.Data.OfStringArray {
			if chunk != `{"object":"list"}` {
				t.Fatalf("chunk = %q", chunk)
			}
		}
		if body.Data.OutputEncoding != encodingUTF8 {
			t.Fatalf("output encoding = %q", body.Data.OutputEncoding)
		}
	})

	t.Run("不存在的日志", func(t *testing.T) {
		if code := get(t, "99").Code; code == http.StatusOK {
			t.Fatalf("code = %d, want non-200", code)
		}
	})
}
