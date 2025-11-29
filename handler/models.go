package handler

import (
	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/providers"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
)

func OpenAIModelsHandler(c *gin.Context) {
	ctx := c.Request.Context()
	models, err := service.ModelsByTypes(ctx, consts.StyleOpenAI, consts.StyleOpenAIRes)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	resModels := make([]providers.Model, 0)
	for _, model := range models {
		resModels = append(resModels, providers.Model{
			ID:      model.Name,
			Object:  "model",
			Created: model.CreatedAt.Unix(),
			OwnedBy: "llmio",
		})
	}
	common.SuccessRaw(c, providers.ModelList{
		Object: "list",
		Data:   resModels,
	})
}
