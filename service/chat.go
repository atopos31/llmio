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
	"github.com/samber/lo"
	"gorm.io/gorm"
)

func BalanceChat(ctx context.Context, start time.Time, style string, before before, providersWithMeta ProvidersWithMeta, reqMeta models.ReqMeta) (*http.Response, uint, error) {
	// 所有模型提供商关联
	modelWithProviders := providersWithMeta.Providers
	modelWithProviderMap := lo.KeyBy(modelWithProviders, func(mp models.ModelWithProvider) uint { return mp.ID })

	slog.Info("request", "model", before.Model, "stream", before.Stream, "tool_call", before.toolCall, "structured_output", before.structuredOutput, "image", before.image)

	provideritems, err := gorm.G[models.Provider](models.DB).
		Where("id IN ?", lo.Map(modelWithProviders, func(mp models.ModelWithProvider, _ int) uint { return mp.ProviderID })).
		Where("type = ?", style).
		Find(ctx)
	if err != nil {
		return nil, 0, err
	}
	if len(provideritems) == 0 {
		return nil, 0, fmt.Errorf("no %s provider found for %s", style, before.Model)
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
		return nil, 0, errors.New("no provider with tool_call or structured_output or image found for models " + before.Model)
	}
	// 收集重试过程中的err日志
	retryErrLog := make(chan models.ChatLog, providersWithMeta.MaxRetry)
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

	timer := time.NewTimer(time.Second * time.Duration(providersWithMeta.TimeOut))
	defer timer.Stop()
	for retry := range providersWithMeta.MaxRetry {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-timer.C:
			return nil, 0, errors.New("retry time out")
		default:
			// 加权负载均衡
			id, err := balancer.WeightedRandom(weightItems)
			if err != nil {
				return nil, 0, err
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
				return nil, 0, err
			}

			slog.Info("using provider", "provider", provider.Name, "model", modelWithProvider.ProviderModel)

			log := models.ChatLog{
				Name:          before.Model,
				ProviderModel: modelWithProvider.ProviderModel,
				ProviderName:  provider.Name,
				Status:        "success",
				Style:         style,
				UserAgent:     reqMeta.UserAgent,
				RemoteIP:      reqMeta.RemoteIP,
				ChatIO:        providersWithMeta.IOLog,
				Retry:         retry,
				ProxyTime:     time.Since(start),
			}
			reqStart := time.Now()
			client := providers.GetClient(time.Second * time.Duration(providersWithMeta.TimeOut) / 3)
			header := buildHeaders(reqMeta.Header, modelWithProvider.WithHeader, modelWithProvider.CustomerHeaders)
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
			logId, err := SaveChatLog(ctx, log)
			if err != nil {
				return nil, 0, err
			}
			return res, logId, nil
		}
	}

	return nil, 0, errors.New("maximum retry attempts reached")
}

func RecordLog(ctx context.Context, reader io.ReadCloser, processer Processer, logId uint, stream bool, ioLog bool, reqStart time.Time) error {
	defer reader.Close()
	log, output, err := processer(ctx, reader, stream, reqStart)
	if err != nil {
		return err
	}
	if _, err := gorm.G[models.ChatLog](models.DB).Where("id = ?", logId).Updates(ctx, *log); err != nil {
		return err
	}
	slog.Info("response", "input", log.PromptTokens, "output", log.CompletionTokens, "total", log.TotalTokens, "firstChunkTime", log.FirstChunkTime, "chunkTime", log.ChunkTime, "tps", log.Tps)

	// 只有开启IO记录才更新输出数据
	if ioLog {
		if _, err := gorm.G[models.ChatIO](models.DB).Where("log_id = ?", logId).Updates(ctx, models.ChatIO{OutputUnion: *output}); err != nil {
			return err
		}
	}
	return nil
}

func SaveChatIO(ctx context.Context, log models.ChatIO) error {
	return gorm.G[models.ChatIO](models.DB).Create(ctx, &log)
}

func SaveChatLog(ctx context.Context, log models.ChatLog) (uint, error) {
	if err := gorm.G[models.ChatLog](models.DB).Create(ctx, &log); err != nil {
		return 0, err
	}
	return log.ID, nil
}

func buildHeaders(source http.Header, withHeader *bool, customHeaders map[string]string) http.Header {
	header := http.Header{}

	if withHeader != nil && *withHeader {
		header = source.Clone()
	}

	for key, value := range customHeaders {
		header.Set(key, value)
	}

	return header
}

type ProvidersWithMeta struct {
	Providers []models.ModelWithProvider
	MaxRetry  int
	TimeOut   int
	IOLog     bool
}

func ProvidersBymodelsName(ctx context.Context, modelName string) (*ProvidersWithMeta, error) {
	mmodel, err := gorm.G[models.Model](models.DB).Where("name = ?", modelName).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if _, err := SaveChatLog(ctx, models.ChatLog{
				Name:   modelName,
				Status: "error",
				Style:  consts.StyleOpenAI,
				Error:  err.Error(),
			}); err != nil {
				return nil, err
			}
			return nil, errors.New("not found model " + modelName)
		}
		return nil, err
	}

	modelWithProviders, err := gorm.G[models.ModelWithProvider](models.DB).
		Where("model_id = ?", mmodel.ID).
		Where("status = ?", true).
		Find(ctx)
	if err != nil {
		return nil, err
	}

	if len(modelWithProviders) == 0 {
		return nil, errors.New("not provider for model " + modelName)
	}
	if mmodel.IOLog == nil {
		mmodel.IOLog = new(bool)
	}
	return &ProvidersWithMeta{
		Providers: modelWithProviders,
		MaxRetry:  mmodel.MaxRetry,
		TimeOut:   mmodel.TimeOut,
		IOLog:     *mmodel.IOLog,
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
