package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atopos31/llmio/common"
	"github.com/atopos31/llmio/models"
	"github.com/atopos31/llmio/service"
	"github.com/gin-gonic/gin"
)

// GetPeakCalendar 读取全局工作日日历。
//
// 与通用的 GET /api/config/:key 的区别：这里返回结构化配置并补上默认值，
// 未配置过时也能直接得到可用对象，前端无需自己拼默认配置。
func GetPeakCalendar(c *gin.Context) {
	cfg := service.GetPeakCalendar(c.Request.Context())
	common.Success(c, cfg)
}

// UpdatePeakCalendar 覆盖全局工作日日历。
func UpdatePeakCalendar(c *gin.Context) {
	var req models.PeakCalendar
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := validatePeakCalendar(req); err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	if err := service.SavePeakCalendar(c.Request.Context(), req); err != nil {
		common.InternalServerError(c, "Failed to save peak calendar: "+err.Error())
		return
	}
	common.Success(c, service.GetPeakCalendar(c.Request.Context()))
}

// PreviewPeakTerms 把一套峰谷条款回放到未来一段时间，返回每个时段命中的区间。
//
// 存在的理由：分时段规则的错误（跨零点写反、星期几位错、时区搞混）
// 靠读配置很难发现，但一看时间轴就一目了然。
//
// 条款由请求体给出（编辑器要在保存前预览），日历取已保存的那份：
// 日历是全局的、在另一个页面维护，预览时按它算才是"保存后会怎样"。
func PreviewPeakTerms(c *gin.Context) {
	var req models.PeakTerms
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := validatePeakTerms(req); err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	days := 7
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 31 {
			common.BadRequest(c, "days must be between 1 and 31")
			return
		}
		days = n
	}

	cal := service.GetPeakCalendar(c.Request.Context())
	resolver := service.NewPeakResolver(req, cal)
	common.Success(c, service.PreviewSchedule(resolver, time.Now(), days))
}

// SyncPeakHolidays 从公共数据源同步指定年份的节假日与调休安排。
//
// 这一项是"工作日"判定的关键补充：中国法定节假日与调休无法由星期几推出，
// 必须依赖外部数据。同步失败时日历保持原样，前端可回退到手动填写或
// 仅按周一~周五判定。
func SyncPeakHolidays(c *gin.Context) {
	year := time.Now().Year()
	if raw := strings.TrimSpace(c.Query("year")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 2000 || n > 2100 {
			common.BadRequest(c, "year must be between 2000 and 2100")
			return
		}
		year = n
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	cfg, count, err := service.SyncHolidays(ctx, year, nil)
	if err != nil {
		// 用 502 而非 500：失败来自外部数据源，不是本服务的内部错误
		c.JSON(http.StatusBadGateway, common.Response{
			Code:    http.StatusBadGateway,
			Message: "Failed to sync holidays: " + err.Error(),
		})
		return
	}

	common.SuccessWithMessage(c, "synced", gin.H{
		"year":     year,
		"count":    count,
		"source":   cfg.HolidaySource,
		"syncedAt": cfg.HolidaySyncedAt,
		"calendar": cfg,
	})
}

// validatePeakTerms 校验峰谷条款，尽量在写入前拦下会静默出错的配置。
func validatePeakTerms(terms models.PeakTerms) error {
	for i, p := range terms.Periods {
		start, err := service.ParseClock(p.Start)
		if err != nil {
			return fmt.Errorf("period %d (%s): start %w", i+1, p.Name, err)
		}
		end, err := service.ParseClock(p.End)
		if err != nil {
			return fmt.Errorf("period %d (%s): end %w", i+1, p.Name, err)
		}
		if start == end {
			return fmt.Errorf("period %d (%s): start and end are the same, which would cover the whole day", i+1, p.Name)
		}
		for _, d := range p.Days {
			if d < 0 || d > 6 {
				return fmt.Errorf("period %d (%s): weekday %d out of range (0=Sunday, 6=Saturday)", i+1, p.Name, d)
			}
		}
	}
	return nil
}

// validatePeakCalendar 校验工作日日历。
func validatePeakCalendar(cal models.PeakCalendar) error {
	for _, d := range cal.Weekdays {
		if d < 0 || d > 6 {
			return fmt.Errorf("weekday %d out of range (0=Sunday, 6=Saturday)", d)
		}
	}
	for date, v := range cal.DateOverrides {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return fmt.Errorf("invalid date override key %q: expected YYYY-MM-DD", date)
		}
		if v != models.DateOverrideWork && v != models.DateOverrideRest {
			return fmt.Errorf("invalid date override for %s: %q (expected %q or %q)",
				date, v, models.DateOverrideWork, models.DateOverrideRest)
		}
	}
	if cal.Timezone != "" {
		if _, err := time.LoadLocation(cal.Timezone); err != nil {
			return fmt.Errorf("invalid timezone %q", cal.Timezone)
		}
	}
	return nil
}
