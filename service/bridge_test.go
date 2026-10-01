package service

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/atopos31/llmio/bridge"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
)

// 接线的测试。翻译本身（逐字段映射、有损记账）在 bridge 包里测；这里只测属于本层的
// 判断：谁能进候选池、方向对不对、记账有没有被收下来。

func TestServableTypes(t *testing.T) {
	cases := []struct {
		style string
		want  []string
	}{
		// Anthropic 上游只支持 OpenAI 协议时，OpenAI 客户端的模型列表里也得有它，
		// 否则客户端根本发现不了这个模型
		{consts.StyleOpenAI, []string{consts.StyleOpenAI, consts.StyleOpenAIRes, consts.StyleAnthropic}},
		{consts.StyleAnthropic, []string{consts.StyleAnthropic, consts.StyleOpenAI}},
		// Responses API 与 Gemini 不参与互转，一个都不多
		{consts.StyleOpenAIRes, []string{consts.StyleOpenAIRes}},
		{consts.StyleGemini, []string{consts.StyleGemini}},
	}
	for _, tc := range cases {
		t.Run(tc.style, func(t *testing.T) {
			got := ServableTypes(tc.style)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("ServableTypes(%q) = %v，期望 %v", tc.style, got, tc.want)
			}
		})
	}
}

func TestPreferDirect(t *testing.T) {
	openaiProvider := models.Provider{Name: "a", Type: consts.StyleOpenAI}
	anotherOpenAI := models.Provider{Name: "b", Type: consts.StyleOpenAI}
	anthropicProvider := models.Provider{Name: "c", Type: consts.StyleAnthropic}

	t.Run("本协议的都在时转换候选不进池", func(t *testing.T) {
		got := preferDirect([]models.Provider{anthropicProvider, openaiProvider, anotherOpenAI}, consts.StyleOpenAI)
		if len(got) != 2 {
			t.Fatalf("应当只剩本协议的两家: %v", got)
		}
		for _, p := range got {
			if p.Type != consts.StyleOpenAI {
				t.Fatalf("混进了别的协议: %v", got)
			}
		}
	})

	t.Run("只有一家本协议的也要它", func(t *testing.T) {
		// 现实里最常见的形状就是"这个模型只挂了一家上游，而它正好说客户端的话"。
		// 判据是"有没有"，不是"够不够多"——按数量挑会让这种模型白白走一趟转换
		got := preferDirect([]models.Provider{anthropicProvider, openaiProvider}, consts.StyleOpenAI)
		if len(got) != 1 || got[0].Name != openaiProvider.Name {
			t.Fatalf("应当只留那家本协议的: %v", got)
		}
	})

	t.Run("一个本协议的都没有才退回转换候选", func(t *testing.T) {
		got := preferDirect([]models.Provider{anthropicProvider}, consts.StyleOpenAI)
		if len(got) != 1 || got[0].Name != anthropicProvider.Name {
			t.Fatalf("应当退回转换候选: %v", got)
		}
	})

	t.Run("本来就是空的", func(t *testing.T) {
		if got := preferDirect(nil, consts.StyleOpenAI); len(got) != 0 {
			t.Fatalf("空候选不该凭空造出东西: %v", got)
		}
	})
}

func TestPoolFor(t *testing.T) {
	openaiProvider := models.Provider{Name: "a", Type: consts.StyleOpenAI}
	anthropicProvider := models.Provider{Name: "c", Type: consts.StyleAnthropic}
	all := []models.Provider{openaiProvider, anthropicProvider}
	on, off := true, false

	t.Run("开关打开：只留本协议", func(t *testing.T) {
		got := poolFor(all, consts.StyleOpenAI, &on)
		if len(got) != 1 || got[0].Type != consts.StyleOpenAI {
			t.Fatalf("打开时就该只剩本协议的: %v", got)
		}
	})

	t.Run("开关关掉：整池留下，由权重说话", func(t *testing.T) {
		got := poolFor(all, consts.StyleOpenAI, &off)
		if len(got) != 2 {
			t.Fatalf("关掉后本协议与可转换协议都要在池子里: %v", got)
		}
	})

	t.Run("没配过（老模型 nil）：按打开处理", func(t *testing.T) {
		// 转换功能上线前建的模型这一列是空的。若把 nil 当成"关"，这些模型的候选池
		// 会凭空多出别协议的上游——升级一次就静默改变了既有流量分配
		got := poolFor(all, consts.StyleOpenAI, nil)
		if len(got) != 1 || got[0].Type != consts.StyleOpenAI {
			t.Fatalf("nil 应当等价于打开: %v", got)
		}
	})

	t.Run("池子里没有本协议的：开关开着也只剩兜底的那家", func(t *testing.T) {
		// 与 preferDirect 的兜底一致：开着是"优先"，不是"只用本协议"，否则这个
		// 模型直接变成没有可用上游
		got := poolFor([]models.Provider{anthropicProvider}, consts.StyleOpenAI, &on)
		if len(got) != 1 || got[0].Name != anthropicProvider.Name {
			t.Fatalf("应当退回转换候选: %v", got)
		}
	})
}

