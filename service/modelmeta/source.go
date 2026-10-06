// Package modelmeta 从公开的聚合模型数据库取「模型能力」与「每百万 token 价格」，
// 为「模型 × 上游」关联弹窗生成预填建议。
//
// 为什么统一走聚合源、不走逐家原生接口，见
// docs/model-capability-autofill-plan.md §1.1：原生接口的字段名互不相同，
// 而使用最广的 OpenAI GET /v1/models 只返回 id/object/created/owned_by。
//
// 这个包**只读**：它产出的建议由前端预填进表单，保存路径完全沿用既有的
// Create/UpdateModelProvider，不做任何服务端补值（§4.2）。
package modelmeta

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/atopos31/llmio/consts"
)

// 两个来源的名字。取值定义在 consts（那里是"谁都要用的词汇"），
// 这里只是本包内的短别名。
const (
	SourceModelsDev = consts.ModelMetaSourceModelsDev
	SourceLiteLLM   = consts.ModelMetaSourceLiteLLM
)

// DefaultSources 是按序尝试的来源。策略里 Sources 留空时用它。
var DefaultSources = consts.ModelMetaDefaultSources

// DefaultTTL 是缓存有效期。两个源都是小时级更新，24 小时足够新鲜，
// 又不至于反复去打扰上游（§3.2）。
const DefaultTTL = 24 * time.Hour

// ErrCatalogPreparing 表示数据还没准备好（第一次抓取正在后台跑）。
//
// 它与"抓取失败"是两件事，对应前端两套文案（§5.2「原因要分开」）：
// 这个是"稍后重试就能好"，那个是"确实取不到"。
var ErrCatalogPreparing = errors.New("model metadata catalog is being prepared")

// ErrUnknownSource 表示策略里写了这个包不认识的来源名。
var ErrUnknownSource = errors.New("unknown model metadata source")

// Entry 是一条模型元数据。
//
// 三个能力字段与三档价格一律用**指针**：源里没有这个字段时是 nil，
// 不是 false / 0。这条区分是整个包的地基（§3.5）——models.dev 有 44% 的模型
// 没有 structured_output，LiteLLM 的 supports_vision 只覆盖 42% 的对话条目，
// 把"源没提供"当成"不支持"会凭空给人断言。
type Entry struct {
	Source       string
	Provider     string // 源内的上游 id（models.dev 的顶层键 / LiteLLM 的 litellm_provider）
	ProviderName string
	Model        string // 源内的模型 id，原样保留
	// Status 是源标出的生命周期："" / "deprecated" / "beta"。
	Status string

	ToolCall         *bool
	StructuredOutput *bool
	Image            *bool

	InputPrice     *float64
	CacheReadPrice *float64
	OutputPrice    *float64
	Currency       string

	// ContextLimit / OutputLimit 只用于跨上游候选的提示文案
	// （"数据源在「Google」下存在同名模型 gemini-2.0-flash（上下文 1,048,576）"）。
	// llmio 的关联表还没有这两列，见 §7。
	ContextLimit int
	OutputLimit  int
}

// indexHit 是索引里的一格。
//
// aliased 记的是"这一格是路径尾名的别名，不是源里的全名"——报告 match_rule
// 时要说实话（§4.1 的 match_rule 是给用户判断可信度用的）。
type indexHit struct {
	idx     int
	aliased bool
}

// Catalog 是一次抓取结果建成的内存索引。查询只读它，不再碰网络。
type Catalog struct {
	Source    string
	FetchedAt time.Time
	// EntryCount 是条数，用于日志与测试断言。
	EntryCount int

	entries []Entry
	// exact / lower 都是 provider -> 模型 id -> 命中。分两张表是为了让
	// match_rule 说实话：走 exact 表命中的叫「精确」，走 lower 表命中的才叫
	// 「忽略大小写」。合并成一张会把大小写差异报成精确匹配。
	exact map[string]map[string]indexHit
	lower map[string]map[string]indexHit
	// byModel 是**跨上游**的索引，键是完整归一化后的模型 id（canonicalID）。
	// 只有在已对齐的上游里找不到时才查它，用来产出候选而非可填写值（§3.4.2）。
	byModel map[string][]int
	// providerNames 是上游 id -> 显示名。
	providerNames map[string]string
	// providerByURL / providerByHost 由 models.dev 的 api 字段建出来。
	// LiteLLM 没有这个字段，所以那两处索引对它恒为空——它的上游对齐
	// 只能靠静态别名表与类型兜底。
	providerByURL  map[string]string
	providerByHost map[string]string
	// providerStyle 是上游 id -> llmio 协议风格，只用于跨上游候选的排序。
	// 折不出来就没有这个键（不是空串键）。
	providerStyle map[string]string
}

// ProviderInfo 是源在模型条目之外提供的上游信息。
type ProviderInfo struct {
	API string // base_url
	Npm string // SDK 包名，用来折出协议风格
}

