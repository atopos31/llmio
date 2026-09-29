package service

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/atopos31/llmio/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

func setupStatsDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stats.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.ChatLog{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	prev := models.DB
	models.DB = db
	// 必须先关连接再让 t.TempDir 清理：Windows 上文件被占用时清理会失败。
	// t.Cleanup 为 LIFO，此处注册晚于 TempDir，因此会先于它执行。
	t.Cleanup(func() {
		models.DB = prev
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

// logAt 构造一条位于指定时刻的日志，减少各用例的样板。
func logAt(id uint, ts time.Time, status string) models.ChatLog {
	return models.ChatLog{
		Model:      gorm.Model{ID: id, CreatedAt: ts},
		Status:     status,
		Name:       "gpt-4o",
		ProviderName: "prov-a",
		ProviderModel: "gpt-4o-2024",
		Currency:   "CNY",
	}
}

// ---------------------------------------------------------------------------
// 分桶
// ---------------------------------------------------------------------------

func TestResolveBucket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		granularity string
		span        time.Duration
		want        time.Duration
		wantErr     bool
	}{
		// span/60 <= 5m 时取最小档位
		{name: "auto 极小跨度取最小档", granularity: BucketAuto, span: time.Minute, want: 5 * time.Minute},
		{name: "auto 空字符串等同 auto", granularity: "", span: time.Minute, want: 5 * time.Minute},
		// 1h 的跨度 → want=1m → 首个 >= 1m 的档位是 5m
		{name: "auto 一小时跨度", granularity: "auto", span: time.Hour, want: 5 * time.Minute},
		// 30d → want=12h → 首个 >= 12h 的是 12h
		{name: "auto 30 天跨度落到 12h", granularity: "auto", span: 30 * 24 * time.Hour, want: 12 * time.Hour},
		// 365d → want=146h，仍能被最大档 7d(168h) 覆盖，由阶梯内命中
		{name: "auto 超长跨度落到最大档", granularity: "auto", span: 365 * 24 * time.Hour, want: 7 * 24 * time.Hour},
		// 600d → want=240h > 168h，阶梯全部不足，走兜底返回最大档
		{name: "auto 跨度超出最大档覆盖范围时走兜底", granularity: "auto", span: 600 * 24 * time.Hour, want: 7 * 24 * time.Hour},
		// 零跨度 → want=0 → 取最小档位
		{name: "auto 零跨度取最小档", granularity: "auto", span: 0, want: 5 * time.Minute},

		// 显式档位
		{name: "显式 5m", granularity: "5m", span: time.Hour, want: 5 * time.Minute},
		{name: "显式 15m", granularity: "15m", span: time.Hour, want: 15 * time.Minute},
		{name: "显式 30m", granularity: "30m", span: time.Hour, want: 30 * time.Minute},
		{name: "显式 1h", granularity: "1h", span: time.Hour, want: time.Hour},
		{name: "显式 2h（auto 之外单独可达，本实现与阶梯同集）", granularity: "2h", span: time.Hour, want: 2 * time.Hour},
		{name: "显式 6h", granularity: "6h", span: time.Hour, want: 6 * time.Hour},
		{name: "显式 12h", granularity: "12h", span: time.Hour, want: 12 * time.Hour},
		{name: "显式 1d", granularity: "1d", span: time.Hour, want: 24 * time.Hour},
		{name: "显式 7d", granularity: "7d", span: time.Hour, want: 7 * 24 * time.Hour},

		{name: "非法档位报错", granularity: "3m", span: time.Hour, wantErr: true},
		{name: "空档位以外的乱值报错", granularity: "1y", span: time.Hour, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveBucket(tc.granularity, tc.span)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
		})
	}
}

// TestBucketLadderIsSingleSourceOfTruth 守住"显式档位与 auto 阶梯同集合"这条约定。
// 若哪天有人只往其中一处加档位，本测试会红。
func TestBucketLadderIsSingleSourceOfTruth(t *testing.T) {
	t.Parallel()

	names := BucketGranularities()
	if len(names) != len(bucketLadder) {
		t.Fatalf("BucketGranularities 长度 %d 与阶梯 %d 不一致", len(names), len(bucketLadder))
	}
	if len(bucketByName) != len(bucketLadder) {
		t.Fatalf("档位名重复：map 有 %d 项，阶梯有 %d 项", len(bucketByName), len(bucketLadder))
	}
	for i, s := range bucketLadder {
		if names[i] != s.name {
			t.Fatalf("第 %d 项名称不符：%q != %q", i, names[i], s.name)
		}
		if got, ok := bucketByName[s.name]; !ok || got != s.d {
			t.Fatalf("档位 %q 未正确登记：%v %v", s.name, got, ok)
		}
		if i > 0 && bucketLadder[i-1].d >= s.d {
			t.Fatalf("阶梯必须严格升序：%v >= %v", bucketLadder[i-1].d, s.d)
		}
	}
}

// ---------------------------------------------------------------------------
// 分位数
// ---------------------------------------------------------------------------

func TestPercentile(t *testing.T) {
	t.Parallel()

	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	tests := []struct {
		name string
		in   []float64
		p    float64
		want float64
	}{
		// 最近秩：idx = floor((n-1)*p)，非插值
		{name: "空切片返回 0", in: nil, p: 0.5, want: 0},
		{name: "p=0 取首项", in: sorted, p: 0, want: 1},
		{name: "p 为负也取首项", in: sorted, p: -1, want: 1},
		{name: "p=1 取末项", in: sorted, p: 1, want: 10},
		{name: "p 大于 1 也取末项", in: sorted, p: 2, want: 10},
		{name: "P50 of 10 项 = idx 4", in: sorted, p: 0.50, want: 5},
		{name: "P90 of 10 项 = idx 8", in: sorted, p: 0.90, want: 9},
		{name: "P95 of 10 项 = idx 8", in: sorted, p: 0.95, want: 9},
		{name: "P99 of 10 项 = idx 8", in: sorted, p: 0.99, want: 9},
		{name: "单项切片任意 p", in: []float64{42}, p: 0.5, want: 42},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Percentile(tc.in, tc.p); got != tc.want {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
		})
	}
}

func TestSortFloats(t *testing.T) {
	t.Parallel()
	v := []float64{3, 1, 2}
	SortFloats(v)
	if v[0] != 1 || v[1] != 2 || v[2] != 3 {
		t.Fatalf("未升序：%v", v)
	}
}

// ---------------------------------------------------------------------------
// 错误归类
// ---------------------------------------------------------------------------

func TestParseStatusCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		in     string
		want   int
		wantOK bool
	}{
		{name: "标准前缀", in: `status: 429, body: {}`, want: 429, wantOK: true},
		{name: "无逗号", in: `status: 500`, want: 500, wantOK: true},
		{name: "多余空格", in: `status:    402, body: {}`, want: 402, wantOK: true},
		{name: "前导空白", in: `  status: 401, body: {}`, want: 401, wantOK: true},
		{name: "更多位数不匹配（只取 3 位锚定）", in: `status: 5000`, wantOK: false},
		{name: "非行首不匹配", in: `upstream said status: 500`, wantOK: false},
		// JSON 体中 "status":500 的引号在冒号前，不应误命中
		{name: "JSON body 不误命中", in: `status: 200, body: {"status":500}`, want: 200, wantOK: true},
		{name: "纯文本无状态码", in: `All retry failed, trace ID: abc`, wantOK: false},
		{name: "空串", in: ``, wantOK: false},
		{name: "只有 status 无数字", in: `status: abc`, wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ParseStatusCode(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("wantOK %v, got %v (value %d)", tc.wantOK, ok, got)
			}
			if ok && got != tc.want {
				t.Fatalf("want %d, got %d", tc.want, got)
			}
		})
	}
}

