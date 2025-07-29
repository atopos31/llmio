package main

import (
	"github.com/atopos31/llmio/handler"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
)

func main() {
	models.Init("llmio.db")
	router := gin.Default()

	v1 := router.Group("/v1")
	v1.POST("/chat/completions", handler.ChatCompletionsHandler)
	v1.GET("/models", handler.ModelsHandler)

	api := router.Group("/api")
	// Provider management
	api.GET("/providers", handler.GetProviders)
	api.POST("/providers", handler.CreateProvider)
	api.PUT("/providers/:id", handler.UpdateProvider)
	api.DELETE("/providers/:id", handler.DeleteProvider)

	// Model management
	api.GET("/models", handler.GetModels)
	api.POST("/models", handler.CreateModel)
	api.PUT("/models/:id", handler.UpdateModel)
	api.DELETE("/models/:id", handler.DeleteModel)

	// Model-provider association management
	api.GET("/model-providers", handler.GetModelProviders)
	api.POST("/model-providers", handler.CreateModelProvider)
	api.PUT("/model-providers/:id", handler.UpdateModelProvider)
	api.DELETE("/model-providers/:id", handler.DeleteModelProvider)

	// System status and monitoring
	api.GET("/status", handler.GetSystemStatus)
	api.GET("/metrics/providers", handler.GetProviderMetrics)
	api.GET("/logs/requests", handler.GetRequestLogs)

	// System configuration
	api.GET("/config", handler.GetSystemConfig)
	api.PUT("/config", handler.UpdateSystemConfig)

	// Provider connectivity test
	api.GET("/test/:id", handler.ProviderTestHandler)

	router.Run(":7070")
}
