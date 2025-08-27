package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/atopos31/llmio/balancer"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
)

func BalanceChat(c *gin.Context, processer TeeProcesser) error {
	proxyStart := time.Now()
	rawData, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	ctx := c.Request.Context()
	before, err := processBefore(rawData)
	if err != nil {
		return err
	}

	llmProvidersWithLimit, err := ProvidersBymodelsName(ctx, before.model)
	if err != nil {
		return err
	}
	llmproviders := llmProvidersWithLimit.Providers

	slog.Info("request", "model", before.model, "stream", before.stream, "tool_call", before.toolCall, "structured_output", before.structuredOutput)

	if len(llmproviders) == 0 {
		return fmt.Errorf("no provider found for models %s", before.model)
	}

	items := make(map[uint]int)
	for _, provider := range llmproviders {
		// 过滤是否开启工具调用
		if before.toolCall && !*provider.ToolCall {
			continue
		}
		// 过滤是否开启结构化输出
		if before.structuredOutput && !*provider.StructuredOutput {
			continue
		}
		items[provider.ProviderID] = provider.Weight
	}

	if len(items) == 0 {
		return errors.New("no provider with tool_call or structured_output found for models " + before.model)
	}
	// 收集重试过程中的err日志
	retryErrLog := make(chan models.ChatLog, llmProvidersWithLimit.MaxRetry)
	defer close(retryErrLog)
	go func() {
		for log := range retryErrLog {
			_, err := SaveChatLog(context.Background(), log)
			if err != nil {
				slog.Error("save chat log error", "error", err)
			}
		}
	}()

	for retry := 0; retry < llmProvidersWithLimit.MaxRetry; retry++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second * time.Duration(llmProvidersWithLimit.TimeOut)):
			return errors.New("retry time out !")
		default:
			// 加权负载均衡
			item, err := balancer.WeightedRandom(items)
			if err != nil {
				return err
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

			chatModel, err := providers.New(provider.Type, provider.Config)
			if err != nil {
				return err
			}

			slog.Info("using provider", "provider", provider.Name, "model", ProviderModel)

			log := models.ChatLog{
				Name:          before.model,
				ProviderModel: ProviderModel,
				ProviderName:  provider.Name,
				Status:        "success",
				Retry:         retry,
				ProxyTime:     time.Since(proxyStart),
			}
			reqStart := time.Now()
			client := providers.GetClient(time.Second * time.Duration(llmProvidersWithLimit.TimeOut) / 3)
			res, err := chatModel.Chat(ctx, client, ProviderModel, before.raw)
			if err != nil {
				retryErrLog <- log.WithError(err)
				// 请求失败 移除待选
				delete(items, *item)
				continue
			}

			if res.StatusCode != http.StatusOK {
				byteBody, err := io.ReadAll(res.Body)
				if err != nil {
					slog.Error("read body error", "error", err)
				}
				retryErrLog <- log.WithError(fmt.Errorf("status: %d, body: %s", res.StatusCode, string(byteBody)))

				if res.StatusCode == http.StatusTooManyRequests {
					// 达到RPM限制 降低权重
					items[*item] -= items[*item] / 3
				} else {
					// 非RPM限制 移除待选
					delete(items, *item)
				}
				res.Body.Close()
				continue
			}
			defer res.Body.Close()

			logId, err := SaveChatLog(ctx, log)
			if err != nil {
				return err
			}

			pr, pw := io.Pipe()
			tee := io.TeeReader(res.Body, pw)

			// 与客户端并行处理响应数据流 同时记录日志
			go func(ctx context.Context) {
				defer pr.Close()
				processer(ctx, pr, before.stream, logId, reqStart)
			}(context.Background())
			// 转发给客户端
			if _, err := io.Copy(c.Writer, tee); err != nil {
				pw.CloseWithError(err)
				return err
			}

			pw.Close()

			return nil
		}
	}

	return errors.New("maximum retry attempts reached !")
}

func SaveChatLog(ctx context.Context, log models.ChatLog) (uint, error) {
	if err := gorm.G[models.ChatLog](models.DB).Create(ctx, &log); err != nil {
		return 0, err
	}
	return log.ID, nil
}

type before struct {
	model            string
	stream           bool
	toolCall         bool
	structuredOutput bool
	raw              []byte
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
	var toolCall bool
	tools := gjson.GetBytes(data, "tools")
	if tools.Exists() && len(tools.Array()) != 0 {
		toolCall = true
	}
	var structuredOutput bool
	if gjson.GetBytes(data, "response_format").Exists() {
		structuredOutput = true
	}
	return &before{
		model:            model,
		stream:           stream,
		toolCall:         toolCall,
		structuredOutput: structuredOutput,
		raw:              data,
	}, nil
}

type ProvidersWithlimit struct {
	Providers []models.ModelWithProvider
	MaxRetry  int
	TimeOut   int
}

func ProvidersBymodelsName(ctx context.Context, modelsName string) (*ProvidersWithlimit, error) {
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
	return &ProvidersWithlimit{
		Providers: llmproviders,
		MaxRetry:  llmmodels.MaxRetry,
		TimeOut:   llmmodels.TimeOut,
	}, nil
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
