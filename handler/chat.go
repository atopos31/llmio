package handler

import (
	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
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

	common.SuccessRaw(c, providers.ModelList{
		Object: "list",
		Data:   models,
	})
}

func ChatCompletionsHandler(c *gin.Context) {
	if err := service.BalanceChat(c, consts.StyleOpenAI); err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
}

func ResponsesHandler(c *gin.Context) {
	if err := service.BalanceChat(c, consts.StyleOpenAIRes); err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
}

func Messages(c *gin.Context) {
	if err := service.BalanceChat(c, consts.StyleAnthropic); err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
}
