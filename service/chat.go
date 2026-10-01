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
	"github.com/atopos31/llmio/bridge"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/pkg/token"
	"github.com/atopos31/llmio/providers"
	"github.com/samber/lo"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
)

func BalanceChat(ctx context.Context, start time.Time, style string, before Before, providersWithMeta ProvidersWithMeta, reqMeta models.ReqMeta, bridgeNotes *BridgeNotes) (*http.Response, *models.ChatLog, error) {
	slog.Info("request", "model", before.Model, "stream", before.Stream, "tool_call", before.toolCall, "structured_output", before.structuredOutput, "image", before.image)

	providerMap := providersWithMeta.ProviderMap

	// 收集重试过程中的err日志
	retryLog := make(chan models.ChatLog, providersWithMeta.MaxRetry)
	defer close(retryLog)

	go RecordRetryLog(context.Background(), retryLog)

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

	// 是否开启熔断
	if providersWithMeta.Breaker {
		balancer = balancers.BalancerWrapperBreaker(balancer)
	}

	// 设置请求超时
	responseHeaderTimeout := time.Second * time.Duration(providersWithMeta.TimeOut)
	// 流式超时时间缩短
	if before.Stream {
		responseHeaderTimeout = responseHeaderTimeout / 3
	}

	authKeyID, _ := ctx.Value(consts.ContextKeyAuthKeyID).(uint)
	authKeyIOLog, _ := ctx.Value(consts.ContextKeyAuthKeyIOLog).(bool)

	traceID, err := token.GenerateRandomChars(10)
	if err != nil {
		return nil, nil, err
	}

	// 会话键在重试循环外解析一次：同一次请求的各次重试共用同一会话��，
	// 避免重试期间会话漂移（随机兜底分支尤其需要）
	sessionKey := ResolveSessionKey(reqMeta.Header, before.SessionID, before.Model, authKeyID, before.raw)

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
				return nil, nil, fmt.Errorf("balancer pop err: %v, traceID: %s", err, traceID)
			}

			modelWithProvider, ok := providersWithMeta.ModelWithProviderMap[id]
			if !ok {
				// 数据不一致，移除该模型避免下次重复命中
				balancer.Delete(id)
				continue
			}

			provider := providerMap[modelWithProvider.ProviderID]

			chatModel, err := providers.New(provider.Type, provider.Config, provider.Proxy)
			if err != nil {
				return nil, nil, err
			}

			client := providers.GetClient(responseHeaderTimeout, provider.Proxy)

			slog.Info("using provider", "provider", provider.Name, "model", modelWithProvider.ProviderModel)

			// 每换一家上游，记账从头计：上一家的记账若留着，最终那条日志就会记着
			// 产出这次响应的那家**没做过**的改写
			bridgeNotes.reset()

			// 客户端协议与上游协议不同时插一层翻译。候选池里可能混着两种协议的上游
			// （见 ProvidersWithMetaBymodelsName），所以每次 Pop 之后重新判一次方向
			translator := TranslatorFor(style, provider.Type)
			if translator != nil {
				slog.Info("bridging protocols", "from", style, "to", provider.Type, "provider", provider.Name)
			}

			// 分时段计价：按请求发起时刻 + **这一条关联的**峰谷条款解析出
			// 生效单价并快照进日志。快照的是实际生效价，因此历史成本不会被
			// 后来的时段配置改动所改写。
			inputPrice, cacheReadPrice, outputPrice, peakPeriod := ResolvePricingFor(
				ctx, start, modelWithProvider.Peak,
				lo.FromPtrOr(modelWithProvider.InputPrice, 0),
				lo.FromPtrOr(modelWithProvider.CacheReadPrice, 0),
				lo.FromPtrOr(modelWithProvider.OutputPrice, 0),
			)

			log := models.ChatLog{
				Name:           before.Model,
				TraceID:        traceID,
				ProviderModel:  modelWithProvider.ProviderModel,
				ProviderName:   provider.Name,
				Status:         consts.StatusRunning,
				Style:          style,
				UpstreamStyle:  provider.Type,
				UserAgent:      reqMeta.UserAgent,
				RemoteIP:       reqMeta.RemoteIP,
				AuthKeyID:      authKeyID,
				SessionID:      before.SessionID,
				ChatIO:         authKeyIOLog,
				Retry:          retry,
				ProxyTime:      time.Since(start),
				InputPrice:     inputPrice,
				CacheReadPrice: cacheReadPrice,
				OutputPrice:    outputPrice,
				Currency:       modelWithProvider.Currency,
				PeakPeriod:     peakPeriod,
			}
			// 这次尝试失败的记录。记账要在这里快照：下一次尝试开头会把盒子清空，
			// 而"这一家失败时我们丢了什么"正是排查重试时要看的东西
			fail := func(err error) {
				log.BridgeNotes = bridgeNotes.String()
				retryLog <- log.WithError(err)
			}
			// 根据请求原始请求头 是否透传请求头 自定义请求头 构建新的请求头
			withHeader := lo.FromPtrOr(modelWithProvider.WithHeader, false)
			headers := BuildHeaders(reqMeta.Header, withHeader, modelWithProvider.CustomerHeaders, before.Stream, HeaderVars{
				Session:       sessionKey,
				SessionID:     before.SessionID,
				Model:         before.Model,
				ProviderModel: modelWithProvider.ProviderModel,
				TraceID:       traceID,
				AuthKeyID:     authKeyID,
			})

			// 请求体：先按上游协议翻译，再套上关联行的 extra_body。
			// 顺序不能反——extra_body 挂在这一条关联上，是按**上游协议**写的；先套再翻
			// 的话，翻译层会把这些键当不认识的字段丢掉
			reqBody := before.raw
			if translator != nil {
				converted, notes, err := translator.Request(reqBody, bridge.Options{})
				if err != nil {
					// 翻不过去 = 这家上游服务不了。与 BuildReq 失败同一条路：记账、
					// 移出待选、换下一家
					fail(fmt.Errorf("bridge request: %w", err))
					balancer.Delete(id)
					continue
				}
				bridgeNotes.record(notes)
				reqBody = converted
			}

			rawBody, err := buildUpstreamBody(reqBody, modelWithProvider.ExtraBody)
			if err != nil {
				fail(err)
				balancer.Delete(id)
				continue
			}

			req, err := chatModel.BuildReq(ctx, headers, modelWithProvider.ProviderModel, rawBody)
			if err != nil {
				fail(err)
				// 构建请求失败 移除待选
				balancer.Delete(id)
				continue
			}

			res, err := client.Do(req)
			if err != nil {
				fail(err)
				// 请求失败 移除待选
				balancer.Delete(id)
				continue
			}

			if res.StatusCode != http.StatusOK {
				byteBody, err := io.ReadAll(res.Body)
				if err != nil {
					slog.Error("read body error", "error", err)
				}
				fail(fmt.Errorf("status: %d, body: %s", res.StatusCode, string(byteBody)))

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

			if provider.ErrorMatcher != "" {
				contentType := strings.ToLower(res.Header.Get("Content-Type"))
				// 流式正常返回通常是 text/event-stream，不提前消费响应体避免影响转发。
				if !strings.Contains(contentType, "text/event-stream") {
					byteBody, err := io.ReadAll(res.Body)
					if err != nil {
						fail(fmt.Errorf("read body failed: %w", err))
						balancer.Delete(id)
						res.Body.Close()
						continue
					}

					if matched, sample := matchProviderBodyError(string(byteBody), provider.ErrorMatcher); matched {
						fail(fmt.Errorf("response matched provider error sample %q, body: %s", sample, string(byteBody)))
						balancer.Delete(id)
						res.Body.Close()
						continue
					}

					res.Body = io.NopCloser(bytes.NewReader(byteBody))
				}
			}

			// 响应体：翻成客户端协议之后再交出去。必须在转发与记录**之前**——
			// 客户端看到的和 ChatLog 记的都得是客户端协议（记录用的 processer 是按
			// 客户端协议选的）
			if translator != nil {
				if err := bridgeResponse(translator, res, before.Stream, bridgeNotes); err != nil {
					fail(fmt.Errorf("bridge response: %w", err))
					balancer.Delete(id)
					res.Body.Close()
					continue
				}
			}

			balancer.Success(id)

			return res, &log, nil
		}
	}

	return nil, nil, fmt.Errorf("All retry failed, trace ID: %s", traceID)
}

