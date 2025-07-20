package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/atopos31/llmio/balancer"
	"github.com/atopos31/llmio/models"
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

	llmproviders, err := ProvidersBymodelsName(ctx, before.model)
	if err != nil {
		return nil, err
	}

	if len(llmproviders) == 0 {
		return nil, fmt.Errorf("no provider found for models %s", before.model)
	}

	items := make(map[uint]int)
	for _, provider := range llmproviders {
		items[provider.ProviderID] = provider.Weight
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second * 20):
			return nil, errors.New("retry time out !")
		default:
			// 加权负载均衡
			item, err := balancer.WeightedRandom(items)
			if err != nil {
				return nil, err
			}
			provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", *item).First(ctx)
			if err != nil {
				delete(items, *item)
				continue
			}

			// 获取对应提供商的原始model名
			index := slices.IndexFunc(llmproviders, func(mp models.ModelWithProvider) bool {
				return mp.ProviderID == provider.ID
			})
			providerName := llmproviders[index].ProviderName

			chatModel, err := providers.New(provider.Type, llmproviders[index].ProviderName, provider.Config)
			if err != nil {
				return nil, err
			}

			slog.Info("using provider", "provider", provider.Name, "model", providerName)

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

				// 达到RPM限制 降低权重
				if status == http.StatusTooManyRequests {
					items[*item] -= 10
				}
				body.Close()
				continue
			}

			// 与客户端并行处理响应数据流
			teeReader := processTee(ctx, reqStart, body)

			return teeReader, nil
		}
	}
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
		// 为processTee记录usage添加选项 PS:很多客户端只会开启stream 而不会开启include_usage
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

func ProvidersBymodelsName(ctx context.Context, modelsName string) ([]models.ModelWithProvider, error) {
	llmmodels, err := gorm.G[models.Model](models.DB).Where("name = ?", modelsName).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("not found model " + modelsName)
		}
		return nil, err
	}

	llmproviders, err := gorm.G[models.ModelWithProvider](models.DB).Where("model_id = ?", llmmodels.ID).Find(ctx)
	if err != nil {
		return nil, err
	}

	if len(llmproviders) == 0 {
		return nil, errors.New("not provider for model " + modelsName)
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
		var usagemu sync.Mutex

		// 首字时延
		var firstChunkTime time.Duration
		var once sync.Once

		logReader := bufio.NewScanner(pr)
		for chunk, err := range ScannerToken(logReader) {
			if err != nil {
				slog.Error("log reader error", "error", err)
				break
			}
			once.Do(func() {
				firstChunkTime = time.Since(start)
			})
			go func() {
				chunk := strings.TrimPrefix(chunk, "data: ")
				if !gjson.Valid(chunk) {
					return
				}

				usageStr := gjson.Get(chunk, "usage")
				if !usageStr.Exists() {
					return
				}

				usagemu.Lock()
				if err := json.Unmarshal([]byte(usageStr.Raw), &usage); err != nil {
					slog.Error("unmarshal usage error, raw:" + usageStr.Raw)
				}
				usagemu.Unlock()
			}()
		}

		chunkTime := time.Since(start) - firstChunkTime
		tps := float64(usage.TotalTokens) / chunkTime.Seconds()
		slog.Info("response", "input", usage.PromptTokens, "output", usage.CompletionTokens, "total", usage.TotalTokens, "firstChunkTime", firstChunkTime, "chunkTime", chunkTime, "tps", tps)
	}()

	return teeReader
}

func ScannerToken(reader *bufio.Scanner) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for reader.Scan() {
			chunk := reader.Text()
			if chunk == "" {
				continue
			}
			if !yield(chunk, nil) {
				return
			}
		}
		yield("", reader.Err())
	}
}
