package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

// ─────────────────────────────────────────────────────────────────────────
// 演示部署（Vercel）专用的启动辅助
//
// 这份文件整体是**为展示服务的旁路代码**：真实部署（本地 / docker / k8s）
// 里 demoSeedEnabled 与 isVercel 都返回 false，于是库路径与监听端口的行为
// 与改动前一模一样。只有设了 VERCEL（平台自带）或显式 LLMIO_DEMO_SEED 时才生效。
// ─────────────────────────────────────────────────────────────────────────

// demoSeedLogCount 是造出来的访问日志条数。取 120 是为了让「最近 24 小时」这个
// 默认时间窗里每个分桶都有几个样本——太少的话趋势图是断的，太多则演示价值不变。
const demoSeedLogCount = 120

// demoSeedLogCols 是 demoSeedChatLogs 那条 INSERT 的列数。
//
// 抽成常量并在构造行时校验：列与占位符靠手数对齐，数错一个就是运行时
// "N values for M columns" 的硬错误，而这条路径只在演示部署上跑，
// 本地测试覆盖不到。让它在构造时就自己炸出来。
const demoSeedLogCols = 28

// demoSeedFlagKey 是"演示数据已灌过"的标记配置项。
const demoSeedFlagKey = "llmio_demo_data"

// demoSeedEnabled 判定这次启动要不要灌演示数据。
//
// 判据是 VERCEL 这个平台自带的变量：本地跑、docker 跑、k8s 跑都不会有它，
// 于是**真实部署永远进不到这里**，不会有人某天发现自己的生产库被塞了演示日志。
// 另给一个显式开关，方便本地预览演示形态。
func demoSeedEnabled() bool {
	if os.Getenv("LLMIO_DEMO_SEED") != "" {
		return true
	}
	return os.Getenv("VERCEL") != ""
}

// isVercel 判定是否跑在 Vercel 上。库路径要跟着平台走，见 demoDBPath。
func isVercel() bool {
	return os.Getenv("VERCEL") != ""
}

// demoDBPath 决定库文件放哪。
//
// Vercel 上只有 /tmp 可写，其余路径（含工作目录）是只读的，而 models.Init 一上来
// 就要建库文件。演示场景下"每个实例各自一份临时库"是可接受的：数据在单次展示
// 期间活着，实例回收后重置——这正是展示要的（每次演示都是干净的初始状态）。
//
// 显式给了 LLMIO_DB_PATH 就听显式的，两个平台都能覆盖。
func demoDBPath() string {
	if p := os.Getenv("LLMIO_DB_PATH"); p != "" {
		return p
	}
	if isVercel() {
		return "/tmp/llmio.db"
	}
	return "./db/llmio.db"
}

// listenerPort 决定监听哪个端口。
//
// 本地与容器里沿用 LLMIO_SERVER_PORT（默认 7070，见 consts.DefaultPort）；
// 平台会注入 PORT 并要求服务监听它，所以在没有显式配置时让 PORT 优先。
// 显式配了 LLMIO_SERVER_PORT 就以它为准——那是使用者的明确意图。
func listenerPort() string {
	if p := os.Getenv("LLMIO_SERVER_PORT"); p != "" {
		return p
	}
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return consts.DefaultPort
}

// demoSeed 往空库里灌一份演示数据，让概览、分析、日志、模型页一进去就有东西看。
//
// 只在库为空时插入，见 models.IsEmptyDB 的判据。
func demoSeed(ctx context.Context) {
	if !models.IsEmptyDB(ctx) {
		return
	}

	if err := models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return demoSeedAll(ctx, tx)
	}); err != nil {
		// 演示数据只是"好看"，不是服务能不能起的前提。失败就跳过，
		// 让人进到一个空控制台，而不是一个起不来的服务。
		slog.Error("demo seed: 灌演示数据失败，跳过", "error", err)
		return
	}

	slog.Info("demo seed: 演示数据已就绪", "logs", demoSeedLogCount)
}

