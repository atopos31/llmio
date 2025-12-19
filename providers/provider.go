package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/atopos31/llmio/consts"
)

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"` // 使用 int64 存储 Unix 时间戳
	OwnedBy string `json:"owned_by"`
}

type Provider interface {
	BuildReq(ctx context.Context, header http.Header, model string, rawData []byte) (*http.Request, error)
	Models(ctx context.Context) ([]Model, error)
}

func New(Type, providerConfig string) (Provider, error) {
	switch Type {
	case consts.StyleOpenAI:
		var openai OpenAI
		if err := json.Unmarshal([]byte(providerConfig), &openai); err != nil {
			return nil, errors.New("invalid openai config")
		}

		return &openai, nil
	case consts.StyleOpenAIRes:
		var openaiRes OpenAIRes
		if err := json.Unmarshal([]byte(providerConfig), &openaiRes); err != nil {
			return nil, errors.New("invalid openai-res config")
		}

		return &openaiRes, nil
	case consts.StyleAnthropic:
		var anthropic Anthropic
		if err := json.Unmarshal([]byte(providerConfig), &anthropic); err != nil {
			return nil, errors.New("invalid anthropic config")
		}
		return &anthropic, nil
	case consts.StyleGemini:
		var gemini Gemini
		if err := json.Unmarshal([]byte(providerConfig), &gemini); err != nil {
			return nil, errors.New("invalid gemini config")
		}
		return &gemini, nil
	default:
		return nil, errors.New("unknown provider")
	}
}
