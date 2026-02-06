package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/atopos31/llmio/service"
	"github.com/atopos31/nsxno/react"
	"github.com/gin-gonic/gin"
	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

const (
	testOpenAI = `{
        "model": "gpt-4.1",
        "messages": [
            {
                "role": "user",
                "content": "Please reply me yes or no"
            }
        ]
    }`

	testOpenAIRes = `{
		"model": "gpt-4.1",
		"input": [
			{
				"role": "user",
				"content": [
					{
						"type": "input_text",
						"text": "Please reply me yes or no"
					}
				]
			}
		]
  	}`

	testAnthropic = `{
    	"model": "claude-sonnet-4-5",
    	"messages": [
      		{
        		"role": "user", 
        		"content": [
					{
						"type": "text",
						"text": "Please reply me yes or no",
						"cache_control": {
							"type": "ephemeral"
						}
					}
				]
      		}
    	]
 	}`

	testGemini = `{
		"contents": [
			{
				"parts": [
					{
						"text": "Please reply me yes or no"
					}
				]
			}
		]
	}`
)

func ProviderTestHandler(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		common.BadRequest(c, "Invalid ID format")
		return
	}
	ctx := c.Request.Context()

	chatModel, err := FindChatModel(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "ModelWithProvider not found")
			return
		}
		common.InternalServerError(c, "Database error")
		return
	}

	// Create the provider instance
	providerInstance, err := providers.New(chatModel.Type, chatModel.Config, chatModel.Proxy)
	if err != nil {
		common.BadRequest(c, "Failed to create provider: "+err.Error())
		return
	}

	// Test connectivity by fetching models
	client := providers.GetClient(time.Second*time.Duration(30), chatModel.Proxy)
	var testBody []byte
	switch chatModel.Type {
	case consts.StyleOpenAI:
		testBody = []byte(testOpenAI)
	case consts.StyleAnthropic:
		testBody = []byte(testAnthropic)
	case consts.StyleOpenAIRes:
		testBody = []byte(testOpenAIRes)
	case consts.StyleGemini:
		testBody = []byte(testGemini)
	default:
		common.BadRequest(c, "Invalid provider type")
		return
	}
	withHeader := false
	if chatModel.WithHeader != nil {
		withHeader = *chatModel.WithHeader
	}
	header := service.BuildHeaders(c.Request.Header, withHeader, chatModel.CustomerHeaders, false)
	req, err := providerInstance.BuildReq(ctx, header, chatModel.Model, []byte(testBody))
	if err != nil {
		common.ErrorWithHttpStatus(c, http.StatusOK, 502, "Failed to connect to provider: "+err.Error())
		return
	}
	res, err := client.Do(req)
	if err != nil {
		common.ErrorWithHttpStatus(c, http.StatusOK, 502, "Failed to connect to provider: "+err.Error())
		return
	}
	defer res.Body.Close()

	content, err := io.ReadAll(res.Body)
	if err != nil {
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, "Failed to send request: "+err.Error())
		return
	}

	if res.StatusCode != http.StatusOK {
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, fmt.Sprintf("code: %d body: %s", res.StatusCode, string(content)))
		return
	}

	common.SuccessWithMessage(c, string(content), nil)
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

	if chatModel.Type != consts.StyleOpenAI {
		c.SSEvent("error", "该测试仅支持 OpenAI 类型")
		return
	}

	var config providers.OpenAI
	if err := json.Unmarshal([]byte(chatModel.Config), &config); err != nil {
		common.ErrorWithHttpStatus(c, http.StatusBadRequest, 400, "Invalid config format")
		return
	}

	client := openai.NewClient(
		option.WithBaseURL(config.BaseURL),
		option.WithAPIKey(config.APIKey),
	)

	agent := react.New(client, 20)
	question := "分两次获取一下南京和北京的天气 每次调用后回复我对应城市的总结信息"
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
	start := time.Now()
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
		checkError = fmt.Errorf("工具调用次数异常: 南京: %d 北京: %d 总计: %d", nankingCount, pekingCount, toolCount)
	}

	if checkError != nil {
		c.SSEvent("error", checkError.Error())
		c.Writer.Flush()
		return
	}
	c.SSEvent("success", fmt.Sprintf("成功通过测试, 耗时: %.2fs", time.Since(start).Seconds()))
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
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Model           string            `json:"model"`
	Config          string            `json:"config"`
	Proxy           string            `json:"proxy,omitempty"`
	WithHeader      *bool             `json:"with_header,omitempty"`
	CustomerHeaders map[string]string `json:"customer_headers,omitempty"`
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
		Name:            provider.Name,
		Type:            provider.Type,
		Model:           modelWithProvider.ProviderModel,
		Config:          provider.Config,
		Proxy:           provider.Proxy,
		WithHeader:      modelWithProvider.WithHeader,
		CustomerHeaders: modelWithProvider.CustomerHeaders,
	}, nil
}