func demoSeedAll(ctx context.Context, tx *gorm.DB) error {
	now := time.Now()

	// ── 供应商 ──
	//
	// Config 里的 api_key 一律是**假的**（sk-demo-*）。这条是刻意的，不是偷懒：
	// 演示站是对外公开的，任何人打开控制台都能读到供应商配置原文。写真的等于泄露。
	// 演示时"测试连通性"按钮会失败，那是预期行为——页面上展示的是配置形态与负载
	// 均衡的可视化，不是真去调上游。
	providers := []models.Provider{
		{
			Name:    "OpenAI 演示渠",
			Type:    consts.StyleOpenAI,
			Config:  `{"base_url":"https://api.openai.com","api_key":"sk-demo-openai"}`,
			Console: "https://platform.openai.com/usage",
		},
		{
			Name:    "Anthropic 演示渠",
			Type:    consts.StyleAnthropic,
			Config:  `{"base_url":"https://api.anthropic.com","api_key":"sk-demo-anthropic","version":"2023-06-01"}`,
			Console: "https://console.anthropic.com/settings/usage",
		},
		{
			Name:    "Gemini 演示渠",
			Type:    consts.StyleGemini,
			Config:  `{"base_url":"https://generativelanguage.googleapis.com","api_key":"sk-demo-gemini"}`,
			Console: "https://aistudio.google.com/apikey",
		},
	}
	if err := tx.Create(&providers).Error; err != nil {
		return fmt.Errorf("seed providers: %w", err)
	}

	// ── 模型 ──
	//
	// 一个开熔断、一个不开：策略与熔断是重点，两个都摆出来才看得出差别。
	modelsList := []models.Model{
		{
			Name: "gpt-4o-mini", Remark: "演示用主力模型：三条上游、加权 3:2:1",
			MaxRetry: 2, TimeOut: 60,
			Strategy: consts.BalancerLottery, Breaker: new(true), PreferDirect: new(true),
			DisplayOrder: 2,
		},
		{
			Name: "claude-3-5-sonnet", Remark: "演示用视觉模型：双上游、顺序轮询",
			MaxRetry: 1, TimeOut: 90,
			Strategy: consts.BalancerRotor, Breaker: new(false), PreferDirect: new(true),
			DisplayOrder: 1,
		},
	}
	if err := tx.Create(&modelsList).Error; err != nil {
		return fmt.Errorf("seed models: %w", err)
	}

	// ── 模型 × 上游 关联 ──
	//
	// 权重 3:2:1 是演示负载均衡时最好看的比例：日志页能一眼看出分配不均，
	// 而不是三条平均分（那看起来跟没配权重一样）。
	assoc := []models.ModelWithProvider{
		{
			ModelID: modelsList[0].ID, ProviderID: providers[0].ID, ProviderModel: "gpt-4o-mini",
			Weight: 3, ToolCall: new(true), StructuredOutput: new(true), Image: new(true),
			WithHeader: new(false), Status: new(true),
			Currency: "CNY", InputPrice: new(0.7), CacheReadPrice: new(0.35), OutputPrice: new(2.8),
		},
		{
			ModelID: modelsList[0].ID, ProviderID: providers[1].ID, ProviderModel: "claude-3-5-haiku",
			Weight: 2, ToolCall: new(true), StructuredOutput: new(true), Image: new(true),
			WithHeader: new(false), Status: new(true),
			Currency: "CNY", InputPrice: new(0.8), CacheReadPrice: new(0.4), OutputPrice: new(3.2),
		},
		{
			ModelID: modelsList[0].ID, ProviderID: providers[2].ID, ProviderModel: "gemini-2.0-flash",
			Weight: 1, ToolCall: new(true), StructuredOutput: new(false), Image: new(true),
			WithHeader: new(false), Status: new(true),
			Currency: "CNY", InputPrice: new(0.1), CacheReadPrice: new(0.05), OutputPrice: new(0.4),
		},
		{
			ModelID: modelsList[1].ID, ProviderID: providers[0].ID, ProviderModel: "gpt-4o",
			Weight: 1, ToolCall: new(true), StructuredOutput: new(true), Image: new(true),
			WithHeader: new(true), Status: new(true),
			Currency: "CNY", InputPrice: new(2.5), CacheReadPrice: new(1.25), OutputPrice: new(10.0),
		},
		{
			ModelID: modelsList[1].ID, ProviderID: providers[1].ID, ProviderModel: "claude-3-5-sonnet-20241022",
			Weight: 1, ToolCall: new(true), StructuredOutput: new(false), Image: new(true),
			WithHeader: new(false), Status: new(true),
			Currency: "CNY", InputPrice: new(3.0), CacheReadPrice: new(1.5), OutputPrice: new(15.0),
		},
	}
	if err := tx.Create(&assoc).Error; err != nil {
		return fmt.Errorf("seed model-providers: %w", err)
	}

	// ── 访问密钥 ──
	//
	// 展示用，不是给人拿去调接口的；AllowAll 开着省得演示时还要挑模型。
	key := models.AuthKey{
		Name: "演示项目", Key: consts.KeyPrefix + "demo0000000000000000000000",
		Status: new(true), IOLog: new(false), AllowAll: new(true),
		UsageCount: int64(demoSeedLogCount), LastUsedAt: new(now),
	}
	if err := tx.Create(&key).Error; err != nil {
		return fmt.Errorf("seed auth keys: %w", err)
	}

	// ── 访问日志 ──
	if err := demoSeedChatLogs(ctx, tx, now, assoc, key); err != nil {
		return err
	}

	// 标记放最后：中途任何一步失败都会回滚，标记不会留下，下次启动能重来一遍。
	return tx.Create(&models.Config{Key: demoSeedFlagKey, Value: "1"}).Error
}

