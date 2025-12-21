package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/tidwall/gjson"
)

const (
	InitScannerBufferSize = 1024 * 8         // 8KB
	MaxScannerBufferSize  = 1024 * 1024 * 64 // 64MB
)

type Processer func(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error)

func ProcesserOpenAI(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once

	var usageStr string
	var output models.OutputUnion
	var size int

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	for chunk, chunkSize := range ScannerToken(scanner) {
		size += chunkSize
		once.Do(func() {
			firstChunkTime = time.Since(start)
		})
		if !stream {
			output.OfString = chunk
			usageStr = gjson.Get(chunk, "usage").String()
			break
		}
		chunk = strings.TrimPrefix(chunk, "data: ")
		if chunk == "[DONE]" {
			break
		}
		// 流式过程中错误
		errStr := gjson.Get(chunk, "error")
		if errStr.Exists() {
			return nil, nil, errors.New(errStr.String())
		}
		output.OfStringArray = append(output.OfStringArray, chunk)

		// 部分厂商openai格式中 每段sse响应都会返回usage 兼容性考虑
		// if usageStr != "" {
		// 	break
		// }

		usage := gjson.Get(chunk, "usage")
		if usage.Exists() && usage.Get("total_tokens").Int() != 0 {
			usageStr = usage.String()
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	// token用量
	var openaiUsage models.Usage
	usage := []byte(usageStr)
	if json.Valid(usage) {
		if err := json.Unmarshal(usage, &openaiUsage); err != nil {
			return nil, nil, err
		}
	}

	chunkTime := time.Since(start) - firstChunkTime

	// 计算 TPS，避免除零产生无穷大值
	var tps float64
	if chunkTime.Seconds() > 0 {
		tps = float64(openaiUsage.TotalTokens) / chunkTime.Seconds()
	}

	return &models.ChatLog{
		FirstChunkTime: firstChunkTime,
		ChunkTime:      chunkTime,
		Usage:          openaiUsage,
		Tps:            tps,
		Size:           size,
	}, &output, nil
}

type OpenAIResUsage struct {
	InputTokens        int64              `json:"input_tokens"`
	OutputTokens       int64              `json:"output_tokens"`
	TotalTokens        int64              `json:"total_tokens"`
	InputTokensDetails InputTokensDetails `json:"input_tokens_details"`
}

type InputTokensDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}

type AnthropicUsage struct {
	InputTokens              int64  `json:"input_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	ServiceTier              string `json:"service_tier"`
}

func ProcesserOpenAiRes(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once

	var usageStr string
	var output models.OutputUnion
	var size int

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	var event string
	for chunk, chunkSize := range ScannerToken(scanner) {
		size += chunkSize
		once.Do(func() {
			firstChunkTime = time.Since(start)
		})
		if !stream {
			output.OfString = chunk
			usageStr = gjson.Get(chunk, "usage").String()
			break
		}

		if after, ok := strings.CutPrefix(chunk, "event: "); ok {
			event = after
			continue
		}
		content := strings.TrimPrefix(chunk, "data: ")
		if content == "" {
			continue
		}
		output.OfStringArray = append(output.OfStringArray, content)
		if event == "response.completed" {
			usageStr = gjson.Get(content, "response.usage").String()
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	var openAIResUsage OpenAIResUsage
	usage := []byte(usageStr)
	if json.Valid(usage) {
		if err := json.Unmarshal(usage, &openAIResUsage); err != nil {
			return nil, nil, err
		}
	}

	chunkTime := time.Since(start) - firstChunkTime

	// 计算 TPS，避免除零产生无穷大值
	var tps float64
	if chunkTime.Seconds() > 0 {
		tps = float64(openAIResUsage.TotalTokens) / chunkTime.Seconds()
	}

	return &models.ChatLog{
		FirstChunkTime: firstChunkTime,
		ChunkTime:      chunkTime,
		Usage: models.Usage{
			PromptTokens:     openAIResUsage.InputTokens,
			CompletionTokens: openAIResUsage.OutputTokens,
			TotalTokens:      openAIResUsage.TotalTokens,
			PromptTokensDetails: models.PromptTokensDetails{
				CachedTokens: openAIResUsage.InputTokensDetails.CachedTokens,
			},
		},
		Tps:  tps,
		Size: size,
	}, &output, nil
}

func ProcesserAnthropic(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once

	var usageStr string

	var output models.OutputUnion
	var size int

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	var event string
	for chunk, chunkSize := range ScannerToken(scanner) {
		size += chunkSize
		once.Do(func() {
			firstChunkTime = time.Since(start)
		})
		if !stream {
			output.OfString = chunk
			usageStr = gjson.Get(chunk, "usage").String()
			break
		}

		if after, ok := strings.CutPrefix(chunk, "event: "); ok {
			event = after
			continue
		}

		after, ok := strings.CutPrefix(chunk, "data: ")
		if !ok {
			continue
		}

		output.OfStringArray = append(output.OfStringArray, after)
		if event == "message_delta" {
			usageStr = gjson.Get(after, "usage").String()
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	var athropicUsage AnthropicUsage
	usage := []byte(usageStr)
	if json.Valid(usage) {
		if err := json.Unmarshal(usage, &athropicUsage); err != nil {
			return nil, nil, err
		}
	}

	chunkTime := time.Since(start) - firstChunkTime
	totalTokens := athropicUsage.InputTokens + athropicUsage.OutputTokens

	// 计算 TPS，避免除零产生无穷大值
	var tps float64
	if chunkTime.Seconds() > 0 {
		tps = float64(totalTokens) / chunkTime.Seconds()
	}

	return &models.ChatLog{
		FirstChunkTime: firstChunkTime,
		ChunkTime:      chunkTime,
		Usage: models.Usage{
			PromptTokens:     athropicUsage.InputTokens,
			CompletionTokens: athropicUsage.OutputTokens,
			TotalTokens:      totalTokens,
			PromptTokensDetails: models.PromptTokensDetails{
				CachedTokens: athropicUsage.CacheReadInputTokens,
			},
		},
		Tps:  tps,
		Size: size,
	}, &output, nil
}

func ProcesserGemini(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once

	var usageStr string
	var output models.OutputUnion
	var size int

	if !stream {
		bodyBytes, err := io.ReadAll(pr)
		if err != nil {
			return nil, nil, err
		}
		size += len(bodyBytes)
		once.Do(func() {
			firstChunkTime = time.Since(start)
		})
		output.OfString = string(bodyBytes)

		usageStr = gjson.GetBytes(bodyBytes, "usageMetadata").String()

	} else {
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
		for chunk, chunkSize := range ScannerToken(scanner) {
			size += chunkSize
			once.Do(func() {
				firstChunkTime = time.Since(start)
			})

			if strings.HasPrefix(chunk, "event:") {
				continue
			}

			payload := ""
			if after, ok := strings.CutPrefix(chunk, "data: "); ok {
				payload = after
			} else if strings.HasPrefix(chunk, "{") || strings.HasPrefix(chunk, "[") {
				payload = chunk
			} else {
				continue
			}
			if payload == "" {
				continue
			}
			if payload == "[DONE]" {
				break
			}

			// 流式过程中错误
			errStr := gjson.Get(payload, "error")
			if errStr.Exists() {
				return nil, nil, errors.New(errStr.String())
			}

			output.OfStringArray = append(output.OfStringArray, payload)
			usageMetadata := gjson.Get(payload, "usageMetadata")
			if usageMetadata.Exists() && usageMetadata.Get("totalTokenCount").Int() != 0 {
				usageStr = usageMetadata.String()
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, nil, err
		}
	}

	var usage models.Usage
	usageMetadata := gjson.Parse(usageStr)
	if usageMetadata.Exists() {
		usage.PromptTokens = usageMetadata.Get("promptTokenCount").Int()
		usage.CompletionTokens = usageMetadata.Get("candidatesTokenCount").Int() + usageMetadata.Get("thoughtsTokenCount").Int()
		usage.TotalTokens = usageMetadata.Get("totalTokenCount").Int()
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
	}

	chunkTime := time.Since(start) - firstChunkTime

	// 计算 TPS，避免除零产生无穷大值
	var tps float64
	if chunkTime.Seconds() > 0 {
		tps = float64(usage.TotalTokens) / chunkTime.Seconds()
	}

	return &models.ChatLog{
		FirstChunkTime: firstChunkTime,
		ChunkTime:      chunkTime,
		Usage:          usage,
		Tps:            tps,
		Size:           size,
	}, &output, nil
}

func ScannerToken(reader *bufio.Scanner) iter.Seq2[string, int] {
	return func(yield func(string, int) bool) {
		for reader.Scan() {
			chunk := reader.Text()
			if chunk == "" {
				continue
			}
			if !yield(chunk, len(reader.Bytes())) {
				return
			}
		}
	}
}
