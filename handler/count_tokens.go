package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func CountTokens(c *gin.Context) {
	ctx := c.Request.Context()

	config, err := gorm.G[models.Config](models.DB).Where("key = ?", models.KeyAnthropicCountTokens).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Anthropic count tokens config not found")
			return
		}
		common.InternalServerError(c, "Failed to retrieve Anthropic count tokens config: "+err.Error())
		return
	}

	var anthropicConfig models.AnthropicCountTokens
	if err := json.Unmarshal([]byte(config.Value), &anthropicConfig); err != nil {
		common.InternalServerError(c, "Failed to parse Anthropic count tokens config: "+err.Error())
		return
	}

	anthropic := providers.Anthropic{
		BaseURL: anthropicConfig.BaseURL,
		APIKey:  anthropicConfig.APIKey,
		Version: anthropicConfig.Version,
	}

	req, err := anthropic.BuildCountTokensReq(ctx, c.Request.Header, c.Request.Body)
	if err != nil {
		common.InternalServerError(c, "Failed to create request: "+err.Error())
		return
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		common.InternalServerError(c, "Failed to send request: "+err.Error())
		return
	}
	defer res.Body.Close()

	c.Status(res.StatusCode)

	for k, values := range res.Header {
		for _, value := range values {
			c.Writer.Header().Add(k, value)
		}
	}
	c.Writer.Flush()

	if _, err := io.Copy(c.Writer, res.Body); err != nil {
		common.InternalServerError(c, "Failed to read response: "+err.Error())
		return
	}
}

const testBody = `{
    	"model": "claude-sonnet-4-5",
    	"messages": [
      		{
        		"role": "user", 
        		"content": "Write a one-sentence bedtime story about a unicorn."
      		}
    	],
		"system": "You are a helpful assistant."
 	}`

type CountTokensResponse struct {
	InputTokens int64 `json:"input_tokens"`
}

func TestCountTokens(c *gin.Context) {
	ctx := c.Request.Context()

	config, err := gorm.G[models.Config](models.DB).Where("key = ?", models.KeyAnthropicCountTokens).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Anthropic count tokens config not found")
			return
		}
		common.InternalServerError(c, "Failed to retrieve Anthropic count tokens config: "+err.Error())
		return
	}

	var anthropicConfig models.AnthropicCountTokens
	if err := json.Unmarshal([]byte(config.Value), &anthropicConfig); err != nil {
		common.InternalServerError(c, "Failed to parse Anthropic count tokens config: "+err.Error())
		return
	}

	anthropic := providers.Anthropic{
		BaseURL: anthropicConfig.BaseURL,
		APIKey:  anthropicConfig.APIKey,
		Version: anthropicConfig.Version,
	}

	req, err := anthropic.BuildCountTokensReq(ctx, nil, strings.NewReader(testBody))
	if err != nil {
		common.InternalServerError(c, "Failed to create request: "+err.Error())
		return
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		common.InternalServerError(c, "Failed to send request: "+err.Error())
		return
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		content, _ := io.ReadAll(res.Body)
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, "Failed to send request. status: "+res.Status+" content: "+string(content))
		return
	}
	var countTokensRes CountTokensResponse
	if err := json.NewDecoder(res.Body).Decode(&countTokensRes); err != nil {
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, "Failed to parse response: "+err.Error())
		return
	}

	if countTokensRes.InputTokens == 0 {
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, "Counted 0 input tokens, something went wrong")
		return
	}

	common.Success(c, "Anthropic count tokens test successful")
}