// pickAssoc 按关联上配的权重挑一条（模型 × 上游），并回传它对应的上游模型名。
//
// 照权重挑而不是均匀挑：日志里 3:2:1 的分布正是负载均衡那页要展示的东西，
// 均匀分布会让"配了权重"这件事在页面上看不出来。
func pickAssoc(rng *rand.Rand, assoc []models.ModelWithProvider, modelID uint) (models.ModelWithProvider, string) {
	candidates := make([]models.ModelWithProvider, 0, 3)
	total := 0
	for _, a := range assoc {
		if a.ModelID != modelID {
			continue
		}
		candidates = append(candidates, a)
		total += a.Weight
	}
	if len(candidates) == 0 || total <= 0 {
		return models.ModelWithProvider{}, ""
	}

	n := rng.Intn(total)
	for _, a := range candidates {
		if n < a.Weight {
			return a, a.ProviderModel
		}
		n -= a.Weight
	}
	last := candidates[len(candidates)-1]
	return last, last.ProviderModel
}

// demoSeedChatLogs 造一份跨最近 24 小时的请求日志。
//
// 用裸 SQL 而不是 tx.Create：这些行的 created_at **必须**散在过去 24 小时里，
// 而 GORM 创建时会自行填当前时刻，之后再 UPDATE 一遍就是两次写。一次 INSERT
// 把列名和值都写全，简单且只写一次。
func demoSeedChatLogs(
	ctx context.Context,
	tx *gorm.DB,
	now time.Time,
	assoc []models.ModelWithProvider,
	key models.AuthKey,
) error {
	// 固定种子：每次重建演示库，图形形状都一样，方便截图和对比。
	rng := rand.New(rand.NewSource(20260814))

	// 上游 → 供应商名的对照，从库里读回来用。
	var providers []models.Provider
	if err := tx.WithContext(ctx).Find(&providers).Error; err != nil {
		return fmt.Errorf("seed chat logs: 读供应商: %w", err)
	}
	providerName := make(map[uint]string, len(providers))
	for _, p := range providers {
		providerName[p.ID] = p.Name
	}
	// 每个模型各挑一条关联当"价格档位"，供日志快照用。
	priceOf := make(map[uint]models.ModelWithProvider)
	for _, a := range assoc {
		if _, ok := priceOf[a.ModelID]; !ok {
			priceOf[a.ModelID] = a
		}
	}
	modelIDs := make([]uint, 0, len(priceOf))
	for id := range priceOf {
		modelIDs = append(modelIDs, id)
	}
	// map 遍历顺序随机，排序保证每次构造的演示库形状一致。
	sort.Slice(modelIDs, func(i, j int) bool { return modelIDs[i] < modelIDs[j] })

	// 与 modelIDs 同序（都按 id 升序），因此下标一一对应。
	var modelNames []string
	if err := tx.WithContext(ctx).Model(&models.Model{}).Order("id ASC").Pluck("name", &modelNames).Error; err != nil {
		return fmt.Errorf("seed chat logs: 读模型名: %w", err)
	}
	if len(modelNames) != len(modelIDs) {
		return fmt.Errorf("seed chat logs: 模型数与关联数对不上，模型=%d 关联=%d", len(modelNames), len(modelIDs))
	}

	// 每条上游的基准延迟，让不同上游的延迟分布拉开差距——
	// 分析页的延迟分布与排行榜才有对比物，而不是三坨一样的数。
	latency := []time.Duration{820 * time.Millisecond, 1150 * time.Millisecond, 480 * time.Millisecond}
	uas := []string{"Claude-Code/1.0.44", "OpenAI-Python/1.58.1", "curl/8.7.1"}
	// 错误按真实会遇到的分类给，错误分析页的分组才不至于只有一种。
	errSamples := []string{
		`{"error":{"type":"rate_limit_error","message":"Rate limit reached for gpt-4o-mini"}}`,
		`{"error":{"type":"server_error","message":"upstream connect timeout"}}`,
	}

	values := make([]string, 0, demoSeedLogCount)
	args := make([]any, 0, demoSeedLogCount*demoSeedLogCols)

	for i := 0; i < demoSeedLogCount; i++ {
		// 均匀铺满 24 小时，再加一点抖动，避免分桶边界上堆成一列。
		ago := time.Duration(float64(24*time.Hour) * (float64(i) + rng.Float64()) / demoSeedLogCount)
		created := now.Add(-ago)

		// 从关联里按权重挑，model / provider / 上游模型名三者天然自洽。
		slot := i % len(modelIDs)
		modelID := modelIDs[slot]
		a, upstreamModel := pickAssoc(rng, assoc, modelID)
		modelName := modelNames[slot]

		failed := rng.Float64() < 0.08 // 8% 失败率，成功率 92% 上下，图上好看又好解释

		prompt := int64(180 + rng.Intn(1400))
		completion := int64(60 + rng.Intn(900))
		if failed {
			completion = 0
		}
		// 缓存命中只在约四成请求上出现：真实场景里命中率不会是常量。
		cached := int64(0)
		if !failed && rng.Float64() < 0.4 {
			cached = prompt / 2
		}

		// TPS 与耗时的因果方向要摆对：真实链路里是"上游以某个速率吐字"，
		// 由它决定这段流持续多久，而不是先定时长再倒算速率。倒算出来的
		// 速率会随随机耗时一起乱飘（实测能飘到 496 tok/s），而分析页的
		// TPS 排行榜是拿它排序的，飘了就一眼假。
		//
		// 取 18–64 tok/s：这是各家上游在主流动模型上的常见区间。
		tps := 18.0 + rng.Float64()*46.0
		// 失败的行没有产出，按上面那条链路的形态给一个"起步就断"的短耗时。
		if failed {
			tps = 0
		}

		base := latency[rng.Intn(len(latency))]
		proxyTime := base
		firstChunk := base / 3
		if completion > 0 {
			proxyTime = time.Duration(float64(completion)/tps*float64(time.Second)) + base
			firstChunk = base / 3
		}

		status := consts.StatusSuccess
		errText := ""
		if failed {
			status = consts.StatusError
			errText = errSamples[rng.Intn(len(errSamples))]
		}

		// 价格三档 + 币种必须给全：ChatLog 上它们是非指针的 float64/string，
		// 落 NULL 会让读路径 Scan 失败（真实写入路径见 service/chat.go）。
		price := priceOf[modelID]
		inputPrice, cacheReadPrice, outputPrice := 0.0, 0.0, 0.0
		currency := "CNY"
		if price.InputPrice != nil {
			inputPrice = *price.InputPrice
		}
		if price.CacheReadPrice != nil {
			cacheReadPrice = *price.CacheReadPrice
		}
		if price.OutputPrice != nil {
			outputPrice = *price.OutputPrice
		}
		if price.Currency != "" {
			currency = price.Currency
		}

		row := []any{
			created, created,
			modelName,
			fmt.Sprintf("trace-demo-%04d", i),
			upstreamModel, providerName[a.ProviderID], status, consts.StyleOpenAI,
			uas[rng.Intn(len(uas))],
			fmt.Sprintf("203.0.113.%d", 10+rng.Intn(200)),
			key.ID, fmt.Sprintf("sess-demo-%d", i/5), false,
			errText, 0,
			// Duration 按**纳秒**存（GORM 的约定，见 models/migrate_test.go 的上游夹具），
			// 所以直接给 time.Duration 本身，不是 Microseconds()。
			int64(proxyTime), int64(firstChunk), int64(proxyTime),
			tps, int(completion * 4),
			prompt, completion, prompt + completion,
			// prompt_tokens_details 是 serializer:json 的列，**必须喂 JSON 文本**：
			// 喂整数落库后读路径反序列化会直接报错；空值也不行（列上有序列化器）。
			fmt.Sprintf(`{"cached_tokens":%d,"audio_tokens":0}`, cached),
			inputPrice, cacheReadPrice, outputPrice, currency,
		}
		if len(row) != demoSeedLogCols {
			return fmt.Errorf("seed chat logs: 列数对不上，row=%d 期望=%d", len(row), demoSeedLogCols)
		}
		values = append(values, "("+strings.TrimSuffix(strings.Repeat("?,", demoSeedLogCols), ",")+")")
		args = append(args, row...)
	}

	stmt := `INSERT INTO chat_logs (
		created_at, updated_at,
		name, trace_id,
		provider_model, provider_name, status, style,
		user_agent, remote_ip,
		auth_key_id, session_id, chat_io,
		error, retry,
		proxy_time, first_chunk_time, chunk_time,
		tps, size,
		prompt_tokens, completion_tokens, total_tokens,
		prompt_tokens_details,
		input_price, cache_read_price, output_price, currency
	) VALUES ` + strings.Join(values, ",")

	if err := tx.WithContext(ctx).Exec(stmt, args...).Error; err != nil {
		return fmt.Errorf("seed chat logs: %w", err)
	}
	return nil
}
