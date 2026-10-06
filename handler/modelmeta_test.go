package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/service/modelmeta"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 这一组测的是**接口那一层**：参数校验、上下游状态码、以及 JSON 的形状。
// 建议本身的逻辑在 service/modelmeta 里测，这里不重复。
//
// 整组不联网：modelMetaManager 这个包级变量就是为这件事留的口子，
// 测试里换成装好索引的管理器，生产路径走的是同一个方法。

// stubMetaSource 是一个不抓网络的来源，只把预先建好的索引交出去。
type stubMetaSource struct {
	cat *modelmeta.Catalog
}

func (s stubMetaSource) Name() string { return modelmeta.SourceModelsDev }

func (s stubMetaSource) Fetch(context.Context) (*modelmeta.Catalog, error) { return s.cat, nil }

// setupModelMetaHandler 起一个临时库、写两条上游、并把建议管理器换成
// 一份不联网的索引。
func setupModelMetaHandler(t *testing.T) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "modelmeta-handler.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.Provider{}, &models.Config{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	providers := []models.Provider{
		// 源里对得上（api 字段命中），模型也在。
		{Name: "Anthropic 官方", Type: "anthropic", Config: `{"base_url":"https://api.anthropic.com"}`},
		// 源里对不上：自建网关。
		{Name: "自建网关", Type: "", Config: `{"base_url":"https://api.my-gateway.example.com/v1"}`},
	}
	for i := range providers {
		providers[i].ID = uint(i + 1)
	}
	if err := db.Create(&providers).Error; err != nil {
		t.Fatalf("seed providers: %v", err)
	}
	// 策略那一行：值留空，LoadPolicy 会退回默认值（Enabled=true）。
	if err := db.Create(&models.Config{Key: models.KeyModelAutofillPolicy, Value: ""}).Error; err != nil {
		t.Fatalf("seed config: %v", err)
	}

	prevDB := models.DB
	models.DB = db
	t.Cleanup(func() {
		models.DB = prevDB
		// 先关连接再让 t.TempDir 清理（Windows 上文件被占用会导致清理失败）
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	// 一份最小索引：anthropic 下有一个模型，别家还有一个同名模型用来出候选。
	entries := []modelmeta.Entry{
		{
			Source: modelmeta.SourceModelsDev, Provider: "anthropic", ProviderName: "Anthropic",
			Model: "claude-sonnet-4-5", Currency: modelmeta.CurrencyUSD,
			ToolCall: boolPtr(true), StructuredOutput: boolPtr(true), Image: boolPtr(true),
			InputPrice: floatPtr(3), CacheReadPrice: floatPtr(0.3), OutputPrice: floatPtr(15),
			ContextLimit: 1000000, OutputLimit: 64000,
		},
		{
			// 与上面同名但属于别家：同名跨上游的那条路。
			Source: modelmeta.SourceModelsDev, Provider: "reseller", ProviderName: "某转售",
			Model: "claude-sonnet-4-5", Currency: modelmeta.CurrencyUSD,
			ToolCall: boolPtr(true),
		},
		{
			// 只有别家有，本家（anthropic）没有：出候选的那条路。
			// 候选只在"上游对齐上了、但这家没有这个模型"时给——
			// 上游压根不在数据源里时给的是另一种说法。
			Source: modelmeta.SourceModelsDev, Provider: "reseller", ProviderName: "某转售",
			Model: "reseller-only-model", Currency: modelmeta.CurrencyUSD,
			ToolCall: boolPtr(true), StructuredOutput: boolPtr(false),
			InputPrice: floatPtr(1), OutputPrice: floatPtr(2),
		},
	}
	infos := map[string]modelmeta.ProviderInfo{
		"anthropic": {Npm: "@ai-sdk/anthropic"},
		"reseller":  {Npm: "@ai-sdk/anthropic"},
	}
	cat := modelmeta.NewCatalog(modelmeta.SourceModelsDev, time.Now(), entries, infos)
	mgr := modelmeta.NewManager(stubMetaSource{cat: cat})
	mgr.SetCatalog(cat)

	prevMgr := modelMetaManager
	modelMetaManager = func() *modelmeta.Manager { return mgr }
	t.Cleanup(func() { modelMetaManager = prevMgr })
}

func boolPtr(v bool) *bool        { return &v }
func floatPtr(v float64) *float64 { return &v }

// metaEnvelope 是接口响应的形状。data 部分留成 RawMessage，由各用例自己解——
// 它们要断言的东西不一样，一个统一的结构体会把"字段名对不对"这件事测丢。
type metaEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func callMetadata(t *testing.T, rawQuery string) (*httptest.ResponseRecorder, metaEnvelope) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/model-providers/metadata?"+rawQuery, nil)

	GetModelProviderMetadata(c)

	var env metaEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解响应: %v（原文 %s）", err, w.Body.String())
	}
	return w, env
}

