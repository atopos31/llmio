package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/providers"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

const (
	gzipProbeSupported    = "supported"
	gzipProbeUnsupported  = "unsupported"
	gzipProbeInconclusive = "inconclusive"
	gzipProbeTimeout      = 30 * time.Second
)

type gzipProbeAssociation struct {
	ID            uint       `json:"id"`
	Model         string     `json:"model"`
	ProviderModel string     `json:"provider_model"`
	LastSuccessAt *time.Time `json:"last_success_at"`
}

type gzipProbeAssociationsResponse struct {
	SuggestedID uint                   `json:"suggested_id"`
	Items       []gzipProbeAssociation `json:"items"`
}

type gzipProbeRequest struct {
	ModelProviderID uint `json:"model_provider_id"`
}

type gzipProbeResponse struct {
	Status          string          `json:"status"`
	HTTPStatus      int             `json:"http_status"`
	Detail          string          `json:"detail"`
	ProbedAt        time.Time       `json:"probed_at"`
	ModelProviderID uint            `json:"model_provider_id"`
	Provider        models.Provider `json:"provider"`
}

type requestGzipRequest struct {
	Enabled *bool `json:"enabled"`
}

// GetProviderGzipAssociations 列出可用于探测的模型关联。
// 探测本身是 provider 级别的，但需要借用一条关联的模型名称与自定义请求头。
func GetProviderGzipAssociations(c *gin.Context) {
	provider, ok := loadProviderByParam(c)
	if !ok {
		return
	}
	rows, err := listGzipProbeAssociations(c.Request.Context(), provider)
	if err != nil {
		common.InternalServerError(c, "Failed to list model associations: "+err.Error())
		return
	}
	common.Success(c, gzipProbeAssociationsResponse{
		SuggestedID: suggestGzipProbeAssociation(rows),
		Items:       rows,
	})
}

// ProbeProviderGzip 同步执行一次最小请求，判断上游是否接受 gzip 请求体。
// 探测不受传输压缩总开关影响；结果按 provider 保存。
func ProbeProviderGzip(c *gin.Context) {
	provider, ok := loadProviderByParam(c)
	if !ok {
		return
	}

	var req gzipProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	assoc, modelName, err := selectGzipProbeAssociation(c.Request.Context(), provider, req.ModelProviderID)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			common.NotFound(c, err.Error())
		case errors.Is(err, errNoGzipProbeAssociation):
			common.Error(c, http.StatusConflict, err.Error())
		case errors.Is(err, errGzipProbeAssociation):
			common.BadRequest(c, err.Error())
		default:
			common.InternalServerError(c, "Failed to load model association: "+err.Error())
		}
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), gzipProbeTimeout)
	defer cancel()

	client := *providers.GetClient(gzipProbeTimeout, provider.Proxy)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	status, httpStatus, detail := runGzipProbe(ctx, &client, provider, assoc, modelName, c.Request.Header)
	updated, err := persistGzipProbeResult(c.Request.Context(), provider, status, detail)
	if err != nil {
		common.InternalServerError(c, "Failed to save probe result: "+err.Error())
		return
	}

	common.Success(c, gzipProbeResponse{
		Status:          status,
		HTTPStatus:      httpStatus,
		Detail:          truncateProbeDetail(detail),
		ProbedAt:        lo.FromPtrOr(updated.RequestGzipProbedAt, time.Now()),
		ModelProviderID: assoc.ID,
		Provider:        updated,
	})
}

// UpdateProviderRequestGzip 打开或关闭 provider 级请求压缩。
// 只允许在探测结论为支持且配置指纹仍然匹配时打开。
func UpdateProviderRequestGzip(c *gin.Context) {
	provider, ok := loadProviderByParam(c)
	if !ok {
		return
	}

	var req requestGzipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	if req.Enabled == nil {
		common.BadRequest(c, "enabled is required")
		return
	}

	if *req.Enabled {
		if provider.RequestGzipSupported == nil || !*provider.RequestGzipSupported {
			common.Error(c, http.StatusConflict, "请先检测并确认上游支持请求压缩")
			return
		}
		if provider.RequestGzipProbeHash == "" || provider.RequestGzipProbeHash != service.ProviderGzipHash(provider) {
			common.Error(c, http.StatusConflict, "检测结果已失效，请重新检测")
			return
		}
	}

	if err := models.DB.WithContext(c.Request.Context()).Model(&models.Provider{}).
		Where("id = ?", provider.ID).
		Updates(map[string]any{"request_gzip_enabled": *req.Enabled}).Error; err != nil {
		common.InternalServerError(c, "Failed to update request compression: "+err.Error())
		return
	}
	updated, err := gorm.G[models.Provider](models.DB).Where("id = ?", provider.ID).First(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve provider: "+err.Error())
		return
	}
	common.Success(c, updated)
}

