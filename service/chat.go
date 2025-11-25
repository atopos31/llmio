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

func BalanceChat(ctx context.Context, start time.Time, style string, before Before, providersWithMeta ProvidersWithMeta, reqMeta models.ReqMeta) (*http.Response, uint, error) {
	slog.Info("request", "model", before.Model, "stream", before.Stream, "tool_call", before.toolCall, "structured_output", before.structuredOutput, "image", before.image)

	providerMap := providersWithMeta.ProviderMap
	weightItems := providersWithMeta.WeightItems

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

	client := providers.GetClient(time.Second * time.Duration(providersWithMeta.TimeOut) / 3)

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

			modelWithProvider, ok := providersWithMeta.ModelWithProviderMap[*id]
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
			// 根据请求原始请求头 是否透传请求头 自定义请求头 构建新的请求头
			header := buildHeaders(reqMeta.Header, modelWithProvider.WithHeader, modelWithProvider.CustomerHeaders)

			reqStart := time.Now()
			trace := &httptrace.ClientTrace{
				GotFirstResponseByte: func() {
					fmt.Printf("响应时间: %v", time.Since(reqStart))
				},
			}

			req, err := chatModel.BuildReq(httptrace.WithClientTrace(ctx, trace), header, modelWithProvider.ProviderModel, before.raw)
			if err != nil {
				retryErrLog <- log.WithError(err)
				// 构建请求失败 移除待选
				delete(weightItems, *id)
				continue
			}

			res, err := client.Do(req)
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
				res.Body.Close()
				return nil, 0, err
			}

			return res, logId, nil
		}
	}

	return nil, 0, errors.New("maximum retry attempts reached")
}

func RecordLog(ctx context.Context, reqStart time.Time, reader io.ReadCloser, processer Processer, logId uint, before Before, ioLog bool) {
	recordFunc := func() error {
		defer reader.Close()
		if ioLog {
			if err := gorm.G[models.ChatIO](models.DB).Create(ctx, &models.ChatIO{
				Input: string(before.raw),
				LogId: logId,
			}); err != nil {
				return err
			}
		}
		log, output, err := processer(ctx, reader, before.Stream, reqStart)
		if err != nil {
			return err
		}
		if _, err := gorm.G[models.ChatLog](models.DB).Where("id = ?", logId).Updates(ctx, *log); err != nil {
			return err
		}
		if ioLog {
			if _, err := gorm.G[models.ChatIO](models.DB).Where("log_id = ?", logId).Updates(ctx, models.ChatIO{OutputUnion: *output}); err != nil {
				return err
			}
		}
		return nil
	}
	if err := recordFunc(); err != nil {
		slog.Error("record log error", "error", err)
	}
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
	ModelWithProviderMap map[uint]models.ModelWithProvider
	WeightItems          map[uint]int
	ProviderMap          map[uint]models.Provider
	MaxRetry             int
	TimeOut              int
	IOLog                bool
}

func ProvidersWithMetaBymodelsName(ctx context.Context, modelName string, style string, before Before) (*ProvidersWithMeta, error) {
	model, err := gorm.G[models.Model](models.DB).Where("name = ?", modelName).First(ctx)
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

	modelWithProviderChain := gorm.G[models.ModelWithProvider](models.DB).Where("model_id = ?", model.ID).Where("status = ?", true)

	if before.toolCall {
		modelWithProviderChain = modelWithProviderChain.Where("tool_call = ?", true)
	}

	if before.structuredOutput {
		modelWithProviderChain = modelWithProviderChain.Where("structured_output = ?", true)
	}

	if before.image {
		modelWithProviderChain = modelWithProviderChain.Where("image = ?", true)
	}

	modelWithProviders, err := modelWithProviderChain.Find(ctx)
	if err != nil {
		return nil, err
	}

	if len(modelWithProviders) == 0 {
		return nil, errors.New("not provider for model " + modelName)
	}

	modelWithProviderMap := lo.KeyBy(modelWithProviders, func(mp models.ModelWithProvider) uint { return mp.ID })

	weightItems := lo.SliceToMap(modelWithProviders, func(modelWithProvider models.ModelWithProvider) (uint, int) {
		return modelWithProvider.ID, modelWithProvider.Weight
	})

	providers, err := gorm.G[models.Provider](models.DB).
		Where("id IN ?", lo.Map(modelWithProviders, func(mp models.ModelWithProvider, _ int) uint { return mp.ProviderID })).
		Where("type = ?", style).
		Find(ctx)
	if err != nil {
		return nil, err
	}

	providerMap := lo.KeyBy(providers, func(p models.Provider) uint { return p.ID })

	if model.IOLog == nil {
		model.IOLog = new(bool)
	}

	return &ProvidersWithMeta{
		ModelWithProviderMap: modelWithProviderMap,
		WeightItems:          weightItems,
		ProviderMap:          providerMap,
		MaxRetry:             model.MaxRetry,
		TimeOut:              model.TimeOut,
		IOLog:                *model.IOLog,
	}, nil
}