func TestClassifyError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       string
		wantCode string
		wantType string
	}{
		// 结构化状态码优先
		{name: "402 余额不足", in: `status: 402, body: {"error":"Insufficient Balance"}`, wantCode: "402", wantType: "余额不足"},
		{name: "429 限流", in: `status: 429, body: rate limited`, wantCode: "429", wantType: "限流"},
		{name: "401 鉴权", in: `status: 401, body: unauthorized`, wantCode: "401", wantType: "鉴权失败"},
		{name: "403 无权限", in: `status: 403, body: forbidden`, wantCode: "403", wantType: "无权限"},
		{name: "404 未找到", in: `status: 404, body: not found`, wantCode: "404", wantType: "上游未找到"},
		{name: "400 请求无效", in: `status: 400, body: bad`, wantCode: "400", wantType: "请求无效"},
		{name: "408 超时", in: `status: 408, body: request timeout`, wantCode: "timeout", wantType: "超时"},

		// 5xx 映射与区间兜底
		{name: "500 上游 5xx", in: `status: 500, body: boom`, wantCode: "5xx", wantType: "上游 5xx"},
		{name: "502 上游 5xx", in: `status: 502, body: bad gateway`, wantCode: "5xx", wantType: "上游 5xx"},
		{name: "503 上游 5xx", in: `status: 503, body: unavailable`, wantCode: "5xx", wantType: "上游 5xx"},
		{name: "504 上游 5xx", in: `status: 504, body: timeout`, wantCode: "5xx", wantType: "上游 5xx"},
		{name: "未列出的 5xx 走区间兜底", in: `status: 599, body: weird`, wantCode: "5xx", wantType: "上游 5xx"},
		{name: "未列出的 4xx 走区间兜底", in: `status: 418, body: teapot`, wantCode: "4xx", wantType: "上游 4xx"},

		// 状态码无对应类别时回落到正文规则（如 200 但正文含业务错误）
		{name: "200 且正文命中业务错误样本", in: `status: 200, body: response matched provider error sample "x"`, wantCode: "upstream-body", wantType: "上游业务错误"},
		{name: "200 且无正文特征归其他", in: `status: 200, body: fine`, wantCode: "other", wantType: "其他错误"},

		// 无状态码时的正文规则，顺序即优先级
		{name: "重试耗尽", in: `All retry failed, trace ID: abc123`, wantCode: "exhausted", wantType: "重试耗尽"},
		{name: "业务错误样本", in: `response matched provider error sample "quota"`, wantCode: "upstream-body", wantType: "上游业务错误"},
		{name: "超时", in: `context deadline exceeded`, wantCode: "timeout", wantType: "超时"},
		{name: "超时（timed out）", in: `request timed out`, wantCode: "timeout", wantType: "超时"},
		{name: "超时（timeout 通用）", in: `i/o timeout`, wantCode: "timeout", wantType: "超时"},
		// 拨号超时同时含"网络"与"超时"特征，必须归为超时（顺序保证）
		{name: "拨号超时优先归超时", in: `dial tcp 1.2.3.4:443: i/o timeout`, wantCode: "timeout", wantType: "超时"},
		{name: "网络-连接被拒", in: `dial tcp: connection refused`, wantCode: "network", wantType: "网络错误"},
		{name: "网络-连接重置", in: `read: connection reset by peer`, wantCode: "network", wantType: "网络错误"},
		{name: "网络-DNS", in: `dial tcp: lookup api.example.com: no such host`, wantCode: "network", wantType: "网络错误"},
		{name: "网络-断管", in: `write: broken pipe`, wantCode: "network", wantType: "网络错误"},
		{name: "网络-EOF", in: `unexpected EOF`, wantCode: "network", wantType: "网络错误"},
		{name: "网络-裸 EOF", in: `EOF`, wantCode: "network", wantType: "网络错误"},

		{name: "完全无特征归其他", in: `something inexplicable happened`, wantCode: "other", wantType: "其他错误"},
		{name: "空串归其他", in: ``, wantCode: "other", wantType: "其他错误"},

		// 大小写不敏感
		{name: "错误文本大写也命中", in: `CONTEXT DEADLINE EXCEEDED`, wantCode: "timeout", wantType: "超时"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			class, sample := ClassifyError(tc.in)
			if class.Code != tc.wantCode {
				t.Fatalf("code: want %q, got %q", tc.wantCode, class.Code)
			}
			if class.Type != tc.wantType {
				t.Fatalf("type: want %q, got %q", tc.wantType, class.Type)
			}
			// 样本必须是原文的前缀（未超长时即原文）
			if len(tc.in) <= errorRawLimit && sample != tc.in {
				t.Fatalf("短文本样本应与原文一致：want %q, got %q", tc.in, sample)
			}
		})
	}
}

