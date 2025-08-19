package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

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
	// responseHeaderTimeout 首字节/响应头到达时间限制
	Chat(ctx context.Context, responseHeaderTimeout time.Duration, rawData []byte) (body io.ReadCloser, status int, err error)
	Models(ctx context.Context) ([]Model, error)
}

func New(Type, model, providerConfig string) (Provider, error) {
	switch Type {
	case "openai":
		var config models.OpenAIConfig
		if err := json.Unmarshal([]byte(providerConfig), &config); err != nil {
			return nil, errors.New("invalid openai config")
		}

		return NewOpenAI(config.BaseUrl, config.ApiKey, model), nil
	default:
		return nil, errors.New("unknown provider")
	}
}
