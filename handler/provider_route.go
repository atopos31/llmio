package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/pkg/token"
	"github.com/atopos31/llmio/providers"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
)

// maxForwardIOBytes 透传日志单侧（请求体/响应体）最多记录的字节数。
// 透传接口不限制内容——可能是几 MB 的音频上传，也可能是很长的 SSE 流；日志是
// 排查用的，不该把库撑大，超出部分截断（前缀通常已足够看清请求与响应形态）。
const maxForwardIOBytes = 1 << 20

// 提供商专属地址：/p/{slug}/*path。
//
// slug 是 models.Provider.ForwardSlug，与提供商名称解耦：改名不影响已发布的地址，
// 必须通过 common.IsSafeForwardSlug 校验；没有配置 slug 的提供商不提供专属地址。
//
// 未知路径的三种处置（models.Provider.ForwardMode）——三种都只作用于专属地址，
// 全局 /v1 的未知路径永远 404，不透传：
//   - 关闭（nil，默认）：未知路径 404，专属地址上仅已知端点可用；
//   - 混合（false）：已知端点（/v1/chat/completions 等）直调现有 handler，
//     行为与全局 /v1 完全一致——全池负载均衡、可容错，URL 指名的提供商不参与
//     路由；其余未知路径透传给 URL 指名的提供商。
//   - 纯转发（true）：所有路径与方法直转给该提供商，客户端直接使用上游原生模型名；
//     转发路径仍按 base_url 惯例归一化（见 providers.NormalizeForwardPath）。
//
// 模型改写策略（models.Provider.ForwardRewrite，混合未知路径与纯转发共用）：
//   - none：原样转发；
//   - known：该模型在本提供商有启用中的绑定时，改写为绑定上的上游模型名；
//   - all：无条件改写为 ForwardTargetModel（Gemini 风格路径 /models/{id} 一并改写）。
//
// 权限控制：已知端点沿用 chatHandler 内的模型级校验；未知路径带 model 的同样
// 按 model 校验；无法定位模型的请求（无 model 或纯转发模式）要求全量权限
// （管理 TOKEN 或 AllowAll 的访问密钥），受限密钥一律 403——专属地址不允许
// 成为绕过模型权限的后门。all 改写会绕过"客户端模型名"这一鉴权维度，因此
// 无论是否带 model 都要求全量权限。
func ProviderDedicatedRoute(c *gin.Context) {
	slug := c.Param("slug")
	if !isSafeProviderSegment(slug) {
		// 专属地址是客户端直连的 API 面，必须返回真实 HTTP 状态码
		// （common.NotFound 是管理接口的 200+code 约定，不适用）。
		common.ErrorWithHttpStatus(c, http.StatusNotFound, http.StatusNotFound, "Provider not found")
		return
	}

	provider, err := gorm.G[models.Provider](models.DB).Where("forward_slug = ?", slug).First(c.Request.Context())
	if err != nil {
		common.ErrorWithHttpStatus(c, http.StatusNotFound, http.StatusNotFound, "Provider not found")
		return
	}

	path := c.Param("path")

	if provider.ForwardMode != nil && *provider.ForwardMode {
		forwardAll(c, provider, path)
		return
	}

	switch path {
	case "/v1/chat/completions":
		ChatCompletionsHandler(c)
	case "/v1/responses":
		ResponsesHandler(c)
	case "/v1/messages":
		Messages(c)
	case "/v1/messages/count_tokens":
		CountTokens(c)
	case "/v1/models":
		OpenAIModelsHandler(c)
	default:
		// ForwardMode == nil：转发关闭，未知路径与全局 /v1 同语义 404。
		if provider.ForwardMode == nil {
			common.ErrorWithHttpStatus(c, http.StatusNotFound, http.StatusNotFound, "Not found")
			return
		}
		passthroughUnknown(c, provider, path)
	}
}

