package handler

import (
	"context"
	"strconv"
	"strings"
	"unicode"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ProviderForwardRequest 转发透传专页的写入口。转发设置与提供商 CRUD 解耦：
// 地址标识、模式、改写策略只能通过本端点修改，避免改名称/配置时被通用更新覆盖。
type ProviderForwardRequest struct {
	// Slug 专属地址中的路径标识（/p/{slug}/...），与提供商名称解耦，改名不影响地址；
	// 置空表示撤销专属地址（仅当 Mode 为 off 时允许）。
	Slug string `json:"slug"`
	// Mode 转发模式：off 关闭 / mixed 混合 / pure 纯转发，见 models.Provider.ForwardMode。
	Mode string `json:"mode"`
	// Rewrite 模型改写策略：none 原样 / known 仅改写已绑定模型 / all 固定改写为 TargetModel。
	Rewrite string `json:"rewrite"`
	// TargetModel all 策略的固定改写目标（上游模型名）。
	TargetModel string `json:"target_model"`
}

const (
	forwardModeOff   = "off"
	forwardModeMixed = "mixed"
	forwardModePure  = "pure"
)

// UpdateProviderForward 保存提供商的转发透传设置（PUT /api/providers/:id/forward）。
func UpdateProviderForward(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}
	ctx := c.Request.Context()

	if _, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(ctx); err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Provider not found")
			return
		}
		common.InternalServerError(c, "Database error: "+err.Error())
		return
	}

	var req ProviderForwardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	mode := strings.TrimSpace(req.Mode)
	var modePtr *bool
	switch mode {
	case forwardModeOff:
	case forwardModeMixed:
		modePtr = new(false)
	case forwardModePure:
		modePtr = new(true)
	default:
		common.BadRequest(c, "invalid forward mode")
		return
	}

	rewrite := strings.TrimSpace(req.Rewrite)
	if rewrite == "" {
		rewrite = consts.ForwardRewriteDefault
	}
	switch rewrite {
	case consts.ForwardRewriteNone, consts.ForwardRewriteKnown, consts.ForwardRewriteAll:
	default:
		common.BadRequest(c, "invalid forward rewrite")
		return
	}

	slug := strings.TrimSpace(req.Slug)
	if slug != "" && !common.IsSafeForwardSlug(slug) {
		common.BadRequest(c, "invalid forward slug: 1-64 chars, start with a letter or digit, letters/digits/-/_/. elsewhere")
		return
	}
	if slug == "" && mode != forwardModeOff {
		common.BadRequest(c, "forward slug is required unless forwarding is off")
		return
	}
	if slug != "" {
		count, err := gorm.G[models.Provider](models.DB).
			Where("forward_slug = ? AND id <> ?", slug, id).Count(ctx, "id")
		if err != nil {
			common.InternalServerError(c, "Database error: "+err.Error())
			return
		}
		if count > 0 {
			common.BadRequest(c, "forward slug already in use")
			return
		}
	}

	target := strings.TrimSpace(req.TargetModel)
	if rewrite == consts.ForwardRewriteAll {
		if target == "" {
			common.BadRequest(c, "forward target model is required for full rewrite")
			return
		}
		if len(target) > 200 || strings.IndexFunc(target, unicode.IsControl) >= 0 {
			common.BadRequest(c, "invalid forward target model")
			return
		}
	} else {
		target = ""
	}

	var slugPtr *string
	if slug != "" {
		slugValue := slug
		slugPtr = &slugValue
	}

	// map 形式的 Updates 会显式写 NULL：清空地址/关闭转发必须能落库。
	// （泛型 gorm.G 的 Updates 只接受结构体，显式 NULL 得走 *gorm.DB。）
	result := models.DB.WithContext(ctx).Model(&models.Provider{}).Where("id = ?", id).Updates(map[string]any{
		"forward_slug":         slugPtr,
		"forward_mode":         modePtr,
		"forward_rewrite":      rewrite,
		"forward_target_model": target,
	})
	if result.Error != nil {
		// 预检查已经挡掉绝大多数冲突，这里是并发下的兜底。不同 SQLite 驱动/版本
		// 会给出 "UNIQUE constraint failed: ..." 或 "unique constraint failed"，
		// 大小写无关匹配，别让大小写差异把唯一冲突变成 500。
		if strings.Contains(strings.ToLower(result.Error.Error()), "unique constraint") {
			common.BadRequest(c, "forward slug already in use")
			return
		}
		common.InternalServerError(c, "Failed to update forward settings: "+result.Error.Error())
		return
	}

	updated, err := gorm.G[models.Provider](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to retrieve updated provider: "+err.Error())
		return
	}
	common.Success(c, updated)
}

// defaultForwardSlug 新建提供商时按名称推导默认地址标识：名称本身就是 URL 安全
// 字符且没有被其他提供商占用时回填，否则留空（用户可在转发专页手动设置）。
func defaultForwardSlug(ctx context.Context, name string) *string {
	if !common.IsSafeForwardSlug(name) {
		return nil
	}
	count, err := gorm.G[models.Provider](models.DB).Where("forward_slug = ?", name).Count(ctx, "id")
	if err != nil || count > 0 {
		return nil
	}
	slug := name
	return &slug
}