func TestClassifyErrorTruncatesSample(t *testing.T) {
	t.Parallel()

	// 构造一个确定超过 errorRawLimit 的 ASCII 正文
	big := ""
	for i := 0; i < 800; i++ {
		big += "x"
	}
	class, sample := ClassifyError(`status: 500, body: ` + big)
	if class.Code != "5xx" {
		t.Fatalf("want 5xx, got %q", class.Code)
	}
	if sample == "" {
		t.Fatal("样本不应为空")
	}
	if len([]rune(sample)) != errorRawLimit+1 { // +1 是省略号
		t.Fatalf("样本应截断到 %d 个字符加省略号，实得 %d", errorRawLimit, len([]rune(sample)))
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "max<=0 返回空", in: "abcdef", max: 0, want: ""},
		{name: "max 为负返回空", in: "abcdef", max: -5, want: ""},
		{name: "短于上限原样返回", in: "abc", max: 10, want: "abc"},
		{name: "恰好等于上限原样返回", in: "abcde", max: 5, want: "abcde"},
		{name: "超过上限截断并加省略号", in: "abcdefg", max: 5, want: "abcde…"},
		// 按 rune 截断，不能切出无效 UTF-8
		{name: "多字节字符按 rune 截断", in: "中文测试内容", max: 3, want: "中文测…"},
		{name: "空串", in: "", max: 5, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Truncate(tc.in, tc.max); got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 计费
// ---------------------------------------------------------------------------

func TestCost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   models.ChatLog
		want float64
	}{
		{
			name: "三档分别计价",
			in: models.ChatLog{
				Usage: models.Usage{
					PromptTokens:        1_000_000,
					CompletionTokens:    500_000,
					PromptTokensDetails: models.PromptTokensDetails{CachedTokens: 200_000},
				},
				InputPrice:     2,
				CacheReadPrice: 0.5,
				OutputPrice:    4,
			},
			// (1000000-200000)*2 + 200000*0.5 + 500000*4 = 1600000+100000+2000000 = 3700000 → /1e6
			want: 3.7,
		},
		{
			name: "无缓存读",
			in: models.ChatLog{
				Usage:           models.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
				InputPrice:      1,
				CacheReadPrice:  1,
				OutputPrice:     1,
			},
			want: 2,
		},
		{
			// 缓存 token 数大于 prompt 是数据异常，不能让非缓存部分变成负数
			name: "缓存超过输入时非缓存部分钳到 0",
			in: models.ChatLog{
				Usage: models.Usage{
					PromptTokens:        100_000,
					PromptTokensDetails: models.PromptTokensDetails{CachedTokens: 200_000},
				},
				InputPrice:     100,
				CacheReadPrice: 1,
			},
			// 非缓存钳到 0：0*100 + 200000*1 = 200000 → 0.2
			want: 0.2,
		},
		{
			name: "全零",
			in:   models.ChatLog{},
			want: 0,
		},
		{
			name: "负数单价",
			in: models.ChatLog{
				Usage:      models.Usage{PromptTokens: 1_000_000},
				InputPrice: -1,
			},
			want: -1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Cost(tc.in)
			if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 筛选
// ---------------------------------------------------------------------------

func TestStatsFilterMatch(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	mk := func() models.ChatLog {
		l := logAt(1, base, consts.StatusSuccess)
		l.Name = "gpt-4o"
		l.ProviderName = "prov-a"
		l.ProviderModel = "gpt-4o-2024"
		l.UserAgent = "claude-cli/1.0"
		l.AuthKeyID = 7
		return l
	}

	tests := []struct {
		name string
		f    StatsFilter
		want bool
	}{
		{name: "空筛选全通过", f: StatsFilter{}, want: true},

		// 窗口为左闭右开
		{name: "From 之前被排除", f: StatsFilter{From: base.Add(time.Second)}, want: false},
		{name: "恰在 From 上保留", f: StatsFilter{From: base}, want: true},
		{name: "恰在 To 上排除", f: StatsFilter{To: base}, want: false},
		{name: "To 之后被排除", f: StatsFilter{To: base.Add(-time.Second)}, want: false},
		{name: "落在窗口内保留", f: StatsFilter{From: base.Add(-time.Hour), To: base.Add(time.Hour)}, want: true},

		// 状态精确匹配
		{name: "状态命中", f: StatsFilter{Statuses: []string{consts.StatusSuccess}}, want: true},
		{name: "状态多值其一命中", f: StatsFilter{Statuses: []string{consts.StatusError, consts.StatusSuccess}}, want: true},
		{name: "状态未命中", f: StatsFilter{Statuses: []string{consts.StatusError}}, want: false},
		{name: "状态不做子串匹配", f: StatsFilter{Statuses: []string{"succ"}}, want: false},

		// 供应商 / 模型 / 请求名 / UA 为子串匹配
		{name: "供应商子串命中", f: StatsFilter{Providers: []string{"prov"}}, want: true},
		{name: "供应商子串未命中", f: StatsFilter{Providers: []string{"other"}}, want: false},
		{name: "供应商多值其一命中", f: StatsFilter{Providers: []string{"zzz", "prov-a"}}, want: true},
		{name: "模型子串命中", f: StatsFilter{Models: []string{"gpt-4o-2024"}}, want: true},
		{name: "模型子串未命中", f: StatsFilter{Models: []string{"claude"}}, want: false},
		{name: "请求名子串命中", f: StatsFilter{Names: []string{"gpt"}}, want: true},
		{name: "请求名未命中", f: StatsFilter{Names: []string{"llama"}}, want: false},
		{name: "UA 子串命中", f: StatsFilter{UserAgents: []string{"claude"}}, want: true},
		{name: "UA 未命中", f: StatsFilter{UserAgents: []string{"curl"}}, want: false},

		// KeyID 精确匹配，且 0（admin）可被显式选中
		{name: "KeyID 命中", f: StatsFilter{KeyIDs: []uint{7}}, want: true},
		{name: "KeyID 多值其一命中", f: StatsFilter{KeyIDs: []uint{1, 7}}, want: true},
		{name: "KeyID 未命中", f: StatsFilter{KeyIDs: []uint{99}}, want: false},
		{name: "KeyID 为 0 时未命中非 0 日志", f: StatsFilter{KeyIDs: []uint{0}}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.f.match(mk()); got != tc.want {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
		})
	}
}

func TestStatsFilterMatchAdminKeyID(t *testing.T) {
	t.Parallel()
	l := logAt(1, time.Now(), consts.StatusSuccess)
	l.AuthKeyID = 0
	if !(StatsFilter{KeyIDs: []uint{0}}).match(l) {
		t.Fatal("KeyID 0（admin）应可被显式选中")
	}
}

func TestContainsHelpers(t *testing.T) {
	t.Parallel()

	if !containsExact([]string{"a", "b"}, "b") {
		t.Fatal("containsExact 应命中")
	}
	if containsExact([]string{"a", "b"}, "ab") {
		t.Fatal("containsExact 不应做子串匹配")
	}
	if containsExact(nil, "a") {
		t.Fatal("nil 切片不应命中")
	}
	// 注意方向：判定的是"日志值是否包含筛选词"
	if !containsSubstr([]string{"gpt"}, "gpt-4o") {
		t.Fatal("containsSubstr 应命中")
	}
	if containsSubstr([]string{"gpt-4o"}, "gpt") {
		t.Fatal("containsSubstr 方向应单向：筛选词长于值时不应命中")
	}
	if containsSubstr(nil, "x") {
		t.Fatal("nil 切片不应命中")
	}
}

// ---------------------------------------------------------------------------
// 聚合
// ---------------------------------------------------------------------------

func TestAggregateKPI(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	mk := func(status string, prompt, cached, completion int64, retry int, cur string) models.ChatLog {
		l := logAt(1, base, status)
		l.PromptTokens = prompt
		l.CompletionTokens = completion
		l.TotalTokens = prompt + completion
		l.PromptTokensDetails = models.PromptTokensDetails{CachedTokens: cached}
		l.Retry = retry
		l.Currency = cur
		return l
	}

	logs := []models.ChatLog{
		mk(consts.StatusSuccess, 100, 40, 50, 0, ""),
		mk(consts.StatusSuccess, 200, 10, 100, 1, "CNY"),
		mk(consts.StatusError, 300, 0, 60, 2, "CNY"),
		mk(consts.StatusRunning, 400, 100, 0, 0, "CNY"),
		// 未知状态：只计入 Total，不计入任何分项
		mk("weird", 50, 0, 0, 0, "CNY"),
	}

	res := AggregateWithBucket(logs, StatsFilter{}, time.Hour)

	if res.KPI.Total != 5 {
		t.Fatalf("Total want 5, got %d", res.KPI.Total)
	}
	if res.KPI.Success != 2 {
		t.Fatalf("Success want 2, got %d", res.KPI.Success)
	}
	if res.KPI.Failed != 1 {
		t.Fatalf("Failed want 1, got %d", res.KPI.Failed)
	}
	if res.KPI.Running != 1 {
		t.Fatalf("Running want 1, got %d", res.KPI.Running)
	}
	// Finished 排除 running 与未知状态
	if res.KPI.Finished != 3 {
		t.Fatalf("Finished want 3, got %d", res.KPI.Finished)
	}
	// 成功率分母为 success+failed=3，排除 running
	if diff := res.KPI.SuccessRate - (2.0/3.0*100); diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("SuccessRate want %v, got %v", 2.0/3.0*100, res.KPI.SuccessRate)
	}
	if res.KPI.PromptTokens != 1050 {
		t.Fatalf("PromptTokens want 1050, got %d", res.KPI.PromptTokens)
	}
	if res.KPI.CompletionTokens != 210 {
		t.Fatalf("CompletionTokens want 210, got %d", res.KPI.CompletionTokens)
	}
	if res.KPI.CachedTokens != 150 {
		t.Fatalf("CachedTokens want 150, got %d", res.KPI.CachedTokens)
	}
	if diff := res.KPI.CacheHitRate - (150.0/1050.0*100); diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("CacheHitRate got %v", res.KPI.CacheHitRate)
	}
	if res.KPI.TotalRetries != 3 {
		t.Fatalf("TotalRetries want 3, got %d", res.KPI.TotalRetries)
	}
	// 币种取首条非空值（首条为空则顺延到第二条）
	if res.KPI.Currency != "CNY" {
		t.Fatalf("Currency want CNY, got %q", res.KPI.Currency)
	}
}

func TestAggregateSuccessRateZeroDenominator(t *testing.T) {
	t.Parallel()

	// 全部在途：分母为 0，成功率与重试率都必须返回 0 而不是 NaN
	logs := []models.ChatLog{
		logAt(1, time.Now(), consts.StatusRunning),
		logAt(2, time.Now(), consts.StatusRunning),
	}
	res := AggregateWithBucket(logs, StatsFilter{}, time.Hour)

	if res.KPI.Finished != 0 {
		t.Fatalf("Finished want 0, got %d", res.KPI.Finished)
	}
	if res.KPI.SuccessRate != 0 {
		t.Fatalf("分母为 0 时成功率应为 0，实得 %v", res.KPI.SuccessRate)
	}
	if res.KPI.CacheHitRate != 0 {
		t.Fatalf("prompt 为 0 时缓存命中率应为 0，实得 %v", res.KPI.CacheHitRate)
	}
	if res.KPI.RetryRate != 0 {
		t.Fatalf("重试率应为 0，实得 %v", res.KPI.RetryRate)
	}
}

func TestAggregateEmptyInput(t *testing.T) {
	t.Parallel()

	res := AggregateWithBucket(nil, StatsFilter{}, time.Hour)

	if res.KPI.Total != 0 {
		t.Fatalf("Total want 0, got %d", res.KPI.Total)
	}
	// 所有集合都必须是非 nil 的空切片，保证 JSON 序列化为 [] 而不是 null
	if res.Trend == nil || res.ByModel == nil || res.ByProvider == nil ||
		res.ByKey == nil || res.ByName == nil || res.ByUserAgent == nil ||
		res.Errors == nil || res.ErrorTrend == nil ||
		res.TopTps == nil || res.Slowest == nil || res.RecentError == nil {
		t.Fatal("空输入的集合应为非 nil 空切片")
	}
	if res.Latency.FirstChunk.List == nil || res.Latency.Tps.List == nil || res.Latency.Proxy.List == nil {
		t.Fatal("延迟分布列表应为非 nil 空切片，避免前端拿到 null")
	}
	if res.BucketMs <= 0 {
		t.Fatalf("BucketMs 应已被解析，实得 %d", res.BucketMs)
	}
}

func TestAggregateTrendBuckets(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	mk := func(offset time.Duration, status string, tokens int64, tps float64, fc time.Duration) models.ChatLog {
		l := logAt(1, base.Add(offset), status)
		l.TotalTokens = tokens
		l.PromptTokens = tokens / 2
		l.CompletionTokens = tokens - l.PromptTokens
		l.Tps = tps
		l.FirstChunkTime = fc
		return l
	}

	logs := []models.ChatLog{
		// 桶 1（10:00）
		mk(0, consts.StatusSuccess, 100, 10, 100*time.Millisecond),
		mk(time.Minute, consts.StatusError, 50, 0, 0),
		// 跳过 10:01 这一桶，用于验证空桶补齐
		// 桶 3（10:02）
		mk(2*time.Hour+2*time.Minute, consts.StatusSuccess, 200, 20, 300*time.Millisecond),
	}

	res := AggregateWithBucket(logs, StatsFilter{}, time.Hour)

	if len(res.Trend) != 3 {
		t.Fatalf("应补齐为 3 个桶，实得 %d", len(res.Trend))
	}
	for i := 1; i < len(res.Trend); i++ {
		if res.Trend[i].Ts-res.Trend[i-1].Ts != time.Hour.Milliseconds() {
			t.Fatalf("桶间隔应恒为 1h：%v", res.Trend)
		}
	}

	// 第一个桶：2 条，1 成功 1 失败，Token 合计 150
	if res.Trend[0].Total != 2 || res.Trend[0].Success != 1 || res.Trend[0].Error != 1 {
		t.Fatalf("桶 1 计数不符：%+v", res.Trend[0])
	}
	if res.Trend[0].Tokens != 150 || res.Trend[0].Prompt != 75 || res.Trend[0].Completion != 75 {
		t.Fatalf("桶 1 token 不符：%+v", res.Trend[0])
	}
	if diff := res.Trend[0].AvgTps - 5; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("桶 1 平均 TPS 应为 (10+0)/2=5，实得 %v", res.Trend[0].AvgTps)
	}
	if diff := res.Trend[0].AvgFirstChunkMs - 50; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("桶 1 平均首包应为 (100+0)/2=50ms，实得 %v", res.Trend[0].AvgFirstChunkMs)
	}

	// 中间那个桶是补齐出来的全零桶
	if res.Trend[1].Total != 0 || res.Trend[1].Tokens != 0 {
		t.Fatalf("中间桶应为空：%+v", res.Trend[1])
	}

	// 末桶
	if res.Trend[2].Total != 1 || res.Trend[2].Success != 1 {
		t.Fatalf("桶 3 计数不符：%+v", res.Trend[2])
	}
}

