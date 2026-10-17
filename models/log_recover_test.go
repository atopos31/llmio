package models

import (
	"context"
	"testing"

	"github.com/atopos31/llmio/consts"
)

// 上一次进程留下的 running 行必须是"被中断"，不能永远显示在途。
//
// 线上 2026-10-17：COMMIT 撞锁失败把连接连着未结束的事务放回池子，整库锁死，
// 结束时的 UPDATE 全部失败——那批请求的日志就停在 running，界面上看起来
// "一直有请求在跑"。重启后这个函数负责把残局收掉。
func TestReconcileInterruptedLogs(t *testing.T) {
	db := openCompressDB(t)
	ctx := context.Background()

	running := ChatLog{Status: consts.StatusRunning, Name: "gpt-4o"}
	success := ChatLog{Status: consts.StatusSuccess, Name: "gpt-4o"}
	broken := ChatLog{Status: consts.StatusError, Error: "boom", Name: "gpt-4o"}
	for _, l := range []*ChatLog{&running, &success, &broken} {
		if err := db.Create(l).Error; err != nil {
			t.Fatalf("造日志失败：%v", err)
		}
	}

	n, err := ReconcileInterruptedLogs(ctx)
	if err != nil {
		t.Fatalf("收尾失败：%v", err)
	}
	if n != 1 {
		t.Fatalf("期望收尾 1 条，实际 %d", n)
	}

	var got ChatLog
	if err := db.First(&got, running.ID).Error; err != nil {
		t.Fatalf("读回收尾后的行失败：%v", err)
	}
	if got.Status != consts.StatusError {
		t.Fatalf("running 行应收尾成 error，实际 %q", got.Status)
	}
	if got.Error == "" {
		t.Fatal("收尾后的行必须写清原因，否则用户只会看到一个没有上下文的 error")
	}

	// 已经结束的行一个字都不该动。
	var ok ChatLog
	if err := db.First(&ok, success.ID).Error; err != nil {
		t.Fatalf("读回成功行失败：%v", err)
	}
	if ok.Status != consts.StatusSuccess || ok.Error != "" {
		t.Fatalf("成功行被改了：status=%q error=%q", ok.Status, ok.Error)
	}

	// 幂等：再跑一次不该再动任何行。
	if n, err := ReconcileInterruptedLogs(ctx); err != nil || n != 0 {
		t.Fatalf("第二次收尾应无事可做，实际 n=%d err=%v", n, err)
	}
}
