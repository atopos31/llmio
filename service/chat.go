package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"time"

	"github.com/atopos31/llmio/balancer"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

func BalanceChat(c *gin.Context, style string) error {
	proxyStart := time.Now()
	rawData, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	ctx := c.Request.Context()
	beforer, processer, err := GetBeforerAndProcesserByStyle(style)
	if err != nil {
		return err
	}
	before, err := beforer(rawData)
	if err != nil {
		return err
	}

	modelWithProvidersWithLimit, err := ProvidersBymodelsName(ctx, before.model)
	if err != nil {
		return err
	}
	// 所有模型提供商关联
	modelWithProviders := modelWithProvidersWithLimit.Providers
	if len(modelWithProviders) == 0 {
		return fmt.Errorf("no provider found for models %s", before.model)
	}
	modelWithProviderMap := lo.KeyBy(modelWithProviders, func(mp models.ModelWithProvider) uint { return mp.ID })

	slog.Info("request", "model", before.model, "stream", before.stream, "tool_call", before.toolCall, "structured_output", before.structuredOutput, "image", before.image)

	provideritems, err := gorm.G[models.Provider](models.DB).
		Where("id IN ?", lo.Map(modelWithProviders, func(mp models.ModelWithProvider, _ int) uint { return mp.ProviderID })).
		Where("type = ?", style).
		Find(ctx)
	if err != nil {
		return err
	}
	if len(provideritems) == 0 {
		return fmt.Errorf("no %s provider found for %s", style, before.model)
	}

	// 构建providerID到provider的映射，避免重复查找
	providerMap := lo.KeyBy(provideritems, func(p models.Provider) uint { return p.ID })
	// modelWithProvider id 与 weight映射
	weightItems := make(map[uint]int)
	for i := range modelWithProviders {
		modelWithProvider := modelWithProviders[i]
		// 过滤是否开启工具调用
		if modelWithProvider.ToolCall != nil && before.toolCall && !*modelWithProvider.ToolCall {
			continue
		}
		// 过滤是否开启结构化输出
		if modelWithProvider.StructuredOutput != nil && before.structuredOutput && !*modelWithProvider.StructuredOutput {
			continue
		}
		// 过滤是否拥有视觉能力
		if modelWithProvider.Image != nil && before.image && !*modelWithProvider.Image {
			continue
		}
		provider := providerMap[modelWithProvider.ProviderID]
		// 过滤提供商类型
		if provider.Type != style {
			continue
		}
		weightItems[modelWithProvider.ID] = modelWithProvider.Weight
	}

	if len(weightItems) == 0 {
		return errors.New("no provider with tool_call or structured_output or image found for models " + before.model)
	}
	// 收集重试过程中的err日志
	retryErrLog := make(chan models.ChatLog, modelWithProvidersWithLimit.MaxRetry)
	defer close(retryErrLog)
	go func() {
		ctx := context.Background()
		for log := range retryErrLog {
			_, err := SaveChatLog(ctx, log)
			if err != nil {
				slog.Error("save chat log error", "error", err)
			}
		}
	}()

	timer := time.NewTimer(time.Second * time.Duration(modelWithProvidersWithLimit.TimeOut))
	defer timer.Stop()
	for retry := range modelWithProvidersWithLimit.MaxRetry {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("retry time out")
		default:
			// 加权负载均衡
			id, err := balancer.WeightedRandom(weightItems)
			if err != nil {
				return err
			}
			modelWithProvider, ok := modelWithProviderMap[*id]
			if !ok {
				// 数据不一致，移除该模型避免下次重复命中
				delete(weightItems, *id)
				continue
			}

			provider := providerMap[modelWithProvider.ProviderID]

			chatModel, err := providers.New(style, provider.Config)
			if err != nil {
				return err
			}

			slog.Info("using provider", "provider", provider.Name, "model", modelWithProvider.ProviderModel)

			log := models.ChatLog{
				Name:          before.model,
				ProviderModel: modelWithProvider.ProviderModel,
				ProviderName:  provider.Name,
				Status:        "success",
				Style:         style,
				UserAgent:     c.Request.UserAgent(),
				RemoteIP:      c.ClientIP(),
				ChatIO:        modelWithProvidersWithLimit.IOLog,
				Retry:         retry,
				ProxyTime:     time.Since(proxyStart),
			}
			reqStart := time.Now()
			client := providers.GetClient(time.Second * time.Duration(modelWithProvidersWithLimit.TimeOut) / 3)
			header := make(http.Header)
			// 请求头透传
			if modelWithProvider.WithHeader != nil && *modelWithProvider.WithHeader {
				header = c.Request.Header
			}
			trace := &httptrace.ClientTrace{
				GotFirstResponseByte: func() {
					fmt.Printf("响应时间: %v", time.Since(reqStart))
				},
			}
			res, err := chatModel.Chat(httptrace.WithClientTrace(ctx, trace), header, client, modelWithProvider.ProviderModel, before.raw)
			if err != nil {
				retryErrLog <- log.WithError(err)
				// 请求失败 移除待选
				delete(weightItems, *id)
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
					weightItems[*id] -= weightItems[*id] / 3
				} else {
					// 非RPM限制 移除待选
					delete(weightItems, *id)
				}
				res.Body.Close()
				continue
			}
			defer res.Body.Close()

			logId, err := SaveChatLog(ctx, log)
			if err != nil {
				return err
			}

			// 记录输入输出数据（如果开启IO记录）
			if modelWithProvidersWithLimit.IOLog {
				if err := gorm.G[models.ChatIO](models.DB).Create(ctx, &models.ChatIO{
					LogId: logId,
					Input: string(before.raw),
				}); err != nil {
					return err
				}
			}

			pr, pw := io.Pipe()
			tee := io.TeeReader(res.Body, pw)

			// 与客户端并行处理响应数据流 同时记录日志
			go func() {
				defer pr.Close()
				ctx := context.Background()
				log, output, err := processer(ctx, pr, before.stream, reqStart)
				if err != nil {
					slog.Error("processer error", "error", err)
					return
				}
				if _, err := gorm.G[models.ChatLog](models.DB).Where("id = ?", logId).Updates(ctx, *log); err != nil {
					slog.Error("update chat log error", "error", err)
					return
				}
				slog.Info("response", "input", log.PromptTokens, "output", log.CompletionTokens, "total", log.TotalTokens, "firstChunkTime", log.FirstChunkTime, "chunkTime", log.ChunkTime, "tps", log.Tps)

				// 只有开启IO记录才更新输出数据
				if modelWithProvidersWithLimit.IOLog {
					if _, err := gorm.G[models.ChatIO](models.DB).Where("log_id = ?", logId).Updates(ctx, models.ChatIO{OutputUnion: *output}); err != nil {
						slog.Error("update chat io error", "error", err)
						return
					}
				}
			}()
			// 转发给客户端
			if before.stream {
				c.Header("Content-Type", "text/event-stream")
				c.Header("Cache-Control", "no-cache")
			} else {
				c.Header("Content-Type", "application/json")
			}
			c.Writer.Flush()
			if _, err := io.Copy(c.Writer, tee); err != nil {
				pw.CloseWithError(err)
				return err
			}

			pw.Close()

			return nil
		}
	}

	return errors.New("maximum retry attempts reached")
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
	IOLog     bool
}

