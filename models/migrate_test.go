package models

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/consts"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 本文件验证**从上游旧库升级到本分支**的路径。
//
// 为什么必须单独测：本分支对 ChatLog 做了三处结构性改动——
//  1. 展开内嵌的 gorm.Model（改为显式 ID/CreatedAt/UpdatedAt/DeletedAt）
//  2. 新增 peak_period 列
//  3. 新增 4 个索引（含 3 个复合）
//
// 新库上 AutoMigrate 总能建对，所以"新库能跑"证明不了升级路径可用。真实风险是：
// AutoMigrate 在已有表上做表重建、丢数据，索引没建上，或者兼容回填把用户自己填过的
// 值覆盖掉。因此这里搭一个**上游形态**的旧库跑一遍 Init，再逐项断言。
//
// 旧库的结构抄自 testdata/upstream_schema.sql（由上游代码自己建库后导出，不是手写）。
// 比上游更老的、缺列的库由 init_test.go 覆盖：AutoMigrate 补出来的列是 NULL，
// 必须由这里一并验的兼容回填收拾。

// legacyCreated 是塞进旧库的那批数据的时间戳。
//
// 用 time.Time 直接当参数写入，让驱动自己序列化——这样写进去的形态与上游实际写入的
// 完全一致（驱动写出来是 `2026-01-02 03:04:05.123456` 这种无时区的 UTC 形态）。
var legacyCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// upstreamSchemaPath 上游建库语句，文件头有它的来历与重导方法。
const upstreamSchemaPath = "testdata/upstream_schema.sql"

// openLegacyDB 建一个上游形态的库，并塞入一批旧数据。
//
// 结构来自 testdata/upstream_schema.sql 而不是本地手写：手写只能证明"按我以为的旧
// 结构能升级"，抄下来的才能证明"按上游实际建出来的旧结构能升级"。
func openLegacyDB(t *testing.T, path string) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	// 中途断言失败也必须释放连接：Windows 上文件被占着，TempDir 的清理会跟着报错，
	// 盖掉真正的失败原因
	closeOnCleanup(t, db)

	statements, err := os.ReadFile(upstreamSchemaPath)
	if err != nil {
		t.Fatalf("读上游表结构失败：%v", err)
	}
	for _, stmt := range splitStatements(string(statements)) {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("执行上游建库语句失败：%v\n语句：%s", err, stmt)
		}
	}

	seedLegacyRows(t, db)

	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// splitStatements 把一行一条的建库语句切成可执行的一条条，跳过注释与空行。
