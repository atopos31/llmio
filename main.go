package main

import (
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/atopos31/llmio/balancer"
	"github.com/atopos31/llmio/prividers"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func main() {
	router := gin.Default()
	v1 := router.Group("/v1")
	v1.POST("/chat/completions", ChatHandler)
	v1.GET("/models", ModelsHandler)
	router.Run(":7070")
}

func ChatHandler(c *gin.Context) {
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return
	}
	model := gjson.GetBytes(data, "model").String()
	stream := gjson.GetBytes(data, "stream").Bool()
	slog.Info("request", "origin model", model, "stream", stream)

	items := make(map[string]int)
	items["deepseek"] = 10
	items["flow"] = 5

	item, err := balancer.WeightedRandom(items)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	slog.Info("selected model", "model", *item)

	var chatModel prividers.Privider
	switch *item {
	case "deepseek":
		chatModel = prividers.NewOpenAI(os.Getenv("DEEPSEEK_BASE_URL"), os.Getenv("DEEPSEEK_API_KEY"), "deepseek-chat")
	case "flow":
		chatModel = prividers.NewOpenAI(os.Getenv("FLOW_BASE_URL"), os.Getenv("FLOW_API_KEY"), "Pro/deepseek-ai/DeepSeek-V3")
	}

	reader, status, err := chatModel.Chat(c, data)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}
	c.Status(status)
	n, err := io.Copy(c.Writer, reader)
	if err != nil {
		slog.Error("copy error", "error", err)
		return
	}

	slog.Info("response", "status", status, "size", n)
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
