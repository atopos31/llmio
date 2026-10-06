package modelmeta

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/atopos31/llmio/models"
	"gorm.io/gorm"
)

// DefaultPolicy 是策略的默认值。
//
// Enabled=true 是**刻意的产品选择**（§4.3）：开箱即用优先。它成立的前提是
// 另外三条同时生效——Overwrite=false、AllowDeprecated=false、源没给的
// 能力不写（见 Suggest）。少了任何一条，"默认启用"就变成"默认改坏数据"。
func DefaultPolicy() models.ModelAutofillPolicy {
	return models.ModelAutofillPolicy{
		Enabled:         true,
		Overwrite:       false,
		AllowDeprecated: false,
		Sources:         append([]string(nil), DefaultSources...),
	}
}

// LoadPolicy 读策略。**任何读不出来的情况都退回默认值，不返回错误。**
//
// 理由是调用方是一个只读的建议端点：策略读不出来时，正确的降级是"按默认
// 策略给建议"，而不是让弹窗整个失败。读失败与 JSON 坏掉都会记一条日志，
// 不会静默。
func LoadPolicy(ctx context.Context) models.ModelAutofillPolicy {
	policy := DefaultPolicy()
	config, err := gorm.G[models.Config](models.DB).Where("key = ?", models.KeyModelAutofillPolicy).First(ctx)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			slog.Warn("modelmeta: 读自动填写策略失败，按默认策略处理", "error", err)
		}
		return policy
	}
	if strings.TrimSpace(config.Value) == "" {
		return policy
	}
	if err := json.Unmarshal([]byte(config.Value), &policy); err != nil {
		slog.Warn("modelmeta: 自动填写策略不是合法 JSON，按默认策略处理", "error", err)
		return DefaultPolicy()
	}
	return normalizePolicy(policy)
}

// normalizePolicy 把策略收进这个包认识的范围：来源名去重、丢掉不认识的、
// 空列表退回默认。它同时被 LoadPolicy 与 handler 用，所以策略的来源集合
// 在两条路上是同一套判定。
func normalizePolicy(policy models.ModelAutofillPolicy) models.ModelAutofillPolicy {
	known := make(map[string]bool, len(DefaultSources)+1)
	// 认识的来源就是 Manager 里注册的那几个；用 DefaultSources 加两个常量
	// 足够——这个包只有这两个源，多写一个不存在的名字没有意义。
	for _, s := range DefaultSources {
		known[s] = true
	}
	known[SourceModelsDev] = true
	known[SourceLiteLLM] = true

	out := make([]string, 0, len(policy.Sources))
	for _, s := range policy.Sources {
		name := strings.TrimSpace(s)
		if name == "" || !known[name] || slices.Contains(out, name) {
			continue
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		out = append(out, DefaultSources...)
	}
	policy.Sources = out
	return policy
}
