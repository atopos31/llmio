package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atopos31/llmio/balancers"
	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"gorm.io/gorm"
)

func BalanceChat(ctx context.Context, start time.Time, style string, before Before, providersWithMeta ProvidersWithMeta, reqMeta models.ReqMeta) (*http.Response, *models.ChatLog, error) {
	slog.Info("request", "model", before.Model, "stream", before.Stream, "tool_call", before.toolCall, "structured_output", before.structuredOutput, "image", before.image)

	providerMap := providersWithMeta.ProviderMap

	// 收集重试过程中的err日志
	retryLog := make(chan models.ChatLog, providersWithMeta.MaxRetry)

	// 使用带超时的 context 来避免 goroutine 泄漏
	logCtx, logCancel := context.WithTimeout(ctx, time.Second*time.Duration(providersWithMeta.TimeOut))
	defer logCancel()

	// 启动日志记录 goroutine
	go RecordRetryLog(logCtx, retryLog)

	// 确保函数退出时关闭 channel 并等待 goroutine 结束
	defer func() {
		close(retryLog)
		// 给 goroutine 一点时间来处理剩余的日志
		time.Sleep(10 * time.Millisecond)
	}()

	// 选择负载均衡策略
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

	// 设置请求超时
	responseHeaderTimeout := time.Second * time.Duration(providersWithMeta.TimeOut)
	// 流式超时时间缩短，提供更快的首字节响应
	if before.Stream {
		responseHeaderTimeout = responseHeaderTimeout / consts.StreamTimeoutDivisor
	}
	client := providers.GetClient(responseHeaderTimeout)

	authKeyID, _ := ctx.Value(consts.ContextKeyAuthKeyID).(uint)

	timer := time.NewTimer(time.Second * time.Duration(providersWithMeta.TimeOut))
	defer timer.Stop()
	for retry := range providersWithMeta.MaxRetry {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-timer.C:
			return nil, nil, errors.New("retry time out")
		default:
			// 加权负载均衡
			id, err := balancer.Pop()
			if err != nil {
				return nil, nil, err
			}

			modelWithProvider, ok := providersWithMeta.ModelWithProviderMap[id]
			if !ok {
				// 数据不一致，移除该模型避免下次重复命中
				balancer.Delete(id)
				continue
			}

			provider := providerMap[modelWithProvider.ProviderID]

			chatModel, err := providers.New(provider.Type, provider.Config)
			if err != nil {
				return nil, nil, err
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
				AuthKeyID:     authKeyID,
				ChatIO:        providersWithMeta.IOLog,
				Retry:         retry,
				ProxyTime:     time.Since(start),
			}
			// 根据请求原始请求头 是否透传请求头 自定义请求头 构建新的请求头
			withHeader := false
			if modelWithProvider.WithHeader != nil {
				withHeader = *modelWithProvider.WithHeader
			}
			header := BuildHeaders(reqMeta.Header, withHeader, modelWithProvider.CustomerHeaders, before.Stream)

			req, err := chatModel.BuildReq(ctx, header, modelWithProvider.ProviderModel, before.raw)
			if err != nil {
				retryLog <- log.WithError(err)
				// 构建请求失败 移除待选
				balancer.Delete(id)
				continue
			}

			res, err := client.Do(req)
			if err != nil {
				retryLog <- log.WithError(err)
				// 请求失败 移除待选
				balancer.Delete(id)
				continue
			}

			if res.StatusCode != http.StatusOK {
				byteBody, err := io.ReadAll(res.Body)
				if err != nil {
					slog.Error("read body error", "error", err)
				}
				retryLog <- log.WithError(fmt.Errorf("status: %d, body: %s", res.StatusCode, string(byteBody)))

				if res.StatusCode == http.StatusTooManyRequests {
					// 达到RPM限制 降低权重
					balancer.Reduce(id)
				} else {
					// 非RPM限制 移除待选
					balancer.Delete(id)
				}
				res.Body.Close()
				continue
			}

			balancer.Success(id)

			return res, &log, nil
		}
	}

	return nil, nil, errors.New("maximum retry attempts reached")
}

