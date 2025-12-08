package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
)

func ChatCompletionsHandler(c *gin.Context) {
	chatHandler(c, service.BeforerOpenAI, service.ProcesserOpenAI, consts.StyleOpenAI)
}

func ResponsesHandler(c *gin.Context) {
	chatHandler(c, service.BeforerOpenAIRes, service.ProcesserOpenAiRes, consts.StyleOpenAIRes)
}

func Messages(c *gin.Context) {
	chatHandler(c, service.BeforerAnthropic, service.ProcesserAnthropic, consts.StyleAnthropic)
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
		common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden, "auth key has no permission to use this model")
		return
	}
	// 按模型获取可用 provider
	providersWithMeta, err := service.ProvidersWithMetaBymodelsName(ctx, style, *before)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	startReq := time.Now()
	// 调用负载均衡后的 provider 并转发
	res, logId, err := service.BalanceChat(ctx, startReq, style, *before, *providersWithMeta, models.ReqMeta{
		Header:    c.Request.Header,
		RemoteIP:  c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
	})
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	defer res.Body.Close()

	pr, pw := io.Pipe()
	tee := io.TeeReader(res.Body, pw)
	// 异步处理输出并记录 tokens
	go service.RecordLog(context.Background(), startReq, pr, postProcessor, logId, *before, providersWithMeta.IOLog)

	writeHeader(c, before.Stream, res.Header)
	if _, err := io.Copy(c.Writer, tee); err != nil {
		pw.CloseWithError(err)
		common.InternalServerError(c, err.Error())
		return
	}

	pw.Close()
}

func writeHeader(c *gin.Context, stream bool, header http.Header) {
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
