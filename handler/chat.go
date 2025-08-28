package handler

import (
	"log/slog"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

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
	slog.Info("models", "models", models)
	common.SuccessRaw(c, providers.ModelList{
		Object: "list",
		Data:   models,
	})
}

func ChatCompletionsHandler(c *gin.Context) {
	if err := service.BalanceChat(c, "openai", service.BeforerOpenAI, service.ProcesserOpenAI); err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
}

func Messages(c *gin.Context) {
	if err := service.BalanceChat(c, "anthropic", service.BeforerAnthropic, service.ProcesserAnthropic); err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
}
