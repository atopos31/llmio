package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/atopos31/llmio/consts"
)

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"` // 使用 int64 存储 Unix 时间戳
	OwnedBy string `json:"owned_by"`
	Group   string `json:"group,omitempty"` // 模型分组
	// 模型能力
	Capabilities *ModelCapabilities `json:"capabilities,omitempty"`
}

// ModelCapabilities 模型能力
type ModelCapabilities struct {
	Vision           bool `json:"vision"`            // 视觉能力（图片理解）
	FunctionCalling  bool `json:"function_calling"`  // 工具调用能力
	StructuredOutput bool `json:"structured_output"` // 结构化输出能力
}

type Provider interface {
	BuildReq(ctx context.Context, header http.Header, model string, rawData []byte) (*http.Request, error)
	Models(ctx context.Context) ([]Model, error)
}

// InferModelGroup 根据模型 ID 推断分组名称
// 参考 Cherry Studio 的分组策略
func InferModelGroup(modelID string) string {
	id := strings.ToLower(modelID)
	
	// OpenAI 模型系列
	if strings.Contains(id, "gpt-5") {
		return "GPT 5"
	}
	if strings.Contains(id, "gpt-4.1") {
		return "GPT 4.1"
	}
	if strings.Contains(id, "gpt-4o") {
		return "GPT 4o"
	}
	if strings.Contains(id, "gpt-4") {
		return "GPT 4"
	}
	if strings.Contains(id, "gpt-3.5") {
		return "GPT 3.5"
	}
	if strings.Contains(id, "o1") || strings.Contains(id, "o3") || strings.Contains(id, "o4") {
		return "OpenAI"
	}
	if strings.Contains(id, "gpt-image") {
		return "GPT Image"
	}
	
	// Claude 模型系列
	if strings.Contains(id, "claude-4.5") || strings.Contains(id, "claude-sonnet-4-5") ||
	   strings.Contains(id, "claude-opus-4-5") || strings.Contains(id, "claude-haiku-4-5") {
		return "Claude 4.5"
	}
	if strings.Contains(id, "claude-4") || strings.Contains(id, "claude-sonnet-4") ||
	   strings.Contains(id, "claude-opus-4") {
		return "Claude 4"
	}
	if strings.Contains(id, "claude-3.7") {
		return "Claude 3.7"
	}
	if strings.Contains(id, "claude-3.5") {
		return "Claude 3.5"
	}
	if strings.Contains(id, "claude-3") {
		return "Claude 3"
	}
	if strings.Contains(id, "claude") {
		return "Claude"
	}
	
	// Gemini 模型系列
	if strings.Contains(id, "gemini-3") {
		return "Gemini 3"
	}
	if strings.Contains(id, "gemini-2.5") {
		return "Gemini 2.5"
	}
	if strings.Contains(id, "gemini-2.0") || strings.Contains(id, "gemini-2-") {
		return "Gemini 2.0"
	}
	if strings.Contains(id, "gemini-1.5") {
		return "Gemini 1.5"
	}
	if strings.Contains(id, "gemini") {
		return "Gemini"
	}
	
	// DeepSeek 模型系列
	if strings.Contains(id, "deepseek-r1") {
		return "DeepSeek R1"
	}
	if strings.Contains(id, "deepseek-v3") {
		return "DeepSeek V3"
	}
	if strings.Contains(id, "deepseek-v2") {
		return "DeepSeek V2"
	}
	if strings.Contains(id, "deepseek") {
		return "DeepSeek"
	}
	
	// Qwen 模型系列
	if strings.Contains(id, "qwen3") {
		return "Qwen 3"
	}
	if strings.Contains(id, "qwen2.5") {
		return "Qwen 2.5"
	}
	if strings.Contains(id, "qwen2") {
		return "Qwen 2"
	}
	if strings.Contains(id, "qwq") {
		return "Qwen"
	}
	if strings.Contains(id, "qwen") {
		return "Qwen"
	}
	
	// GLM 模型系列
	if strings.Contains(id, "glm-4.7") {
		return "GLM 4.7"
	}
	if strings.Contains(id, "glm-4.6") {
		return "GLM 4.6"
	}
	if strings.Contains(id, "glm-4.5") {
		return "GLM 4.5"
	}
	if strings.Contains(id, "glm-4") {
		return "GLM 4"
	}
	if strings.Contains(id, "glm") {
		return "GLM"
	}
	
	// Llama 模型系列
	if strings.Contains(id, "llama-4") || strings.Contains(id, "llama4") {
		return "Llama 4"
	}
	if strings.Contains(id, "llama-3.3") || strings.Contains(id, "llama3.3") {
		return "Llama 3.3"
	}
	if strings.Contains(id, "llama-3.2") || strings.Contains(id, "llama3.2") {
		return "Llama 3.2"
	}
	if strings.Contains(id, "llama-3.1") || strings.Contains(id, "llama3.1") {
		return "Llama 3.1"
	}
	if strings.Contains(id, "llama-3") || strings.Contains(id, "llama3") {
		return "Llama 3"
	}
	if strings.Contains(id, "llama") {
		return "Llama"
	}
	
	// Mistral 模型系列
	if strings.Contains(id, "pixtral") {
		return "Pixtral"
	}
	if strings.Contains(id, "mistral") || strings.Contains(id, "mixtral") {
		return "Mistral"
	}
	
	// Moonshot (Kimi) 模型系列
	if strings.Contains(id, "kimi") || strings.Contains(id, "moonshot") {
		return "Moonshot"
	}
	
	// MiniMax 模型系列
	if strings.Contains(id, "minimax-m2") {
		return "MiniMax M2"
	}
	if strings.Contains(id, "minimax") || strings.Contains(id, "abab") {
		return "MiniMax"
	}
	
	// Doubao (豆包) 模型系列
	if strings.Contains(id, "doubao") {
		return "Doubao"
	}
	
	// Baichuan 模型系列
	if strings.Contains(id, "baichuan") {
		return "Baichuan"
	}
	
	// Yi 模型系列
	if strings.Contains(id, "yi-") {
		return "Yi"
	}
	
	// Gemma 模型系列
	if strings.Contains(id, "gemma") {
		return "Gemma"
	}
	
	// Grok 模型系列
	if strings.Contains(id, "grok") {
		return "Grok"
	}
	
	// ERNIE (文心) 模型系列
	if strings.Contains(id, "ernie") {
		return "ERNIE"
	}
	
	// Hunyuan (混元) 模型系列
	if strings.Contains(id, "hunyuan") {
		return "Hunyuan"
	}
	
	// Step 模型系列
	if strings.Contains(id, "step-") {
		return "Step"
	}
	
	// Sonar (Perplexity) 模型系列
	if strings.Contains(id, "sonar") {
		return "Sonar"
	}
	
	// Jina 模型系列
	if strings.Contains(id, "jina") {
		return "Jina AI"
	}
	
	// BAAI 模型系列
	if strings.Contains(id, "bge-") || strings.Contains(id, "baai") {
		return "BAAI"
	}
	
	// Embedding 模型
	if strings.Contains(id, "embedding") || strings.Contains(id, "embed") {
		return "Embedding"
	}
	
	// Rerank 模型
	if strings.Contains(id, "rerank") {
		return "Rerank"
	}
	
	// 默认返回模型 ID 的首个部分作为分组
	parts := strings.Split(id, "-")
	if len(parts) > 0 && parts[0] != "" {
		return strings.Title(parts[0])
	}
	
	return "Other"
}