func TestAggregateGroupsSortedByTotalThenName(t *testing.T) {
	t.Parallel()

	base := time.Now()
	mk := func(name, provider string, status string) models.ChatLog {
		l := logAt(1, base, status)
		l.Name = name
		l.ProviderName = provider
		l.PromptTokens = 100
		l.CompletionTokens = 20
		l.TotalTokens = 120
		l.PromptTokensDetails = models.PromptTokensDetails{CachedTokens: 10}
		l.Tps = 5
		l.FirstChunkTime = 200 * time.Millisecond
		l.ProxyTime = 400 * time.Millisecond
		return l
	}

	logs := []models.ChatLog{
		// beta 2 条（1 成功 1 失败）
		mk("beta", "p1", consts.StatusSuccess),
		mk("beta", "p1", consts.StatusError),
		// alpha 2 条（同量，按名称序应在 beta 之后？——同 Total 时按名称升序，alpha 在前）
		mk("alpha", "p2", consts.StatusSuccess),
		mk("alpha", "p2", consts.StatusSuccess),
		// gamma 1 条
		mk("gamma", "p3", consts.StatusRunning),
	}

	res := AggregateWithBucket(logs, StatsFilter{}, time.Hour)

	if len(res.ByName) != 3 {
		t.Fatalf("应有 3 个分组，实得 %d", len(res.ByName))
	}
	// 同 Total=2 时 alpha 在 beta 前
	if res.ByName[0].Name != "alpha" || res.ByName[1].Name != "beta" || res.ByName[2].Name != "gamma" {
		t.Fatalf("分组排序不符：%s,%s,%s", res.ByName[0].Name, res.ByName[1].Name, res.ByName[2].Name)
	}

	alpha := res.ByName[0]
	if alpha.Total != 2 || alpha.Success != 2 || alpha.Error != 0 {
		t.Fatalf("alpha 计数不符：%+v", alpha)
	}
	if alpha.SuccessRate != 100 {
		t.Fatalf("alpha 成功率应为 100，实得 %v", alpha.SuccessRate)
	}
	if alpha.TotalTokens != 240 || alpha.Cached != 20 {
		t.Fatalf("alpha token 不符：%+v", alpha)
	}
	if diff := alpha.CacheHitRate - 10.0; diff > 1e-9 || diff < -1e-9 { // 20/200
		t.Fatalf("alpha 缓存命中率应为 10，实得 %v", alpha.CacheHitRate)
	}
	if alpha.AvgTps != 5 || alpha.MaxTps != 5 {
		t.Fatalf("alpha TPS 不符：%+v", alpha)
	}
	if diff := alpha.AvgFirstChunkMs - 200; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("alpha 平均首包应为 200ms，实得 %v", alpha.AvgFirstChunkMs)
	}
	if diff := alpha.AvgProxyMs - 400; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("alpha 平均代理耗时应为 400ms，实得 %v", alpha.AvgProxyMs)
	}

	beta := res.ByName[1]
	if beta.SuccessRate != 50 {
		t.Fatalf("beta 成功率应为 50，实得 %v", beta.SuccessRate)
	}

	// 只有 running 的分组：successSeen 为 0，均值类字段应保持 0 而不是除零
	gamma := res.ByName[2]
	if gamma.AvgTps != 0 || gamma.AvgFirstChunkMs != 0 || gamma.AvgProxyMs != 0 {
		t.Fatalf("无成功请求的分组均值应为 0：%+v", gamma)
	}
	// 分组内无成功请求 → 成功率分母为 0 → 0
	if gamma.SuccessRate != 0 {
		t.Fatalf("gamma 成功率应为 0，实得 %v", gamma.SuccessRate)
	}
}