func TestTranslatorFor(t *testing.T) {
	cases := []struct {
		client, upstream string
		want             bool
	}{
		{consts.StyleOpenAI, consts.StyleAnthropic, true},
		{consts.StyleAnthropic, consts.StyleOpenAI, true},
		// 同协议一个字节都不动
		{consts.StyleOpenAI, consts.StyleOpenAI, false},
		{consts.StyleAnthropic, consts.StyleAnthropic, false},
		// 不参与互转的协议
		{consts.StyleOpenAIRes, consts.StyleAnthropic, false},
		{consts.StyleOpenAI, consts.StyleOpenAIRes, false},
		{consts.StyleGemini, consts.StyleOpenAI, false},
		{consts.StyleOpenAI, consts.StyleGemini, false},
	}
	for _, tc := range cases {
		tr := TranslatorFor(tc.client, tc.upstream)
		if (tr != nil) != tc.want {
			t.Fatalf("TranslatorFor(%q, %q) 存在性 = %v，期望 %v", tc.client, tc.upstream, tr != nil, tc.want)
		}
		if !tc.want {
			continue
		}
		// 方向不能反：翻了反方向的请求发出去就是 400
		req, _, err := tr.Request(clientSample(tc.client), bridge.Options{})
		if err != nil {
			t.Fatalf("请求转换失败: %v", err)
		}
		if !strings.Contains(string(req), `"messages"`) {
			t.Fatalf("两头都有 messages，形状认不出来: %s", req)
		}
	}
}

// clientSample 给两个方向各造一份最小的、能转过去的请求体。
func clientSample(style string) []byte {
	if style == consts.StyleAnthropic {
		return []byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	}
	return []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
}

func TestBridgeNotes(t *testing.T) {
	t.Run("nil 接收者不炸", func(t *testing.T) {
		var notes *BridgeNotes
		notes.record([]bridge.Note{bridge.NoteDroppedSeed})
		if notes.String() != "" || notes.Notes() != nil {
			t.Fatal("nil 记账盒应当什么都不做")
		}
	})

	t.Run("去重并保持出现顺序", func(t *testing.T) {
		notes := NewBridgeNotes()
		notes.record([]bridge.Note{bridge.NoteDroppedSeed, bridge.NoteDroppedTopK})
		notes.record([]bridge.Note{bridge.NoteDroppedSeed, bridge.NoteDroppedUser})
		got := notes.String()
		if got != "dropped_seed,dropped_top_k,dropped_user" {
			t.Fatalf("记账串不对: %q", got)
		}
	})

	t.Run("空记账是空串", func(t *testing.T) {
		if got := NewBridgeNotes().String(); got != "" {
			t.Fatalf("没记下东西时应当给空串: %q", got)
		}
	})

	t.Run("取出来的列表改动不到盒子里", func(t *testing.T) {
		notes := NewBridgeNotes()
		notes.record([]bridge.Note{bridge.NoteDroppedSeed})
		notes.Notes()[0] = bridge.NoteDroppedUser
		if got := notes.String(); got != string(bridge.NoteDroppedSeed) {
			t.Fatalf("盒子被外面的改动影响了: %q", got)
		}
	})

	t.Run("换一家上游时从头计", func(t *testing.T) {
		// 重试：第一家要翻译、失败，第二家直连。第二家没丢任何东西，日志里就不能
		// 留着第一家的记账——那会读成"产出这次响应的上游丢了参数"
		notes := NewBridgeNotes()
		notes.record([]bridge.Note{bridge.NoteDroppedSeed})
		notes.reset()
		if got := notes.String(); got != "" {
			t.Fatalf("重置后应当是空的: %q", got)
		}
		// 重置之后还能接着记（第二家自己也有记账的话）
		notes.record([]bridge.Note{bridge.NoteDroppedTopK})
		if got := notes.String(); got != string(bridge.NoteDroppedTopK) {
			t.Fatalf("重置把盒子弄坏了: %q", got)
		}
	})

	t.Run("nil 盒子也能重置", func(t *testing.T) {
		var notes *BridgeNotes
		notes.reset()
	})
}

