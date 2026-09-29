package handler

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 统计口径
const (
	metricByCount  = "count"  // 按调用数量统计
	metricByTokens = "tokens" // 按 Tokens 统计
)

// cachedTokensSumExpr 求和 JSON 列 prompt_tokens_details 中的 cached_tokens；NULL 与非法 JSON 均记为 0
const cachedTokensSumExpr = `COALESCE(SUM(CASE WHEN json_valid(prompt_tokens_details) THEN json_extract(prompt_tokens_details, '$.cached_tokens') ELSE 0 END), 0) as cached_tokens`

// metricsSelectExpr 汇总请求数与各 Token 口径
const metricsSelectExpr = `COUNT(id) as reqs,
	COALESCE(SUM(total_tokens), 0) as tokens,
	COALESCE(SUM(prompt_tokens), 0) as prompt_tokens, ` +
	cachedTokensSumExpr

// 折线图时间维度
const (
	rangeToday = "today"
	range24h   = "24h"
	range7d    = "7d"
	range30d   = "30d"
	range90d   = "90d"
)

// dailyRangeDays 按天聚合的时间维度及其跨度
var dailyRangeDays = map[string]int{range7d: 7, range30d: 30, range90d: 90}

// 时间桶标签格式，与下方 SQL 分桶表达式输出的一一对应
const (
	hourBucketLayout = "2006-01-02 15:04" // 对应 strftime('%Y-%m-%d %H:00', ...)
	dayBucketLayout  = "2006-01-02"       // 对应 date(...)
)

// SQL 分桶表达式。必须带 'localtime'：驱动写入的时间戳形如
// "2026-09-29 06:30:00.123+08:00"，SQLite 日期函数会先按偏移折算到 UTC，
// 少了 'localtime' 会把本地凌晨的请求算进前一天（实测 00:00 会整体丢一天）。
const (
	hourBucketExpr = `strftime('%Y-%m-%d %H:00', created_at, 'localtime')`
	dayBucketExpr  = `date(created_at, 'localtime')`
)

// startOfHour 按本地时区取整到整点。
// 不用 time.Truncate：它以 Unix 纪元为基准做整除，对非整小时偏移的时区会错位。
func startOfHour(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, t.Hour(), 0, 0, 0, t.Location())
}

// startOfDay 按本地时区取整到当日 00:00
func startOfDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}

// timelineBuckets 依据时间维度生成升序桶起点、标签格式、SQL 分桶表达式与查询上界（不含）
func timelineBuckets(rangeKey string, now time.Time) (buckets []time.Time, layout, bucketExpr string, end time.Time, ok bool) {
	switch rangeKey {
	case rangeToday:
		// 本地自然日 00:00 起逐小时，含当前所在小时
		for b := startOfDay(now); !b.After(now); b = b.Add(time.Hour) {
			buckets = append(buckets, b)
		}
		return buckets, hourBucketLayout, hourBucketExpr, buckets[len(buckets)-1].Add(time.Hour), true
	case range24h:
		// 滚动 24 小时，含当前所在小时
		last := startOfHour(now)
		for i := 23; i >= 0; i-- {
			buckets = append(buckets, last.Add(-time.Duration(i)*time.Hour))
		}
		return buckets, hourBucketLayout, hourBucketExpr, last.Add(time.Hour), true
	case range7d, range30d, range90d:
		days := dailyRangeDays[rangeKey]
		last := startOfDay(now)
		for i := days - 1; i >= 0; i-- {
			buckets = append(buckets, last.AddDate(0, 0, -i))
		}
		return buckets, dayBucketLayout, dayBucketExpr, last.AddDate(0, 0, 1), true
	default:
		return nil, "", "", time.Time{}, false
	}
}

// parseMetricBy 解析 by 查询参数，缺省按数量统计
func parseMetricBy(c *gin.Context) (string, bool) {
	switch by := c.Query("by"); by {
	case "", metricByCount:
		return metricByCount, true
	case metricByTokens:
		return metricByTokens, true
	default:
		return "", false
	}
}

// metricValueExpr 返回统计口径对应的聚合表达式，别名固定为 value
func metricValueExpr(by string) string {
	if by == metricByTokens {
		return "COALESCE(SUM(total_tokens), 0) as value"
	}
	return "COUNT(*) as value"
}

type MetricsRes struct {
	Reqs         int64 `json:"reqs"`
	Tokens       int64 `json:"tokens"`
	PromptTokens int64 `json:"prompt_tokens"`
	CachedTokens int64 `json:"cached_tokens"`
}

type metricsRow struct {
	Reqs         int64 `gorm:"column:reqs"`
	Tokens       int64 `gorm:"column:tokens"`
	PromptTokens int64 `gorm:"column:prompt_tokens"`
	CachedTokens int64 `gorm:"column:cached_tokens"`
}

func Metrics(c *gin.Context) {
	days, err := strconv.Atoi(c.Param("days"))
	if err != nil {
		common.BadRequest(c, "Invalid days parameter")
		return
	}

	now := time.Now()
	year, month, day := now.Date()
	chain := gorm.G[models.ChatLog](models.DB).Where("created_at >= ?", time.Date(year, month, day, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -days))

	var row metricsRow
	if err := chain.Select(metricsSelectExpr).Scan(c.Request.Context(), &row); err != nil {
		common.InternalServerError(c, "Failed to aggregate metrics: "+err.Error())
		return
	}

	common.Success(c, MetricsRes{
		Reqs:         row.Reqs,
		Tokens:       row.Tokens,
		PromptTokens: row.PromptTokens,
		CachedTokens: row.CachedTokens,
	})
}

