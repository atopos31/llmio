package handler

import (
	"io"
	"log/slog"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/model"
	"github.com/atopos31/llmio/providers"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ChatCompletionsHandler(c *gin.Context) {
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return
	}
	reader, err := service.BalanceChat(c.Request.Context(), data)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	n, err := io.Copy(c.Writer, reader)
	if err != nil {
		slog.Error("copy error", "error", err)
		return
	}

	slog.Info("response", "size", n)
}

func ModelsHandler(c *gin.Context) {
	llmModels, err := gorm.G[model.Model](model.DB).Find(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	slog.Info("models", "models", llmModels)
	models := make([]providers.Model, len(llmModels))
	for _, llmModel := range llmModels {
		models = append(models, providers.Model{
			ID:      llmModel.Name,
			Object:  "model",
			Created: llmModel.CreatedAt.Unix(),
			OwnedBy: "llmio",
		})
	}
	common.Success(c, providers.ModelList{
		Object: "list",
		Data:   models,
	})
}
