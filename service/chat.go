package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/atopos31/llmio/balancer"
	"github.com/atopos31/llmio/model"
	"github.com/atopos31/llmio/providers"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
)

type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

func BalanceChat(ctx context.Context, rawData []byte) (io.Reader, error) {
	before, err := processBefore(rawData)
	if err != nil {
		return nil, err
	}

	slog.Info("request", "model", before.model, "stream", before.stream)

	llmproviders, err := ProvidersByModelName(ctx, before.model)
	if err != nil {
		return nil, err
	}

	if len(llmproviders) == 0 {
		return nil, fmt.Errorf("no provider found for model %s", before.model)
	}

	items := make(map[uint]int)
	for _, provider := range llmproviders {
		items[provider.ProviderID] = provider.Weight
	}

	restry := 10
	for range restry {
		// 加权负载均衡
		item, err := balancer.WeightedRandom(items)
		if err != nil {
			return nil, err
		}
		provider, err := gorm.G[model.Provider](model.DB).Where("id = ?", *item).First(ctx)
		if err != nil {
			delete(items, *item)
			continue
		}

		index := slices.IndexFunc(llmproviders, func(mp model.ModelWithProvider) bool {
			return mp.ProviderID == provider.ID
		})

		var chatModel prividers.Privider
		switch provider.Type {
		case "openai":
			var config model.OpenAIConfig
			if err := json.Unmarshal([]byte(provider.Config), &config); err != nil {
				delete(items, *item)
				continue
			}

			providerName := llmproviders[index].ProviderName

			slog.Info("using provider", "provider", provider.Name, "model", providerName)

			chatModel = prividers.NewOpenAI(config.BaseUrl, config.ApiKey, providerName)
		default:
			continue
		}

		reqStart := time.Now()
		body, status, err := chatModel.Chat(ctx, before.raw)
		if err != nil {
			slog.Error("chat error", "error", err)
			delete(items, *item)
			continue
		}

		if status != http.StatusOK {
			byteBody, err := io.ReadAll(body)
			if err != nil {
				slog.Error("read body error", "error", err)
			}
			slog.Error("chat error", "status", status, "body", string(byteBody))

			// 非RPM限制 移除待选
			if status != http.StatusTooManyRequests {
				delete(items, *item)
			}

			// 达到RPM限制
			if status == http.StatusTooManyRequests {
				time.Sleep(time.Second * 2)
			}
			body.Close()
			continue
		}

		// 与客户端并行处理响应数据流
		teeReader := processTee(ctx, reqStart, body)

		return teeReader, nil
	}
	return nil, errors.New("no provider available")
}

type before struct {
	model  string
	stream bool
	raw    []byte
}

func processBefore(data []byte) (*before, error) {
	model := gjson.GetBytes(data, "model").String()
	if model == "" {
		return nil, errors.New("model is empty")
	}
	stream := gjson.GetBytes(data, "stream").Bool()
	if stream {
		// 为processTee记录usage添加选项
		newData, err := sjson.SetBytes(data, "stream_options", struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true})
		if err != nil {
			return nil, err
		}
		data = newData
	}
	return &before{
		model:  model,
		stream: stream,
		raw:    data,
	}, nil
}

func ProvidersByModelName(ctx context.Context, modelName string) ([]model.ModelWithProvider, error) {
	llmModel, err := gorm.G[model.Model](model.DB).Where("name = ?", modelName).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("not found model " + modelName)
		}
		return nil, err
	}

	llmproviders, err := gorm.G[model.ModelWithProvider](model.DB).Where("model_id = ?", llmModel.ID).Find(ctx)
	if err != nil {
		return nil, err
	}

	if len(llmproviders) == 0 {
		return nil, errors.New("not provider for model " + modelName)
	}
	return llmproviders, nil
}

func processTee(ctx context.Context, start time.Time, body io.ReadCloser) io.Reader {
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
		var once sync.Once
		var firstChunkTime time.Duration
		logReader := bufio.NewScanner(pr)
		for logReader.Scan() {
			once.Do(func() {
				firstChunkTime = time.Since(start)
				slog.Info("response", "first_chunk_time", firstChunkTime)
			})

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

		chunkTime := time.Since(start) - firstChunkTime
		tps := float64(usage.TotalTokens) / chunkTime.Seconds()
		slog.Info("response", "input", usage.PromptTokens, "output", usage.CompletionTokens, "total", usage.TotalTokens, "chunkTime", chunkTime, "tps", tps)
	}()

	return teeReader
}