func TestAggregateGroupEmptyNameBecomesDash(t *testing.T) {
	t.Parallel()

	l := logAt(1, time.Now(), consts.StatusSuccess)
	l.Name = ""
	l.ProviderName = ""
	l.UserAgent = ""
	res := AggregateWithBucket([]models.ChatLog{l}, StatsFilter{}, time.Hour)

	for _, g := range [][]GroupStat{res.ByName, res.ByProvider, res.ByUserAgent} {
		if len(g) != 1 || g[0].Name != "-" {
			t.Fatalf("空名称分组应归一为 %q，实得 %+v", "-", g)
		}
	}
}

func TestAggregateByKeyLabels(t *testing.T) {
	t.Parallel()

	a := logAt(1, time.Now(), consts.StatusSuccess)
	a.AuthKeyID = 0 // admin
	b := logAt(2, time.Now(), consts.StatusSuccess)
	b.AuthKeyID = 42

	res := AggregateWithBucket([]models.ChatLog{a, b}, StatsFilter{}, time.Hour)

	names := map[string]bool{}
	for _, g := range res.ByKey {
		names[g.Name] = true
	}
	if !names["admin"] {
		t.Fatal("AuthKeyID 0 应展示为 admin")
	}
	if !names["42"] {
		t.Fatalf("AuthKeyID 42 应展示为 \"42\"，实得 %+v", names)
	}
}

func TestAggregateErrors(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	mkErr := func(id uint, ts time.Time, provider, name, errText string) models.ChatLog {
		l := logAt(id, ts, consts.StatusError)
		l.ProviderName = provider
		l.Name = name
		l.Error = errText
		return l
	}

	// rate 类 2 条（不同供应商/模型），balance 类 1 条，且 rate 类样本数上限为 3
	logs := []models.ChatLog{
		mkErr(1, base, "p1", "gpt", `status: 429, body: rate limited`),
		mkErr(2, base, "p2", "claude", `status: 429, body: rate limited`),
		mkErr(3, base.Add(time.Hour), "p1", "gpt", `status: 402, body: insufficient balance`),
		// 成功请求不应进入错误分析
		logAt(4, base, consts.StatusSuccess),
	}

	res := AggregateWithBucket(logs, StatsFilter{}, time.Hour)

	if len(res.Errors) != 2 {
		t.Fatalf("应有 2 个错误类别，实得 %d", len(res.Errors))
	}
	// 数量降序：429 在前
	if res.Errors[0].Code != "429" || res.Errors[0].Count != 2 {
		t.Fatalf("首个错误类别不符：%+v", res.Errors[0])
	}
	if res.Errors[1].Code != "402" || res.Errors[1].Count != 1 {
		t.Fatalf("第二个错误类别不符：%+v", res.Errors[1])
	}
	// 受影响供应商/模型
	if len(res.Errors[0].Providers) != 2 {
		t.Fatalf("429 应波及 2 个供应商：%+v", res.Errors[0].Providers)
	}
	if len(res.Errors[0].Models) != 2 {
		t.Fatalf("429 应波及 2 个模型：%+v", res.Errors[0].Models)
	}
	// 样本
	if len(res.Errors[0].Samples) != 2 {
		t.Fatalf("429 应有 2 条样本：%+v", res.Errors[0].Samples)
	}
	if res.Errors[0].Samples[0].ID != 1 {
		t.Fatalf("样本应带日志 ID：%+v", res.Errors[0].Samples)
	}
	if res.Errors[0].Samples[0].Error == "" {
		t.Fatal("样本应保留错误文本")
	}

	// 错误时间序列：两个桶各 2 与 1
	if len(res.ErrorTrend) != 2 {
		t.Fatalf("错误趋势应有 2 个桶，实得 %d", len(res.ErrorTrend))
	}
	if res.ErrorTrend[0].Error != 2 || res.ErrorTrend[1].Error != 1 {
		t.Fatalf("错误趋势计数不符：%+v", res.ErrorTrend)
	}
	// 错误趋势的其它字段不应被填充
	if res.ErrorTrend[0].Total != 0 || res.ErrorTrend[0].Tokens != 0 {
		t.Fatalf("错误趋势只填 Error 字段：%+v", res.ErrorTrend[0])
	}
}

func TestAggregateErrorSampleCap(t *testing.T) {
	t.Parallel()

	base := time.Now()
	logs := make([]models.ChatLog, 0, 10)
	for i := 1; i <= 10; i++ {
		l := logAt(uint(i), base, consts.StatusError)
		l.Error = `status: 500, body: boom`
		logs = append(logs, l)
	}

	res := AggregateWithBucket(logs, StatsFilter{}, time.Hour)
	if len(res.Errors) != 1 {
		t.Fatalf("应聚为 1 类，实得 %d", len(res.Errors))
	}
	if len(res.Errors[0].Samples) != maxErrorSamples {
		t.Fatalf("样本上限应为 %d，实得 %d", maxErrorSamples, len(res.Errors[0].Samples))
	}
	if res.Errors[0].Count != 10 {
		t.Fatalf("计数应为 10，实得 %d", res.Errors[0].Count)
	}
}

func TestAggregateErrorGroupTopN(t *testing.T) {
	t.Parallel()

	// 构造 7 个不同供应商的同类错误，验证受影响供应商被截断到 topGroupLimit
	base := time.Now()
	logs := make([]models.ChatLog, 0, 7)
	for i := 1; i <= 7; i++ {
		l := logAt(uint(i), base, consts.StatusError)
		l.Error = `status: 429, body: rate limited`
		l.ProviderName = fmt.Sprintf("prov-%d", i)
		l.Name = fmt.Sprintf("model-%d", i)
		logs = append(logs, l)
	}

	res := AggregateWithBucket(logs, StatsFilter{}, time.Hour)
	if len(res.Errors[0].Providers) != topGroupLimit {
		t.Fatalf("受影响供应商应截断到 %d，实得 %d", topGroupLimit, len(res.Errors[0].Providers))
	}
	if len(res.Errors[0].Models) != topGroupLimit {
		t.Fatalf("受影响模型应截断到 %d，实得 %d", topGroupLimit, len(res.Errors[0].Models))
	}
}