// suggestion 是建议的响应形状，字段名照抄对外契约。
// 三个能力与三档价格都是指针：nil 即"源未提供"，这条区分是整套设计的地基。
type suggestion struct {
	Matched bool   `json:"matched"`
	Reason  string `json:"reason"`

	Source            string `json:"source"`
	Provider          string `json:"provider"`
	ProviderName      string `json:"provider_name"`
	ProviderMatchRule string `json:"provider_match_rule"`
	Model             string `json:"model"`
	MatchRule         string `json:"match_rule"`
	Status            string `json:"status"`

	ToolCall         *bool `json:"tool_call"`
	StructuredOutput *bool `json:"structured_output"`
	Image            *bool `json:"image"`

	InputPrice     *float64 `json:"input_price"`
	CacheReadPrice *float64 `json:"cache_read_price"`
	OutputPrice    *float64 `json:"output_price"`
	Currency       string   `json:"currency"`

	ContextLimit int `json:"context_limit"`
	OutputLimit  int `json:"output_limit"`

	Candidates []struct {
		Source       string `json:"source"`
		Provider     string `json:"provider"`
		ProviderName string `json:"provider_name"`
		Model        string `json:"model"`
		ToolCall     *bool  `json:"tool_call"`
		SameProtocol bool   `json:"same_protocol"`
	} `json:"candidates"`
}

func decodeSuggestion(t *testing.T, raw json.RawMessage) suggestion {
	t.Helper()
	var s suggestion
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("解建议: %v（原文 %s）", err, raw)
	}
	return s
}

func TestGetModelProviderMetadataMatched(t *testing.T) {
	setupModelMetaHandler(t)

	w, env := callMetadata(t, "provider_id=1&provider_model=claude-sonnet-4-5")
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP 状态 = %d，期望 200", w.Code)
	}
	if env.Code != http.StatusOK {
		t.Fatalf("业务码 = %d，期望 200", env.Code)
	}

	s := decodeSuggestion(t, env.Data)
	if !s.Matched {
		t.Fatalf("期望命中，得到 reason=%q", s.Reason)
	}
	if s.Provider != "anthropic" || s.ProviderName != "Anthropic" {
		t.Errorf("上游 = (%q, %q)，期望 (anthropic, Anthropic)", s.Provider, s.ProviderName)
	}
	if s.ProviderMatchRule == "" {
		t.Error("provider_match_rule 必须回给前端：对齐错了，模型 id 再准也会取到别家的价格")
	}
	if s.MatchRule != "exact" {
		t.Errorf("match_rule = %q，期望 exact", s.MatchRule)
	}
	if s.ToolCall == nil || !*s.ToolCall {
		t.Error("tool_call 期望 true")
	}
	if s.InputPrice == nil || *s.InputPrice != 3 {
		t.Errorf("input_price = %v，期望 3", s.InputPrice)
	}
	if s.Currency != modelmeta.CurrencyUSD {
		t.Errorf("currency = %q，期望 %q", s.Currency, modelmeta.CurrencyUSD)
	}

	// JSON 里必须**真的有这三个键**。前端的做法是"键在就填、键是 null 就不动"，
	// 所以 omitempty 之类的写法会把这个区分抹掉，这正是不该加它的地方。
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		t.Fatalf("解原始字段: %v", err)
	}
	for _, key := range []string{"tool_call", "structured_output", "image", "input_price", "output_price"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("响应里必须有 %q 这个键（值可以是 null）", key)
		}
	}
}