func TestBridgeResponseNonStream(t *testing.T) {
	tr := TranslatorFor(consts.StyleOpenAI, consts.StyleAnthropic)
	if tr == nil {
		t.Fatal("这两端之间应当有翻译器")
	}

	t.Run("翻成客户端协议并记账", func(t *testing.T) {
		notes := NewBridgeNotes()
		body, err := bridgeResponse(tr, io.NopCloser(strings.NewReader(`{
			"id":"msg_1","type":"message","role":"assistant","model":"claude",
			"content":[{"type":"text","text":"你好"}],"stop_reason":"end_turn"}`)), false, notes)
		if err != nil {
			t.Fatalf("转换失败: %v", err)
		}
		raw, err := io.ReadAll(body)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if !strings.Contains(string(raw), `"chat.completion"`) {
			t.Fatalf("没翻成 OpenAI 形状: %s", raw)
		}
		if notes.String() != "" {
			t.Fatalf("这次不该有记账: %s", notes.String())
		}
	})

	t.Run("有损的地方记下来", func(t *testing.T) {
		notes := NewBridgeNotes()
		body, err := bridgeResponse(tr, io.NopCloser(strings.NewReader(`{
			"id":"msg_1","type":"message","role":"assistant","model":"claude",
			"content":[{"type":"thinking","thinking":"心里话","signature":"s"},{"type":"text","text":"答案"}],
			"stop_reason":"end_turn"}`)), false, notes)
		if err != nil {
			t.Fatalf("转换失败: %v", err)
		}
		if _, err := io.ReadAll(body); err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if notes.String() != string(bridge.NoteDroppedThinking) {
			t.Fatalf("思考块被丢掉这件事没记下来: %q", notes.String())
		}
	})

	t.Run("上游读挂了", func(t *testing.T) {
		body, err := bridgeResponse(tr, &failingBody{}, false, NewBridgeNotes())
		if err == nil {
			t.Fatal("读挂了应当报错")
		}
		if body != nil {
			t.Fatal("出错时不该给出响应体")
		}
	})

	t.Run("响应解不出来", func(t *testing.T) {
		body, err := bridgeResponse(tr, io.NopCloser(strings.NewReader(`{半截`)), false, NewBridgeNotes())
		if err == nil {
			t.Fatal("解不开的响应应当报错——给了客户端等于发出一份坏数据")
		}
		if body != nil {
			t.Fatal("出错时不该给出响应体")
		}
	})
}

func TestBridgeResponseStream(t *testing.T) {
	tr := TranslatorFor(consts.StyleOpenAI, consts.StyleAnthropic)
	notes := NewBridgeNotes()
	// 上游是 Anthropic：思维链没有对应块，丢掉并记账。记账要读到末尾才完整——流跑到
	// 一半时的那份列表还会长
	upstream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude\",\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"想\"}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"答案\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n"

	body, err := bridgeResponse(tr, io.NopCloser(strings.NewReader(upstream)), true, notes)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	if notes.String() != "" {
		t.Fatalf("还没读完就不该有记账（列表还在长）: %q", notes.String())
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	// 翻出来的必须是 OpenAI 的事件流：客户端是 OpenAI
	if !strings.Contains(string(raw), `"chat.completion.chunk"`) || !strings.Contains(string(raw), "[DONE]") {
		t.Fatalf("没翻成 OpenAI 的事件流: %s", raw)
	}
	if strings.Contains(string(raw), "想") {
		t.Fatalf("思考内容不该进客户端: %s", raw)
	}
	if notes.String() != string(bridge.NoteDroppedThinking) {
		t.Fatalf("读完之后的记账不对: %q", notes.String())
	}
	if err := body.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
}

// failingBody 一读就报错的上游响应体。
type failingBody struct{}

func (b *failingBody) Read([]byte) (int, error) { return 0, errors.New("上游断了") }
func (b *failingBody) Close() error             { return nil }