func ProvidersBymodelsName(ctx context.Context, modelsName string) (*ProvidersWithlimit, error) {
	llmmodels, err := gorm.G[models.Model](models.DB).Where("name = ?", modelsName).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("not found model " + modelsName)
		}
		return nil, err
	}

	modelWithProviders, err := gorm.G[models.ModelWithProvider](models.DB).
		Where("model_id = ?", llmmodels.ID).
		Where("status = ?", true).
		Find(ctx)
	if err != nil {
		return nil, err
	}

	if len(modelWithProviders) == 0 {
		return nil, errors.New("not provider for model " + modelsName)
	}
	if llmmodels.IOLog == nil {
		llmmodels.IOLog = new(bool)
	}
	return &ProvidersWithlimit{
		Providers: modelWithProviders,
		MaxRetry:  llmmodels.MaxRetry,
		TimeOut:   llmmodels.TimeOut,
		IOLog:     *llmmodels.IOLog,
	}, nil
}

func GetBeforerAndProcesserByStyle(style string) (Beforer, Processer, error) {
	switch style {
	case consts.StyleOpenAI:
		return BeforerOpenAI, ProcesserOpenAI, nil
	case consts.StyleOpenAIRes:
		return BeforerOpenAIRes, ProcesserOpenAiRes, nil
	case consts.StyleAnthropic:
		return BeforerAnthropic, ProcesserAnthropic, nil
	default:
		return nil, nil, errors.New("unsupported style: " + style)
	}
}
