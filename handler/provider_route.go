package handler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
)

// 提供商专属地址：/p/{name}/*path。
//
// name 即提供商名称，必须通过 isSafeProviderSegment 校验（URL 安全字符）；
// 不含 URL 安全字符的名称（空格、中文等）不提供专属地址。
//
// 未知路径的三种处置（models.Provider.ForwardMode）——三种都只作用于专属地址，
// 全局 /v1 的未知路径永远 404，不透传：
//   - 关闭（nil，默认）：未知路径 404，专属地址上仅已知端点可用；
//   - 混合（false）：已知端点（/v1/chat/completions 等）直调现有 handler，
//     行为与全局 /v1 完全一致——全池负载均衡、可容错，URL 指名的提供商不参与
//     路由；其余未知路径透传给 URL 指名的提供商。
//   - 纯转发（true）：所有路径与方法无条件直转给该提供商，不改写模型名，客户端
//     直接使用上游原生模型名。
//
// 权限控制：已知端点沿用 chatHandler 内的模型级校验；未知路径带 model 的同样
// 按 model 校验；无法定位模型的请求（无 model 或纯转发模式）要求全量权限
// （管理 TOKEN 或 AllowAll 的访问密钥），受限密钥一律 403——专属地址不允许
// 成为绕过模型权限的后门。
func ProviderDedicatedRoute(c *gin.Context) {
	name := c.Param("name")
	if !isSafeProviderSegment(name) {
		// 专属地址是客户端直连的 API 面，必须返回真实 HTTP 状态码
		// （common.NotFound 是管理接口的 200+code 约定，不适用）。
		common.ErrorWithHttpStatus(c, http.StatusNotFound, http.StatusNotFound, "Provider not found")
		return
	}

	provider, err := gorm.G[models.Provider](models.DB).Where("name = ?", name).First(c.Request.Context())
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
// 客户端约定的 base_url 是 /p/{name}/v1（与已知端点对齐），而提供商配置的
// base_url 按惯例自带版本段（如 https://api.example.com/v1），因此转发前剥掉
// 客户端写法里的 /v1 前缀，避免拼出 /v1/v1/...。只剥到段边界：/v1beta/... 这类
// 版本变体不能被咬成 beta/...（否则 Gemini 原生路径透传直接 400）。其余前缀原样拼接。
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

	// 权限控制：能定位模型的按模型校验，定位不了的按全量权限校验。
	if model != "" {
		valid, err := validateAuthKey(ctx, model)
		if err != nil {
			common.InternalServerError(c, err.Error())
			return
		}
		if !valid {
			common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden,
				fmt.Sprintf("auth key has no permission to use %s", model))
			return
		}
	} else if !allowAllModels(ctx) {
		common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden,
			"auth key has no permission to use passthrough endpoints without a model")
		return
	}

	// 模型名改写：该模型在此提供商存在启用中的绑定时，改写为绑定上的上游模型名；
	// 未绑定的原样透传（视为上游原生模型名）。
	upstreamModel := model
	if model != "" {
		if rewritten, ok := rewriteModelForProvider(ctx, model, provider.ID, &body); ok {
			upstreamModel = rewritten
		}
	}

	target, err := providers.ForwardTargetFromConfig(provider.Type, provider.Config, provider.Proxy)
	if err != nil {
		common.InternalServerError(c, "invalid provider config: "+err.Error())
		return
	}
	// 只剥到段边界，规则见函数注释。
	forwardPath := path
	if rest, ok := strings.CutPrefix(path, "/v1"); ok && (rest == "" || rest[0] == '/') {
		forwardPath = rest
	}
	if _, err := providers.SanitizeForwardPath(forwardPath); err != nil {
		common.ErrorWithHttpStatus(c, http.StatusBadRequest, http.StatusBadRequest, err.Error())
		return
	}

	start := time.Now()
	res, err := providers.Forward(ctx, target, c.Request.Method, forwardPath, c.Request.URL.Query(),
		c.GetHeader("Accept"), c.GetHeader("Content-Type"), body)
	log := newPassthroughLog(ctx, provider, model, upstreamModel, start)
	if err != nil {
		savePassthroughLog(log.WithError(err))
		common.ErrorWithHttpStatus(c, http.StatusBadGateway, http.StatusBadGateway,
			"upstream request failed: "+err.Error())
		return
	}
	defer res.Body.Close()

	size, copyErr := relayForwardResponse(c, res)

	log.ProxyTime = time.Since(start)
	log.Size = size
	if res.StatusCode >= http.StatusBadRequest {
		log = log.WithError(fmt.Errorf("upstream status: %d", res.StatusCode))
	} else {
		log.Status = consts.StatusSuccess
	}
	if copyErr != nil {
		log = log.WithError(copyErr)
	}
	savePassthroughLog(log)
}