// passthroughUnknown 混合模式下的未知路径：透传给 URL 指名的提供商。
//
// 转发路径经 providers.NormalizeForwardPath 归一化：提供商配置的 base_url 自带
// 版本段（模板里是 .../v1），客户端写法是 /p/{slug}/v1/...，剥掉开头一级 /v1 才不会
// 拼出 /v1/v1/...；只认完整段，/v1beta/... 原样保留。其余前缀原样拼接。
// 客户端 Content-Type 原样透传：multipart 上传（audio 转写等）的 boundary 不能丢。
func passthroughUnknown(c *gin.Context, provider models.Provider, path string) {
	switch c.Request.Method {
	case http.MethodGet, http.MethodPost:
	default:
		common.ErrorWithHttpStatus(c, http.StatusMethodNotAllowed, http.StatusMethodNotAllowed,
			"passthrough only supports GET and POST")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	c.Request.Body.Close()

	ctx := c.Request.Context()
	model := gjson.GetBytes(body, "model").String()

	// 权限控制：能定位模型的按模型校验，定位不了的按全量权限校验；
	// all 改写与请求里的模型名无关，客户端模型不再是可鉴权的维度。
	if forwardRewritePolicy(provider) == consts.ForwardRewriteAll {
		if !allowAllModels(ctx) {
			common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden,
				"forward rewrite requires a token with full model permission")
			return
		}
	} else if !authorizeForwardModel(c, model) {
		return
	}

	// 原始请求体单独留一份给日志：known/all 会改写 body 里的 model，但记录里应当
	// 是用户真正发来的内容；被改写成什么由 log.ProviderModel 体现。
	ioBody := append([]byte(nil), body...)
	forwardPath, upstreamModel, rewriteErr := rewriteForwardModel(ctx, provider, path, model, &body)
	if rewriteErr != nil {
		common.InternalServerError(c, rewriteErr.Error())
		return
	}
	forwardUpstream(c, provider, model, upstreamModel, body, ioBody, forwardPath)
}

// forwardAll 纯转发模式：所有路径与方法直转给该提供商。
// 路径仍按 base_url 惯例归一化（providers.NormalizeForwardPath）：客户端 base_url
// 同样是专属地址 /p/{slug}/v1，不剥掉开头一级 /v1 就会拼出 /v1/v1/...。
func forwardAll(c *gin.Context, provider models.Provider, path string) {
	ctx := c.Request.Context()
	// 无模型语义，无法按模型鉴权，因此要求全量权限。
	if !allowAllModels(ctx) {
		common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden,
			"forward mode requires a token with full model permission")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	c.Request.Body.Close()

	model := gjson.GetBytes(body, "model").String()
	ioBody := append([]byte(nil), body...)
	forwardPath, upstreamModel, err := rewriteForwardModel(ctx, provider, path, model, &body)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	forwardUpstream(c, provider, model, upstreamModel, body, ioBody, forwardPath)
}

