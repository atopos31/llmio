package service

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atopos31/llmio/models"
)

// 内置节假日数据。来源 NateScarlet/holiday-cn，数据整理自国务院办公厅
// 每年发布的放假安排通知（文件内的 papers 字段保留了公告原文链接，可溯源）。
//
// 为什么要内置而不只依赖运行时拉取：本项目是离线单二进制工具，
// 部署环境常常没有外网出口。内置后：
//   - 已知年份始终可用，同步失败也不影响正确性
//   - 运行时同步成为"获取新年度数据"的更新手段，而非唯一来源
//   - 仍保留远端优先，以便当年的最终安排发布后能拿到修正
//
//go:embed holidays/*.json
var embeddedHolidays embed.FS

// embeddedHolidayFS 是内置数据的读取句柄。
//
// 声明为变量是为了给测试留一个接缝：下方的容错分支（子目录、非数字文件名、
// 损坏的 JSON、空文件）在实际构建产物里不会出现，但真出现时会导致某个年份
// 被静默跳过——所以防护要留着，也要能被测到。测试用 testing/fstest 注入。
var embeddedHolidayFS fs.FS = embeddedHolidays

// embeddedHolidaysDir 与上方 go:embed 路径保持一致。
const embeddedHolidaysDir = "holidays"

// HolidaySourceURLs 远端数据源，**按顺序尝试**，首个成功者胜出。
//
// JSDelivr 在前：它是 CDN，国内可达性与速度都明显优于 raw.githubusercontent.com。
// raw 作为回退：JSDelivr 对 GitHub 的缓存有延迟，而 raw 是权威源，
// 当年安排临时调整时 raw 会先更新。
//
// 声明为变量而非常量，以便测试把地址指向本地服务器，不依赖外网。
var HolidaySourceURLs = []string{
	"https://cdn.jsdelivr.net/gh/NateScarlet/holiday-cn@master/%d.json",
	"https://raw.githubusercontent.com/NateScarlet/holiday-cn/master/%d.json",
}

// holidayResponse 数据源的响应结构（只取需要的字段）。
type holidayResponse struct {
	Year int                 `json:"year"`
	Days []models.HolidayDay `json:"days"`
}

// HolidayFetcher 拉取某年的节假日数据。抽成函数类型以便测试注入假实现，
// 避免测试依赖外网。
type HolidayFetcher func(ctx context.Context, year int) ([]models.HolidayDay, string, error)

// ---------------------------------------------------------------------------
// 远端
// ---------------------------------------------------------------------------

// FetchHolidaysFromRemote 依次尝试各镜像，返回首个成功的结果。
// 全部失败时返回最后一个错误（而非聚合错误），保持错误信息可读。
func FetchHolidaysFromRemote(ctx context.Context, year int) ([]models.HolidayDay, string, error) {
	var lastErr error
	for _, tmpl := range HolidaySourceURLs {
		url := fmt.Sprintf(tmpl, year)
		days, err := fetchHolidayURL(ctx, url)
		if err == nil {
			return days, url, nil
		}
		lastErr = err

		// context 已取消时无需再试其他镜像
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no holiday source configured")
	}
	return nil, "", lastErr
}

// fetchHolidayURL 拉取并解析单个地址。
func fetchHolidayURL(ctx context.Context, url string) ([]models.HolidayDay, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned status %d", url, res.StatusCode)
	}
	// 限制读取体积，避免异常响应把内存吃满
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	var payload holidayResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%s returned unparsable body: %w", url, err)
	}
	if len(payload.Days) == 0 {
		return nil, fmt.Errorf("%s returned no entries", url)
	}
	return payload.Days, nil
}

// ---------------------------------------------------------------------------
// 内置
// ---------------------------------------------------------------------------

// EmbeddedHolidayYears 返回二进制内已内置的年份，升序。
func EmbeddedHolidayYears() []int {
	entries, err := fs.ReadDir(embeddedHolidayFS, embeddedHolidaysDir)
	if err != nil {
		return nil
	}
	years := make([]int, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		y, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		years = append(years, y)
	}
	sort.Ints(years)
	return years
}

// LoadEmbeddedHolidays 读取内置的某年数据。
func LoadEmbeddedHolidays(year int) ([]models.HolidayDay, string, error) {
	name := path.Join(embeddedHolidaysDir, fmt.Sprintf("%d.json", year))
	raw, err := fs.ReadFile(embeddedHolidayFS, name)
	if err != nil {
		return nil, "", fmt.Errorf("no bundled holiday data for %d", year)
	}

	var payload holidayResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, name, fmt.Errorf("bundled holiday data for %d is corrupt: %w", year, err)
	}
	if len(payload.Days) == 0 {
		return nil, name, fmt.Errorf("bundled holiday data for %d is empty", year)
	}
	return payload.Days, "bundled:" + name, nil
}

// ---------------------------------------------------------------------------
// 取数策略
// ---------------------------------------------------------------------------

// DefaultHolidayFetcher 取数策略：
//
//	远端镜像（JSDelivr → raw） → 内置数据 → 报错
//
// 远端优先的原因：当年的放假安排可能在年中调整，远端会更新而内置的不会。
// 回落到内置的原因：离线环境下必须仍然可用，否则调休判定退化为"只按周一~周五"。
//
// 两者都不可用时才返回错误，此时调用方应保留既有配置不做改动。
func DefaultHolidayFetcher(ctx context.Context, year int) ([]models.HolidayDay, string, error) {
	days, source, remoteErr := FetchHolidaysFromRemote(ctx, year)
	if remoteErr == nil && len(days) > 0 {
		return days, "remote:" + source, nil
	}

	embedded, embeddedSource, embeddedErr := LoadEmbeddedHolidays(year)
	if embeddedErr == nil {
		return embedded, embeddedSource, nil
	}

	return nil, "", fmt.Errorf(
		"no holiday data for %d: remote failed (%v), bundled failed (%v)",
		year, remoteErr, embeddedErr,
	)
}