type Count struct {
	Model string `json:"model"`
	Value int64  `json:"value"`
}

func Counts(c *gin.Context) {
	by, ok := parseMetricBy(c)
	if !ok {
		common.BadRequest(c, "Invalid by parameter: must be 'count' or 'tokens'")
		return
	}

	results := make([]Count, 0)
	if err := models.DB.
		Model(&models.ChatLog{}).
		Select("name as model, " + metricValueExpr(by)).
		Group("name").
		Order("value DESC").
		Scan(&results).Error; err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	const topN = 5
	if len(results) > topN {
		var othersValue int64
		for _, item := range results[topN:] {
			othersValue += item.Value
		}
		othersCount := Count{
			Model: "others",
			Value: othersValue,
		}
		results = append(results[:topN], othersCount)
	}

	common.Success(c, results)
}

type TimelinePoint struct {
	Bucket       string `json:"bucket"`        // 桶标签：按小时 "2006-01-02 15:04"，按天 "2006-01-02"
	Tokens       int64  `json:"tokens"`        // 总 tokens
	CachedTokens int64  `json:"cached_tokens"` // 缓存 tokens
}

// Timeline Tokens 用量趋势：按时间维度分桶聚合 total_tokens 与 cached_tokens，空桶补零
func Timeline(c *gin.Context) {
	buckets, layout, bucketExpr, end, ok := timelineBuckets(c.Query("range"), time.Now())
	if !ok {
		common.BadRequest(c, "Invalid range parameter: must be one of today, 24h, 7d, 30d, 90d")
		return
	}

	type timelineRow struct {
		Bucket       string `gorm:"column:bucket"`
		Tokens       int64  `gorm:"column:tokens"`
		CachedTokens int64  `gorm:"column:cached_tokens"`
	}

	rows := make([]timelineRow, 0, len(buckets))
	if err := models.DB.
		Model(&models.ChatLog{}).
		Select(bucketExpr+" as bucket, COALESCE(SUM(total_tokens), 0) as tokens, "+cachedTokensSumExpr).
		Where("created_at >= ? AND created_at < ?", buckets[0], end).
		Group("bucket").
		Order("bucket ASC").
		Scan(&rows).Error; err != nil {
		common.InternalServerError(c, "Failed to aggregate timeline: "+err.Error())
		return
	}

	byBucket := make(map[string]timelineRow, len(rows))
	for _, row := range rows {
		byBucket[row.Bucket] = row
	}

	// 空桶补零，否则折线会出现断点
	points := make([]TimelinePoint, 0, len(buckets))
	for _, bucket := range buckets {
		key := bucket.Format(layout)
		row := byBucket[key]
		points = append(points, TimelinePoint{
			Bucket:       key,
			Tokens:       row.Tokens,
			CachedTokens: row.CachedTokens,
		})
	}

	common.Success(c, points)
}

type ProjectCount struct {
	Project string `json:"project"`
	Value   int64  `json:"value"`
}

func ProjectCounts(c *gin.Context) {
	by, ok := parseMetricBy(c)
	if !ok {
		common.BadRequest(c, "Invalid by parameter: must be 'count' or 'tokens'")
		return
	}

	type authKeyMetric struct {
		AuthKeyID uint  `gorm:"column:auth_key_id"`
		Value     int64 `gorm:"column:value"`
	}

	rows := make([]authKeyMetric, 0)
	if err := models.DB.
		Model(&models.ChatLog{}).
		Select("auth_key_id, " + metricValueExpr(by)).
		Group("auth_key_id").
		Order("value DESC").
		Scan(&rows).Error; err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	ids := make([]uint, 0)
	for _, row := range rows {
		if row.AuthKeyID == 0 {
			continue
		}
		ids = append(ids, row.AuthKeyID)
	}

	keys := make([]models.AuthKey, 0)
	if len(ids) > 0 {
		if err := models.DB.
			Model(&models.AuthKey{}).
			Where("id IN ?", ids).
			Find(&keys).Error; err != nil {
			common.InternalServerError(c, err.Error())
			return
		}
	}

	keyMap := make(map[uint]string, len(keys))
	for _, key := range keys {
		keyMap[key.ID] = strings.TrimSpace(key.Name)
	}

	projectValues := make(map[string]int64)
	for _, row := range rows {
		project := "-"
		if row.AuthKeyID == 0 {
			project = "admin"
		} else if name, ok := keyMap[row.AuthKeyID]; ok && name != "" {
			project = name
		}
		projectValues[project] += row.Value
	}

	results := make([]ProjectCount, 0, len(projectValues))
	for project, value := range projectValues {
		results = append(results, ProjectCount{
			Project: project,
			Value:   value,
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Value > results[j].Value })

	const topN = 5
	if len(results) > topN {
		var othersValue int64
		for _, item := range results[topN:] {
			othersValue += item.Value
		}
		othersCount := ProjectCount{
			Project: "others",
			Value:   othersValue,
		}
		results = append(results[:topN], othersCount)
	}

	common.Success(c, results)
}