// forwardUpstream 完成一次透传：构造上游目标、归一化并校验路径、发请求、中继响应，
// 最后落一条 ChatLog（ioLog 打开时再写 ChatIO）。
//
// 混合模式的未知路径与纯转发共用这里——两者的差别只在鉴权与是否改写，转发、
// 日志、IO 记录的语义完全一样；分成两份迟早会长出两种口径。
// ioBody 是改写前的客户端原始请求体，只用于 ChatIO 记录。
func forwardUpstream(c *gin.Context, provider models.Provider, model, upstreamModel string, body, ioBody []byte, forwardPath string) {
	ctx := c.Request.Context()

	target, err := providers.ForwardTargetFromConfig(provider.Type, provider.Config, provider.Proxy)
	if err != nil {
		common.InternalServerError(c, "invalid provider config: "+err.Error())
		return
	}
	// 剥掉与上游 base_url 版本段重复的 /v1，规则见 providers.NormalizeForwardPath。
	forwardPath = providers.NormalizeForwardPath(forwardPath)
	if _, err := providers.SanitizeForwardPath(forwardPath); err != nil {
		common.ErrorWithHttpStatus(c, http.StatusBadRequest, http.StatusBadRequest, err.Error())
		return
	}

	// ioLog 决定这次要不要写 ChatIO。请求体超限时整条 IO 都不写，并让 ChatIO
	// 标志保持 false——前端按这个标志决定要不要跳 IO 详情，标志为真而行不存在
	// 会得到一张空页面。
	ioLog := shouldRecordForwardIO(ctx) && len(ioBody) <= maxForwardIOBytes

	start := time.Now()
	res, err := providers.Forward(ctx, target, c.Request.Method, forwardPath,
		c.Request.URL.Query(), c.GetHeader("Accept"), c.GetHeader("Content-Type"), body)
	log := newPassthroughLog(c, provider, model, upstreamModel, ioLog, start)
	if err != nil {
		savePassthroughLog(log.WithError(err))
		common.ErrorWithHttpStatus(c, http.StatusBadGateway, http.StatusBadGateway,
			"upstream request failed: "+err.Error())
		return
	}
	defer res.Body.Close()

	captureLimit := 0
	if ioLog {
		captureLimit = maxForwardIOBytes
	}
	relay := relayForwardResponse(c, res, start, captureLimit)

	log.ProxyTime = time.Since(start)
	log.Size = relay.Size
	log.FirstChunkTime = relay.FirstChunkTime
	if log.FirstChunkTime > 0 && log.ProxyTime > log.FirstChunkTime {
		log.ChunkTime = log.ProxyTime - log.FirstChunkTime
	}
	if res.StatusCode >= http.StatusBadRequest {
		log = log.WithError(fmt.Errorf("upstream status: %d", res.StatusCode))
	} else {
		log.Status = consts.StatusSuccess
	}
	if relay.Err != nil {
		log = log.WithError(relay.Err)
	}
	// 透传接口可能返回任何东西：只有响应里确实找得到用量时才填，找不到保持 0。
	if usage, ok := forwardResponseUsage(provider.Type, res.Header.Get("Content-Type"), relay.Captured); ok {
		log.Usage = usage
		if usage.CompletionTokens > 0 && log.ProxyTime > 0 {
			log.Tps = float64(usage.CompletionTokens) / log.ProxyTime.Seconds()
		}
	}
	logID := savePassthroughLog(log)
	if ioLog {
		savePassthroughIO(logID, ioBody, forwardOutputUnion(res.Header.Get("Content-Type"), relay.Captured))
	}
}

// forwardRewritePolicy 归一化改写策略：空串（升级前的老行）按 known 处理。
func forwardRewritePolicy(provider models.Provider) string {
	switch provider.ForwardRewrite {
	case consts.ForwardRewriteNone, consts.ForwardRewriteAll:
		return provider.ForwardRewrite
	default:
		return consts.ForwardRewriteKnown
	}
}

// authorizeForwardModel 未知路径的模型级鉴权：能定位模型的按模型校验，
// 定位不了的（无 model）要求全量权限。返回 false 时响应已写好。
func authorizeForwardModel(c *gin.Context, model string) bool {
	ctx := c.Request.Context()
	if model != "" {
		valid, err := validateAuthKey(ctx, model)
		if err != nil {
			common.InternalServerError(c, err.Error())
			return false
		}
		if !valid {
			common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden,
				fmt.Sprintf("auth key has no permission to use %s", model))
			return false
		}
		return true
	}
	if !allowAllModels(ctx) {
		common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden,
			"auth key has no permission to use passthrough endpoints without a model")
		return false
	}
	return true
}