// Source 一个可查询的模型元数据来源。
type Source interface {
	Name() string
	Fetch(ctx context.Context) (*Catalog, error)
}

// buildCatalog 把条目建成索引。所有 Source 都走这一个入口，
// 免得两个解析器各建一套索引、各有一套边界行为。
//
// NewCatalog 从一组条目建索引。
//
// 它同时是两个 Source 的内部入口与对外入口：解析器调它，测试与将来
// "把快照编进二进制"（§7 的离线种子）也调它——两条路建出来的必须**是同一份**
// 索引，否则测过的行为与线上跑的就不是一回事。
func NewCatalog(source string, fetchedAt time.Time, entries []Entry, infos map[string]ProviderInfo) *Catalog {
	return buildCatalog(source, fetchedAt, entries, infos)
}

// infos 是"上游 id -> 它的 base_url 与 SDK 包名"，只有 models.dev 提供。
func buildCatalog(source string, fetchedAt time.Time, entries []Entry, infos map[string]ProviderInfo) *Catalog {
	c := &Catalog{
		Source:         source,
		FetchedAt:      fetchedAt,
		EntryCount:     len(entries),
		entries:        entries,
		exact:          make(map[string]map[string]indexHit, 64),
		lower:          make(map[string]map[string]indexHit, 64),
		byModel:        make(map[string][]int, len(entries)),
		providerNames:  make(map[string]string, 64),
		providerByURL:  make(map[string]string, 64),
		providerByHost: make(map[string]string, 64),
		providerStyle:  make(map[string]string, 64),
	}
	for i, e := range entries {
		hit := indexHit{idx: i}
		if c.exact[e.Provider] == nil {
			c.exact[e.Provider] = make(map[string]indexHit)
			c.lower[e.Provider] = make(map[string]indexHit)
		}
		// 全名优先：先写进索引的不会被别名顶掉。
		putIfAbsent(c.exact[e.Provider], e.Model, hit)
		putIfAbsent(c.lower[e.Provider], lowerID(e.Model), hit)

		canon := canonicalID(e.Model)
		c.byModel[canon] = append(c.byModel[canon], i)

		// 路径尾名别名。LiteLLM 的键带 `<上游>/` 命名空间（zai 的 16 条全是
		// `zai/glm-5` 这种形态），而用户在 llmio 里填的是上游自己的名字
		// （`glm-5`）。不做这层别名，LiteLLM 对这批上游一条也匹配不上。
		if tail := pathTail(e.Model); tail != e.Model {
			alias := indexHit{idx: i, aliased: true}
			putIfAbsent(c.exact[e.Provider], tail, alias)
			putIfAbsent(c.lower[e.Provider], lowerID(tail), alias)
			tailCanon := canonicalID(tail)
			if tailCanon != canon {
				c.byModel[tailCanon] = append(c.byModel[tailCanon], i)
			}
		}

		if e.ProviderName != "" {
			if _, ok := c.providerNames[e.Provider]; !ok {
				c.providerNames[e.Provider] = e.ProviderName
			}
		}
	}
	for id, info := range infos {
		if info.API != "" {
			// 两张表都用 preferProvider 选赢家。info 是从 map 里迭代出来的，
			// 顺序随机——用"先写的赢"会让同一个主机 / 同一个 URL 上的多个上游
			// 每次启动换一个人，而实测就是有三对这样的（zhipuai / zai /
			// volcengine 各带一个 -coding-plan 变体）。
			if u := normalizeURL(info.API); u != "" && preferProvider(c.providerByURL[u], id) {
				c.providerByURL[u] = id
			}
			if h := hostOf(info.API); h != "" && preferProvider(c.providerByHost[h], id) {
				c.providerByHost[h] = id
			}
		}
		if s := npmStyle(info.Npm); s != "" {
			c.providerStyle[id] = s
		}
	}
	return c
}

func putIfAbsent(m map[string]indexHit, key string, hit indexHit) {
	if key == "" {
		return
	}
	if _, ok := m[key]; !ok {
		m[key] = hit
	}
}

// preferProvider 决定同一主机挂多个上游时选谁。
//
// 实测会撞上的形态是"基名 + 变体"：open.bigmodel.cn 同时挂在 zhipuai 与
// zhipuai-coding-plan 下，api.z.ai 同时挂在 zai 与 zai-coding-plan 下，
// ark.cn-beijing.volces.com 同时挂在 volcengine 与 volcengine-coding-plan 下。
// 变体的 id 总是以基名开头并且更长，所以"短的优先、同长按字典序"这条规则
// 在这三种情形下都选中基名，而且是确定的（不依赖 map 迭代顺序）。
func preferProvider(current, candidate string) bool {
	if candidate == "" {
		return false
	}
	if current == "" {
		return true
	}
	if len(candidate) != len(current) {
		return len(candidate) < len(current)
	}
	return candidate < current
}

