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
		case <-time.After(time.Second * 60):
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
			ProviderModel := llmproviders[index].ProviderModel

			chatModel, err := providers.New(provider.Type, ProviderModel, provider.Config)
			if err != nil {
				return nil, err
			}

			slog.Info("using provider", "provider", provider.Name, "model", ProviderModel)

			log := &models.ChatLog{
				Name:          before.model,
				ProviderModel: ProviderModel,
				ProviderName:  provider.Name,
				Status:        "success",
			}

			reqStart := time.Now()
			body, status, err := chatModel.Chat(ctx, before.raw)
			if err != nil {
				slog.Error("chat error", "error", err)
				go SaveChatLog(ctx, log, err)
				delete(items, *item)
				continue
			}

			if status != http.StatusOK {
				byteBody, err := io.ReadAll(body)
				if err != nil {
					slog.Error("read body error", "error", err)
				}
				slog.Error("chat error", "status", status, "body", string(byteBody))
				go SaveChatLog(ctx, log, fmt.Errorf("status: %d, body: %s", status, string(byteBody)))

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

			SaveChatLog(ctx, log, nil)

			// 与客户端并行处理响应数据流 同时记录日志
			teeReader := processTee(ctx, before.stream, log.ID, reqStart, body)

			return teeReader, nil
		}
	}
}

func SaveChatLog(ctx context.Context, log *models.ChatLog, err error) {
	if err != nil {
		log.Error = err.Error()
		log.Status = "error"
	}
	if err := gorm.G[models.ChatLog](models.DB).Create(ctx, log); err != nil {
		slog.Error("Failed to create log: ", "err", err)
	}
}

type before struct {
	model  string
	stream bool
	raw    []byte
}

// 向provicer发送请求之前进行body处理
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

func processTee(ctx context.Context, stream bool, logId uint, start time.Time, body io.ReadCloser) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		defer body.Close()
		defer pw.Close()

		// 等待请求结束
		<-ctx.Done()
	}()

	teeReader := io.TeeReader(body, pw)
	go func(ctx context.Context) {
		// token用量
		var usage models.Usage
		var usagemu sync.Mutex

		// 首字时延
		var firstChunkTime time.Duration
		var once sync.Once

		var wg sync.WaitGroup

		logReader := bufio.NewScanner(pr)
		for chunk := range ScannerToken(logReader) {
			once.Do(func() {
				firstChunkTime = time.Since(start)
			})
			wg.Add(1)
			go func() {
				defer wg.Done()
				if stream {
					chunk := strings.TrimPrefix(chunk, "data: ")
					if !gjson.Valid(chunk) {
						return
					}
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
		if err := logReader.Err(); err != nil {
			slog.Error("log reader error", "error", err)
			return
		}
		chunkTime := time.Since(start) - firstChunkTime
		// 等待日志数据
		wg.Wait()

		var tps float64
		if stream {
			tps = float64(usage.TotalTokens) / chunkTime.Seconds()
		}

		log := models.ChatLog{
			Usage:          usage,
			ChunkTime:      chunkTime,
			Tps:            tps,
			FirstChunkTime: firstChunkTime,
		}

		if _, err := gorm.G[models.ChatLog](models.DB).Where("id = ?", logId).Updates(ctx, log); err != nil {
			slog.Error("update chat log error", "error", err)
		}
		slog.Info("response", "input", usage.PromptTokens, "output", usage.CompletionTokens, "total", usage.TotalTokens, "firstChunkTime", firstChunkTime, "chunkTime", chunkTime, "tps", tps)
	}(context.Background())

	return teeReader
}

func ScannerToken(reader *bufio.Scanner) iter.Seq[string] {
	return func(yield func(string) bool) {
		for reader.Scan() {
			chunk := reader.Text()
			if chunk == "" {
				continue
			}
			if !yield(chunk) {
				return
			}
		}
	}
}
