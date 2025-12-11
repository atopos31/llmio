package handler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/pkg"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AuthKeyRequest struct {
	Name      string   `json:"name" binding:"required"`
	Status    *bool    `json:"status"`
	AllowAll  *bool    `json:"allow_all"`
	Models    []string `json:"models"`
	ExpiresAt *string  `json:"expires_at"`
}

func GetAuthKeys(c *gin.Context) {
	// 解析分页参数
	params, err := common.ParsePagination(c)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	// 构建查询
	query := models.DB.Model(&models.AuthKey{})

	// 搜索过滤
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + search + "%"
		query = query.Where("name LIKE ? OR key LIKE ?", like, like)
	}

	// 状态过滤
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		switch status {
		case "active":
			query = query.Where("status = ?", true)
		case "inactive":
			query = query.Where("status = ?", false)
		default:
			common.BadRequest(c, "Invalid status filter")
			return
		}
	}

	// AllowAll 过滤
	if allowAll := strings.TrimSpace(c.Query("allow_all")); allowAll != "" {
		switch allowAll {
		case "true":
			query = query.Where("allow_all = ?", true)
		case "false":
			query = query.Where("allow_all = ?", false)
		default:
			common.BadRequest(c, "Invalid allow_all filter")
			return
		}
	}

	// 执行分页查询
	var keys []models.AuthKey
	total, err := common.PaginateQuery(
		query.Order("id DESC"),
		params,
		&keys,
	)
	if err != nil {
		common.InternalServerError(c, "Failed to query auth keys: "+err.Error())
		return
	}

	// 返回分页响应
	response := common.NewPaginationResponse(keys, total, params)
	common.Success(c, response)
}

func CreateAuthKey(c *gin.Context) {
	var req AuthKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	if err := validateAuthKeyRequest(req); err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	key, err := pkg.GenerateRandomCharsKey(36)
	if err != nil {
		common.InternalServerError(c, "Failed to generate key: "+err.Error())
		return
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		parsedExpiresAt, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			common.BadRequest(c, "Invalid expires_at format, must be RFC3339")
			return
		}
		expiresAt = &parsedExpiresAt
	}

	ctx := c.Request.Context()

	authKey := models.AuthKey{
		Name:      req.Name,
		Key:       fmt.Sprintf("%s%s", consts.KeyPrefix, key),
		Status:    req.Status,
		AllowAll:  req.AllowAll,
		Models:    sanitizeModels(req.Models),
		ExpiresAt: expiresAt,
	}

	if err := gorm.G[models.AuthKey](models.DB).Create(ctx, &authKey); err != nil {
		common.InternalServerError(c, "Failed to create auth key: "+err.Error())
		return
	}

	common.Success(c, authKey)
}

func UpdateAuthKey(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID")
		return
	}

	var req AuthKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	if err := validateAuthKeyRequest(req); err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	ctx := c.Request.Context()

	if _, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", id).First(ctx); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Auth key not found")
			return
		}
		common.InternalServerError(c, "Failed to load auth key: "+err.Error())
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		parsedExpiresAt, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			common.BadRequest(c, "Invalid expires_at format, must be RFC3339")
			return
		}
		expiresAt = &parsedExpiresAt
	}

	update := models.AuthKey{
		Name:      req.Name,
		Status:    req.Status,
		AllowAll:  req.AllowAll,
		Models:    sanitizeModels(req.Models),
		ExpiresAt: expiresAt,
	}

	if _, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", id).Updates(ctx, update); err != nil {
		common.InternalServerError(c, "Failed to update auth key: "+err.Error())
		return
	}

	updated, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to load updated auth key: "+err.Error())
		return
	}

	common.Success(c, updated)
}

func DeleteAuthKey(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID")
		return
	}
	ctx := c.Request.Context()
	if _, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", id).Delete(ctx); err != nil {
		common.InternalServerError(c, "Failed to delete auth key: "+err.Error())
		return
	}
	common.SuccessWithMessage(c, "Deleted", gin.H{"id": id})
}

// ToggleAuthKeyStatus 切换 AuthKey 状态
func ToggleAuthKeyStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID")
		return
	}

	ctx := c.Request.Context()

	// 获取当前的 AuthKey
	authKey, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Auth key not found")
			return
		}
		common.InternalServerError(c, "Failed to load auth key: "+err.Error())
		return
	}

	// 切换状态
	newStatus := !*authKey.Status
	update := models.AuthKey{
		Status: &newStatus,
	}

	if _, err := gorm.G[models.AuthKey](models.DB).Where("id = ?", id).Updates(ctx, update); err != nil {
		common.InternalServerError(c, "Failed to update status: "+err.Error())
		return
	}

	// 返回更新后的记录
	authKey.Status = &newStatus
	common.Success(c, authKey)
}

// GetAuthKeysList 获取所有项目（AuthKey）的简化列表（ID 和 Name）
func GetAuthKeysList(c *gin.Context) {
	ctx := c.Request.Context()

	keys, err := gorm.G[models.AuthKey](models.DB).Select("id", "name").Find(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to query auth keys: "+err.Error())
		return
	}

	// 构建简化的返回结果
	type KeyItem struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}

	result := make([]KeyItem, len(keys))
	for i, key := range keys {
		result[i] = KeyItem{
			ID:   key.ID,
			Name: key.Name,
		}
	}
	result = append(result, KeyItem{ID: 0, Name: "admin"})

	common.Success(c, result)
}

func validateAuthKeyRequest(req AuthKeyRequest) error {
	if req.AllowAll != nil && !*req.AllowAll && len(req.Models) == 0 {
		return errors.New("请至少选择一个允许的模型或启用允许全部模型")
	}
	return nil
}

func sanitizeModels(modelsList []string) []string {
	result := make([]string, 0, len(modelsList))
	seen := make(map[string]struct{}, len(modelsList))
	for _, name := range modelsList {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}
