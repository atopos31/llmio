package models

import "context"

// chatLogsDeletedAtIndex 是 chat_logs.deleted_at 上那条**有害**索引的名字。
//
// 标签已经从 ChatLog.DeletedAt 上摘掉（见 model.go 的字段注释），这个名字只剩
// "清理存量库"这一个用途：老版本建出来的库里它还在，且 AutoMigrate 不会删索引。
const chatLogsDeletedAtIndex = "idx_chat_logs_deleted_at"

// DropChatLogsDeletedAtIndex 摘掉 chat_logs.deleted_at 上的索引。可重复调用。
//
// 这条索引不是"可有可无"，是**有害**的。chat_logs 的每一条删除路径都是
// Unscoped 硬删除（service/log_cleanup.go 的定时清理、handler/api.go 的按 id
// 清理），deleted_at 因此恒为 NULL。于是这条索引：
//
//   - 对任何查询都命中整张表，窄化不了任何东西；
//   - 却因为"看起来选择性很高"稳居查询计划器首选——GORM 会给每条 chat_logs
//     读语句自动补 `deleted_at IS NULL`，分析页那条按时间窗取数的查询因此放着
//     现成的 idx_chat_logs_created_at 不用，改成"扫完 deleted_at 索引 + 临时
//     B 树排序"，两条筛选下拉的 DISTINCT 也一起退化成全表扫。
//
// 实测（20 万行、无 sqlite_stat1，即线上真实状态；临时库上跑 EXPLAIN QUERY PLAN）：
//
//	按时间窗取数           114ms → 16ms（改走 idx_chat_logs_created_at，临时 B 树消失）
//	DISTINCT provider_model  100ms →  50ms（改走同名覆盖索引）
//	DISTINCT user_agent       58ms →  47ms（同上）
//
// 线上 1.34 GB 的库上第一条是 2.07 秒。回滚日志模式下读事务挡写，这 2 秒就是
// 写请求的排队时间：INSERT 等满 busy_timeout 报 SQLITE_BUSY、请求积压，最终把
// 128 MiB 的容器顶到 OOMKilled（2026-10-29 的事故：容器重启 2 次，cgroup 峰值
// 134,172,672 B，占上限 99.97%）。
// 少一条索引顺带省掉每一次 chat_logs 写入的索引维护成本——这一列既然恒为 NULL，
// 写进去的每一个索引项都是白写。
//
// 时序前提与 EnsureChatIOLogIDIndex 相同：调用点在 Init 里、监听端口之前，
// 此刻没有在途请求。DROP INDEX 只改 B 树指针，对大表同样是瞬时操作。
func DropChatLogsDeletedAtIndex(ctx context.Context) error {
	return DB.WithContext(ctx).Exec("DROP INDEX IF EXISTS " + chatLogsDeletedAtIndex).Error
}