func buildUpstreamBody(raw []byte, extraBody map[string]any) ([]byte, error) {
	rawBody := raw
	if len(extraBody) > 0 {
		for key, value := range extraBody {
			var err error
			rawBody, err = sjson.SetBytes(rawBody, key, value)
			if err != nil {
				slog.Warn("failed to set extra body key", "key", key, "error", err)
			}
		}
	}

	rawBody, err := sjson.DeleteBytes(rawBody, "session_id")
	if err != nil {
		return nil, fmt.Errorf("delete session_id from upstream body: %w", err)
	}
	return rawBody, nil
}

func RecordRetryLog(ctx context.Context, retryLog chan models.ChatLog) {
	for log := range retryLog {
		if _, err := SaveChatLog(ctx, log); err != nil {
			slog.Error("save chat log error", "error", err)
		}
	}
}

func RecordLog(ctx context.Context, reqStart time.Time, reader io.ReadCloser, processer Processer, logId uint, before Before, ioLog bool, bridgeNotes *BridgeNotes) {
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
		// 协议互转的记账：响应侧要到流读完才完整，所以在这里（日志落库前）取
		log.BridgeNotes = bridgeNotes.String()
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

func BuildHeaders(source http.Header, withHeader bool, customHeaders map[string]string, stream bool, vars HeaderVars) http.Header {
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
		// 支持 {{...}} 占位符动态求值（如会话键）；字面量值行为不变
		rendered := renderHeaderValue(value, vars)
		if rendered == "" {
			// 已配置的自定义头必须给出非空值：部分上游（如 opencode）缺少该头
			// 或值为空都会直接返回 400，空值等同于未配置
			rendered = newRandomID()
		}
		header.Set(key, rendered)
	}

	// Accept-Encoding 必须留给 Go Transport 自行协商：一旦该头被显式设置（透传客户端头或自定义头），
	// Transport 就不再透明解压上游响应，压缩后的字节会被原样记录进 ChatIO（乱码），
	// 同时 usage 解析失败导致 token 统计为 0。放在自定义头之后删除，避免配置重新引入该问题。
	header.Del("Accept-Encoding")

	return header
}

