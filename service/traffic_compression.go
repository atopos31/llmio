package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

// DefaultTrafficCompressionPolicy 默认全部关闭。
//
// 传输压缩改变的是实际发往公网的数据形态；在上游、代理和客户端尚未验证
// 之前，默认开启等于把兼容性风险直接放进生产请求。探测、功能开关和提供商
// 开关都要求逐层明确开启。
func DefaultTrafficCompressionPolicy() *models.TrafficCompressionPolicy {
	return &models.TrafficCompressionPolicy{}
}

// GetTrafficCompressionPolicy 读取传输压缩策略。
//
// 缺行、空值、坏 JSON 一律回默认（全关）继续服务。每请求一次单行 KV 点查，
// 与转发路径上既有的策略读取同量级。
func GetTrafficCompressionPolicy(ctx context.Context) *models.TrafficCompressionPolicy {
	config, err := gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeyTrafficCompressionPolicy).First(ctx)
	if err != nil || strings.TrimSpace(config.Value) == "" {
		return DefaultTrafficCompressionPolicy()
	}
	policy := DefaultTrafficCompressionPolicy()
	if err := json.Unmarshal([]byte(config.Value), policy); err != nil {
		return DefaultTrafficCompressionPolicy()
	}
	return policy
}

// EnsureTrafficCompressionPolicyConfig 在缺行时落一行默认策略。可重复调用。
func EnsureTrafficCompressionPolicyConfig(ctx context.Context) error {
	return models.EnsureConfigIfMissing(ctx, models.KeyTrafficCompressionPolicy, DefaultTrafficCompressionPolicy())
}

// ProviderGzipHash 是探测结论对应的配置指纹。
//
// 只包含决定上游端点的字段：类型、基础地址、代理。API Key 轮换不应让
// 探测结论失效；基础地址或代理变化则必须重新探测。
func ProviderGzipHash(provider models.Provider) string {
	baseURL := strings.TrimRight(gjson.Get(provider.Config, "base_url").String(), "/")
	sum := sha256.Sum256([]byte(provider.Type + "|" + baseURL + "|" + provider.Proxy))
	return hex.EncodeToString(sum[:])
}

// ShouldGzipUpstream 汇总四层条件。任何一层不满足都保持未压缩。
func ShouldGzipUpstream(policy *models.TrafficCompressionPolicy, provider models.Provider, bodyLen int, header http.Header) bool {
	if policy == nil || !policy.Enabled || !policy.UpstreamRequest {
		return false
	}
	if !provider.RequestGzipEnabled ||
		provider.RequestGzipSupported == nil ||
		!*provider.RequestGzipSupported {
		return false
	}
	if provider.RequestGzipProbeHash == "" || provider.RequestGzipProbeHash != ProviderGzipHash(provider) {
		return false
	}
	if bodyLen < providers.MinRequestGzipBytes {
		return false
	}
	encoding := strings.ToLower(strings.TrimSpace(header.Get("Content-Encoding")))
	return encoding == "" || encoding == "identity"
}

// isGzipRejectionStatus 是"可能是压缩请求体被拒"的状态码集合。
// 400/422 同时也是参数自愈的候选状态码，所以压缩降级必须先于自愈判定。
func isGzipRejectionStatus(code int) bool {
	return code == http.StatusBadRequest ||
		code == http.StatusUnsupportedMediaType ||
		code == http.StatusUnprocessableEntity
}

// MarkProviderRequestGzipUnsupported 在运行时确认上游拒绝压缩请求体后，
// 关闭该提供商的请求压缩并记录原因。明文重发成功后调用，避免后续请求
// 再次承担一次失败往返。
func MarkProviderRequestGzipUnsupported(ctx context.Context, providerID uint, detail string) error {
	if len(detail) > 255 {
		detail = detail[:255]
	}
	return models.DB.WithContext(ctx).Model(&models.Provider{}).
		Where("id = ?", providerID).
		Updates(map[string]any{
			"request_gzip_enabled":      false,
			"request_gzip_supported":    false,
			"request_gzip_probed_at":    time.Now(),
			"request_gzip_probe_detail": detail,
		}).Error
}
