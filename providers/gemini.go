package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/atopos31/llmio/consts"
)

// Gemini 调用 Gemini 原生 REST API。
// BaseURL 推荐: https://generativelanguage.googleapis.com/v1beta
// 通过 POST /models/{model}:generateContent 进行内容生成。
type Gemini struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Proxy   string `json:"-"`
}

func (g *Gemini) BuildReq(ctx context.Context, header http.Header, model string, rawBody []byte) (*http.Request, error) {
	model = strings.TrimPrefix(model, "models/")
	stream, _ := ctx.Value(consts.ContextKeyGeminiStream).(bool)
	action := "generateContent"
	urlSuffix := ""
	if stream {
		action = "streamGenerateContent"
		urlSuffix = "?alt=sse"
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/models/%s:%s%s", g.BaseURL, model, action, urlSuffix),
		bytes.NewReader(rawBody),
	)
	if err != nil {
		return nil, err
	}
	if header != nil {
		req.Header = header
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.APIKey)
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}

	return req, nil
}

type geminiListModelsResponse struct {
	Models        []geminiModel `json:"models"`
	NextPageToken string        `json:"nextPageToken"`
}

type geminiModel struct {
	Name string `json:"name"` // e.g. "models/gemini-2.5-flash"
}

func (g *Gemini) Models(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/models", strings.TrimRight(g.BaseURL, "/")), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", g.APIKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := GetClient(30*time.Second, g.Proxy).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", res.StatusCode)
	}

	var resp geminiListModelsResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, err
	}

	var models []Model
	for _, m := range resp.Models {
		models = append(models, Model{
			ID:      m.Name,
			Object:  "model",
			OwnedBy: "google",
		})
	}
	return models, nil
}
