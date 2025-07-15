package main

import (
	"github.com/atopos31/llmio/handler"
	"github.com/atopos31/llmio/model"
	"github.com/gin-gonic/gin"
)

func main() {
	model.InitDB("llmio.db")
	router := gin.Default()

	v1 := router.Group("/v1")
	v1.POST("/chat/completions", handler.ChatCompletionsHandler)
	v1.GET("/models", handler.ModelsHandler)

	router.Run(":7070")
}