func splitStatements(content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// seedLegacyRows 往旧库里塞数据。
//
// 数据是按"升级时最容易被弄坏的地方"挑的，不是随便填的：
//   - 每张表都有行，才能发现 AutoMigrate 把某张表重建坏了
//   - models / model_with_providers / auth_keys / chat_logs 各留一行**空值**，
//     用来验兼容回填确实补上了
//   - 同一张表另有一行**用户填过的值**，用来验兼容回填没有越界覆盖
//   - chat_logs 留一行软删的，用来验它既没被硬删也没被复活
func seedLegacyRows(t *testing.T, db *gorm.DB) {
	t.Helper()

	exec := func(what, sql string, args ...any) {
		t.Helper()
		if err := db.Exec(sql, args...).Error; err != nil {
			t.Fatalf("塞旧数据（%s）失败：%v", what, err)
		}
	}

	exec("providers", `INSERT INTO providers
		(id,created_at,updated_at,name,type,config,console,proxy,error_matcher)
		VALUES (?,?,?,?,?,?,?,?,?), (?,?,?,?,?,?,?,?,?)`,
		1, legacyCreated, legacyCreated, "prov-a", "openai",
		`{"base_url":"https://a.example","api_key":"sk-a"}`, "", "", "",
		2, legacyCreated, legacyCreated, "prov-b", "anthropic",
		`{"base_url":"https://b.example","api_key":"sk-b"}`,
		"https://console.example", "http://proxy.example:8080", "rate limit")

	exec("models", `INSERT INTO models
		(id,created_at,updated_at,name,remark,max_retry,time_out,strategy,breaker,display_order)
		VALUES (?,?,?,?,?,?,?,?,?,?), (?,?,?,?,?,?,?,?,?,?)`,
		1, legacyCreated, legacyCreated, "gpt-4o", "旧备注", 3, 600, "rotor", 1, 5,
		// 第二行是"从来没有被配过"的那种行：策略空、熔断 NULL、顺序 0，全靠兼容回填收拾
		2, legacyCreated, legacyCreated, "claude-3", "", 3, 600, "", nil, 0)

	exec("model_with_providers", `INSERT INTO model_with_providers
		(id,created_at,updated_at,model_id,provider_model,provider_id,tool_call,structured_output,image,with_header,status,customer_headers,extra_body,weight,input_price,cache_read_price,output_price,currency)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?),
		       (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?),
		       (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		1, legacyCreated, legacyCreated, 1, "gpt-4o-2024", 1, 1, 1, 1, 1, 1,
		`{"X-Trace":"1"}`, `{"temperature":0.2}`, 3, 1.5, 0.3, 6.0, "CNY",
		// 全空的一行：状态/自定义头/三个价格/币种都要靠兼容回填
		2, legacyCreated, legacyCreated, 2, "claude-3-opus", 2, 0, 0, 1, 0, nil,
		nil, "", 1, nil, nil, nil, "",
		// 用户填过的低价行：USD 不能被"币种为空则补 CNY"蹭到
		3, legacyCreated, legacyCreated, 1, "gpt-4o-mini", 2, 0, 0, 0, 0, 1,
		`{}`, "", 1, 0.1, 0.0, 0.4, "USD")

	exec("auth_keys", `INSERT INTO auth_keys
		(id,created_at,updated_at,name,key,status,io_log,allow_all,models,expires_at,usage_count,last_used_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?), (?,?,?,?,?,?,?,?,?,?,?,?)`,
		1, legacyCreated, legacyCreated, "k1", "sk-llmio-aaaa", 1, 1, 1, "", nil, 7, legacyCreated,
		2, legacyCreated, legacyCreated, "k2", "sk-llmio-bbbb", 1, nil, 0, `["gpt-4o"]`, legacyCreated, 0, nil)

	exec("chat_logs", `INSERT INTO chat_logs
		(id,created_at,updated_at,deleted_at,name,trace_id,provider_model,provider_name,status,style,user_agent,remote_ip,auth_key_id,session_id,chat_io,error,retry,proxy_time,first_chunk_time,chunk_time,tps,size,prompt_tokens,completion_tokens,total_tokens,prompt_tokens_details,input_price,cache_read_price,output_price,currency)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?),
		       (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?),
		       (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		1, legacyCreated, legacyCreated, nil, "gpt-4o", "trace-1", "gpt-4o-2024", "prov-a",
		"success", "openai", "claude-cli/1.0", "127.0.0.1", 1, "sess-1", true, "", 0,
		int64(1200*time.Millisecond), int64(300*time.Millisecond), int64(900*time.Millisecond),
		42.5, 2048, 100, 50, 150, `{"cached_tokens":20}`, 1.5, 0.3, 6.0, "CNY",
		// 失败行：auth_key_id 与 currency 是空的，走兼容回填；错误原文必须原样留着
		2, legacyCreated, legacyCreated, nil, "claude-3", "trace-2", "claude-3-opus", "prov-b",
		"error", "anthropic", "curl/8.0", "10.0.0.9", nil, "", false, "upstream 500", 1,
		int64(2000*time.Millisecond), 0, 0, 0.0, 512, 0, 0, 0, `{}`, 0, 0, 0, "",
		// 软删的行：升级不该把它硬删掉，也不该把它复活
		3, legacyCreated, legacyCreated, legacyCreated, "gpt-4o", "trace-3", "gpt-4o-mini", "prov-b",
		"success", "openai", "curl/8.0", "10.0.0.9", 2, "", true, "", 0,
		int64(900*time.Millisecond), int64(100*time.Millisecond), int64(800*time.Millisecond),
		55.0, 128, 10, 5, 15, `{}`, 0.1, 0, 0.4, "USD")

	exec("chat_ios", `INSERT INTO chat_ios
		(id,created_at,updated_at,log_id,input,of_string,of_string_array)
		VALUES (?,?,?,?,?,?,?), (?,?,?,?,?,?,?)`,
		1, legacyCreated, legacyCreated, 1, `{"model":"gpt-4o"}`, `{"id":"chatcmpl-1"}`, "",
		2, legacyCreated, legacyCreated, 2, `{"model":"claude-3"}`, "", `[]`)

	// 上游的 Init 自己就会塞一条日志清理策略，因此旧库里本来就有它。
	// 这里给的是**用户改过**的值（默认是关闭、30 天），用来验升级不会把它按回默认。
	policy, err := json.Marshal(LogCleanupPolicy{Enabled: true, RetentionDays: 7})
	if err != nil {
		t.Fatalf("序列化清理策略失败：%v", err)
	}
	exec("configs", `INSERT INTO configs (id,created_at,updated_at,key,value)
		VALUES (?,?,?,?,?), (?,?,?,?,?)`,
		1, legacyCreated, legacyCreated, KeyLogCleanupPolicy, string(policy),
		2, legacyCreated, legacyCreated, "some_user_key", `{"kept":true}`)

	exec("log_cleanup_records", `INSERT INTO log_cleanup_records
		(id,created_at,updated_at,retention_days,deleted_count,duration_ms,source,type)
		VALUES (?,?,?,?,?,?,?,?)`,
		1, legacyCreated, legacyCreated, 7, 123, 45, "schedule", "count")
}

func TestInit_UpgradesLegacySchemaInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	openLegacyDB(t, path)

	// 跑一次真实的启动初始化
	Init(context.Background(), path)
	closeOnCleanup(t, DB)

	t.Run("每张表的历史数据都在", func(t *testing.T) {
		// 这条断言守的是最坏情况：AutoMigrate 走了表重建路径却没搬数据。
		// 在 SQLite 上 GORM 会为"列定义变化"重建表，一旦实现有变就可能丢行。
		// 行数用原生 SQL 数，软删的行也算在内——被硬删掉同样是丢数据。
		want := map[string]int64{
			"providers":            2,
			"models":               2,
			"model_with_providers": 3,
			"auth_keys":            2,
			"chat_logs":            3,
			"chat_ios":             2,
			"configs":              2,
			"log_cleanup_records":  1,
		}
		names := make([]string, 0, len(want))
		for name := range want {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if got := countRows(t, name); got != want[name] {
				t.Fatalf("%s 应有 %d 行，实得 %d 行", name, want[name], got)
			}
		}

		// 行数对不代表内容没被改写，再抽查几张非日志表的关键字段
		var provider Provider
		if err := DB.First(&provider, 2).Error; err != nil {
			t.Fatalf("load provider: %v", err)
		}
		if provider.Name != "prov-b" || provider.Proxy != "http://proxy.example:8080" ||
			provider.ErrorMatcher != "rate limit" {
			t.Fatalf("提供商配置被改写：%+v", provider)
		}

		var key AuthKey
		if err := DB.First(&key, 2).Error; err != nil {
			t.Fatalf("load auth key: %v", err)
		}
		if key.Name != "k2" || key.Key != "sk-llmio-bbbb" || len(key.Models) != 1 ||
			key.Models[0] != "gpt-4o" {
			t.Fatalf("密钥被改写：%+v", key)
		}

		var io ChatIO
		if err := DB.First(&io, 2).Error; err != nil {
			t.Fatalf("load chat io: %v", err)
		}
		if io.Input != `{"model":"claude-3"}` || io.LogId != 2 {
			t.Fatalf("IO 记录被改写：%+v", io)
		}

		var record LogCleanupRecord
		if err := DB.First(&record, 1).Error; err != nil {
			t.Fatalf("load cleanup record: %v", err)
		}
		if record.RetentionDays != 7 || record.DeletedCount != 123 || record.Source != "schedule" {
			t.Fatalf("清理历史被改写：%+v", record)
		}
	})

	t.Run("configs 里用户改过的策略没有被按回默认", func(t *testing.T) {
		var cfg Config
		if err := DB.Where("key = ?", KeyLogCleanupPolicy).First(&cfg).Error; err != nil {
			t.Fatalf("load policy: %v", err)
		}
		var policy LogCleanupPolicy
		if err := json.Unmarshal([]byte(cfg.Value), &policy); err != nil {
			t.Fatalf("解析策略失败（值：%q）：%v", cfg.Value, err)
		}
		if !policy.Enabled || policy.RetentionDays != 7 {
			t.Fatalf("既有清理策略被覆盖：%+v", policy)
		}

		// 升级也不该凭空多出第二条策略
		if got := countRowsWhere(t, "configs", "key = ?", KeyLogCleanupPolicy); got != 1 {
			t.Fatalf("清理策略应有且只有 1 条，实得 %d 条", got)
		}
	})

	t.Run("chat_logs 的旧字段一个没被改写", func(t *testing.T) {
		var rows []ChatLog
		if err := DB.Order("id ASC").Find(&rows).Error; err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(rows) != 2 {
			// 默认作用域不含软删行，因此这里是 2 而不是 3
			t.Fatalf("应有 2 行（不含软删），实得 %d", len(rows))
		}

		row := rows[0]
		if row.Name != "gpt-4o" || row.TraceID != "trace-1" || row.ProviderName != "prov-a" {
			t.Fatalf("业务字段被改写：%+v", row)
		}
		if row.Status != "success" || row.Currency != "CNY" || row.Style != "openai" {
			t.Fatalf("状态/币种/类型被改写：%+v", row)
		}
		if row.PromptTokens != 100 || row.CompletionTokens != 50 || row.TotalTokens != 150 {
			t.Fatalf("token 字段被改写：%+v", row)
		}
		if row.PromptTokensDetails.CachedTokens != 20 {
			t.Fatalf("JSON 详情列被改写：%+v", row.PromptTokensDetails)
		}
		if row.InputPrice != 1.5 || row.OutputPrice != 6.0 {
			t.Fatalf("价格快照被改写：%+v", row)
		}
		if row.Tps != 42.5 || row.Size != 2048 ||
			row.ProxyTime != 1200*time.Millisecond || row.FirstChunkTime != 300*time.Millisecond {
			t.Fatalf("性能字段被改写：%+v", row)
		}
		if row.SessionID != "sess-1" || !row.ChatIO {
			t.Fatalf("会话/IO 标记被改写：%+v", row)
		}
		// 主键与时间戳必须保住（展开 gorm.Model 后仍是同名同义列）
		if row.ID != 1 {
			t.Fatalf("主键被改写：%d", row.ID)
		}
		if !row.CreatedAt.Equal(legacyCreated) {
			t.Fatalf("created_at 被改写：%v", row.CreatedAt)
		}
		// 错误原文是排障的凭据，升级不能动它
		if rows[1].Error != "upstream 500" || rows[1].Status != "error" || rows[1].Retry != 1 {
			t.Fatalf("错误行被改写：%+v", rows[1])
		}
	})

	t.Run("软删的行既没被硬删也没被复活", func(t *testing.T) {
		var n int64
		if err := DB.Raw("SELECT COUNT(*) FROM chat_logs WHERE deleted_at IS NOT NULL").Scan(&n).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1 {
			t.Fatalf("软删行应仍是 1 行，实得 %d", n)
		}
		var row ChatLog
		if err := DB.Unscoped().First(&row, 3).Error; err != nil {
			t.Fatalf("软删行应该仍在库里：%v", err)
		}
		if !row.DeletedAt.Valid {
			t.Fatal("软删行被复活了")
		}
	})

	t.Run("新列已补齐且不对历史行编造时段", func(t *testing.T) {
		// 旧数据没有时段概念，升级后应为空（即按基础价计费），而不是被凭空赋一个时段名
		var n int64
		if err := DB.Raw("SELECT COUNT(*) FROM chat_logs WHERE peak_period IS NOT NULL AND peak_period <> ''").
			Scan(&n).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 0 {
			t.Fatalf("历史行的 peak_period 应全为空，实得 %d 行非空", n)
		}
		var row ChatLog
		if err := DB.First(&row, 1).Error; err != nil {
			t.Fatalf("load: %v", err)
		}
		if row.PeakPeriod != "" {
			t.Fatalf("历史行的 peak_period 应为空，实得 %q", row.PeakPeriod)
		}
	})

	t.Run("上游的索引还在，新增索引也建上了", func(t *testing.T) {
		// 上游那批单列索引必须留住：升级不是"换成新的"，是"在旧的之上加"
		for _, idx := range []string{
			"idx_chat_logs_name",
			"idx_chat_logs_status",
			"idx_chat_logs_trace_id",
			"idx_chat_logs_provider_model",
			"idx_chat_logs_provider_name",
			"idx_chat_logs_user_agent",
			"idx_chat_logs_session_id",
			"idx_chat_logs_auth_key_id",
			"idx_chat_logs_deleted_at",
		} {
			if !indexExists(t, DB, idx) {
				t.Fatalf("升级后上游索引 %s 不见了", idx)
			}
		}
		// 新索引不能只对名字：GORM 会用同一个名字建出**只有前导列**的索引
		// （丢掉 created_at 的标签就会这样），而那种索引对 `created_at >= ?`
		// 这种范围过滤没有用处——名字对、列不对，比缺索引更难发现。
		for index, wantCols := range map[string][]string{
			"idx_chat_logs_created_at":     {"created_at"},
			"idx_chat_logs_status_created": {"status", "created_at"},
			"idx_chat_logs_key_created":    {"auth_key_id", "created_at"},
			"idx_chat_logs_name_created":   {"name", "created_at"},
		} {
			got := indexColumns(t, DB, index)
			if strings.Join(got, ",") != strings.Join(wantCols, ",") {
				t.Fatalf("索引 %s 的列应为 %v，实得 %v（升级时建错了列）", index, wantCols, got)
			}
		}
	})

	t.Run("兼容回填只补空值", func(t *testing.T) {
		var mwp ModelWithProvider
		if err := DB.First(&mwp, 2).Error; err != nil {
			t.Fatalf("load association: %v", err)
		}
		if mwp.Status == nil || !*mwp.Status {
			t.Fatalf("status 应被回填为 true：%+v", mwp.Status)
		}
		if mwp.CustomerHeaders == nil {
			t.Fatal("customer_headers 应被回填为空的 JSON 对象，而不是留 NULL")
		}
		if mwp.InputPrice == nil || *mwp.InputPrice != 0 {
			t.Fatalf("input_price 应被回填为 0：%+v", mwp.InputPrice)
		}
		if mwp.CacheReadPrice == nil || *mwp.CacheReadPrice != 0 {
			t.Fatalf("cache_read_price 应被回填为 0：%+v", mwp.CacheReadPrice)
		}
		if mwp.OutputPrice == nil || *mwp.OutputPrice != 0 {
			t.Fatalf("output_price 应被回填为 0：%+v", mwp.OutputPrice)
		}
		if mwp.Currency != "CNY" {
			t.Fatalf("空币种应被回填为 CNY，实得 %q", mwp.Currency)
		}

		var m Model
		if err := DB.First(&m, 2).Error; err != nil {
			t.Fatalf("load model: %v", err)
		}
		if m.Strategy != consts.BalancerDefault {
			t.Fatalf("空策略应被回填为 %q，实得 %q", consts.BalancerDefault, m.Strategy)
		}
		if m.Breaker == nil || *m.Breaker {
			t.Fatalf("breaker 应被回填为 false：%+v", m.Breaker)
		}
		// 展示顺序是 0 的那一行要被排到既有最大值之后，不能与别人并列
		if m.DisplayOrder != 6 {
			t.Fatalf("display_order 应接在最大值之后（6），实得 %d", m.DisplayOrder)
		}

		var key AuthKey
		if err := DB.First(&key, 2).Error; err != nil {
			t.Fatalf("load auth key: %v", err)
		}
		if key.IOLog == nil || *key.IOLog {
			t.Fatalf("io_log 应被回填为 false：%+v", key.IOLog)
		}
		if key.UsageCount != 0 || key.LastUsedAt != nil {
			t.Fatalf("未用过的密钥不该被写上次数字段：%+v", key)
		}

		var log ChatLog
		if err := DB.First(&log, 2).Error; err != nil {
			t.Fatalf("load log: %v", err)
		}
		if log.AuthKeyID != 0 {
			t.Fatalf("空的 auth_key_id 应被回填为 0，实得 %d", log.AuthKeyID)
		}
		// chat_logs.currency **不在**回填之列：上游的兼容更新只覆盖关联表。
		// 这一行钉的是现状而非理想——日志里的币种是当时的快照，历史空值保持为空，
		// 与上游行为一致。若哪天决定也回填它，改这条断言的同时把口径写进文档。
		if log.Currency != "" {
			t.Fatalf("chat_logs.currency 不在回填范围内，实得 %q", log.Currency)
		}
	})

	t.Run("兼容回填不碰用户填过的值", func(t *testing.T) {
		// 回填的 WHERE 条件写错（比如漏掉 IS NULL）就会把下面这些值改掉，
		// 而且改的是**用户自己的配置**，比丢日志更难被发现。
		var m Model
		if err := DB.First(&m, 1).Error; err != nil {
			t.Fatalf("load model: %v", err)
		}
		if m.Strategy != "rotor" {
			t.Fatalf("用户选的轮询策略被改成了 %q", m.Strategy)
		}
		if m.Breaker == nil || !*m.Breaker {
			t.Fatalf("用户打开的熔断被关掉了：%+v", m.Breaker)
		}
		if m.DisplayOrder != 5 || m.Remark != "旧备注" {
			t.Fatalf("既有模型配置被改写：%+v", m)
		}

		var mwp ModelWithProvider
		if err := DB.First(&mwp, 3).Error; err != nil {
			t.Fatalf("load association: %v", err)
		}
		if mwp.Currency != "USD" || mwp.InputPrice == nil || *mwp.InputPrice != 0.1 {
			t.Fatalf("用户的美元计价被改成了 %q / %+v", mwp.Currency, mwp.InputPrice)
		}

		var key AuthKey
		if err := DB.First(&key, 1).Error; err != nil {
			t.Fatalf("load auth key: %v", err)
		}
		if key.IOLog == nil || !*key.IOLog {
			t.Fatalf("用户打开的 IO 记录被关掉了：%+v", key.IOLog)
		}
		if key.UsageCount != 7 || key.LastUsedAt == nil {
			t.Fatalf("既有用量被清零：%+v", key)
		}

		var log ChatLog
		if err := DB.First(&log, 1).Error; err != nil {
			t.Fatalf("load log: %v", err)
		}
		if log.AuthKeyID != 1 {
			t.Fatalf("既有 auth_key_id 被改写为 %d", log.AuthKeyID)
		}
	})

	t.Run("旧行落在新的时间范围查询里", func(t *testing.T) {
		// 分析与日志页都按 `created_at >= ? AND created_at < ?` 取数（见 service.Stats），
		// 这也是 created_at 上那三个复合索引的用途。升级后旧行必须落在同一个查询里。
		var logs []ChatLog
		if err := DB.
			Where("created_at >= ?", legacyCreated.Add(-time.Hour)).
			Where("created_at < ?", legacyCreated.Add(time.Hour)).
			Order("created_at DESC").
			Find(&logs).Error; err != nil {
			t.Fatalf("find: %v", err)
		}
		if len(logs) != 2 {
			t.Fatalf("时间窗内应有 2 行（软删的那行不算），实得 %d", len(logs))
		}
		// 同一时刻写入的三行里，软删的那行不该出现在统计口径里
		for _, l := range logs {
			if l.ID == 3 {
				t.Fatal("软删的行不该出现在聚合查询里")
			}
		}
	})

	t.Run("升级后仍可正常写入与查询", func(t *testing.T) {
		// 用新字段写一条，确认新旧行在同表里共存且都可读
		fresh := ChatLog{
			CreatedAt:    time.Now(),
			Name:         "claude-3",
			Status:       "success",
			ProviderName: "prov-b",
			Currency:     "CNY",
			PeakPeriod:   "夜间优惠",
			Usage:        Usage{PromptTokens: 10, TotalTokens: 10},
		}
		if err := DB.Create(&fresh).Error; err != nil {
			t.Fatalf("create: %v", err)
		}

		var all []ChatLog
		if err := DB.Order("id ASC").Find(&all).Error; err != nil {
			t.Fatalf("find: %v", err)
		}
		if len(all) != 3 {
			t.Fatalf("应有 3 行（2 旧 1 新），实得 %d", len(all))
		}
		if all[0].PeakPeriod != "" || all[1].PeakPeriod != "" || all[2].PeakPeriod != "夜间优惠" {
			t.Fatalf("新旧行的 peak_period 不符：%q / %q / %q",
				all[0].PeakPeriod, all[1].PeakPeriod, all[2].PeakPeriod)
		}
	})
}

