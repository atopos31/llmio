package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/atopos31/llmio/consts"
)

const (
	// commandCodeUsageURL 是 commandcode 的用量接口地址。
	// 它挂在 /alpha/billing/credits 下，与 provider 的 base_url（/provider/v1）不同源，
	// 无法由 base_url 推导，故按类型硬编码。
	commandCodeUsageURL = "https://api.commandcode.ai/alpha/billing/credits"

	// commandCodeMonthlyQuota 是 commandcode 的固定月度信用额度，
	// 用于把「剩余信用点数」折算成月度用量百分比。
	commandCodeMonthlyQuota = 262.0

	// usageRequestTimeout 是用量查询的响应头超时。
	usageRequestTimeout = 15 * time.Second
)

// UsageWindow 是归一化后的单个用量窗口，percent 统一为 0-100。
type UsageWindow struct {
	Percent  float64 `json:"percent"`
	Status   string  `json:"status,omitempty"`    // ok / exceeded
	ResetsAt string  `json:"resets_at,omitempty"` // RFC3339，空表示上游未提供
}

// ProviderUsage 是归一化后的供应商用量，供 /api/providers/usage/:id 返回。
type ProviderUsage struct {
	Supported bool         `json:"supported"`
	Rolling   *UsageWindow `json:"rolling,omitempty"`
	Weekly    *UsageWindow `json:"weekly,omitempty"`
	Monthly   *UsageWindow `json:"monthly,omitempty"`
}

// FetchUsage 按 provider 类型拉取并归一化用量。
// 目前仅 opencode / commandcode 提供用量接口，其余类型返回 Supported=false（err 为 nil）；
// 支持的类型但拉取失败时返回 nil + err，由调用方决定如何降级。
func FetchUsage(ctx context.Context, providerType, providerConfig, proxy string) (*ProviderUsage, error) {
	switch providerType {
	case consts.StyleOpenCode:
		return fetchOpenCodeUsage(ctx, providerConfig, proxy)
	case consts.StyleCommandCode:
		return fetchCommandCodeUsage(ctx, providerConfig, proxy)
	default:
		return &ProviderUsage{Supported: false}, nil
	}
}

// usageCredentials 从 provider 配置中取出 base_url 与 api_key（OpenAI 兼容结构）。
func usageCredentials(providerConfig string) (baseURL, apiKey string, err error) {
	var cfg OpenAI
	if err := json.Unmarshal([]byte(providerConfig), &cfg); err != nil {
		return "", "", errors.New("invalid provider config")
	}
	if cfg.BaseURL == "" {
		return "", "", errors.New("base_url is empty")
	}
	return cfg.BaseURL, cfg.APIKey, nil
}

// getUsageJSON 发起带 Bearer 鉴权的 GET 并把响应体解码到 out。
func getUsageJSON(ctx context.Context, rawURL, apiKey, proxy string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := GetClient(usageRequestTimeout, proxy).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status code: %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// --- opencode: GET {base_url}/usage ---

type openCodeUsageResponse struct {
	Usage struct {
		Rolling *openCodeWindow `json:"rolling"`
		Weekly  *openCodeWindow `json:"weekly"`
		Monthly *openCodeWindow `json:"monthly"`
	} `json:"usage"`
}

type openCodeWindow struct {
	Status   string  `json:"status"`
	Percent  float64 `json:"percent"`
	ResetsAt string  `json:"resetsAt"`
}

func (w *openCodeWindow) toWindow() *UsageWindow {
	if w == nil {
		return nil
	}
	return &UsageWindow{
		Percent:  w.Percent,
		Status:   w.Status,
		ResetsAt: normalizeRFC3339(w.ResetsAt),
	}
}

func fetchOpenCodeUsage(ctx context.Context, providerConfig, proxy string) (*ProviderUsage, error) {
	baseURL, apiKey, err := usageCredentials(providerConfig)
	if err != nil {
		return nil, err
	}

	var res openCodeUsageResponse
	if err := getUsageJSON(ctx, strings.TrimRight(baseURL, "/")+"/usage", apiKey, proxy, &res); err != nil {
		return nil, err
	}

	return &ProviderUsage{
		Supported: true,
		Rolling:   res.Usage.Rolling.toWindow(),
		Weekly:    res.Usage.Weekly.toWindow(),
		Monthly:   res.Usage.Monthly.toWindow(),
	}, nil
}

// --- commandcode: GET /alpha/billing/credits ---

type commandCodeUsageResponse struct {
	Credits struct {
		MonthlyCredits   float64 `json:"monthlyCredits"`
		PurchasedCredits float64 `json:"purchasedCredits"`
		FreeCredits      float64 `json:"freeCredits"`
	} `json:"credits"`
	WindowLimits struct {
		FiveHour *commandCodeWindow `json:"fiveHour"`
		Weekly   *commandCodeWindow `json:"weekly"`
	} `json:"windowLimits"`
}

type commandCodeWindow struct {
	Used     float64 `json:"used"`
	Cap      float64 `json:"cap"`
	Exceeded bool    `json:"exceeded"`
	ResetAt  int64   `json:"resetAt"` // unix 毫秒
}

func (w *commandCodeWindow) toWindow() *UsageWindow {
	if w == nil {
		return nil
	}
	percent := 0.0
	if w.Cap > 0 {
		percent = w.Used / w.Cap * 100
	}
	status := "ok"
	if w.Exceeded {
		status = "exceeded"
	}
	return &UsageWindow{
		Percent:  percent,
		Status:   status,
		ResetsAt: unixMilliToRFC3339(w.ResetAt),
	}
}

func fetchCommandCodeUsage(ctx context.Context, providerConfig, proxy string) (*ProviderUsage, error) {
	_, apiKey, err := usageCredentials(providerConfig)
	if err != nil {
		return nil, err
	}

	var res commandCodeUsageResponse
	if err := getUsageJSON(ctx, commandCodeUsageURL, apiKey, proxy, &res); err != nil {
		return nil, err
	}

	// 约定口径：月度用量 =（月度信用 + 已购信用 + 赠送信用）/ 固定额度。
	monthlyPercent := (res.Credits.MonthlyCredits + res.Credits.PurchasedCredits + res.Credits.FreeCredits) / commandCodeMonthlyQuota * 100

	return &ProviderUsage{
		Supported: true,
		Rolling:   res.WindowLimits.FiveHour.toWindow(),
		Weekly:    res.WindowLimits.Weekly.toWindow(),
		Monthly:   &UsageWindow{Percent: monthlyPercent},
	}, nil
}

// normalizeRFC3339 把上游返回的时间串统一成 UTC 的 RFC3339；解析失败则原样返回。
func normalizeRFC3339(raw string) string {
	if raw == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return t.UTC().Format(time.RFC3339)
}

// unixMilliToRFC3339 把 unix 毫秒时间戳转成 UTC 的 RFC3339；非正值返回空串。
func unixMilliToRFC3339(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
