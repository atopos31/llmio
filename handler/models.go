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

func AnthropicModelsHandler(c *gin.Context) {
	ctx := c.Request.Context()
	models, err := service.ModelsByTypes(ctx, consts.StyleAnthropic)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	resModels := make([]providers.AnthropicModel, 0)
	for _, model := range models {
		resModels = append(resModels, providers.AnthropicModel{
			ID:          model.Name,
			CreatedAt:   model.CreatedAt,
			DisplayName: model.Name,
			Type:        "model",
		})
	}
	common.SuccessRaw(c, providers.AnthropicModelsResponse{
		Data:    resModels,
		HasMore: false,
	})
}

type GeminiModelsResponse struct {
	Models []GeminiModel `json:"models"`
}

type GeminiModel struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName,omitempty"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`
}

func GeminiModelsHandler(c *gin.Context) {
	ctx := c.Request.Context()
	models, err := service.ModelsByTypes(ctx, consts.StyleGemini)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	resModels := make([]GeminiModel, 0, len(models))
	for _, model := range models {
		resModels = append(resModels, GeminiModel{
			Name:        "models/" + model.Name,
			DisplayName: model.Name,
			SupportedGenerationMethods: []string{
				"generateContent",
				"streamGenerateContent",
			},
		})
	}
	common.SuccessRaw(c, GeminiModelsResponse{
		Models: resModels,
	})
}
