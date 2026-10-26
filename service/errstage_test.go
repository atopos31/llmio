package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func canceledRequestCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// 客户端断开必须能和其它失败区分开：日志里只剩裸的 "context canceled" 时，
// 排查的人分不清是用户点了停止、中间代理掐了连接，还是上游真的挂了。
func TestMarkStageClassifiesClientCancel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stage   ErrStage
		reqCtx  context.Context
		err     error
		wantSub []string
		denySub []string
	}{
		{
			name:    "请求 ctx 已取消 → 归因为客户端断开",
			stage:   StageResponseStream,
			reqCtx:  canceledRequestCtx(),
			err:     context.Canceled,
			wantSub: []string{"[response_stream]", errClientCancelReason, "context canceled"},
		},
		{
			name:    "请求 ctx 未取消 → 只加阶段前缀，不误判成客户端断开",
			stage:   StageResponseStream,
			reqCtx:  context.Background(),
			err:     context.Canceled,
			wantSub: []string{"[response_stream] context canceled"},
			denySub: []string{errClientCancelReason},
		},
		{
			name:    "落库失败 → 只加阶段前缀",
			stage:   StageLogPersist,
			reqCtx:  canceledRequestCtx(),
			err:     errors.New("database is locked"),
			wantSub: []string{"[log_persist] database is locked"},
			denySub: []string{errClientCancelReason},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := MarkStage(tc.stage, tc.reqCtx, tc.err).Error()
			for _, want := range tc.wantSub {
				if !strings.Contains(got, want) {
					t.Fatalf("error = %q，期望包含 %q", got, want)
				}
			}
			for _, deny := range tc.denySub {
				if strings.Contains(got, deny) {
					t.Fatalf("error = %q，不应包含 %q", got, deny)
				}
			}
		})
	}
}

// MarkStage 在重试链路和 RecordLog 里都会被调用，绝不能套成
// "[a] [a] ..." 这种两层前缀。
func TestMarkStageIsIdempotent(t *testing.T) {
	t.Parallel()

	once := MarkStage(StageResponseStream, canceledRequestCtx(), context.Canceled)
	twice := MarkStage(StageLogPersist, canceledRequestCtx(), once)
	if twice.Error() != once.Error() {
		t.Fatalf("二次标记改变了错误文本：\n got: %q\nwant: %q", twice.Error(), once.Error())
	}
}

// 标记必须保留 Unwrap 链：上层仍要用 errors.Is 认出原始错误来分流处理。
func TestMarkStageKeepsUnwrapChain(t *testing.T) {
	t.Parallel()

	err := MarkStage(StageUpstreamRequest, context.Background(), context.DeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("errors.Is 丢链了：%v", err)
	}
}

func TestMarkStagePassesNilThrough(t *testing.T) {
	t.Parallel()

	if got := MarkStage(StageResponseStream, context.Background(), nil); got != nil {
		t.Fatalf("nil 错误应原样返回 nil，实得 %v", got)
	}
}
