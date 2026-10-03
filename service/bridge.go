package service

import (
	"bytes"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/atopos31/llmio/bridge"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
)

// 协议互转的接线：判断这次转发要不要翻译、往哪个方向翻、翻完把"改了什么"记在哪里。
//
// 翻译本身在 bridge 包里，是纯函数；这里只做三件属于本层的事：把 llmio 的 style 映射
// 成协议、决定候选池里放谁（直连优先，转换为兜底）、把记账收集起来交给日志。

// bridgeProtocol 把 llmio 的 style 映射成翻译层认识的协议。不参与互转的
// （openai-res、gemini）返回 false。
func bridgeProtocol(style string) (bridge.Protocol, bool) {
	switch style {
	case consts.StyleOpenAI:
		return bridge.ProtocolOpenAI, true
	case consts.StyleAnthropic:
		return bridge.ProtocolAnthropic, true
	}
	return "", false
}

// TranslatorFor 返回把客户端 style 翻成上游 style 的翻译器。两端相同、或有一端不参与
// 互转时返回 nil（意思是**照原样转发**，一个字节都不动）。
func TranslatorFor(clientStyle, upstreamStyle string) *bridge.Translator {
	client, ok := bridgeProtocol(clientStyle)
	if !ok {
		return nil
	}
	upstream, ok := bridgeProtocol(upstreamStyle)
	if !ok {
		return nil
	}
	translator, ok := bridge.NewTranslator(client, upstream)
	if !ok {
		return nil
	}
	return translator
}

// ServableTypes 返回能服务该客户端协议的提供商类型：本协议的，加上可转换协议作为兜底。
//
// 模型列表端点用它——Claude Code 靠它发现模型，只列本协议的会让"只有 OpenAI 上游"的
// 模型在 Anthropic 客户端里根本看不见，用户以为模型没配。openai-res 与 gemini 不参与
// 互转，原样返回。
func ServableTypes(style string) []string {
	types := []string{style}
	// openai 客户端的模型列表一直与 openai-res 共用（见 handler.OpenAIModelsHandler）
	if style == consts.StyleOpenAI {
		types = append(types, consts.StyleOpenAIRes)
	}
	for _, other := range []string{consts.StyleOpenAI, consts.StyleAnthropic} {
		if other == style {
			continue
		}
		if TranslatorFor(style, other) != nil {
			types = append(types, other)
		}
	}
	return slices.Compact(types)
}

// preferDirect 在候选里挑出本协议的提供商；一个都没有时才退回全部（即借用可转换协议
// 的那些）。转换只在"这个模型下没人说客户端的话"时发生，行为可预期。
func preferDirect(providers []models.Provider, style string) []models.Provider {
	direct := make([]models.Provider, 0, len(providers))
	for _, p := range providers {
		if p.Type == style {
			direct = append(direct, p)
		}
	}
	if len(direct) > 0 {
		return direct
	}
	return providers
}

// poolFor 决定这个模型的候选池：优先同协议开着就只挑本协议的（转换为兜底），
// 关掉就整池按权重摇——本协议与可转换协议的上游一起参与。
//
// 关掉是给"我就要按权重分配"的场景留的口子：比如同协议的几家都不太稳，宁可让流量
// 按权重分到另一协议的上游上，而不是被优先级挡在外面。
func poolFor(providers []models.Provider, style string, preferSameProtocol *bool) []models.Provider {
	// nil = 没配过（转换功能上线前建的模型），按开处理
	if preferSameProtocol == nil || *preferSameProtocol {
		return preferDirect(providers, style)
	}
	return providers
}

// BridgeNotes 收集一次协议互转的记账（"改了什么"）。
//
// 记账有两处来源，时间上一前一后：请求侧当场就有，响应侧要等流读完才是终态（中途的
// 列表还会长）。所以用一个带锁的小盒子：转发路径往里写，日志落库时读出来。
type BridgeNotes struct {
	mu    sync.Mutex
	notes []bridge.Note
}

// NewBridgeNotes 建一个记账盒。
func NewBridgeNotes() *BridgeNotes { return &BridgeNotes{} }

// record 收下一批记账，去重并保持出现顺序。nil 接收者不做事——同协议路径上调用方
// 常常直接传 nil。
func (b *BridgeNotes) record(notes []bridge.Note) {
	if b == nil || len(notes) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, n := range notes {
		if !slices.Contains(b.notes, n) {
			b.notes = append(b.notes, n)
		}
	}
}

// reset 丢弃目前记下的条目，供下一次转发尝试从头开始。
//
// 一次请求可能试好几家上游（重试），而记账描述的是**产出这次响应的那一家**：上一家
// 若留着它的记账，就会被挂到最终这条日志上，看着像这次响应丢了参数。
func (b *BridgeNotes) reset() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notes = nil
}

// Notes 返回目前记下的条目。
func (b *BridgeNotes) Notes() []bridge.Note {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.notes)
}

// String 把记账拼成逗号分隔的串（短码里没有逗号），供落库与日志使用。没记下东西时
// 返回空串。
func (b *BridgeNotes) String() string {
	notes := b.Notes()
	if len(notes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(notes))
	for _, n := range notes {
		parts = append(parts, string(n))
	}
	return strings.Join(parts, ",")
}

// bridgedBody 把翻译后的响应体接到记账盒上。
//
// 流式的记账要读到末尾才完整（中途还在长），所以在这里等 EOF 或 Close，落库时才是
// 终态的那一份。
type bridgedBody struct {
	*bridge.BridgedBody
	notes *BridgeNotes
}

func (b *bridgedBody) Read(p []byte) (int, error) {
	n, err := b.BridgedBody.Read(p)
	if err != nil {
		b.notes.record(b.BridgedBody.Notes())
	}
	return n, err
}

func (b *bridgedBody) Close() error {
	b.notes.record(b.BridgedBody.Notes())
	return b.BridgedBody.Close()
}

// bridgeResponse 把上游响应**就地**翻成客户端协议：换掉 res.Body，顺带作废
// Content-Length。
//
// 作废那个头不是洁癖：它是随响应一起从上游复制过来的，而翻译后的字节数几乎不可能
// 与上游那份相同（字段名、外层包裹、块结构全变了）。net/http 在写响应时会拿它做校验，
// 写出超过声明长度就直接掐断连接（`http: wrote more than the declared Content-Length`），
// 客户端拿到的是半截响应——比长度不准严重得多。删掉之后按分块传输走，长度由传输层算。
//
// 流式边走边翻；非流式在这里整体读完再翻——非流式的响应体反正要被完整读一遍
// （ErrorMatcher 与记录都在读），留在 Body 里等调用方读只是把同一件事推后。
func bridgeResponse(translator *bridge.Translator, res *http.Response, stream bool, notes *BridgeNotes) error {
	if stream {
		res.Body = &bridgedBody{BridgedBody: bridge.NewBridgedBody(res.Body, translator.Stream()), notes: notes}
	} else {
		raw, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			return err
		}
		converted, responseNotes, err := translator.Response(raw)
		if err != nil {
			return err
		}
		notes.record(responseNotes)
		res.Body = io.NopCloser(bytes.NewReader(converted))
	}
	res.Header.Del("Content-Length")
	return nil
}
