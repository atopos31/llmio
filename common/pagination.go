package common

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// PaginationParams 分页请求参数
type PaginationParams struct {
	Page     int `json:"page"`      // 当前页码，从1开始
	PageSize int `json:"page_size"` // 每页大小
}

// PaginationResponse 分页响应结构
type PaginationResponse struct {
	Data     any   `json:"data"`      // 数据列表
	Total    int64 `json:"total"`     // 总记录数
	Page     int   `json:"page"`      // 当前页码
	PageSize int   `json:"page_size"` // 每页大小
	Pages    int64 `json:"pages"`     // 总页数
}

// DefaultPageSize 默认每页大小
const DefaultPageSize = 20

// MaxPageSize 最大每页大小
const MaxPageSize = 100

// ParsePagination 从 Gin Context 解析分页参数
// 参数无效时返回错误信息，调用者应该使用 common.BadRequest 响应
func ParsePagination(c *gin.Context) (PaginationParams, error) {
	return ParsePaginationWithDefaults(c, 1, DefaultPageSize)
}

// ParsePaginationWithDefaults 从 Gin Context 解析分页参数（带自定义默认值）
func ParsePaginationWithDefaults(c *gin.Context, defaultPage, defaultPageSize int) (PaginationParams, error) {
	page := defaultPage
	if pageStr := c.Query("page"); pageStr != "" {
		p, err := strconv.Atoi(pageStr)
		if err != nil || p < 1 {
			return PaginationParams{}, fmt.Errorf("invalid page parameter")
		}
		page = p
	}

	pageSize := defaultPageSize
	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		ps, err := strconv.Atoi(pageSizeStr)
		if err != nil || ps < 1 || ps > MaxPageSize {
			return PaginationParams{}, fmt.Errorf("invalid page_size parameter (1-%d)", MaxPageSize)
		}
		pageSize = ps
	}

	return PaginationParams{
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// NewPaginationResponse 创建分页响应
func NewPaginationResponse(data any, total int64, params PaginationParams) PaginationResponse {
	pages := (total + int64(params.PageSize) - 1) / int64(params.PageSize)
	return PaginationResponse{
		Data:     data,
		Total:    total,
		Page:     params.Page,
		PageSize: params.PageSize,
		Pages:    pages,
	}
}

// ApplyPagination 应用分页到 GORM 查询
// 用法：query = common.ApplyPagination(query, params)
func ApplyPagination(db *gorm.DB, params PaginationParams) *gorm.DB {
	offset := (params.Page - 1) * params.PageSize
	return db.Offset(offset).Limit(params.PageSize)
}

// PaginateQuery 通用分页查询辅助函数
//
// 用法示例:
//
//	var results []Model
//	total, err := common.PaginateQuery(
//	    models.DB.Model(&Model{}),
//	    params,
//	    &results,
//	)
//
// query: 已配置好 WHERE 条件的 GORM 查询
// params: 分页参数
// dest: 结果目标切片的指针
// 返回: 总记录数和错误
func PaginateQuery(query *gorm.DB, params PaginationParams, dest any) (int64, error) {
	var total int64

	// 获取总数
	if err := query.Count(&total).Error; err != nil {
		return 0, err
	}

	// 执行分页查询
	if err := ApplyPagination(query, params).Find(dest).Error; err != nil {
		return 0, err
	}

	return total, nil
}
