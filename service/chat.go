package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/atopos31/llmio/balancer"
	"github.com/atopos31/llmio/providers"
	"github.com/tidwall/gjson"
)

type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

func BalanceChat(ctx context.Context, rawData []byte) (io.Reader, error) {
	originModel := gjson.GetBytes(rawData, "model").String()
	stream := gjson.GetBytes(rawData, "stream").Bool()
	slog.Info("request", "origin model", originModel, "stream", stream)
	items := make(map[string]int)
	items["deepseek"] = 10
	items["flow"] = 5

	restry := 3
	for range restry {
		item, err := balancer.WeightedRandom(items)
		if err != nil {
			return nil, err
		}
		slog.Info("selected model", "model", *item)
		var chatModel prividers.Privider
		switch *item {
		case "deepseek":
			chatModel = prividers.NewOpenAI(os.Getenv("DEEPSEEK_BASE_URL"), os.Getenv("DEEPSEEK_API_KEY"), "deepseek-chat")
		case "flow":
			chatModel = prividers.NewOpenAI(os.Getenv("FLOW_BASE_URL"), os.Getenv("FLOW_API_KEY"), "Pro/deepseek-ai/DeepSeek-V3")
		}
		body, status, err := chatModel.Chat(ctx, rawData)
		if err != nil {
			slog.Error("chat error", "error", err)
			delete(items, *item)
			continue
		}
		if status != http.StatusOK {
			slog.Error("chat error", "status", status)
			if status != http.StatusTooManyRequests {
				delete(items, *item)
			}
			body.Close()
			continue
		}
		teeReader := processTee(ctx, body)

		return teeReader, nil
	}
	return nil, errors.New("no provider available")
}

func processTee(ctx context.Context, body io.ReadCloser) io.Reader {
	pr, pw := io.Pipe()
	teeReader := io.TeeReader(body, pw)
	go func() {
		var usage Usage
		logReader := bufio.NewScanner(pr)
		for logReader.Scan() {
			usageStr := logReader.Text()
			if usageStr == "" {
				continue
			}
			usageStr = strings.TrimPrefix(usageStr, "data: ")
			if !gjson.Valid(usageStr) {
				continue
			}
			usage.PromptTokens = gjson.Get(usageStr, "usage.prompt_tokens").Int()
			usage.CompletionTokens = gjson.Get(usageStr, "usage.completion_tokens").Int()
			usage.TotalTokens = gjson.Get(usageStr, "usage.total_tokens").Int()
		}
		slog.Info("reader off", "input", usage.PromptTokens, "output", usage.CompletionTokens, "total", usage.TotalTokens)
	}()
	go func() {
		defer body.Close()
		defer pw.Close()
		<-ctx.Done()
	}()
	return teeReader
}
