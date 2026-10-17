package models

import (
	"context"

	"github.com/atopos31/llmio/consts"
	"gorm.io/gorm"
)

// ReconcileInterruptedLogs 把上次进程留下的"在途"日志收尾。
//
// status=running 意味着"请求开始了、结束时的 UPDATE 没落库"。进程重启之后，那些
// 请求的 goroutine 已经不存在，它们的日志不可能再被更新——不清理的话，界面上
// 它们会永远显示在途，用户看到的是"一直有请求在跑"的假象。
//
// 2026-10-17 线上就是这个形状：一次 COMMIT 撞锁失败之后连接带着未结束的事务回到
// 池子，整库的读写全部等满 busy_timeout 报 SQLITE_BUSY，结束时的 UPDATE 成片失败，
// 于是错的那批请求停在 running；直到进程重启（SQLite 用残留的 -journal 回滚）
// 才恢复。这里补的是"重启之后把上一轮的残局扫掉"——重启之后数据库事实是干净的，
// 这些行的事实是"被中断"，把它写回库里比留在界面上更重要。
//
// 只在**启动路径**调用：此刻进程里没有任何在途请求，判"被中断"是确定的事实。
func ReconcileInterruptedLogs(ctx context.Context) (int, error) {
	return gorm.G[ChatLog](DB).
		Where("status = ?", consts.StatusRunning).
		Updates(ctx, ChatLog{
			Status: consts.StatusError,
			Error:  "服务重启：该请求在记录结束前被中断",
		})
}