// providerName 返回上游显示名，没有就用 id 顶替（LiteLLM 没有显示名，
// 而提示文案里必须写出"是哪个上游"，空字符串会让那句话读不通）。
func (c *Catalog) providerName(id string) string {
	if n, ok := c.providerNames[id]; ok && n != "" {
		return n
	}
	return id
}

// Manager 按来源分别缓存抓取结果，并保证同一次抓取不会并发跑两遍。
type Manager struct {
	sources map[string]Source
	ttl     time.Duration
	now     func() time.Time

	mu       sync.Mutex
	cache    map[string]*Catalog
	inflight map[string]bool
	lastErr  map[string]error
}

// NewManager 建一个管理器。不传 source 时用默认的两个。
func NewManager(sources ...Source) *Manager {
	if len(sources) == 0 {
		client := defaultHTTPClient()
		sources = []Source{newModelsDevSource(client), newLiteLLMSource(client)}
	}
	m := &Manager{
		sources:  make(map[string]Source, len(sources)),
		ttl:      DefaultTTL,
		now:      time.Now,
		cache:    make(map[string]*Catalog, len(sources)),
		inflight: make(map[string]bool, len(sources)),
		lastErr:  make(map[string]error, len(sources)),
	}
	for _, s := range sources {
		m.sources[s.Name()] = s
	}
	return m
}

var (
	defaultOnce    sync.Once
	defaultManager *Manager
)

// Default 返回进程级的管理器。抓取结果在进程内共享，不随请求走。
func Default() *Manager {
	defaultOnce.Do(func() { defaultManager = NewManager() })
	return defaultManager
}

// Catalog 取一个来源的索引。
//
// 三条规则，分别对应三种真实处境（§3.2）：
//
//   - 缓存新鲜 —— 直接用。
//   - 缓存陈旧 —— **先返回陈旧的，同时在后台刷新**。绝不因为一次网络抖动
//     或一次 TTL 过期让用户看到空建议。
//   - 没有缓存 —— 起后台抓取并返回 ErrCatalogPreparing。5.3 MB 的下载不能
//     挂在一个弹窗的请求上，前端拿到这个错误就等一会儿重试。
func (m *Manager) Catalog(ctx context.Context, name string) (*Catalog, error) {
	if _, ok := m.sources[name]; !ok {
		return nil, ErrUnknownSource
	}

	m.mu.Lock()
	cached := m.cache[name]
	m.mu.Unlock()

	if cached != nil && m.now().Sub(cached.FetchedAt) < m.ttl {
		return cached, nil
	}

	m.kick(name)

	if cached != nil {
		return cached, nil
	}
	return nil, ErrCatalogPreparing
}

// Refresh 同步抓一次并写进缓存。抓失败时**保留**上一次成功的缓存并记下错误。
func (m *Manager) Refresh(ctx context.Context, name string) (*Catalog, error) {
	src, ok := m.sources[name]
	if !ok {
		return nil, ErrUnknownSource
	}

	cat, err := src.Fetch(ctx)
	if err != nil {
		m.mu.Lock()
		m.lastErr[name] = err
		stale := m.cache[name]
		m.mu.Unlock()
		if stale != nil {
			slog.Warn("modelmeta: 抓取失败，沿用上次成功的缓存",
				"source", name, "cached_at", stale.FetchedAt, "error", err)
		}
		return stale, err
	}

	m.mu.Lock()
	m.cache[name] = cat
	delete(m.lastErr, name)
	m.mu.Unlock()
	return cat, nil
}

// kick 起一次后台刷新。已经在跑就不重复起。
//
// 用 context.WithoutCancel：请求结束了抓取还要继续——它换来的是一份
// 全进程共享的缓存，不该跟着某一个弹窗的请求一起被取消。
func (m *Manager) kick(name string) {
	m.mu.Lock()
	if m.inflight[name] {
		m.mu.Unlock()
		return
	}
	m.inflight[name] = true
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			m.inflight[name] = false
			m.mu.Unlock()
		}()
		if _, err := m.Refresh(context.WithoutCancel(context.Background()), name); err != nil {
			slog.Warn("modelmeta: 后台抓取失败", "source", name, "error", err)
		}
	}()
}

// Invalidate 丢掉某个来源的缓存，下一次查询会重新抓。
func (m *Manager) Invalidate(name string) {
	m.mu.Lock()
	delete(m.cache, name)
	m.mu.Unlock()
}

// SetCatalog 直接把一份索引装进缓存，跳过网络。
//
// 两个用处：测试里装一份不联网的索引；以及将来把快照编进二进制当离线种子
// （§7）。走的是与真抓取**同一个**缓存槽，所以后续的 TTL 判定、后台刷新
// 对它一视同仁。
func (m *Manager) SetCatalog(cat *Catalog) {
	if cat == nil {
		return
	}
	m.mu.Lock()
	m.cache[cat.Source] = cat
	m.mu.Unlock()
}