// TestInit_UpgradeIsIdempotent 重复启动不应重复建索引或改写数据。
//
// 比的是**每张表的全部内容**而不是某一列：兼容回填每次启动都会跑一遍，
// 只要 WHERE 写得不够严，第二次启动就会把第一次的结果再改一次。
func TestInit_UpgradeIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	openLegacyDB(t, path)

	Init(context.Background(), path)
	closeCurrentDBOnCleanup(t)

	after := snapshotAll(t)

	// 关掉第一个连接再跑第二次，否则前一个连接会占着库文件
	closeCurrentDB(t)

	Init(context.Background(), path)

	again := snapshotAll(t)
	for _, table := range tableNames() {
		if after[table] != again[table] {
			t.Fatalf("二次启动改写了 %s：\n第一次 %s\n第二次 %s", table, after[table], again[table])
		}
	}
}

// TestInit_FreshDatabaseStillWorks 新库路径不能被升级逻辑带坏。
func TestInit_FreshDatabaseStillWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")

	Init(context.Background(), path)
	closeOnCleanup(t, DB)

	if !indexExists(t, DB, "idx_chat_logs_status_created") {
		t.Fatal("新库也应建成复合索引")
	}
	if err := DB.Create(&ChatLog{Name: "m", Status: "success"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
}

// tableNames 是要在升级测试里逐张核对的表。
func tableNames() []string {
	return []string{
		"auth_keys", "chat_ios", "chat_logs", "configs",
		"log_cleanup_records", "model_with_providers", "models", "providers",
	}
}

// countRows 数一张表的行数，含软删的行（原生 SQL，绕过 GORM 的默认作用域）。
func countRows(t *testing.T, table string) int64 {
	t.Helper()
	var n int64
	if err := DB.Raw("SELECT COUNT(*) FROM " + table).Scan(&n).Error; err != nil {
		t.Fatalf("数 %s 失败：%v", table, err)
	}
	return n
}

// countRowsWhere 带条件数行。
func countRowsWhere(t *testing.T, table, cond string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := DB.Raw("SELECT COUNT(*) FROM "+table+" WHERE "+cond, args...).Scan(&n).Error; err != nil {
		t.Fatalf("数 %s 失败：%v", table, err)
	}
	return n
}

// snapshotAll 把每张表的全部内容拍成一个字符串，用于比对两次初始化之间有没有改动。
func snapshotAll(t *testing.T) map[string]string {
	t.Helper()
	out := make(map[string]string, len(tableNames()))
	for _, table := range tableNames() {
		var rows []map[string]any
		if err := DB.Raw("SELECT * FROM " + table + " ORDER BY id").Scan(&rows).Error; err != nil {
			t.Fatalf("快照 %s 失败：%v", table, err)
		}
		out[table] = fmt.Sprintf("%v", rows)
	}
	return out
}

// closeCurrentDB 关闭当前包级连接。
//
// 多次调用 Init 的测试必须在两次之间调用它：Init 每次都会换一个新的
// 包级连接，前一个若不关就一直占着文件，Windows 上 t.TempDir 清理会失败。
func closeCurrentDB(t *testing.T) {
	t.Helper()
	if DB == nil {
		return
	}
	if sqlDB, err := DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// closeCurrentDBOnCleanup 把 closeCurrentDB 注册为清理步骤。
func closeCurrentDBOnCleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { closeCurrentDB(t) })
}

// indexExists 查询 sqlite_master 判断索引是否存在。
func indexExists(t *testing.T, db *gorm.DB, name string) bool {
	t.Helper()
	var n int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", name,
	).Scan(&n).Error; err != nil {
		t.Fatalf("query index %s: %v", name, err)
	}
	return n > 0
}
