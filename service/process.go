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
	MaxScannerBufferSize  = 1024 * 1024 * 15 // 15MB
)

type Processer func(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error)

func ProcesserOpenAI(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once

	var usageStr string
	var output models.OutputUnion

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	for chunk := range ScannerToken(scanner) {
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

		if usageStr != "" {
			break
		}

		usageStr = gjson.Get(chunk, "usage").String()
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

	return &models.ChatLog{
		FirstChunkTime: firstChunkTime,
		ChunkTime:      chunkTime,
		Usage:          openaiUsage,
		Tps:            float64(openaiUsage.TotalTokens) / chunkTime.Seconds(),
	}, &output, nil
}

type OpenAIResUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
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

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	var event string
	for chunk := range ScannerToken(scanner) {
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

	return &models.ChatLog{
		FirstChunkTime: firstChunkTime,
		ChunkTime:      chunkTime,
		Usage: models.Usage{
			PromptTokens:     openAIResUsage.InputTokens,
			CompletionTokens: openAIResUsage.OutputTokens,
			TotalTokens:      openAIResUsage.TotalTokens,
		},
		Tps: float64(openAIResUsage.TotalTokens) / chunkTime.Seconds(),
	}, &output, nil
}

func ProcesserAnthropic(ctx context.Context, pr io.Reader, stream bool, start time.Time) (*models.ChatLog, *models.OutputUnion, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once

	var usageStr string

	var output models.OutputUnion

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	var event string
	for chunk := range ScannerToken(scanner) {
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

	return &models.ChatLog{
		FirstChunkTime: firstChunkTime,
		ChunkTime:      chunkTime,
		Usage: models.Usage{
			PromptTokens:     athropicUsage.InputTokens,
			CompletionTokens: athropicUsage.OutputTokens,
			TotalTokens:      totalTokens,
		},
		Tps: float64(totalTokens) / chunkTime.Seconds(),
	}, &output, nil
}

func ScannerToken(reader *bufio.Scanner) iter.Seq[string] {
	return func(yield func(string) bool) {
		for reader.Scan() {
			chunk := reader.Text()
			if chunk == "" {
				continue
			}
			if !yield(chunk) {
				return
			}
		}
	}
}