func RecordRetryLog(ctx context.Context, retryLog chan models.ChatLog) {
	for {
		select {
		case <-ctx.Done():
			// context 被取消，清空剩余日志并退出
			for {
				select {
				case log := <-retryLog:
					// 只保存有效的日志记录
					if log.Name != "" || log.Error != "" {
						if _, err := SaveChatLog(context.Background(), log); err != nil {
							slog.Error("save chat log error", "error", err)
						}
					}
				default:
					return
				}
			}
		case log, ok := <-retryLog:
			if !ok {
				return
			}
			// 只保存有效的日志记录
			if log.Name != "" || log.Error != "" {
				if _, err := SaveChatLog(ctx, log); err != nil {
					slog.Error("save chat log error", "error", err)
				}
			}
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
	Strategy             string // 负载均衡策略
	Breaker              bool   // 是否开启熔断
}

func ProvidersWithMetaBymodelsName(ctx context.Context, style string, before Before) (*ProvidersWithMeta, error) {
	model, err := gorm.G[models.Model](models.DB).Where("name = ?", before.Model).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			modelErr := common.NewModelError(before.Model, "model not found")
			if _, err := SaveChatLog(ctx, models.ChatLog{
				Name:   before.Model,
				Status: "error",
				Style:  style,
				Error:  modelErr.Error(),
			}); err != nil {
				return nil, err
			}
			return nil, modelErr
		}
		return nil, fmt.Errorf("database error when querying model %s: %w", before.Model, err)
	}

	// 使用原始 SQL 查询优化，一次性获取所需数据
	type ModelWithProviderJoined struct {
		models.ModelWithProvider
		ProviderName string `gorm:"column:provider_name"`
		ProviderType string `gorm:"column:provider_type"`
		Config       string `gorm:"column:config"`
	}

	var joinedResults []ModelWithProviderJoined

	// 构建基础查询条件
	whereConditions := []string{
		"model_with_providers.model_id = ?",
		"model_with_providers.status = ?",
		"providers.type = ?",
	}
	args := []interface{}{model.ID, true, style}

	if before.toolCall {
		whereConditions = append(whereConditions, "model_with_providers.tool_call = ?")
		args = append(args, true)
	}

	if before.structuredOutput {
		whereConditions = append(whereConditions, "model_with_providers.structured_output = ?")
		args = append(args, true)
	}

	if before.image {
		whereConditions = append(whereConditions, "model_with_providers.image = ?")
		args = append(args, true)
	}

	whereClause := strings.Join(whereConditions, " AND ")

	err = models.DB.Raw(`
		SELECT model_with_providers.*, 
		       providers.name as provider_name, 
		       providers.type as provider_type, 
		       providers.config
		FROM model_with_providers 
		JOIN providers ON model_with_providers.provider_id = providers.id 
		WHERE `+whereClause, args...).Scan(&joinedResults).Error

	if err != nil {
		return nil, fmt.Errorf("database error when querying model providers: %w", err)
	}

	if len(joinedResults) == 0 {
		return nil, common.NewModelError(before.Model, "no available providers for this model")
	}

	// 构建结果映射
	modelWithProviderMap := make(map[uint]models.ModelWithProvider)
	providerMap := make(map[uint]models.Provider)
	weightItems := make(map[uint]int)

	for _, result := range joinedResults {
		modelWithProviderMap[result.ID] = result.ModelWithProvider
		providerMap[result.ProviderID] = models.Provider{
			Model:  gorm.Model{ID: result.ProviderID},
			Name:   result.ProviderName,
			Type:   result.ProviderType,
			Config: result.Config,
		}
		weightItems[result.ID] = result.Weight
	}

	if model.IOLog == nil {
		model.IOLog = new(bool)
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
