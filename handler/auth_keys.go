package handler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
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
	page, pageSize, err := parsePagination(c.Query("page"), c.Query("page_size"))
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	query := models.DB.Model(&models.AuthKey{})

	if search := strings.TrimSpace(c.Query("search")); search != "" {
		like := "%" + search + "%"
		query = query.Where("name LIKE ? OR key LIKE ?", like, like)
	}

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

	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.InternalServerError(c, "Failed to count auth keys: "+err.Error())
		return
	}

	var keys []models.AuthKey
	if err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&keys).Error; err != nil {
		common.InternalServerError(c, "Failed to query auth keys: "+err.Error())
		return
	}

	common.Success(c, map[string]any{
		"data":      keys,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"pages":     (total + int64(pageSize) - 1) / int64(pageSize),
	})
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

	key, err := generateAuthKey()
	if err != nil {
		common.InternalServerError(c, "Failed to generate key: "+err.Error())
		return
	}

	expiresAt, err := parseExpiresAt(req.ExpiresAt)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	ctx := c.Request.Context()

	authKey := models.AuthKey{
		Name:      req.Name,
		Key:       key,
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

	expiresAt, err := parseExpiresAt(req.ExpiresAt)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
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

func parsePagination(pageStr, pageSizeStr string) (int, int, error) {
	page := 1
	if pageStr != "" {
		p, err := strconv.Atoi(pageStr)
		if err != nil || p < 1 {
			return 0, 0, fmt.Errorf("Invalid page parameter")
		}
		page = p
	}

	pageSize := 20
	if pageSizeStr != "" {
		ps, err := strconv.Atoi(pageSizeStr)
		if err != nil || ps < 1 || ps > 100 {
			return 0, 0, fmt.Errorf("Invalid page_size parameter (1-100)")
		}
		pageSize = ps
	}
	return page, pageSize, nil
}

func parseExpiresAt(value *string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil, fmt.Errorf("Invalid expires_at format, expected RFC3339")
	}
	return &t, nil
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

func generateAuthKey() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