var (
	errNoGzipProbeAssociation = errors.New("该提供商没有可用的模型关联，无法检测。请先在模型关联页面添加一条关联")
	errGzipProbeAssociation   = errors.New("模型关联不属于该提供商")
)

func loadProviderByParam(c *gin.Context) (models.Provider, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return models.Provider{}, false
	}
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(c.Request.Context())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Provider not found")
			return models.Provider{}, false
		}
		common.InternalServerError(c, "Database error: "+err.Error())
		return models.Provider{}, false
	}
	return provider, true
}

func listGzipProbeAssociations(ctx context.Context, provider models.Provider) ([]gzipProbeAssociation, error) {
	// SQLite 的 datetime 列以文本存储，驱动不会把聚合结果自动 Scan 成
	// time.Time；先取 Unix 秒，再在 Go 侧转换，避免依赖具体时间格式。
	type row struct {
		ID              uint   `gorm:"column:id"`
		Model           string `gorm:"column:model"`
		ProviderModel   string `gorm:"column:provider_model"`
		LastSuccessUnix *int64 `gorm:"column:last_success_unix"`
	}
	var rawRows []row
	err := models.DB.WithContext(ctx).Raw(`
		SELECT a.id AS id,
		       m.name AS model,
		       a.provider_model AS provider_model,
		       (SELECT CAST(strftime('%s', MAX(l.created_at)) AS INTEGER)
		          FROM chat_logs l
		         WHERE l.provider_name = ?
		           AND l.name = m.name
		           AND l.provider_model = a.provider_model
		           AND l.status = ?
		           AND l.deleted_at IS NULL) AS last_success_unix
		FROM model_with_providers a
		JOIN models m ON m.id = a.model_id
		WHERE a.provider_id = ?
		  AND a.deleted_at IS NULL
		  AND m.deleted_at IS NULL
		ORDER BY a.id
	`, provider.Name, string(consts.StatusSuccess), provider.ID).Scan(&rawRows).Error
	if err != nil {
		return nil, err
	}
	items := make([]gzipProbeAssociation, 0, len(rawRows))
	for _, r := range rawRows {
		var last *time.Time
		if r.LastSuccessUnix != nil {
			v := time.Unix(*r.LastSuccessUnix, 0)
			last = &v
		}
		items = append(items, gzipProbeAssociation{
			ID:            r.ID,
			Model:         r.Model,
			ProviderModel: r.ProviderModel,
			LastSuccessAt: last,
		})
	}
	return items, nil
}

func suggestGzipProbeAssociation(rows []gzipProbeAssociation) uint {
	var suggested uint
	var latest time.Time
	for _, row := range rows {
		if row.LastSuccessAt != nil && (suggested == 0 || row.LastSuccessAt.After(latest)) {
			suggested = row.ID
			latest = *row.LastSuccessAt
		}
	}
	if suggested != 0 {
		return suggested
	}
	if len(rows) > 0 {
		return rows[0].ID
	}
	return 0
}

func selectGzipProbeAssociation(ctx context.Context, provider models.Provider, associationID uint) (models.ModelWithProvider, string, error) {
	if associationID == 0 {
		rows, err := listGzipProbeAssociations(ctx, provider)
		if err != nil {
			return models.ModelWithProvider{}, "", err
		}
		associationID = suggestGzipProbeAssociation(rows)
		if associationID == 0 {
			return models.ModelWithProvider{}, "", errNoGzipProbeAssociation
		}
	}

	var assoc models.ModelWithProvider
	if err := models.DB.WithContext(ctx).
		Where("id = ? AND provider_id = ?", associationID, provider.ID).
		First(&assoc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.ModelWithProvider{}, "", errGzipProbeAssociation
		}
		return models.ModelWithProvider{}, "", err
	}

	var model models.Model
	if err := models.DB.WithContext(ctx).Where("id = ?", assoc.ModelID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.ModelWithProvider{}, "", fmt.Errorf("模型关联对应的模型不存在")
		}
		return models.ModelWithProvider{}, "", err
	}
	return assoc, model.Name, nil
}

type probeHTTPResult struct {
	Status      int
	ContentType string
	Detail      string
	Err         error
}

