package service

import (
	"context"
	"errors"
	"fmt"
)

// ErrStage 标记一次请求失败发生在日志写入链路的哪一步。
//
// 存在的唯一理由：ChatLog.Error 以前直接存 err.Error()，而 context.Canceled
// 的字符串就是 "context canceled"——既看不出断在哪一步，也看不出是谁导致的。
// 加上阶段前缀后，日志页一眼就能分辨「上游没连上 / 流断了 / 库挂了」。
type ErrStage string

const (
	// StageUpstreamRequest 发起或等待上游响应阶段失败（构建请求、client.Do、等待响应头）。
	StageUpstreamRequest ErrStage = "upstream_request"
	// StageResponseStream 读取或解析上游响应流阶段失败（processer、转发给客户端）。
	StageResponseStream ErrStage = "response_stream"
	// StageLogPersist 日志自身落库阶段失败（RecordLog 的 DB 写入）。
	StageLogPersist ErrStage = "log_persist"
)

// errClientCancelReason 是客户端或中间代理中途断开时替换原始文案的说明。
//
// 不直接用 "context canceled" 的原因很简单：那句话对排查毫无信息量，
// 而实际语义是「对端先跑了」——可能是用户点了停止，也可能是中间的
// nginx / 网关按 proxy_read_timeout 掐断了连接。
const errClientCancelReason = "canceled by client or intermediary proxy"

// stageError 给原始错误套一层「阶段 + 归因」，并保留 Unwrap 链。
type stageError struct {
	stage        ErrStage
	clientCancel bool
	cause        error
}

func (e *stageError) Error() string {
	if e.clientCancel {
		return fmt.Sprintf("[%s] %s: %v", e.stage, errClientCancelReason, e.cause)
	}
	return fmt.Sprintf("[%s] %v", e.stage, e.cause)
}

func (e *stageError) Unwrap() error { return e.cause }

// IsClientCancel 判定错误是否由客户端（或中间代理）主动断开导致。
//
// 只用 errors.Is(err, context.Canceled) 不够：context.Canceled 也可能来自
// 我们自己主动取消的场景，那类不该被归因成「客户端跑了」。所以必须再用
// reqCtx.Err() 交叉验证，两个条件同时成立才认定。
//
// 只认 context.Canceled，不把 broken pipe / connection reset 也吞进来：
// 那两类已经由 stats.ClassifyError 归到「网络错误」，重复归因只会让两边打架。
func IsClientCancel(reqCtx context.Context, err error) bool {
	if !errors.Is(err, context.Canceled) {
		return false
	}
	return reqCtx != nil && errors.Is(reqCtx.Err(), context.Canceled)
}

// MarkStage 给错误打上阶段前缀，并在能确认时标注客户端主动断开。
//
// reqCtx 必须是**请求自身的 context**（handler 里的 c.Request.Context()）。
// 传 context.Background() 只会得到阶段前缀、拿不到断开归因，
// 所以调用点尽量放在离请求最近的地方（handler / BalanceChat）。
//
// 幂等：已经标记过的错误原样返回，避免在重试链路或 RecordLog 里被套成
// "[response_stream] [response_stream] ..."。
func MarkStage(stage ErrStage, reqCtx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var marked *stageError
	if errors.As(err, &marked) {
		return err
	}
	return &stageError{
		stage:        stage,
		clientCancel: IsClientCancel(reqCtx, err),
		cause:        err,
	}
}