// TestGetModelProviderMetadataMissingFieldsAreNull 钉的是线上那一半：
// 源没给的字段在 JSON 里是 null，**不是 false / 0**。前端遇到 null 就不动
// 表单里对应那一格；遇到 false 就会把它写成"不支持"。
func TestGetModelProviderMetadataMissingFieldsAreNull(t *testing.T) {
	setupModelMetaHandler(t)

	// 第二条 anthropic 的条目没有 structured_output 与价格，但它是 reseller。
	// 这里直接用第一条上游、换一个只标了 tool_call 的模型。
	entries := []modelmeta.Entry{{
		Source: modelmeta.SourceModelsDev, Provider: "anthropic", ProviderName: "Anthropic",
		Model: "claude-haiku-4-5", Currency: modelmeta.CurrencyUSD,
		ToolCall: boolPtr(true),
	}}
	cat := modelmeta.NewCatalog(modelmeta.SourceModelsDev, time.Now(), entries,
		map[string]modelmeta.ProviderInfo{"anthropic": {Npm: "@ai-sdk/anthropic"}})
	mgr := modelmeta.NewManager(stubMetaSource{cat: cat})
	mgr.SetCatalog(cat)
	prev := modelMetaManager
	modelMetaManager = func() *modelmeta.Manager { return mgr }
	t.Cleanup(func() { modelMetaManager = prev })

	_, env := callMetadata(t, "provider_id=1&provider_model=claude-haiku-4-5")

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		t.Fatalf("解原始字段: %v", err)
	}
	for _, key := range []string{"structured_output", "image", "input_price", "cache_read_price", "output_price"} {
		got, ok := raw[key]
		if !ok {
			t.Errorf("响应里必须有 %q 这个键", key)
			continue
		}
		if string(got) != "null" {
			t.Errorf("%s = %s，源没给就是 null，不能是 false / 0", key, got)
		}
	}
	if string(raw["tool_call"]) != "true" {
		t.Errorf("tool_call = %s，期望 true", raw["tool_call"])
	}

	// Go 侧看到的也必须是 nil。
	s := decodeSuggestion(t, env.Data)
	if s.StructuredOutput != nil || s.Image != nil {
		t.Error("源没给的能力字段解出来该是 nil")
	}
	if s.InputPrice != nil || s.CacheReadPrice != nil || s.OutputPrice != nil {
		t.Error("源没给的价格解出来该是 nil")
	}
}

// TestGetModelProviderMetadataCandidates：上游对齐上了、但这家没有这个模型，
// 而别家有同名的——这时给候选，**不给可填写值**（§4.1.1）。
func TestGetModelProviderMetadataCandidates(t *testing.T) {
	setupModelMetaHandler(t)

	// 第一条上游是 anthropic 官方，它下面没有 reseller-only-model。
	_, env := callMetadata(t, "provider_id=1&provider_model=reseller-only-model")

	s := decodeSuggestion(t, env.Data)
	if s.Matched {
		t.Fatal("本家没有这个模型，不该给可填写值")
	}
	// 对齐上了上游、只是没这个模型——这与"上游都不在数据源里"是两件事。
	if s.Reason != "no_model_match" {
		t.Errorf("reason = %q，期望 no_model_match", s.Reason)
	}
	if s.Model != "" || s.Provider != "" {
		t.Error("没命中就不该带上游与模型信息")
	}
	// 顶层一个可填写值都没有——前端的预填只看顶层，这条是硬约束。
	if s.ToolCall != nil || s.StructuredOutput != nil || s.Image != nil {
		t.Error("有候选时顶层不该有可填写值")
	}
	if s.InputPrice != nil || s.OutputPrice != nil || s.CacheReadPrice != nil {
		t.Error("有候选时顶层不该有价格")
	}
	if len(s.Candidates) != 1 {
		t.Fatalf("候选数 = %d，期望 1", len(s.Candidates))
	}
	c := s.Candidates[0]
	if c.Model != "reseller-only-model" || c.Provider != "reseller" {
		t.Errorf("候选 = (%q, %q)，期望 (reseller, reseller-only-model)", c.Provider, c.Model)
	}
	if c.ProviderName != "某转售" {
		t.Errorf("候选显示名 = %q：提示文案里那句话说不出是哪个上游就没用了", c.ProviderName)
	}
	if c.ToolCall == nil || !*c.ToolCall {
		t.Error("候选该带自己那份能力值")
	}
	// 候选的协议与当前上游一致（都是 anthropic），排前面。
	if !c.SameProtocol {
		t.Error("same_protocol 期望 true")
	}
}

// TestGetModelProviderMetadataProviderNotInSource：上游压根不在数据源里时
// **不给候选**，给的是"这一家不在数据源里"。
//
// 这两句话对用户是两件不同的事：前者他自己能处理（换个模型名或在数据源里
// 找找），后者是"这家上游没被收录"，而候选列表在那种情况下会变成一面墙
// ——源里所有上游的同名模型都算"别家"，没有一条是有依据的。
func TestGetModelProviderMetadataProviderNotInSource(t *testing.T) {
	setupModelMetaHandler(t)

	_, env := callMetadata(t, "provider_id=2&provider_model=claude-sonnet-4-5")

	s := decodeSuggestion(t, env.Data)
	if s.Matched {
		t.Fatal("对不上上游时不该命中")
	}
	if s.Reason != "no_provider_match" {
		t.Errorf("reason = %q，期望 no_provider_match", s.Reason)
	}
	if len(s.Candidates) != 0 {
		t.Errorf("上游没对齐上时不该给候选，得到 %d 条", len(s.Candidates))
	}
}

