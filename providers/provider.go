package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/atopos31/llmio/models"
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
		var config models.OpenAIConfig
		if err := json.Unmarshal([]byte(providerConfig), &config); err != nil {
			return nil, errors.New("invalid openai config")
		}

		return NewOpenAI(config.BaseUrl, config.ApiKey), nil
	default:
		return nil, errors.New("unknown provider")
	}
}