func TestAggregateErrorsSameCountStableByCode(t *testing.T) {
	t.Parallel()

	base := time.Now()
	logs := []models.ChatLog{
		func() models.ChatLog {
			l := logAt(1, base, consts.StatusError)
			l.Error = `status: 500, body: x`
			return l
		}(),
		func() models.ChatLog {
			l := logAt(2, base, consts.StatusError)
			l.Error = `status: 401, body: y`
			return l
		}(),
	}

	// 数量相同时按 code 升序，保证结果稳定（不随 map 迭代顺序变化）
	for i := 0; i < 20; i++ {
		res := AggregateWithBucket(logs, StatsFilter{}, time.Hour)
		if res.Errors[0].Code != "401" || res.Errors[1].Code != "5xx" {
			t.Fatalf("同数量时应按 code 稳定排序，实得 %s,%s", res.Errors[0].Code, res.Errors[1].Code)
		}
	}
}

func TestAggregateLatency(t *testing.T) {
	t.Parallel()

	base := time.Now()
	// 10 条成功样本，首包 0.1s..1.0s、TPS 10..100、代理耗时 1s..10s
	logs := make([]models.ChatLog, 0, 12)
	for i := 1; i <= 10; i++ {
		l := logAt(uint(i), base, consts.StatusSuccess)
		l.FirstChunkTime = time.Duration(i*100) * time.Millisecond
		l.Tps = float64(i * 10)
		l.ProxyTime = time.Duration(i) * time.Second
		logs = append(logs, l)
	}
	// 失败与在途不计入延迟样本，且刻意给极端值以证明它们被排除
	for i := 11; i <= 12; i++ {
		l := logAt(uint(i), base, consts.StatusError)
		l.FirstChunkTime = 9 * time.Second
		l.Tps = 999
		l.ProxyTime = 9 * time.Second
		logs = append(logs, l)
	}

	res := AggregateWithBucket(logs, StatsFilter{}, time.Hour)

	if len(res.Latency.FirstChunk.List) != 10 {
		t.Fatalf("首包样本应只含 10 条成功请求，实得 %d", len(res.Latency.FirstChunk.List))
	}

	// 最近秩：n=10 时 idx = floor(9*p)。
	// P50→idx4=0.5s；P90/P95/P99→idx8=0.9s（小样本下三者会收敛到同一秩，这是该定义的正确行为）
	if diff := res.Latency.FirstChunk.P50 - 0.5; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("首包 P50 应为 0.5s，实得 %v", res.Latency.FirstChunk.P50)
	}
	if diff := res.Latency.FirstChunk.P90 - 0.9; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("首包 P90 应为 0.9s，实得 %v", res.Latency.FirstChunk.P90)
	}
	if diff := res.Latency.FirstChunk.P99 - 0.9; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("首包 P99 应为 0.9s（最近秩在 n=10 时与 P90 同秩），实得 %v", res.Latency.FirstChunk.P99)
	}
	if diff := res.Latency.FirstChunk.Max - 1.0; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("首包 Max 应为 1.0s，实得 %v", res.Latency.FirstChunk.Max)
	}
	if diff := res.Latency.FirstChunk.Avg - 0.55; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("首包 Avg 应为 0.55s，实得 %v", res.Latency.FirstChunk.Avg)
	}

	// TPS：10..100
	if diff := res.Latency.Tps.Avg - 55; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("TPS 均值应为 55，实得 %v", res.Latency.Tps.Avg)
	}
	if res.Latency.Tps.Max != 100 {
		t.Fatalf("TPS 最大值应为 100，实得 %v", res.Latency.Tps.Max)
	}

	// 代理耗时：1s..10s（毫秒口径）
	if diff := res.Latency.Proxy.Avg - 5500; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("代理耗时均值应为 5500ms，实得 %v", res.Latency.Proxy.Avg)
	}
	// 列表需升序，供前端直接画直方图
	for i := 1; i < len(res.Latency.Proxy.List); i++ {
		if res.Latency.Proxy.List[i] < res.Latency.Proxy.List[i-1] {
			t.Fatalf("延迟列表必须升序：%v", res.Latency.Proxy.List)
		}
	}
}

func TestAggregateLeaderboards(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	mk := func(id uint, offset time.Duration, status string, tps float64, fc time.Duration) models.ChatLog {
		l := logAt(id, base.Add(offset), status)
		l.Tps = tps
		l.FirstChunkTime = fc
		l.CompletionTokens = 123
		l.PromptTokens = 456
		return l
	}

	logs := []models.ChatLog{
		mk(1, 0, consts.StatusSuccess, 10, 300*time.Millisecond),
		mk(2, time.Minute, consts.StatusSuccess, 50, 100*time.Millisecond),
		mk(3, 2*time.Minute, consts.StatusSuccess, 30, 200*time.Millisecond),
		// 失败请求不应进入 TPS/最慢榜，但应进入最近错误榜
		mk(4, 3*time.Minute, consts.StatusError, 999, 900*time.Millisecond),
	}

	res := AggregateWithBucket(logs, StatsFilter{}, time.Hour)

	// TPS 降序
	if len(res.TopTps) != 3 {
		t.Fatalf("TPS 榜应只含成功请求（3 条），实得 %d", len(res.TopTps))
	}
	if res.TopTps[0].ID != 2 || res.TopTps[1].ID != 3 || res.TopTps[2].ID != 1 {
		t.Fatalf("TPS 榜应为 2,3,1，实得 %d,%d,%d", res.TopTps[0].ID, res.TopTps[1].ID, res.TopTps[2].ID)
	}
	// 首包耗时降序
	if res.Slowest[0].ID != 1 || res.Slowest[1].ID != 3 || res.Slowest[2].ID != 2 {
		t.Fatalf("最慢榜应为 1,3,2，实得 %d,%d,%d", res.Slowest[0].ID, res.Slowest[1].ID, res.Slowest[2].ID)
	}
	// 最近错误榜按时间倒序
	if len(res.RecentError) != 1 || res.RecentError[0].ID != 4 {
		t.Fatalf("最近错误榜不符：%+v", res.RecentError)
	}
	// 榜上行需带齐展示字段
	row := res.TopTps[0]
	if row.CompletionToken != 123 || row.PromptToken != 456 {
		t.Fatalf("榜上行 token 字段不符：%+v", row)
	}
	if row.Model == "" || row.Provider == "" || row.KeyName == "" {
		t.Fatalf("榜上行应带模型/供应商/Key 名：%+v", row)
	}
}

func TestAggregateLeaderboardLimit(t *testing.T) {
	t.Parallel()

	base := time.Now()
	logs := make([]models.ChatLog, 0, 25)
	for i := 1; i <= 25; i++ {
		l := logAt(uint(i), base.Add(time.Duration(i)*time.Second), consts.StatusSuccess)
		l.Tps = float64(i)
		logs = append(logs, l)
	}

	res := AggregateWithBucket(logs, StatsFilter{}, time.Hour)
	if len(res.TopTps) != leaderboardLimit {
		t.Fatalf("TPS 榜上限应为 %d，实得 %d", leaderboardLimit, len(res.TopTps))
	}
	if len(res.Slowest) != leaderboardLimit {
		t.Fatalf("最慢榜上限应为 %d，实得 %d", leaderboardLimit, len(res.Slowest))
	}
}

