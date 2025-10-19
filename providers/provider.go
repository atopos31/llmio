package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	// client 用于HTTP请求的客户端
	Chat(ctx context.Context, client *http.Client, model string, rawData []byte) (*http.Response, error)
	Models(ctx context.Context) ([]Model, error)
}

func New(Type, providerConfig string) (Provider, error) {
	switch Type {
	case "openai":
		var openai OpenAI
		if err := json.Unmarshal([]byte(providerConfig), &openai); err != nil {
			return nil, errors.New("invalid openai config")
		}

		return &openai, nil
	case "openai-res":
		var openaiRes OpenAIRes
		if err := json.Unmarshal([]byte(providerConfig), &openaiRes); err != nil {
			return nil, errors.New("invalid openai-res config")
		}

		return &openaiRes, nil
	case "anthropic":
		var anthropic Anthropic
		if err := json.Unmarshal([]byte(providerConfig), &anthropic); err != nil {
			return nil, errors.New("invalid anthropic config")
		}
		return &anthropic, nil
	default:
		return nil, errors.New("unknown provider")
	}
}
