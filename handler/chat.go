package handler

import (
	"context"
	"io"
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
	// 步骤1：读取原始请求体
	reqBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	// 步骤1：预处理、提取模型参数
	before, err := service.BeforerOpenAI(reqBody)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	// 步骤2：按模型获取可用 provider
	ctx := c.Request.Context()
	providersWithMeta, err := service.ProvidersWithMetaBymodelsName(ctx, before.Model, consts.StyleOpenAI)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	startReq := time.Now()
	// 步骤2：调用负载均衡后的 provider 并转发
	res, logId, err := service.BalanceChat(ctx, startReq, consts.StyleOpenAI, *before, *providersWithMeta, models.ReqMeta{
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
	// 步骤3：异步处理输出并记录 tokens
	go service.RecordLog(context.Background(), startReq, pr, service.ProcesserOpenAI, logId, *before, providersWithMeta.IOLog)

	WithContentType(c, before.Stream)
	if _, err := io.Copy(c.Writer, tee); err != nil {
		pw.CloseWithError(err)
		common.InternalServerError(c, err.Error())
		return
	}

	pw.Close()
}

func ResponsesHandler(c *gin.Context) {
	// 步骤1：读取原始请求体
	reqBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	// 步骤1：预处理、提取模型参数
	before, err := service.BeforerOpenAIRes(reqBody)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	// 步骤2：按模型获取可用 provider
	ctx := c.Request.Context()
	providersWithMeta, err := service.ProvidersWithMetaBymodelsName(ctx, before.Model, consts.StyleOpenAIRes)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	startReq := time.Now()
	// 步骤2：调用负载均衡后的 provider 并转发
	res, logId, err := service.BalanceChat(ctx, startReq, consts.StyleOpenAIRes, *before, *providersWithMeta, models.ReqMeta{
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
	// 步骤3：异步处理输出并记录 tokens
	go service.RecordLog(context.Background(), startReq, pr, service.ProcesserOpenAiRes, logId, *before, providersWithMeta.IOLog)

	WithContentType(c, before.Stream)
	if _, err := io.Copy(c.Writer, tee); err != nil {
		pw.CloseWithError(err)
		common.InternalServerError(c, err.Error())
		return
	}

	pw.Close()
}

func Messages(c *gin.Context) {
	// 步骤1：读取原始请求体
	reqBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	// 步骤1：预处理、提取模型参数
	before, err := service.BeforerAnthropic(reqBody)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	// 步骤2：按模型获取可用 provider
	ctx := c.Request.Context()
	providersWithMeta, err := service.ProvidersWithMetaBymodelsName(ctx, before.Model, consts.StyleAnthropic)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	startReq := time.Now()
	// 步骤2：调用负载均衡后的 provider 并转发
	res, logId, err := service.BalanceChat(ctx, startReq, consts.StyleAnthropic, *before, *providersWithMeta, models.ReqMeta{
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
	// 步骤3：异步处理输出并记录 tokens
	go service.RecordLog(context.Background(), startReq, pr, service.ProcesserAnthropic, logId, *before, providersWithMeta.IOLog)

	WithContentType(c, before.Stream)
	if _, err := io.Copy(c.Writer, tee); err != nil {
		pw.CloseWithError(err)
		common.InternalServerError(c, err.Error())
		return
	}

	pw.Close()
}

func WithContentType(c *gin.Context, stream bool) {
	// 根据是否流式设置响应头，保持 SSE 或 JSON 客户端兼容
	if stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
	} else {
		c.Header("Content-Type", "application/json")
	}
	c.Writer.Flush()
}