// InferModelCapabilities 根据模型名称推断模型能力
// 这是一个基于常见模型命名规则的启发式方法
func InferModelCapabilities(modelID string) *ModelCapabilities {
	id := strings.ToLower(modelID)
	
	caps := &ModelCapabilities{
		Vision:           false,
		FunctionCalling:  false,
		StructuredOutput: false,
	}
	
	// OpenAI 模型
	if strings.Contains(id, "gpt-4") {
		caps.FunctionCalling = true
		caps.StructuredOutput = true
		// GPT-4 Vision 模型
		if strings.Contains(id, "vision") || strings.Contains(id, "turbo") || strings.Contains(id, "o") {
			caps.Vision = true
		}
	}
	if strings.Contains(id, "gpt-3.5") {
		caps.FunctionCalling = true
		caps.StructuredOutput = true
	}
	if strings.Contains(id, "o1") || strings.Contains(id, "o3") {
		caps.FunctionCalling = true
		caps.StructuredOutput = true
		caps.Vision = true
	}
	
	// Claude 模型
	if strings.Contains(id, "claude") {
		caps.FunctionCalling = true
		caps.StructuredOutput = true
		// Claude 3 及以上版本支持视觉
		if strings.Contains(id, "claude-3") || strings.Contains(id, "claude-4") {
			caps.Vision = true
		}
	}
	
	// Gemini 模型
	if strings.Contains(id, "gemini") {
		caps.FunctionCalling = true
		caps.StructuredOutput = true
		// Gemini Pro Vision 或 Gemini 1.5+ 支持视觉
		if strings.Contains(id, "vision") || strings.Contains(id, "1.5") || strings.Contains(id, "2.0") || strings.Contains(id, "2.5") {
			caps.Vision = true
		}
	}
	
	// Qwen 模型
	if strings.Contains(id, "qwen") {
		caps.FunctionCalling = true
		caps.StructuredOutput = true
		// Qwen-VL 系列支持视觉
		if strings.Contains(id, "vl") || strings.Contains(id, "vision") {
			caps.Vision = true
		}
	}
	
	// DeepSeek 模型
	if strings.Contains(id, "deepseek") {
		caps.FunctionCalling = true
		caps.StructuredOutput = true
		// DeepSeek-VL 系列支持视觉
		if strings.Contains(id, "vl") || strings.Contains(id, "vision") {
			caps.Vision = true
		}
	}
	
	// GLM 模型
	if strings.Contains(id, "glm") {
		caps.FunctionCalling = true
		caps.StructuredOutput = true
		// GLM-4V 系列支持视觉
		if strings.Contains(id, "4v") || strings.Contains(id, "vision") {
			caps.Vision = true
		}
	}
	
	// Llama 模型
	if strings.Contains(id, "llama") {
		// Llama 3.1+ 支持工具调用
		if strings.Contains(id, "3.1") || strings.Contains(id, "3.2") || strings.Contains(id, "3.3") {
			caps.FunctionCalling = true
			caps.StructuredOutput = true
		}
		// Llama Vision 模型
		if strings.Contains(id, "vision") {
			caps.Vision = true
		}
	}
	
	// Mistral 模型
	if strings.Contains(id, "mistral") || strings.Contains(id, "mixtral") {
		caps.FunctionCalling = true
		caps.StructuredOutput = true
		// Pixtral 支持视觉
		if strings.Contains(id, "pixtral") {
			caps.Vision = true
		}
	}
	
	// 通用视觉模型标识
	if strings.Contains(id, "vision") || strings.Contains(id, "-vl") || strings.Contains(id, "vl-") {
		caps.Vision = true
	}
	
	return caps
}

func New(Type, providerConfig string) (Provider, error) {
	switch Type {
	case consts.StyleOpenAI:
		var openai OpenAI
		if err := json.Unmarshal([]byte(providerConfig), &openai); err != nil {
			return nil, errors.New("invalid openai config")
		}

		return &openai, nil
	case consts.StyleOpenAIRes:
		var openaiRes OpenAIRes
		if err := json.Unmarshal([]byte(providerConfig), &openaiRes); err != nil {
			return nil, errors.New("invalid openai-res config")
		}

		return &openaiRes, nil
	case consts.StyleAnthropic:
		var anthropic Anthropic
		if err := json.Unmarshal([]byte(providerConfig), &anthropic); err != nil {
			return nil, errors.New("invalid anthropic config")
		}
		return &anthropic, nil
	case consts.StyleGemini:
		var gemini Gemini
		if err := json.Unmarshal([]byte(providerConfig), &gemini); err != nil {
			return nil, errors.New("invalid gemini config")
		}
		return &gemini, nil
	default:
		return nil, errors.New("unknown provider")
	}
}