// rewriteForwardModel 按提供商的改写策略改写请求体中的 model 与 Gemini 风格路径，
// 返回 (转发路径, 实际上游模型名, 错误)。不负责鉴权；all 缺目标模型时返回错误。
func rewriteForwardModel(ctx context.Context, provider models.Provider, path, model string, body *[]byte) (string, string, error) {
	switch forwardRewritePolicy(provider) {
	case consts.ForwardRewriteNone:
		return path, model, nil
	case consts.ForwardRewriteAll:
		target := strings.TrimSpace(provider.ForwardTargetModel)
		if target == "" {
			return path, model, fmt.Errorf("forward target model is not configured")
		}
		upstreamModel := model
		// body 里没有 model（或不是合法 JSON）就不动 body；只有模型名走 URL 的
		// Gemini generateContent 仍有改写余地，见下面的路径改写。
		if model != "" && gjson.ValidBytes(*body) {
			if rewritten, err := sjson.SetBytes(*body, "model", target); err == nil {
				*body = rewritten
				upstreamModel = target
			}
		}
		if rewrittenPath, ok := rewriteModelInForwardPath(path, target); ok {
			path = rewrittenPath
			upstreamModel = target
		}
		return path, upstreamModel, nil
	default: // known
		upstreamModel := model
		if model != "" {
			if rewritten, ok := rewriteModelForProvider(ctx, model, provider.ID, body); ok {
				upstreamModel = rewritten
			}
		}
		return path, upstreamModel, nil
	}
}

// forwardModelPathPattern 匹配 Gemini 风格的 /models/{model}:action。
var forwardModelPathPattern = regexp.MustCompile(`/models/([^/:]+)(.*)$`)

// rewriteModelInForwardPath 把 Gemini 风格路径里的模型名替换为 target。
// target 含 /、:、空白时不改写（无法安全地塞进路径），返回 (原路径, false)。
func rewriteModelInForwardPath(path, target string) (string, bool) {
	if target == "" || strings.ContainsAny(target, "/: \t\r\n") {
		return path, false
	}
	loc := forwardModelPathPattern.FindStringSubmatchIndex(path)
	if loc == nil {
		return path, false
	}
	if path[loc[2]:loc[3]] == target {
		return path, false
	}
	// loc[0] 是 "/models/" 起点，loc[4] 是路径里模型名之后的部分（":action" 等）。
	return path[:loc[0]] + "/models/" + target + path[loc[4]:], true
}

// rewriteModelForProvider 把请求模型名改写为该提供商绑定上的上游模型名。
// 返回 (上游模型名, 是否改写)。
func rewriteModelForProvider(ctx context.Context, model string, providerID uint, body *[]byte) (string, bool) {
	m, err := gorm.G[models.Model](models.DB).Where("name = ?", model).First(ctx)
	if err != nil {
		return "", false
	}
	assoc, err := gorm.G[models.ModelWithProvider](models.DB).
		Where("model_id = ? AND provider_id = ?", m.ID, providerID).First(ctx)
	if err != nil {
		return "", false
	}
	if assoc.Status != nil && !*assoc.Status {
		return "", false
	}
	if assoc.ProviderModel == "" {
		return model, false
	}
	rewritten, err := sjson.SetBytes(*body, "model", assoc.ProviderModel)
	if err != nil {
		return "", false
	}
	*body = rewritten
	return assoc.ProviderModel, true
}

// allowAllModels 当前请求是否具备全量模型权限（管理 TOKEN 或 AllowAll 密钥）。
func allowAllModels(ctx context.Context) bool {
	allowAll, _ := ctx.Value(consts.ContextKeyAllowAllModel).(bool)
	return allowAll
}

// forwardRelay 是响应中继的产出：透传日志的耗时、大小与 IO 记录都取自这里。
type forwardRelay struct {
	Size           int
	FirstChunkTime time.Duration
	Captured       []byte
	Err            error
}

// responseCapture 边回传边记录。首字节耗时总是记（与是否记录 body 无关），
// 响应体只在 limit > 0 时保留前 limit 字节。Write 永远返回成功：抓日志是附带
// 行为，抓取出问题绝不能反过来打断转发。
type responseCapture struct {
	buf      bytes.Buffer
	limit    int
	start    time.Time
	first    time.Duration
	gotFirst bool
}