func sendGzipProbe(ctx context.Context, client *http.Client, provider models.Provider, assoc models.ModelWithProvider, modelName string, clientHeader http.Header, gzip bool) probeHTTPResult {
	instance, err := providers.New(provider.Type, provider.Config, provider.Proxy)
	if err != nil {
		return probeHTTPResult{Err: fmt.Errorf("create provider: %w", err)}
	}

	var body string
	switch provider.Type {
	case consts.StyleOpenAI:
		body = testOpenAI
	case consts.StyleOpenAIRes:
		body = testOpenAIRes
	case consts.StyleAnthropic:
		body = testAnthropic
	case consts.StyleGemini:
		body = testGemini
	default:
		return probeHTTPResult{Err: fmt.Errorf("unsupported provider type %q", provider.Type)}
	}

	header := service.BuildHeaders(clientHeader, lo.FromPtrOr(assoc.WithHeader, false), assoc.CustomerHeaders, false, service.HeaderVars{
		Session:       "llmio-gzip-probe",
		Model:         modelName,
		ProviderModel: assoc.ProviderModel,
	})
	req, err := instance.BuildReq(ctx, header, assoc.ProviderModel, []byte(body))
	if err != nil {
		return probeHTTPResult{Err: fmt.Errorf("build request: %w", err)}
	}
	if gzip {
		if err := providers.GzipRequestBody(req); err != nil {
			return probeHTTPResult{Err: fmt.Errorf("gzip request: %w", err)}
		}
	}

	res, err := client.Do(req)
	if err != nil {
		return probeHTTPResult{Err: err}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<10))
	detail := fmt.Sprintf("HTTP %d", res.StatusCode)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if snippet := sanitizeProbeSnippet(raw); snippet != "" {
			detail += ": " + snippet
		}
	}
	return probeHTTPResult{
		Status:      res.StatusCode,
		ContentType: strings.ToLower(res.Header.Get("Content-Type")),
		Detail:      detail,
	}
}

func runGzipProbe(ctx context.Context, client *http.Client, provider models.Provider, assoc models.ModelWithProvider, modelName string, header http.Header) (string, int, string) {
	gzipResult := sendGzipProbe(ctx, client, provider, assoc, modelName, header, true)
	if gzipResult.Err != nil {
		return gzipProbeInconclusive, 0, "gzip request failed: " + gzipResult.Err.Error()
	}
	if gzipResult.Status >= 200 && gzipResult.Status < 300 {
		if strings.Contains(gzipResult.ContentType, "text/html") {
			return gzipProbeInconclusive, gzipResult.Status, "gzip " + gzipResult.Detail + "; 返回了 HTML 页面，无法确认"
		}
		return gzipProbeSupported, gzipResult.Status, "gzip " + gzipResult.Detail
	}
	if gzipResult.Status != http.StatusBadRequest &&
		gzipResult.Status != http.StatusUnsupportedMediaType &&
		gzipResult.Status != http.StatusUnprocessableEntity {
		return gzipProbeInconclusive, gzipResult.Status, "gzip " + gzipResult.Detail
	}

	plainResult := sendGzipProbe(ctx, client, provider, assoc, modelName, header, false)
	if plainResult.Err != nil {
		return gzipProbeInconclusive, gzipResult.Status, "gzip " + gzipResult.Detail + "; plaintext request failed: " + plainResult.Err.Error()
	}
	if plainResult.Status >= 200 && plainResult.Status < 300 {
		return gzipProbeUnsupported, gzipResult.Status, "gzip " + gzipResult.Detail + "; plaintext " + plainResult.Detail
	}
	return gzipProbeInconclusive, gzipResult.Status, "gzip " + gzipResult.Detail + "; plaintext " + plainResult.Detail
}

func persistGzipProbeResult(ctx context.Context, provider models.Provider, status, detail string) (models.Provider, error) {
	detail = truncateProbeDetail(detail)
	updates := map[string]any{
		"request_gzip_probed_at":    time.Now(),
		"request_gzip_probe_detail": detail,
	}
	switch status {
	case gzipProbeSupported:
		updates["request_gzip_supported"] = true
		updates["request_gzip_probe_hash"] = service.ProviderGzipHash(provider)
	case gzipProbeUnsupported:
		updates["request_gzip_supported"] = false
		updates["request_gzip_enabled"] = false
		updates["request_gzip_probe_hash"] = service.ProviderGzipHash(provider)
	}
	if err := models.DB.WithContext(ctx).Model(&models.Provider{}).
		Where("id = ?", provider.ID).
		Updates(updates).Error; err != nil {
		return models.Provider{}, err
	}
	return gorm.G[models.Provider](models.DB).Where("id = ?", provider.ID).First(ctx)
}

func sanitizeProbeSnippet(raw []byte) string {
	text := strings.Join(strings.Fields(string(raw)), " ")
	return truncateProbeDetail(text)
}

func truncateProbeDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) <= 200 {
		return detail
	}
	detail = detail[:200]
	for len(detail) > 0 && !utf8.ValidString(detail) {
		detail = detail[:len(detail)-1]
	}
	return detail
}
