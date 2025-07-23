package main

import (
	"github.com/atopos31/llmio/handler"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
)

func main() {
	models.InitDB("llmio.db")
	router := gin.Default()

	v1 := router.Group("/v1")
	v1.POST("/chat/completions", handler.ChatCompletionsHandler)
	v1.GET("/models", handler.ModelsHandler)

	api := router.Group("/api")
	api.GET("/test/:id", handler.ProviderTestHandler)

	router.Run(":7070")
}