func (rc *responseCapture) Write(p []byte) (int, error) {
	if !rc.gotFirst && len(p) > 0 {
		rc.gotFirst = true
		rc.first = time.Since(rc.start)
	}
	if rc.limit > 0 {
		if remaining := rc.limit - rc.buf.Len(); remaining > 0 {
			if len(p) <= remaining {
				rc.buf.Write(p)
			} else {
				rc.buf.Write(p[:remaining])
			}
		}
	}
	return len(p), nil
}

// relayForwardResponse 把上游响应原样回传给客户端：剔除 hop-by-hop 头与
// Content-Length（流式拷贝由 net/http 自行分帧），逐块 Flush 保证 SSE 实时性。
// captureLimit > 0 时顺手抓取响应体的前 captureLimit 字节，供透传日志的 IO 记录。
func relayForwardResponse(c *gin.Context, res *http.Response, start time.Time, captureLimit int) forwardRelay {
	for key, values := range res.Header {
		if isHopByHopHeader(key) || key == "Content-Length" {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}
	c.Writer.WriteHeader(res.StatusCode)

	capture := &responseCapture{limit: captureLimit, start: start}
	writer := &flushWriter{w: c.Writer}
	size, err := io.Copy(io.MultiWriter(writer, capture), res.Body)
	return forwardRelay{
		Size:           int(size),
		FirstChunkTime: capture.first,
		Captured:       capture.buf.Bytes(),
		Err:            err,
	}
}

func isHopByHopHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	}
	return false
}

// newPassthroughLog 组一条透传日志。与聊天日志的差别是：没有协议处理、没有
// 重试链，但 TraceID / UA / IP / 请求路径这些排查字段必须与聊天日志一致——
// 客户端拿 trace 反馈问题时，日志页得能按同一个 trace 找到它。
func newPassthroughLog(c *gin.Context, provider models.Provider, model, upstreamModel string, ioLog bool, start time.Time) models.ChatLog {
	authKeyID, _ := c.Request.Context().Value(consts.ContextKeyAuthKeyID).(uint)
	// 生成失败只影响可读性，trace 置空即可；日志本身仍然要落。
	traceID, _ := token.GenerateRandomChars(10)
	return models.ChatLog{
		Name:          model,
		TraceID:       traceID,
		ProviderModel: upstreamModel,
		ProviderName:  provider.Name,
		Status:        consts.StatusError,
		Style:         consts.StylePassthrough,
		UpstreamStyle: provider.Type,
		UserAgent:     c.Request.UserAgent(),
		RemoteIP:      c.ClientIP(),
		AuthKeyID:     authKeyID,
		ChatIO:        ioLog,
		RequestPath:   forwardRequestPath(c),
		ProxyTime:     time.Since(start),
	}
}

// forwardRequestPath 只保留方法与路径，不记查询串：有的客户端把密钥放在
// query 里，日志不该把它抄一份。
func forwardRequestPath(c *gin.Context) string {
	return c.Request.Method + " " + c.Request.URL.Path
}

// savePassthroughLog 落库并返回日志 ID（IO 行要用它关联）。用脱离请求生命周期的
// ctx：客户端断连（流式里很常见）会取消请求 ctx，而断连现场恰是 client-cancel、
// 上游断流这些最需要日志的时候，不能跟着请求一起丢。
func savePassthroughLog(log models.ChatLog) uint {
	saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logID, err := service.SaveChatLog(saveCtx, log)
	if err != nil {
		slog.Error("save passthrough log", "err", err)
		return 0
	}
	return logID
}

// shouldRecordForwardIO 透传请求是否记录请求/响应体：管理 TOKEN 没有访问密钥
// 配置（AuthKeyID 为 0），默认记录；访问密钥按自身的 ioLog 开关（middleware 已
// 把它放进 ctx）。
func shouldRecordForwardIO(ctx context.Context) bool {
	authKeyID, _ := ctx.Value(consts.ContextKeyAuthKeyID).(uint)
	if authKeyID == 0 {
		return allowAllModels(ctx)
	}
	ioLog, _ := ctx.Value(consts.ContextKeyAuthKeyIOLog).(bool)
	return ioLog
}

