package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atopos31/llmio/balancers"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/pkg/token"
	"github.com/atopos31/llmio/providers"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

type balanceChatResult struct {
	response *http.Response
	log      *models.ChatLog
}

func BalanceChat(ctx context.Context, start time.Time, style string, before Before, providersWithMeta ProvidersWithMeta, reqMeta models.ReqMeta) (*http.Response, *models.ChatLog, error) {
	slog.Info("request", "model", before.Model, "stream", before.Stream, "tool_call", before.toolCall, "structured_output", before.structuredOutput, "image", before.image)

	providerMap := providersWithMeta.ProviderMap
	retryLog := make(chan models.ChatLog, providersWithMeta.MaxRetry)
	defer close(retryLog)

	go RecordRetryLog(context.Background(), retryLog)

	var balancer balancers.Balancer
	switch providersWithMeta.Strategy {
	case consts.BalancerLottery:
		balancer = balancers.NewLottery(providersWithMeta.WeightItems)
	case consts.BalancerRotor:
		balancer = balancers.NewRotor(providersWithMeta.WeightItems)
	default:
		balancer = balancers.NewLottery(providersWithMeta.WeightItems)
	}

	if providersWithMeta.Breaker {
		balancer = balancers.BalancerWrapperBreaker(balancer)
	}

	responseHeaderTimeout := time.Second * time.Duration(providersWithMeta.TimeOut)
	if before.Stream {
		responseHeaderTimeout = responseHeaderTimeout / 3
	}

	authKeyID, _ := ctx.Value(consts.ContextKeyAuthKeyID).(uint)

	traceID, err := token.GenerateRandomChars(10)
	if err != nil {
		return nil, nil, err
	}

	currentRetry := 0
	result, err := DoRetry(ctx, RetryConfig{
		MaxRetry: providersWithMeta.MaxRetry,
		Timeout:  time.Second * time.Duration(providersWithMeta.TimeOut),
		OnRetry: func(attempt int) {
			currentRetry = attempt
		},
	}, func() (*balanceChatResult, error) {
		id, err := balancer.Pop()
		if err != nil {
			return nil, MarkPermanent(err)
		}

		modelWithProvider, ok := providersWithMeta.ModelWithProviderMap[id]
		if !ok {
			balancer.Delete(id)
			return nil, fmt.Errorf("model provider %d not found", id)
		}

		provider, ok := providerMap[modelWithProvider.ProviderID]
		if !ok {
			balancer.Delete(id)
			return nil, fmt.Errorf("provider %d not found", modelWithProvider.ProviderID)
		}

		chatModel, err := providers.New(provider.Type, provider.Config, provider.Proxy)
		if err != nil {
			return nil, MarkPermanent(err)
		}

		client := providers.GetClient(responseHeaderTimeout, provider.Proxy)
		slog.Info("using provider", "provider", provider.Name, "model", modelWithProvider.ProviderModel)

		log := models.ChatLog{
			Name:          before.Model,
			TraceID:       traceID,
			ProviderModel: modelWithProvider.ProviderModel,
			ProviderName:  provider.Name,
			Status:        consts.StatusRunning,
			Style:         style,
			UserAgent:     reqMeta.UserAgent,
			RemoteIP:      reqMeta.RemoteIP,
			AuthKeyID:     authKeyID,
			ChatIO:        providersWithMeta.IOLog,
			Retry:         currentRetry,
			ProxyTime:     time.Since(start),
		}

		withHeader := lo.FromPtrOr(modelWithProvider.WithHeader, false)
		headers := BuildHeaders(reqMeta.Header, withHeader, modelWithProvider.CustomerHeaders, before.Stream)

		req, err := chatModel.BuildReq(ctx, headers, modelWithProvider.ProviderModel, before.raw)
		if err != nil {
			retryLog <- log.WithError(err)
			balancer.Delete(id)
			return nil, err
		}

		res, err := client.Do(req)
		if err != nil {
			retryLog <- log.WithError(err)
			balancer.Delete(id)
			return nil, err
		}

		if res.StatusCode != http.StatusOK {
			byteBody, readErr := io.ReadAll(res.Body)
			if readErr != nil {
				slog.Error("read body error", "error", readErr)
			}

			statusErr := fmt.Errorf("status: %d, body: %s", res.StatusCode, string(byteBody))
			retryLog <- log.WithError(statusErr)

			if res.StatusCode == http.StatusTooManyRequests {
				balancer.Reduce(id)
			} else {
				balancer.Delete(id)
			}
			res.Body.Close()
			return nil, statusErr
		}

		if provider.ErrorMatcher != "" {
			contentType := strings.ToLower(res.Header.Get("Content-Type"))
			if !strings.Contains(contentType, "text/event-stream") {
				byteBody, readErr := io.ReadAll(res.Body)
				if readErr != nil {
					bodyErr := fmt.Errorf("read body failed: %w", readErr)
					retryLog <- log.WithError(bodyErr)
					balancer.Delete(id)
					res.Body.Close()
					return nil, bodyErr
				}

				if matched, sample := matchProviderBodyError(string(byteBody), provider.ErrorMatcher); matched {
					matchErr := fmt.Errorf("response matched provider error sample %q, body: %s", sample, string(byteBody))
					retryLog <- log.WithError(matchErr)
					balancer.Delete(id)
					res.Body.Close()
					return nil, matchErr
				}

				res.Body = io.NopCloser(bytes.NewReader(byteBody))
			}
		}

		balancer.Success(id)
		return &balanceChatResult{response: res, log: &log}, nil
	})
	if err != nil {
		return nil, nil, err
	}

	return result.response, result.log, nil
}

