package main

import (
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/atopos31/llmio/model"
	"github.com/atopos31/llmio/providers"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
)

func main() {
	model.InitDB("llmio.db")
	router := gin.Default()
	v1 := router.Group("/v1")
	v1.POST("/chat/completions", ChatCompletionsHandler)
	v1.GET("/models", ModelsHandler)
	router.Run(":7070")
}

func ChatCompletionsHandler(c *gin.Context) {
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return
	}
	reader, err := service.BalanceChat(c.Request.Context(), data)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
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
	chatModel := prividers.NewOpenAI("https://api.moonshot.cn/v1", os.Getenv("KIMI_API_KEY"), "kimi-k2-0711-preview")
	models, err := chatModel.Models(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, prividers.ModelList{
		Object: "list",
		Data:   models,
	})
}
