package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

type TeeProcesser func(ctx context.Context, pr io.ReadCloser, stream bool, logId uint, start time.Time)

func ProcesserOpenAI(ctx context.Context, pr io.ReadCloser, stream bool, logId uint, start time.Time) {
	// 首字时延
	var firstChunkTime time.Duration
	var once sync.Once

	var chunkErr error
	var lastchunk string

	logReader := bufio.NewScanner(pr)
	for chunk := range ScannerToken(logReader) {
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
	if err := logReader.Err(); err != nil {
		chunkErr = err
	}
	// token用量
	var usage models.Usage
	usageStr := gjson.Get(lastchunk, "usage")
	slog.Info("usage", "usage", usageStr.String())
	if usageStr.Exists() && usageStr.Get("total_tokens").Int() != 0 {
		if err := json.Unmarshal([]byte(usageStr.Raw), &usage); err != nil {
			slog.Error("unmarshal usage error, raw:" + usageStr.Raw)
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

	if _, err := gorm.G[models.ChatLog](models.DB).Where("id = ?", logId).Updates(ctx, log); err != nil {
		slog.Error("update chat log error", "error", err)
	}
	slog.Info("response", "input", usage.PromptTokens, "output", usage.CompletionTokens, "total", usage.TotalTokens, "firstChunkTime", firstChunkTime, "chunkTime", chunkTime, "tps", tps)

}