// savePassthroughIO 写透传的 ChatIO 行（请求体 + 响应体）。写入失败只记错误，
// 不影响已经回给客户端的响应。
func savePassthroughIO(logID uint, body []byte, output *models.OutputUnion) {
	if logID == 0 {
		return
	}
	saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := service.SaveChatIO(saveCtx, logID, body, output); err != nil {
		slog.Error("save passthrough chat io", "log_id", logID, "err", err)
	}
}

// savePassthroughLog 落库。用脱离请求生命周期的 ctx：客户端断连（流式里很常见）
// 会取消请求 ctx，而断连现场恰是 client-cancel、上游断流这些最需要日志的时候，
// 不能跟着请求一起丢。authKeyID 等取值已在 newPassthroughLog 里从请求 ctx 完成后才到这里。
// forwardResponseUsage 从上游响应里抽取 token 用量。透传接口不知道响应属于哪种
// 协议，只能按 provider.Type（客户端请求的上游协议）解析；响应是 SSE 时逐条
// data 负载合并——OpenAI 在最后一条 chunk 给 usage，Anthropic 的输入在
// message_start、输出在 message_delta，所以必须按字段取最大值而不是只看最后一条。
func forwardResponseUsage(providerType, contentType string, captured []byte) (models.Usage, bool) {
	if len(captured) == 0 {
		return models.Usage{}, false
	}
	payloads := [][]byte{captured}
	if isSSEContentType(contentType) {
		if parsed := forwardSSEPayloads(captured); len(parsed) > 0 {
			payloads = parsed
		}
	}
	var usage models.Usage
	found := false
	for _, payload := range payloads {
		u, ok := parseForwardUsage(providerType, payload)
		if !ok {
			continue
		}
		found = true
		mergeForwardUsage(&usage, u)
	}
	if !found {
		return models.Usage{}, false
	}
	// Anthropic 只给分项、不给总数；其余协议的 total 偶尔也会缺。
	if usage.TotalTokens < usage.PromptTokens+usage.CompletionTokens {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage, true
}

// mergeForwardUsage 合并两次用量。流式响应里的各字段是累计值，取最大值；
// 只看最后一条会丢掉只在首个事件里出现过的输入 token。
func mergeForwardUsage(dst *models.Usage, src models.Usage) {
	if src.PromptTokens > dst.PromptTokens {
		dst.PromptTokens = src.PromptTokens
	}
	if src.CompletionTokens > dst.CompletionTokens {
		dst.CompletionTokens = src.CompletionTokens
	}
	if src.TotalTokens > dst.TotalTokens {
		dst.TotalTokens = src.TotalTokens
	}
	if src.PromptTokensDetails.CachedTokens > dst.PromptTokensDetails.CachedTokens {
		dst.PromptTokensDetails.CachedTokens = src.PromptTokensDetails.CachedTokens
	}
	if src.PromptTokensDetails.AudioTokens > dst.PromptTokensDetails.AudioTokens {
		dst.PromptTokensDetails.AudioTokens = src.PromptTokensDetails.AudioTokens
	}
}

// parseForwardUsage 按上游协议解析一条响应负载里的用量。
func parseForwardUsage(providerType string, payload []byte) (models.Usage, bool) {
	switch providerType {
	case consts.StyleAnthropic:
		usage := gjson.GetBytes(payload, "usage")
		if !usage.Exists() {
			return models.Usage{}, false
		}
		prompt := usage.Get("input_tokens").Int() +
			usage.Get("cache_creation_input_tokens").Int() +
			usage.Get("cache_read_input_tokens").Int()
		completion := usage.Get("output_tokens").Int()
		return models.Usage{
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
			PromptTokensDetails: models.PromptTokensDetails{
				CachedTokens: usage.Get("cache_read_input_tokens").Int(),
			},
		}, true
	case consts.StyleGemini:
		usage := gjson.GetBytes(payload, "usageMetadata")
		if !usage.Exists() {
			return models.Usage{}, false
		}
		return models.Usage{
			PromptTokens:     usage.Get("promptTokenCount").Int(),
			CompletionTokens: usage.Get("candidatesTokenCount").Int(),
			TotalTokens:      usage.Get("totalTokenCount").Int(),
			PromptTokensDetails: models.PromptTokensDetails{
				CachedTokens: usage.Get("cachedContentTokenCount").Int(),
			},
		}, true
	case consts.StyleOpenAIRes:
		usage := gjson.GetBytes(payload, "usage")
		if !usage.Exists() {
			return models.Usage{}, false
		}
		// Responses API 的字段；也兼容把 openai-res 响应写成 chat 形态的网关。
		if usage.Get("input_tokens").Exists() || usage.Get("output_tokens").Exists() {
			prompt := usage.Get("input_tokens").Int()
			completion := usage.Get("output_tokens").Int()
			return models.Usage{
				PromptTokens:     prompt,
				CompletionTokens: completion,
				TotalTokens:      usage.Get("total_tokens").Int(),
				PromptTokensDetails: models.PromptTokensDetails{
					CachedTokens: usage.Get("input_tokens_details.cached_tokens").Int(),
				},
			}, true
		}
		return parseOpenAIUsage(payload)
	default:
		return parseOpenAIUsage(payload)
	}
}

// parseOpenAIUsage 解析 OpenAI Chat Completions 形态的 usage（字段与 models.Usage 一致）。
func parseOpenAIUsage(payload []byte) (models.Usage, bool) {
	raw := gjson.GetBytes(payload, "usage")
	if !raw.Exists() {
		return models.Usage{}, false
	}
	var usage models.Usage
	if err := json.Unmarshal([]byte(raw.Raw), &usage); err != nil {
		return models.Usage{}, false
	}
	return usage, true
}

// forwardSSEPayloads 从 SSE 原始字节里提取 data 负载，口径与 service/process.go
// 的流式解析一致：去掉 "data:" 前缀，跳过 [DONE] 与 event/id 等控制行。
func forwardSSEPayloads(captured []byte) [][]byte {
	scanner := bufio.NewScanner(bytes.NewReader(captured))
	scanner.Buffer(make([]byte, 0, 64*1024), 2*maxForwardIOBytes)
	var payloads [][]byte
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		payloads = append(payloads, []byte(data))
	}
	return payloads
}