func RecordRetryLog(ctx context.Context, retryLog chan models.ChatLog) {
	for log := range retryLog {
		if _, err := SaveChatLog(ctx, log); err != nil {
			slog.Error("save chat log error", "error", err)
		}
	}
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
		log.Status = consts.StatusSuccess
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
		if _, err := gorm.G[models.ChatLog](models.DB).Where("id = ?", logId).Updates(ctx, models.ChatLog{
			Status: consts.StatusError,
			Error:  err.Error(),
		}); err != nil {
			slog.Error("record log error", "error", err)
		}
	}
}

func SaveChatLog(ctx context.Context, log models.ChatLog) (uint, error) {
	if err := gorm.G[models.ChatLog](models.DB).Create(ctx, &log); err != nil {
		return 0, err
	}
	return log.ID, nil
}

func BuildHeaders(source http.Header, withHeader bool, customHeaders map[string]string, stream bool) http.Header {
	header := http.Header{}
	if withHeader {
		header = source.Clone()
	}

	if stream {
		header.Set("X-Accel-Buffering", "no")
	}

	header.Del("Authorization")
	header.Del("X-Api-Key")
	header.Del("X-Goog-Api-Key")

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
	Strategy             string
	Breaker              bool
}

func ProvidersWithMetaBymodelsName(ctx context.Context, style string, before Before) (*ProvidersWithMeta, error) {
	model, err := gorm.G[models.Model](models.DB).Where("name = ?", before.Model).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if _, err := SaveChatLog(ctx, models.ChatLog{
				Name:   before.Model,
				Status: consts.StatusError,
				Style:  style,
				Error:  err.Error(),
			}); err != nil {
				return nil, err
			}
			return nil, errors.New("not found model " + before.Model)
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
		return nil, errors.New("not provider for model " + before.Model)
	}

	modelWithProviderMap := lo.KeyBy(modelWithProviders, func(mp models.ModelWithProvider) uint { return mp.ID })

	providers, err := gorm.G[models.Provider](models.DB).
		Where("id IN ?", lo.Map(modelWithProviders, func(mp models.ModelWithProvider, _ int) uint { return mp.ProviderID })).
		Where("type = ?", style).
		Find(ctx)
	if err != nil {
		return nil, err
	}

	providerMap := lo.KeyBy(providers, func(p models.Provider) uint { return p.ID })

	weightItems := make(map[uint]int)
	for _, mp := range modelWithProviders {
		if _, ok := providerMap[mp.ProviderID]; !ok {
			continue
		}
		weightItems[mp.ID] = mp.Weight
	}

	if model.IOLog == nil {
		model.IOLog = new(false)
	}

	breaker := false
	if model.Breaker != nil {
		breaker = *model.Breaker
	}

	return &ProvidersWithMeta{
		ModelWithProviderMap: modelWithProviderMap,
		WeightItems:          weightItems,
		ProviderMap:          providerMap,
		MaxRetry:             model.MaxRetry,
		TimeOut:              model.TimeOut,
		IOLog:                *model.IOLog,
		Strategy:             model.Strategy,
		Breaker:              breaker,
	}, nil
}
