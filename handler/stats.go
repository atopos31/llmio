package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
)

// defaultStatsWindow 未指定 from 时的默认回看窗口。
//
// 有意设默认窗口而非"不限时间"：聚合在内存中完成且有 StatsScanLimit 上限，
// 不限时间会在长期运行的实例上命中上限并静默给出不完整的数字。
const defaultStatsWindow = 7 * 24 * time.Hour

// Stats 分析聚合端点。用单端点而非按维度拆多个，是为了让一次请求对应一个
// 时间切片——前端「筛选行作用于其下所有图表」的约定因此天然成立，
// 不会出现各图之间因分别请求而产生的时间窗漂移。
//
// Query:
//
//	from, to      时间范围。支持 unix 秒 / unix 毫秒 / RFC3339 / 2006-01-02 / "2006-01-02 15:04:05"
//	granularity   分桶档位，缺省 auto
//	provider, model, name, status, ua, key_id   逗号分隔的多值筛选
func Stats(c *gin.Context) {
	filter, err := parseStatsFilter(c)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	res, _, err := service.ComputeStats(c.Request.Context(), filter)
	if err != nil {
		common.InternalServerError(c, "Failed to compute stats: "+err.Error())
		return
	}
	common.Success(c, res)
}

// StatsGranularities 返回受支持的分桶档位，供前端下拉使用。
func StatsGranularities(c *gin.Context) {
	common.Success(c, service.BucketGranularities())
}

// parseStatsFilter 从查询参数构造筛选条件。
// 抽取为独立函数以便表驱动测试，不依赖 gin 的运行时。
func parseStatsFilter(c *gin.Context) (service.StatsFilter, error) {
	var f service.StatsFilter

	to := time.Now()
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		t, err := parseFlexibleTime(raw)
		if err != nil {
			return f, fmt.Errorf("invalid to parameter: %w", err)
		}
		to = t
	}

	from := to.Add(-defaultStatsWindow)
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		t, err := parseFlexibleTime(raw)
		if err != nil {
			return f, fmt.Errorf("invalid from parameter: %w", err)
		}
		from = t
	}
	if !from.Before(to) {
		return f, fmt.Errorf("from must be earlier than to")
	}
	f.From = from
	f.To = to

	granularity := strings.TrimSpace(c.Query("granularity"))
	if granularity != "" {
		// 早失败：非法档位在此拦下，而不是等到聚合时静默退化为 auto
		if _, err := service.ResolveBucket(granularity, f.To.Sub(f.From)); err != nil {
			return f, err
		}
	}
	f.Granularity = granularity

	f.Statuses = splitCSV(c.Query("status"))
	f.Providers = splitCSV(c.Query("provider"))
	f.Models = splitCSV(c.Query("model"))
	f.Names = splitCSV(c.Query("name"))
	f.UserAgents = splitCSV(c.Query("ua"))

	keyIDs, err := parseUintCSV(c.Query("key_id"))
	if err != nil {
		return f, err
	}
	f.KeyIDs = keyIDs

	return f, nil
}

// splitCSV 拆分逗号分隔的多值参数，去除空白并丢弃空项。
func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseUintCSV 解析逗号分隔的无符号整数列表（用于 key_id）。
// 允许出现 0，它代表管理台 TOKEN 直连。
func parseUintCSV(raw string) ([]uint, error) {
	parts := splitCSV(raw)
	if len(parts) == 0 {
		return nil, nil
	}
	out := make([]uint, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid key_id %q", p)
		}
		out = append(out, uint(n))
	}
	return out, nil
}

// timeLayouts 额外接受的时间格式。RFC3339 由 time.Parse 单独处理。
var timeLayouts = []string{
	"2006-01-02",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04",
}

// parseFlexibleTime 解析多种时间表示。
//
// 纯数字按位数解释：<=10 位视为 unix 秒，11..13 位视为 unix 毫秒。
// 这样前端无论用哪种都无需自行换算——dashboard 原实现只认其中一种，
// 导致前后端来回对时间单位做手工转换，是个反复出错的点。
func parseFlexibleTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty time value")
	}

	if isAllDigits(raw) {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("%q is not a valid timestamp", raw)
		}
		switch {
		case len(raw) <= 10:
			return time.Unix(n, 0), nil
		case len(raw) <= 13:
			return time.UnixMilli(n), nil
		default:
			return time.Time{}, fmt.Errorf("%q is too long to be a timestamp", raw)
		}
	}

	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a recognized time format", raw)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
