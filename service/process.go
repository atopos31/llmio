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

type Processer func(ctx context.Context, pr io.Reader, stream bool, logId uint, start time.Time) (*models.ChatLog, error)

func ProcesserOpenAI(ctx context.Context, pr io.Reader, stream bool, logId uint, start time.Time) (*models.ChatLog, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once

	var chunkErr error
	var lastchunk string

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	for chunk := range ScannerToken(scanner) {
		once.Do(func() {
			firstChunkTime = time.Since(start)
		})
		if stream {
			chunk = strings.TrimPrefix(chunk, "data: ")
		}
		if chunk == "[DONE]" {
			break
		}
		// 流式过程中错误
		errStr := gjson.Get(chunk, "error")
		if errStr.Exists() {
			chunkErr = errors.New(errStr.String())
			break
		}
		lastchunk = chunk
	}
	// 耗时
	chunkTime := time.Since(start) - firstChunkTime
	// reader错误
	if err := scanner.Err(); err != nil {
		chunkErr = err
	}
	// token用量
	var usage models.Usage
	usageStr := gjson.Get(lastchunk, "usage")
	if usageStr.Exists() && usageStr.Get("total_tokens").Int() != 0 {
		if err := json.Unmarshal([]byte(usageStr.Raw), &usage); err != nil {
			return nil, err
		}
	}

	// tps
	var tps float64
	if stream {
		tps = float64(usage.TotalTokens) / chunkTime.Seconds()
	}

	log := models.ChatLog{
		Usage:          usage,
		ChunkTime:      chunkTime,
		Tps:            tps,
		FirstChunkTime: firstChunkTime,
	}
	if chunkErr != nil {
		log = log.WithError(chunkErr)
	}

	return &log, nil
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

func ProcesserOpenAiRes(ctx context.Context, pr io.Reader, stream bool, logId uint, start time.Time) (*models.ChatLog, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once
	var chunkErr error

	var event string
	var usageStr string

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	for chunk := range ScannerToken(scanner) {
		once.Do(func() {
			firstChunkTime = time.Since(start)
		})
		if stream {
			content := strings.TrimPrefix(chunk, "data: ")
			if event == "response.completed" {
				usageStr = gjson.Get(content, "response.usage").String()
			}
			if after, ok := strings.CutPrefix(chunk, "event: "); ok {
				event = after
			}
		} else {
			usageStr = gjson.Get(chunk, "usage").String()
		}
	}
	var openAIResUsage OpenAIResUsage
	if err := json.Unmarshal([]byte(usageStr), &openAIResUsage); err != nil {
		return nil, err
	}
	totalTokens := openAIResUsage.TotalTokens
	// 耗时
	chunkTime := time.Since(start) - firstChunkTime
	// tps
	var tps float64
	if stream {
		tps = float64(totalTokens) / chunkTime.Seconds()
	}

	usage := models.Usage{
		PromptTokens:     openAIResUsage.InputTokens,
		CompletionTokens: openAIResUsage.OutputTokens,
		TotalTokens:      totalTokens,
	}

	log := models.ChatLog{
		Usage:          usage,
		ChunkTime:      chunkTime,
		Tps:            tps,
		FirstChunkTime: firstChunkTime,
	}
	if err := scanner.Err(); err != nil {
		chunkErr = err
	}
	if chunkErr != nil {
		log = log.WithError(chunkErr)
	}
	return &log, nil
}

func ProcesserAnthropic(ctx context.Context, pr io.Reader, stream bool, logId uint, start time.Time) (*models.ChatLog, error) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once
	var chunkErr error

	var event string
	var usageStr string

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 0, InitScannerBufferSize), MaxScannerBufferSize)
	for chunk := range ScannerToken(scanner) {
		once.Do(func() {
			firstChunkTime = time.Since(start)
		})
		if stream {
			content := strings.TrimPrefix(chunk, "data: ")
			if event == "message_delta" {
				usageStr = gjson.Get(content, "usage").String()
			}
			event = strings.TrimPrefix(chunk, "event: ")
		} else {
			usageStr = gjson.Get(chunk, "usage").String()
		}
	}
	var athropicUsage AnthropicUsage
	if err := json.Unmarshal([]byte(usageStr), &athropicUsage); err != nil {
		return nil, err
	}
	totalTokens := athropicUsage.InputTokens + athropicUsage.OutputTokens
	// 耗时
	chunkTime := time.Since(start) - firstChunkTime
	// tps
	var tps float64
	if stream {
		tps = float64(totalTokens) / chunkTime.Seconds()
	}

	usage := models.Usage{
		PromptTokens:     athropicUsage.InputTokens,
		CompletionTokens: athropicUsage.OutputTokens,
		TotalTokens:      totalTokens,
	}

	log := models.ChatLog{
		Usage:          usage,
		ChunkTime:      chunkTime,
		Tps:            tps,
		FirstChunkTime: firstChunkTime,
	}
	if err := scanner.Err(); err != nil {
		chunkErr = err
	}
	if chunkErr != nil {
		log = log.WithError(chunkErr)
	}
	return &log, nil
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
