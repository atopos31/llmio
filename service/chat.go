package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/atopos31/llmio/balancer"
	"github.com/atopos31/llmio/model"
	"github.com/atopos31/llmio/providers"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

func BalanceChat(ctx context.Context, rawData []byte) (io.Reader, error) {
	originModel := gjson.GetBytes(rawData, "model").String()
	if originModel == "" {
		return nil, errors.New("model is empty")
	}
	llmModel, err := gorm.G[model.Model](model.DB).Where("name = ?", originModel).First(ctx)
	if err != nil {
		return nil, err
	}

	llmproviders, err := gorm.G[model.ModelWithProvider](model.DB).Where("model_id = ?", llmModel.ID).Find(ctx)
	if err != nil {
		return nil, err
	}

	stream := gjson.GetBytes(rawData, "stream").Bool()
	slog.Info("request", "origin model", originModel, "stream", stream)

	items := make(map[uint]int)
	for _, provider := range llmproviders {
		items[provider.ProviderID] = provider.Weight
	}

	restry := 3
	for range restry {
		item, err := balancer.WeightedRandom(items)
		if err != nil {
			return nil, err
		}
		provider, err := gorm.G[model.Provider](model.DB).Where("id = ?", *item).First(ctx)
		if err != nil {
			delete(items, *item)
			continue
		}
		slog.Info("selected provider", "provider", provider)
		var chatModel prividers.Privider
		switch provider.Type {
		case "openai":
			baseUrl := gjson.Get(provider.Config, "base_url")
			apiKey := gjson.Get(provider.Config, "api_key")
			id := provider.ID
			index := slices.IndexFunc(llmproviders, func(provider model.ModelWithProvider) bool {
				return provider.ProviderID == id
			})

			chatModel = prividers.NewOpenAI(baseUrl.String(), apiKey.String(), llmproviders[index].ProviderName)
		default:
			continue
		}

		body, status, err := chatModel.Chat(ctx, rawData)
		if err != nil {
			slog.Error("chat error", "error", err)
			delete(items, *item)
			continue
		}
		if status != http.StatusOK {
			slog.Error("chat error", "status", status)
			if status != http.StatusTooManyRequests {
				delete(items, *item)
			}
			body.Close()
			continue
		}

		// 与客户端并行处理响应数据流
		teeReader := processTee(ctx, body)

		return teeReader, nil
	}
	return nil, errors.New("no provider available")
}

func processTee(ctx context.Context, body io.ReadCloser) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		defer body.Close()
		defer pw.Close()

		// 等待请求结束
		<-ctx.Done()
	}()

	teeReader := io.TeeReader(body, pw)
	go func() {
		var usage Usage
		logReader := bufio.NewScanner(pr)
		for logReader.Scan() {
			usageStr := logReader.Text()
			if usageStr == "" {
				continue
			}
			usageStr = strings.TrimPrefix(usageStr, "data: ")
			if !gjson.Valid(usageStr) {
				continue
			}
			usage.PromptTokens = gjson.Get(usageStr, "usage.prompt_tokens").Int()
			usage.CompletionTokens = gjson.Get(usageStr, "usage.completion_tokens").Int()
			usage.TotalTokens = gjson.Get(usageStr, "usage.total_tokens").Int()
		}
		slog.Info("reader off", "input", usage.PromptTokens, "output", usage.CompletionTokens, "total", usage.TotalTokens)
	}()

	return teeReader
}
