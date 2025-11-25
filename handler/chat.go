package handler

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ModelsHandler 列出当前可用模型，直接从数据库读取基础信息并按 OpenAI 协议返回。
func ModelsHandler(c *gin.Context) {
	llmModels, err := gorm.G[models.Model](models.DB).Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	models := make([]providers.Model, 0)
	for _, llmModel := range llmModels {
		models = append(models, providers.Model{
			ID:      llmModel.Name,
			Object:  "model",
			Created: llmModel.CreatedAt.Unix(),
			OwnedBy: "llmio",
		})
	}

	common.SuccessRaw(c, providers.ModelList{
		Object: "list",
		Data:   models,
	})
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
	// 按模型获取可用 provider
	ctx := c.Request.Context()
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