// forwardOutputUnion 把抓到的响应体变成 ChatIO 的两种形态之一（互斥，见
// service/process.go）：SSE 拆成 data 负载数组（聊天页按流式渲染），其余文本
// 整包进 OfString。二进制不记——写进 TEXT 列既没意义又会撑大库。
func forwardOutputUnion(contentType string, captured []byte) *models.OutputUnion {
	if len(captured) == 0 || !forwardContentCapturable(contentType, captured) {
		return nil
	}
	if isSSEContentType(contentType) {
		if payloads := forwardSSEPayloads(captured); len(payloads) > 0 {
			chunks := make([]string, 0, len(payloads))
			for _, payload := range payloads {
				chunks = append(chunks, string(payload))
			}
			return &models.OutputUnion{OfStringArray: chunks}
		}
	}
	return &models.OutputUnion{OfString: string(captured)}
}

// forwardContentCapturable 只记录文本类响应：JSON、text/*、SSE；Content-Type
// 缺失或不可信时，退一步用"body 是不是合法 JSON"判断。
func forwardContentCapturable(contentType string, captured []byte) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "json") || strings.HasPrefix(ct, "text/") || strings.Contains(ct, "event-stream") {
		return true
	}
	return gjson.ValidBytes(captured)
}

func isSSEContentType(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "event-stream")
}

// isSafeProviderSegment 专属地址中的路径标识只允许 URL 安全字符，规则见
// common.IsSafeForwardSlug（首字符为字母或数字，天然排除 "." 与 ".."）。
func isSafeProviderSegment(s string) bool {
	return common.IsSafeForwardSlug(s)
}
