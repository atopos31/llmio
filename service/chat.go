package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/atopos31/llmio/balancer"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func BalanceChat(c *gin.Context, style string, Beforer Beforer, processer Processer) error {
	proxyStart := time.Now()
	rawData, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	ctx := c.Request.Context()
	before, err := Beforer(rawData)
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

	provideritems, err := gorm.G[models.Provider](models.DB).Where("id IN ?", slices.Collect(maps.Keys(items))).Where("type = ?", style).Find(ctx)
	if err != nil {
		return err
	}

	if len(provideritems) == 0 {
		return fmt.Errorf("no %s provider found for %s", style, before.model)
	}

	FirstProvider := func(providerID uint) *models.Provider {
		for _, provider := range provideritems {
			if provider.ID == providerID {
				return &provider
			}
		}
		return nil
	}

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
			provider := FirstProvider(*item)
			if provider == nil {
				delete(items, *item)
				continue
			}

			// 获取对应提供商的原始model名
			index := slices.IndexFunc(llmproviders, func(mp models.ModelWithProvider) bool {
				return mp.ProviderID == provider.ID
			})
			ProviderModel := llmproviders[index].ProviderModel

			chatModel, err := providers.New(style, provider.Config)
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