type ProvidersWithMeta struct {
	ModelWithProviderMap map[uint]models.ModelWithProvider
	WeightItems          map[uint]int
	ProviderMap          map[uint]models.Provider
	MaxRetry             int
	TimeOut              int
	Strategy             string
	Breaker              bool
}

func ProvidersWithMetaBymodelsName(ctx context.Context, style string, before Before) (*ProvidersWithMeta, error) {
	model, err := gorm.G[models.Model](models.DB).Where("name = ?", before.Model).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if _, err := SaveChatLog(ctx, models.ChatLog{
				Name:      before.Model,
				Status:    consts.StatusError,
				Style:     style,
				SessionID: before.SessionID,
				Error:     err.Error(),
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
		Where("type IN ?", ServableTypes(style)).
		Find(ctx)
	if err != nil {
		return nil, err
	}

	// 直连优先，转换为兜底：只要这个模型下有一个说客户端话的上游，候选池就只有它们。
	// 转换只在该模型下"没人说客户端的话"时启用，行为可预期；现有部署（上游与客户端
	// 同协议）的候选集也因此与转换功能上线前完全一致。
	// 模型上可以把这条关掉（PreferDirect），关掉就是整池按权重摇
	providers = poolFor(providers, style, model.PreferDirect)

	providerMap := lo.KeyBy(providers, func(p models.Provider) uint { return p.ID })

	weightItems := make(map[uint]int)
	for _, mp := range modelWithProviders {
		if _, ok := providerMap[mp.ProviderID]; !ok {
			continue
		}
		weightItems[mp.ID] = mp.Weight
	}

	return &ProvidersWithMeta{
		ModelWithProviderMap: modelWithProviderMap,
		WeightItems:          weightItems,
		ProviderMap:          providerMap,
		MaxRetry:             model.MaxRetry,
		TimeOut:              model.TimeOut,
		Strategy:             model.Strategy,
		Breaker:              lo.FromPtrOr(model.Breaker, false),
	}, nil
}