func TestAggregateAppliesFilter(t *testing.T) {
	t.Parallel()

	base := time.Now()
	keep := logAt(1, base, consts.StatusSuccess)
	keep.ProviderName = "target"
	drop := logAt(2, base, consts.StatusSuccess)
	drop.ProviderName = "other"

	res := AggregateWithBucket([]models.ChatLog{keep, drop}, StatsFilter{Providers: []string{"target"}}, time.Hour)
	if res.KPI.Total != 1 {
		t.Fatalf("筛选后应只剩 1 条，实得 %d", res.KPI.Total)
	}
	if len(res.ByProvider) != 1 || res.ByProvider[0].Name != "target" {
		t.Fatalf("分组应只含 target：%+v", res.ByProvider)
	}
}

func TestAggregateResolvesBucketFromFilter(t *testing.T) {
	t.Parallel()

	// 走 Aggregate（而非 AggregateWithBucket），验证档位解析被串起来
	base := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	res := Aggregate(nil, StatsFilter{
		From:        base,
		To:          base.Add(30 * 24 * time.Hour),
		Granularity: "6h",
	})
	if res.BucketMs != (6 * time.Hour).Milliseconds() {
		t.Fatalf("BucketMs 应为 6h，实得 %d", res.BucketMs)
	}
	// 范围回填
	if res.Range.From != base.UnixMilli() {
		t.Fatalf("Range.From 不符：%d vs %d", res.Range.From, base.UnixMilli())
	}
	if res.Range.To != base.Add(30*24*time.Hour).UnixMilli() {
		t.Fatalf("Range.To 不符：%d", res.Range.To)
	}
}

func TestAggregateZeroRangeIsZero(t *testing.T) {
	t.Parallel()

	// 未指定范围时，Range 应报 0，而不是 time.Time{}.UnixMilli() 的巨负值
	res := AggregateWithBucket(nil, StatsFilter{}, time.Hour)
	if res.Range.From != 0 || res.Range.To != 0 {
		t.Fatalf("零值时间应表示为 0，实得 %+v", res.Range)
	}
}

// ---------------------------------------------------------------------------
// 内部工具
// ---------------------------------------------------------------------------

func TestDurationToMs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   time.Duration
		want float64
	}{
		{name: "零", in: 0, want: 0},
		{name: "1 纳秒", in: time.Nanosecond, want: 1e-6},
		{name: "1 毫秒", in: time.Millisecond, want: 1},
		{name: "1500 毫秒", in: 1500 * time.Millisecond, want: 1500},
		{name: "1 秒", in: time.Second, want: 1000},
		// 恒按纳秒换算，不引入"大数即纳秒"的猜测
		{name: "大值不触发猜测分支", in: 10 * time.Second, want: 10000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := durationToMs(tc.in); got != tc.want {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
		})
	}
}

func TestBucketStart(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 9, 29, 10, 37, 42, 0, time.UTC)

	got := bucketStart(ts, time.Hour)
	want := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("want %v, got %v", want, got)
	}

	// bucket<=0 时原样返回，避免 Truncate 除零
	if !bucketStart(ts, 0).Equal(ts) {
		t.Fatal("bucket<=0 应原样返回时间")
	}
	if !bucketStart(ts, -time.Hour).Equal(ts) {
		t.Fatal("负 bucket 应原样返回时间")
	}
}

func TestNormalizeTrendSeries(t *testing.T) {
	t.Parallel()

	t.Run("空切片原样返回", func(t *testing.T) {
		if got := normalizeTrendSeries(nil, time.Hour); got != nil {
			t.Fatalf("want nil, got %v", got)
		}
	})

	t.Run("bucket<=0 原样返回", func(t *testing.T) {
		in := []TrendPoint{{Ts: 1}, {Ts: 2}}
		got := normalizeTrendSeries(in, 0)
		if len(got) != 2 {
			t.Fatalf("want 2, got %d", len(got))
		}
	})

	t.Run("补齐中间空桶", func(t *testing.T) {
		step := time.Hour.Milliseconds()
		in := []TrendPoint{
			{Ts: 0, Total: 1},
			{Ts: 3 * step, Total: 2},
		}
		got := normalizeTrendSeries(in, time.Hour)
		if len(got) != 4 {
			t.Fatalf("want 4 个桶，got %d", len(got))
		}
		if got[0].Total != 1 || got[3].Total != 2 {
			t.Fatalf("端点数据应保留：%+v", got)
		}
		for _, i := range []int{1, 2} {
			if got[i].Total != 0 || got[i].Ts != int64(i)*step {
				t.Fatalf("第 %d 个桶应为空且时间正确：%+v", i, got[i])
			}
		}
	})

	t.Run("首尾相邻时不补桶", func(t *testing.T) {
		step := time.Hour.Milliseconds()
		in := []TrendPoint{{Ts: 0}, {Ts: step}}
		got := normalizeTrendSeries(in, time.Hour)
		if len(got) != 2 {
			t.Fatalf("want 2, got %d", len(got))
		}
	})
}

func TestKeyLabel(t *testing.T) {
	t.Parallel()

	if got := keyLabel(0); got != "admin" {
		t.Fatalf("want admin, got %q", got)
	}
	if got := keyLabel(7); got != "7" {
		t.Fatalf("want \"7\", got %q", got)
	}
}

func TestUnixMilliOrZero(t *testing.T) {
	t.Parallel()

	if got := unixMilliOrZero(time.Time{}); got != 0 {
		t.Fatalf("零值时间应为 0，实得 %d", got)
	}
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	if got := unixMilliOrZero(now); got != now.UnixMilli() {
		t.Fatalf("want %d, got %d", now.UnixMilli(), got)
	}
}

func TestRatioMeanMaxOf(t *testing.T) {
	t.Parallel()

	// 分母为 0（含负）时返回 0，避免 NaN/Inf 进入 JSON
	if got := ratio(5, 0); got != 0 {
		t.Fatalf("分母 0 应为 0，实得 %v", got)
	}
	if got := ratio(5, -1); got != 0 {
		t.Fatalf("分母负应为 0，实得 %v", got)
	}
	if got := ratio(1, 4); got != 0.25 {
		t.Fatalf("want 0.25, got %v", got)
	}
	if got := ratio(0, 4); got != 0 {
		t.Fatalf("分子 0 应为 0，实得 %v", got)
	}

	if got := mean(nil); got != 0 {
		t.Fatalf("空切片均值应为 0，实得 %v", got)
	}
	if got := mean([]float64{1, 2, 3}); got != 2 {
		t.Fatalf("want 2, got %v", got)
	}

	if got := maxOf(nil); got != 0 {
		t.Fatalf("空切片最大值应为 0，实得 %v", got)
	}
	if got := maxOf([]float64{1, 2, 3}); got != 3 {
		t.Fatalf("want 3, got %v", got)
	}
}

func TestHead(t *testing.T) {
	t.Parallel()

	// len <= n 时返回原切片
	full := []int{1, 2}
	if got := head(full, 5); len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	// len > n 时截断，且不共享底层数组（避免后续修改互相影响）
	big := []int{1, 2, 3, 4, 5}
	got := head(big, 2)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("want [1 2], got %v", got)
	}
	got[0] = 99
	if big[0] != 1 {
		t.Fatal("head 应复制而非共享底层数组")
	}
}

func TestBump(t *testing.T) {
	t.Parallel()

	items := bump(nil, "a")
	if len(items) != 1 || items[0].Name != "a" || items[0].Count != 1 {
		t.Fatalf("首次插入不符：%+v", items)
	}
	items = bump(items, "a")
	if items[0].Count != 2 {
		t.Fatalf("同项应累加，实得 %+v", items)
	}
	items = bump(items, "b")
	if len(items) != 2 {
		t.Fatalf("新项应追加，实得 %+v", items)
	}
	// 空名归一为 "-"
	items = bump(items, "")
	if items[2].Name != "-" {
		t.Fatalf("空名应归一为 %q，实得 %+v", "-", items)
	}
}

