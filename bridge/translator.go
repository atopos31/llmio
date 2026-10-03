package bridge

// 一次转发要用的翻译器。
//
// 上层的路由决定"这次请求发给谁"，这里回答"发给它的东西要不要换个形状"。之所以要有
// 这么一层：两端协议的组合有四种，而调用方只想知道"要不要翻、怎么翻"，不该自己拿
// style 字符串去 if 判断——那种写法很容易在某一处漏掉方向，而漏掉的方向只有在真机上
// 才会暴露（请求发出去是 400，响应回来是解不开的 JSON）。

// Protocol 是翻译层认识的两种协议。
//
// 与 llmio 的 consts.Style* 是两回事：那是路由用的 style（还有 openai-res、gemini
// 这些翻译层不认识的），这是协议本身的形状。转换层不 import consts，映射由上层做。
type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"
	ProtocolAnthropic Protocol = "anthropic"
)

// Translator 把客户端的请求翻成上游协议，把上游的响应翻回客户端协议。
type Translator struct {
	from Protocol
	to   Protocol
}

// NewTranslator 返回两端之间的翻译器。两端相同、或有一端不认识时返回 false，意思是
// **照原样转发**——同协议路径一个字节都不该动。
func NewTranslator(client, upstream Protocol) (*Translator, bool) {
	switch {
	case client == ProtocolOpenAI && upstream == ProtocolAnthropic:
	case client == ProtocolAnthropic && upstream == ProtocolOpenAI:
	default:
		return nil, false
	}
	return &Translator{from: client, to: upstream}, true
}

// From / To 是这次翻译的两端，供日志记录用。
func (t *Translator) From() Protocol { return t.from }
func (t *Translator) To() Protocol   { return t.to }

// Request 把客户端请求体翻成上游协议的形状。
func (t *Translator) Request(raw []byte, opts Options) ([]byte, []Note, error) {
	if t.from == ProtocolOpenAI {
		return OpenAIRequestToAnthropic(raw, opts)
	}
	return AnthropicRequestToOpenAI(raw, opts)
}

// Response 把上游的非流式响应体翻回客户端协议。
func (t *Translator) Response(raw []byte) ([]byte, []Note, error) {
	if t.from == ProtocolOpenAI {
		return AnthropicResponseToOpenAI(raw)
	}
	return OpenAIResponseToAnthropic(raw)
}

// Stream 起一条把上游事件流翻回客户端协议的流。
func (t *Translator) Stream() Streamer {
	if t.from == ProtocolOpenAI {
		return NewAnthropicToOpenAIStream()
	}
	return NewOpenAIToAnthropicStream()
}
