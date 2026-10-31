package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
)

// flushWriter 包装 http.ResponseWriter，在每次 Write 后自动 Flush
// 用于 SSE 流式响应，确保客户端实时收到数据
type flushWriter struct {
	w http.ResponseWriter
}

func (fw *flushWriter) Write(p []byte) (n int, err error) {
	n, err = fw.w.Write(p)
	if n > 0 {
		if flusher, ok := fw.w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	return
}

func ChatCompletionsHandler(c *gin.Context) {
	chatHandler(c, service.BeforerOpenAI, service.ProcesserOpenAI, consts.StyleOpenAI)
}

func ResponsesHandler(c *gin.Context) {
	chatHandler(c, service.BeforerOpenAIRes, service.ProcesserOpenAiRes, consts.StyleOpenAIRes)
}

func Messages(c *gin.Context) {
	chatHandler(c, service.BeforerAnthropic, service.ProcesserAnthropic, consts.StyleAnthropic)
}

// GeminiGenerateContentHandler 转发 Gemini 原生接口:
// POST /v1beta/models/{model}:generateContent
func GeminiGenerateContentHandler(c *gin.Context) {
	modelAction := strings.TrimPrefix(c.Param("modelAction"), "/")
	model, method, ok := strings.Cut(modelAction, ":")
	if !ok || model == "" || method == "" {
		common.BadRequest(c, "Invalid Gemini model action")
		return
	}
	stream := false
	switch method {
	case "generateContent":
		stream = false
	case "streamGenerateContent":
		stream = true
	default:
		common.BadRequest(c, "Unsupported Gemini method: "+method)
		return
	}

	ctx := context.WithValue(c.Request.Context(), consts.ContextKeyGeminiStream, stream)
	c.Request = c.Request.WithContext(ctx)

	chatHandler(c, service.NewBeforerGemini(model, stream), service.ProcesserGemini, consts.StyleGemini)
}

func chatHandler(c *gin.Context, preProcessor service.Beforer, postProcessor service.Processer, style string) {
	// 读取原始请求体
	reqBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	c.Request.Body.Close()
	// 预处理、提取模型参数
	before, err := preProcessor(reqBody)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	ctx := c.Request.Context()
	// 校验 authKey 是否有权限使用该模型
	valid, err := validateAuthKey(ctx, before.Model)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	if !valid {
		common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden, fmt.Sprintf("auth key has no permission to use %s", before.Model))
		return
	}
	// 按模型获取可用 provider
	providersWithMeta, err := service.ProvidersWithMetaBymodelsName(ctx, style, *before)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	startReq := time.Now()
	// 协议互转的记账盒：转发路径往里写"改了什么"，日志落库时读
	bridgeNotes := service.NewBridgeNotes()
	// 调用负载均衡后的 provider 并转发
	res, log, err := service.BalanceChat(ctx, startReq, style, *before, *providersWithMeta, models.ReqMeta{
		Header:    c.Request.Header,
		RemoteIP:  c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
	}, bridgeNotes)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	defer res.Body.Close()

	logId, err := service.SaveChatLog(ctx, *log)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	pr, pw := io.Pipe()
	tee := io.TeeReader(res.Body, pw)
	// 异步处理输出并记录 tokens
	authKeyIOLog, _ := ctx.Value(consts.ContextKeyAuthKeyIOLog).(bool)
	slog.Info("start recording log", "logId", logId, "authKeyIOLog", authKeyIOLog)
	go service.RecordLog(context.Background(), startReq, pr, postProcessor, logId, *before, authKeyIOLog, bridgeNotes)

	policy := service.GetTrafficCompressionPolicy(c.Request.Context())
	gzipOn := before.Stream &&
		policy.Enabled && policy.SSEResponse &&
		res.Header.Get("Content-Encoding") == "" &&
		acceptsGzip(c.Request.Header.Get("Accept-Encoding"))
	var gzipWriter *sseGzipWriter
	if gzipOn {
		gzipWriter = newSSEGzipWriter(c.Writer)
		c.Writer = gzipWriter
		defer gzipWriter.Close()
	}
	writeHeader(c, before.Stream, gzipOn, res.Header)

	// 流式响应使用 flushWriter 确保数据实时发送；gzip 包装器已在每次 Write 后刷新
	var writer io.Writer = c.Writer
	if before.Stream && !gzipOn {
		writer = &flushWriter{w: c.Writer}
	}

	if _, err := io.Copy(writer, tee); err != nil {
		// 转发给客户端失败（多半是客户端/中间代理断开）。这里离请求 ctx 最近，
		// 由它负责打上阶段与「客户端断开」归因——错误经管道传给 RecordLog 落库，
		// 那边拿到的已是 Background ctx，补不出归因。
		pw.CloseWithError(service.MarkStage(service.StageResponseStream, c.Request.Context(), err))
		slog.Error("io copy", "err:", err)
		return
	}

	pw.Close()

	// 这次转发改过什么（丢了参数、补了默认值）在这里提示一次：客户端从响应里看不出
	// 差别，日志是唯一能对上号的地方。同协议路径没有记账，不会有这条
	if notes := bridgeNotes.String(); notes != "" {
		slog.Warn("protocol bridged",
			"traceID", log.TraceID, "from", log.Style, "to", log.UpstreamStyle, "notes", notes)
	}
}

func writeHeader(c *gin.Context, stream, gzipOn bool, header http.Header) {
	for k, values := range header {
		for _, value := range values {
			c.Writer.Header().Add(k, value)
		}
	}

	if stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
	}
	if gzipOn {
		c.Writer.Header().Del("Content-Length")
		c.Writer.Header().Set("Content-Encoding", "gzip")
		c.Writer.Header().Add("Vary", "Accept-Encoding")
	}
	c.Writer.Flush()
}

// 校验auhtKey的模型使用权限
func validateAuthKey(ctx context.Context, model string) (bool, error) {
	// 验证是否为允许全部模型
	allowAll, ok := ctx.Value(consts.ContextKeyAllowAllModel).(bool)
	if !ok {
		return false, errors.New("invalid auth key")
	}
	if allowAll {
		return true, nil
	}
	// 验证是否有权限使用该模型
	allowedModels, ok := ctx.Value(consts.ContextKeyAllowModels).([]string)
	if !ok {
		return false, errors.New("invalid auth key")
	}
	return slices.Contains(allowedModels, model), nil
}