// forwardAll 纯转发模式：所有路径与方法无条件直转，不做任何改写。
func forwardAll(c *gin.Context, provider models.Provider, path string) {
	// 无模型语义，无法按模型鉴权，因此要求全量权限。
	if !allowAllModels(c.Request.Context()) {
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

	target, err := providers.ForwardTargetFromConfig(provider.Type, provider.Config, provider.Proxy)
	if err != nil {
		common.InternalServerError(c, "invalid provider config: "+err.Error())
		return
	}
	if _, err := providers.SanitizeForwardPath(path); err != nil {
		common.ErrorWithHttpStatus(c, http.StatusBadRequest, http.StatusBadRequest, err.Error())
		return
	}

	start := time.Now()
	res, err := providers.Forward(c.Request.Context(), target, c.Request.Method, path,
		c.Request.URL.Query(), c.GetHeader("Accept"), c.GetHeader("Content-Type"), body)
	log := newPassthroughLog(c.Request.Context(), provider, "", "", start)
	if err != nil {
		savePassthroughLog(log.WithError(err))
		common.ErrorWithHttpStatus(c, http.StatusBadGateway, http.StatusBadGateway,
			"upstream request failed: "+err.Error())
		return
	}
	defer res.Body.Close()

	size, copyErr := relayForwardResponse(c, res)

	log.ProxyTime = time.Since(start)
	log.Size = size
	if res.StatusCode >= http.StatusBadRequest {
		log = log.WithError(fmt.Errorf("upstream status: %d", res.StatusCode))
	} else {
		log.Status = consts.StatusSuccess
	}
	if copyErr != nil {
		log = log.WithError(copyErr)
	}
	savePassthroughLog(log)
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

// relayForwardResponse 把上游响应原样回传给客户端：剔除 hop-by-hop 头与
// Content-Length（流式拷贝由 net/http 自行分帧），逐块 Flush 保证 SSE 实时性。
// 返回回传的字节数。
func relayForwardResponse(c *gin.Context, res *http.Response) (int, error) {
	for key, values := range res.Header {
		if isHopByHopHeader(key) || key == "Content-Length" {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}
	c.Writer.WriteHeader(res.StatusCode)

	writer := &flushWriter{w: c.Writer}
	size, err := io.Copy(writer, res.Body)
	return int(size), err
}

func isHopByHopHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	}
	return false
}

func newPassthroughLog(ctx context.Context, provider models.Provider, model, upstreamModel string, start time.Time) models.ChatLog {
	authKeyID, _ := ctx.Value(consts.ContextKeyAuthKeyID).(uint)
	return models.ChatLog{
		Name:          model,
		ProviderModel: upstreamModel,
		ProviderName:  provider.Name,
		Status:        consts.StatusError,
		Style:         consts.StylePassthrough,
		UpstreamStyle: provider.Type,
		AuthKeyID:     authKeyID,
		ProxyTime:     time.Since(start),
	}
}

// savePassthroughLog 落库。用脱离请求生命周期的 ctx：客户端断连（流式里很常见）
// 会取消请求 ctx，而断连现场恰是 client-cancel、上游断流这些最需要日志的时候，
// 不能跟着请求一起丢。authKeyID 等取值已在 newPassthroughLog 里从请求 ctx 完成后才到这里。
func savePassthroughLog(log models.ChatLog) {
	saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := service.SaveChatLog(saveCtx, log); err != nil {
		slog.Error("save passthrough log", "err", err)
	}
}

// isSafeProviderSegment 专属地址中的提供商标识只允许 URL 安全字符：
// 首字符为字母或数字，其余为字母、数字、-、_、.，长度 1-64。
// 首字符限制天然排除了 "." 与 ".."。
func isSafeProviderSegment(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case (r == '-' || r == '_' || r == '.') && i > 0:
		default:
			return false
		}
	}
	return true
}