func TestTopN(t *testing.T) {
	t.Parallel()

	items := []CountItem{
		{Name: "b", Count: 1},
		{Name: "a", Count: 3},
		{Name: "d", Count: 3},
		{Name: "c", Count: 2},
	}
	got := topN(items, 3)
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	// 计数降序；同计数按名称升序
	if got[0].Name != "a" || got[1].Name != "d" || got[2].Name != "c" {
		t.Fatalf("排序不符：%+v", got)
	}

	// n 大于长度时全量返回
	all := topN([]CountItem{{Name: "x", Count: 1}}, 5)
	if len(all) != 1 {
		t.Fatalf("want 1, got %d", len(all))
	}
}

// ---------------------------------------------------------------------------
// 数据库接入
// ---------------------------------------------------------------------------

func TestLoadChatLogsNoFilter(t *testing.T) {
	setupStatsDB(t)

	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		l := logAt(uint(i), base.Add(time.Duration(i)*time.Minute), consts.StatusSuccess)
		if err := models.DB.Create(&l).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	logs, truncated, err := LoadChatLogs(context.Background(), StatsFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if truncated {
		t.Fatal("不应截断")
	}
	if len(logs) != 3 {
		t.Fatalf("want 3, got %d", len(logs))
	}
	// 排序为 created_at DESC
	if !logs[0].CreatedAt.After(logs[1].CreatedAt) {
		t.Fatal("结果应按 created_at 倒序")
	}
}

func TestLoadChatLogsFilters(t *testing.T) {
	setupStatsDB(t)

	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	seed := []models.ChatLog{
		{Model: gorm.Model{CreatedAt: base.Add(1 * time.Minute)}, Status: consts.StatusSuccess, Name: "gpt-4o", ProviderName: "prov-a", ProviderModel: "m-a", UserAgent: "ua-a", AuthKeyID: 1, Currency: "CNY"},
		{Model: gorm.Model{CreatedAt: base.Add(2 * time.Minute)}, Status: consts.StatusError, Name: "claude", ProviderName: "prov-b", ProviderModel: "m-b", UserAgent: "ua-b", AuthKeyID: 2, Currency: "CNY"},
		{Model: gorm.Model{CreatedAt: base.Add(3 * time.Minute)}, Status: consts.StatusRunning, Name: "gpt-4o", ProviderName: "prov-a", ProviderModel: "m-a", UserAgent: "ua-a", AuthKeyID: 0, Currency: "CNY"},
	}
	for i := range seed {
		if err := models.DB.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	tests := []struct {
		name  string
		f     StatsFilter
		wantN int
	}{
		{name: "按状态", f: StatsFilter{Statuses: []string{consts.StatusError}}, wantN: 1},
		{name: "按状态多值", f: StatsFilter{Statuses: []string{consts.StatusError, consts.StatusRunning}}, wantN: 2},
		{name: "按供应商子串", f: StatsFilter{Providers: []string{"prov-a"}}, wantN: 2},
		{name: "按供应商多值（OR）", f: StatsFilter{Providers: []string{"prov-a", "prov-b"}}, wantN: 3},
		{name: "按模型子串", f: StatsFilter{Models: []string{"m-b"}}, wantN: 1},
		{name: "按请求名子串", f: StatsFilter{Names: []string{"gpt"}}, wantN: 2},
		{name: "按 UA 子串", f: StatsFilter{UserAgents: []string{"ua-b"}}, wantN: 1},
		{name: "按 KeyID", f: StatsFilter{KeyIDs: []uint{2}}, wantN: 1},
		{name: "按 KeyID 多值", f: StatsFilter{KeyIDs: []uint{2, 0}}, wantN: 2},
		{name: "按窗口 From", f: StatsFilter{From: base.Add(150 * time.Second)}, wantN: 1},
		{name: "按窗口 To（右开）", f: StatsFilter{To: base.Add(2 * time.Minute)}, wantN: 1},
		{name: "按窗口 From+To", f: StatsFilter{From: base.Add(90 * time.Second), To: base.Add(150 * time.Second)}, wantN: 1},
		{name: "混合筛选无结果", f: StatsFilter{Providers: []string{"prov-a"}, Statuses: []string{consts.StatusError}}, wantN: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs, _, err := LoadChatLogs(context.Background(), tc.f)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(logs) != tc.wantN {
				t.Fatalf("want %d, got %d", tc.wantN, len(logs))
			}
		})
	}
}

func TestLoadChatLogsTruncatesAtScanLimit(t *testing.T) {
	setupStatsDB(t)

	prev := StatsScanLimit
	StatsScanLimit = 2
	t.Cleanup(func() { StatsScanLimit = prev })

	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	for i := 1; i <= 4; i++ {
		l := logAt(uint(i), base.Add(time.Duration(i)*time.Minute), consts.StatusSuccess)
		if err := models.DB.Create(&l).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	logs, truncated, err := LoadChatLogs(context.Background(), StatsFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !truncated {
		t.Fatal("超出扫描上限时应标记截断")
	}
	if len(logs) != 2 {
		t.Fatalf("应截断到 2 条，实得 %d", len(logs))
	}
	// 截断保留的是最新的 N 条
	if logs[0].ID != 4 || logs[1].ID != 3 {
		t.Fatalf("应保留最新记录，实得 ID %d,%d", logs[0].ID, logs[1].ID)
	}
}

func TestComputeStats(t *testing.T) {
	setupStatsDB(t)

	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	ok := logAt(1, base, consts.StatusSuccess)
	ok.PromptTokens = 100
	ok.CompletionTokens = 50
	ok.TotalTokens = 150
	ok.InputPrice = 1
	bad := logAt(2, base.Add(time.Minute), consts.StatusError)
	bad.Error = `status: 429, body: rate limited`
	bad.Currency = "CNY"

	for _, l := range []models.ChatLog{ok, bad} {
		row := l
		if err := models.DB.Create(&row).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	res, truncated, err := ComputeStats(context.Background(), StatsFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if truncated {
		t.Fatal("不应截断")
	}
	if res.KPI.Total != 2 || res.KPI.Success != 1 || res.KPI.Failed != 1 {
		t.Fatalf("KPI 不符：%+v", res.KPI)
	}
	if res.KPI.SuccessRate != 50 {
		t.Fatalf("成功率应为 50，实得 %v", res.KPI.SuccessRate)
	}
	if len(res.Errors) != 1 || res.Errors[0].Code != "429" {
		t.Fatalf("错误归类不符：%+v", res.Errors)
	}
}

func TestComputeStatsPropagatesDBError(t *testing.T) {
	setupStatsDB(t)

	// 通过删表制造真实的查询失败。
	// 注意不要用 models.DB = nil：gorm 在 nil 接收者上会 panic 而不是返回 error，
	// 那不是这条错误分支要覆盖的情形。
	if err := models.DB.Exec("DROP TABLE chat_logs").Error; err != nil {
		t.Fatalf("drop table: %v", err)
	}

	if _, _, err := ComputeStats(context.Background(), StatsFilter{}); err == nil {
		t.Fatal("表不存在时 ComputeStats 应返回错误")
	}
	if _, _, err := LoadChatLogs(context.Background(), StatsFilter{}); err == nil {
		t.Fatal("表不存在时 LoadChatLogs 应返回错误")
	}
}