func TestGetModelProviderMetadataNoMatch(t *testing.T) {
	setupModelMetaHandler(t)

	_, env := callMetadata(t, "provider_id=1&provider_model=no-such-model")

	s := decodeSuggestion(t, env.Data)
	if s.Matched {
		t.Fatal("不该命中")
	}
	// 对齐上了上游、只是没这个模型——这与"上游都不在数据源里"是两件事。
	if s.Reason != "no_model_match" {
		t.Errorf("reason = %q，期望 no_model_match", s.Reason)
	}
	if len(s.Candidates) != 0 {
		t.Errorf("得到 %d 条候选，期望 0：同名才对", len(s.Candidates))
	}
}

func TestGetModelProviderMetadataBadRequests(t *testing.T) {
	setupModelMetaHandler(t)

	for _, tc := range []struct {
		name  string
		query string
	}{
		{"缺 provider_id", "provider_model=claude-sonnet-4-5"},
		{"缺 provider_model", "provider_id=1"},
		{"两个都缺", ""},
		{"provider_model 是空串", "provider_id=1&provider_model="},
		{"provider_id 不是数字", "provider_id=abc&provider_model=m"},
		{"provider_id 是负数", "provider_id=-1&provider_model=m"},
		{"provider_id 是小数", "provider_id=1.5&provider_model=m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, env := callMetadata(t, tc.query)
			// 参数错在业务码上表达，HTTP 仍是 200——与这个仓库既有接口一致。
			if w.Code != http.StatusOK {
				t.Errorf("HTTP 状态 = %d，期望 200", w.Code)
			}
			if env.Code != http.StatusBadRequest {
				t.Errorf("业务码 = %d，期望 400", env.Code)
			}
		})
	}
}

func TestGetModelProviderMetadataProviderNotFound(t *testing.T) {
	setupModelMetaHandler(t)

	w, env := callMetadata(t, "provider_id=999&provider_model=claude-sonnet-4-5")
	if w.Code != http.StatusOK {
		t.Errorf("HTTP 状态 = %d，期望 200", w.Code)
	}
	if env.Code != http.StatusNotFound {
		t.Errorf("业务码 = %d，期望 404", env.Code)
	}
}

// TestGetModelProviderMetadataIsReadOnly：这个端点是纯读的——
// 它不写任何业务表，也不因为策略里 Enabled=false 就改行为。
//
// Enabled 管的是"弹窗要不要预填"，而落库与否完全由用户点的那个保存按钮
// 决定（§4.2）。服务端在这里检查它只会制造"改了开关但接口行为跟着变"的
// 谁也说不清的现象。
func TestGetModelProviderMetadataIsReadOnly(t *testing.T) {
	setupModelMetaHandler(t)

	var before int64
	if err := models.DB.Model(&models.Provider{}).Count(&before).Error; err != nil {
		t.Fatalf("数上游: %v", err)
	}

	// 把策略关掉。
	if _, err := gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeyModelAutofillPolicy).
		Update(context.Background(), "value", `{"enabled":false}`); err != nil {
		t.Fatalf("关策略: %v", err)
	}

	_, env := callMetadata(t, "provider_id=1&provider_model=claude-sonnet-4-5")
	if s := decodeSuggestion(t, env.Data); !s.Matched {
		t.Fatalf("建议端点不该看 Enabled，得到 reason=%q", s.Reason)
	}

	var after int64
	if err := models.DB.Model(&models.Provider{}).Count(&after).Error; err != nil {
		t.Fatalf("数上游: %v", err)
	}
	if before != after {
		t.Errorf("上游数从 %d 变成 %d：这个端点不该写任何表", before, after)
	}
}

// TestGetModelProviderMetadataBaseURLFromProviderRow：base_url 从库里那条
// provider 现取，而不是让前端再传一份。
//
// 让前端传的话，"表单里的 base_url 与库里的一致"就成了一条要人维护的约定，
// 两边不一致时接口会按错的源去查，而且不报任何错。
func TestGetModelProviderMetadataBaseURLFromProviderRow(t *testing.T) {
	setupModelMetaHandler(t)

	// 前端就算传了 base_url 也不该被采纳：库里那条是 anthropic 官方，
	// 而查询串里给的是另一台机器。
	_, env := callMetadata(t, "provider_id=1&provider_model=claude-sonnet-4-5&base_url=https://api.my-gateway.example.com")
	s := decodeSuggestion(t, env.Data)
	if !s.Matched {
		t.Fatalf("期望按库里的 base_url 命中，得到 reason=%q", s.Reason)
	}
	if s.Provider != "anthropic" {
		t.Errorf("provider = %q，期望 anthropic（按库里那条对齐）", s.Provider)
	}
}
