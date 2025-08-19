package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/atopos31/nsxno/react"
	"github.com/gin-gonic/gin"
	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

const testBody = `{
        "model": "gpt-4.1",
		"stream": true,
        "messages": [
            {
                "role": "user",
                "content": "Write a one-sentence bedtime story about a unicorn."
            }
        ]
    }`

func ProviderTestHandler(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	chatModel, err := FindChatModel(c.Request.Context(), id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "ModelWithProvider not found")
			return
		}
		common.InternalServerError(c, "Database error")
		return
	}

	// Create the provider instance
	providerInstance, err := providers.New(chatModel.Type, chatModel.Model, chatModel.Config)
	if err != nil {
		common.BadRequest(c, "Failed to create provider: "+err.Error())
		return
	}

	// Test connectivity by fetching models
	body, status, err := providerInstance.Chat(c.Request.Context(), time.Second*time.Duration(30), []byte(testBody))
	if err != nil {
		common.ErrorWithHttpStatus(c, 502, 502, "Failed to connect to provider: "+err.Error())
		return
	}
	defer body.Close()

	if status != http.StatusOK {
		common.ErrorWithHttpStatus(c, status, status, "Provider returned non-200 status code: "+strconv.Itoa(status))
		return
	}

	common.SuccessWithMessage(c, "Successfully connected to provider", nil)
}

func TestReactHandler(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if id == "" {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	chatModel, err := FindChatModel(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "ModelWithProvider not found")
			return
		}
		common.InternalServerError(c, "Database error")
		return
	}

	var config models.OpenAIConfig
	if err := json.Unmarshal([]byte(chatModel.Config), &config); err != nil {
		common.ErrorWithHttpStatus(c, http.StatusBadRequest, 400, "Invalid config format")
		return
	}

	client := openai.NewClient(
		option.WithBaseURL(config.BaseUrl),
		option.WithAPIKey(config.ApiKey),
	)

	agent := react.New(client, 20)
	question := "分两次获取一下南京和北京的天气 两次之间分别回复我总结信息"
	model := chatModel.Model

	tools := []openai.ChatCompletionToolUnionParam{
		openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
			Name:        "get_weather",
			Description: openai.String("Get weather at the given location"),
			Parameters: openai.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]string{
						"type":        "string",
						"description": "The city name",
					},
				},
				"required": []string{"location"},
			},
		}),
	}
	var checkError error
	var toolCount int
	var nankingCount int
	var pekingCount int

	c.SSEvent("start", fmt.Sprintf("提供商:%s 模型:%s 问题:%s", chatModel.Name, chatModel.Model, question))
	for content, err := range agent.RunStream(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(question),
		},
		Tools: tools,
		Model: model,
	}, GetWeather) {
		if err != nil {
			c.SSEvent("error", err.Error())
			break
		}
		var res string
		switch content.Cate {
		case "message":
			if len(content.Chunk.Choices) > 0 {
				res = content.Chunk.Choices[0].Delta.Content
			}
		case "toolcall":
			data, err := json.Marshal(content.ToolCall.Function)
			if err != nil {
				c.SSEvent("error", err.Error())
				break
			}
			res = string(data)
			location := gjson.Get(content.ToolCall.Function.Arguments, "location").String()
			if location == "南京" {
				nankingCount++
			}
			if location == "北京" {
				pekingCount++
			}
			if content.Step == 0 && location != "南京" {
				checkError = errors.New("第一次应选择南京")
			}
			if content.Step == 1 && location != "北京" {
				checkError = errors.New("第二次应选择北京")
			}
			toolCount++
		case "toolres":
			data, err := json.Marshal(content.ToolRes)
			if err != nil {
				c.SSEvent("error", err.Error())
				break
			}
			res = string(data)
		}
		c.SSEvent(content.Cate, res)
		c.Writer.Flush()
	}
	if toolCount != 2 || nankingCount != 1 || pekingCount != 1 {
		checkError = errors.New("工具调用次数异常")
	}

	if checkError != nil {
		c.SSEvent("error", checkError.Error())
		c.Writer.Flush()
		return
	}
	c.SSEvent("success", "成功通过测试")
}

func GetWeather(ctx context.Context, call openai.ChatCompletionChunkChoiceDeltaToolCallFunction) (*openai.ChatCompletionToolMessageParamContentUnion, error) {
	if call.Name != "get_weather" {
		return nil, fmt.Errorf("invalid tool call name: %s", call.Name)
	}
	location := gjson.Get(call.Arguments, "location")
	var res string
	switch location.String() {
	case "南京":
		res = "南京天气晴转多云，温度 18℃"
	case "北京":
		res = "北京天气大雨转小雨，温度 15℃"
	default:
		res = "暂不支持该地区天气查询"
	}
	return &openai.ChatCompletionToolMessageParamContentUnion{
		OfString: openai.String(res),
	}, nil
}

type ChatModel struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Model  string `json:"model"`
	Config string `json:"config"`
}

func FindChatModel(ctx context.Context, id string) (*ChatModel, error) {
	// Get ModelWithProvider by ID
	modelWithProvider, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		return nil, err
	}

	// Get the Provider
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", modelWithProvider.ProviderID).First(ctx)
	if err != nil {
		return nil, err
	}

	return &ChatModel{
		Name:   provider.Name,
		Type:   provider.Type,
		Model:  modelWithProvider.ProviderModel,
		Config: provider.Config,
	}, nil
}
