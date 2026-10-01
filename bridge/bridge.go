// Package bridge 在 OpenAI Chat Completions 与 Anthropic Messages 两套协议之间做翻译。
//
// 只做两件事：把一份请求体翻成另一种协议的形状，把一份响应体（含 SSE 事件流）翻回来。
// 它不碰 HTTP、不认识 provider 记录、不读配置——纯函数，进出都是 []byte。这样做的理由
// 是可测：协议互转的坑（工具调用参数丢失、tool_use 与 tool_result 的配对、流式里缺失的
// finish_reason）全都发生在"给定输入 → 期望输出"这一层，把它锁在纯函数里，测试就能
// 逐条钉住，不必起一个假上游。
//
// 逐字段映射表与设计取舍见 docs/protocol-bridge.md。
package bridge

import "fmt"

// DefaultMaxTokens 是 Anthropic 方向缺省 max_tokens 时的兜底值。
//
// Anthropic 的 max_tokens 必填，OpenAI 的可选，且大多数 OpenAI 客户端不填。于是只有
// 两条路：拒绝（那么"客户端不填 max_tokens"这一个条件就能让 Anthropic 上游完全用不了，
// 把功能的主要用途砍掉），或者补一个默认值（有损：本想生成 32k 的请求被截到 8k）。
//
// 取后者，因为**截断是可观测的**——stop_reason 会是 max_tokens，调用方看得见；而拒绝
// 是整体不可用。默认值可由上游配置覆盖（见 Options）。
const DefaultMaxTokens = 8192

// Note 记录一次**有损但可以继续**的改写，稳定的小写短码，供上层记日志与界面展示。
//
// 三种处置的判据（docs/protocol-bridge.md §2），按"调用方能不能看出差别"划线：
//
//   - **拒绝**（Unsupported）：模型要读的内容搬不过去——工具结果里的图片、n>1、JSON
//     保证。丢了模型给出的答案就不一样了，而调用方从响应里看不出来。
//   - **记账**（Note）：模型读不到、但影响的是提示与参数——seed、user、top_k、思考块、
//     图片精度档。少了它们答案会变，可差别是"软"的，而且换个上游通常就是没有。
//   - **不记账**：纯粹的结构重排，一个字不多一个字不少——连续同角色消息合并、同一
//     消息里的多个文本块并成一段。这类在 Anthropic 侧没有表示法，任何转发都必须做。
//
// 之所以要把第三类摘干净：连续 tool 消息合并、Claude Code 每轮多个文本块，都是**几乎
// 每次请求都会发生**的事。这类 Note 天天出现，看的人就会学会无视整个列表，真正有损的
// 那几条也就没人看了。
type Note string

const (
	// NoteReorderedToolResult 把 tool_result 块挪到了消息最前。Anthropic 要求如此。
	NoteReorderedToolResult Note = "reordered_tool_result"
	// NoteFilledMissingToolResult 给没有结果的 tool_use 补了一条 is_error 的结果。
	NoteFilledMissingToolResult Note = "filled_missing_tool_result"
	// NoteDroppedOrphanToolResult 丢掉了找不到对应 tool_use 的 tool_result。
	NoteDroppedOrphanToolResult Note = "dropped_orphan_tool_result"
	// NoteDroppedEmptyText 丢掉了空白的文本块。Anthropic 拒收空文本块。
	NoteDroppedEmptyText Note = "dropped_empty_text"
	// NoteDroppedEmptyMessage 丢掉了转换后一条内容都不剩的消息。
	NoteDroppedEmptyMessage Note = "dropped_empty_message"
	// NoteDefaultedMaxTokens 请求没带 max_tokens，补了默认值（会截断）。
	NoteDefaultedMaxTokens Note = "defaulted_max_tokens"
	// NoteDroppedSeed 丢掉了 seed（OpenAI 的尽力而为确定性提示，无对应）。
	NoteDroppedSeed Note = "dropped_seed"
	// NoteDroppedUser 丢掉了 user（滥用追踪提示，无对应）。
	NoteDroppedUser Note = "dropped_user"
	// NoteDroppedTopK 丢掉了 top_k（Anthropic 专有，上游没有对应参数）。
	NoteDroppedTopK Note = "dropped_top_k"
	// NoteDroppedThinking 丢掉了 thinking 相关字段（上游协议里没有这一档）。
	NoteDroppedThinking Note = "dropped_thinking"
	// NoteDroppedMetadata 丢掉了 metadata（Anthropic 的 user_id，无对应）。
	NoteDroppedMetadata Note = "dropped_metadata"
	// NoteDroppedImageDetail 丢掉了 image_url.detail（Anthropic 没有精度档位）。
	NoteDroppedImageDetail Note = "dropped_image_detail"
	// NoteImageByURL 图片用了 URL 源而不是 base64。需要较新的 API 版本才认。
	NoteImageByURL Note = "image_by_url"
	// NoteFlattenedContentOrder 消息里文本与图片（或工具调用）的相对次序无法保留。
	NoteFlattenedContentOrder Note = "flattened_content_order"
	// NoteDefaultedInputSchema 工具没给 parameters / input_schema，补了一个空对象 Schema。
	NoteDefaultedInputSchema Note = "defaulted_input_schema"
	// NoteDroppedToolStrict 丢掉了函数的 strict 标记（上游没有严格模式这一档）。
	NoteDroppedToolStrict Note = "dropped_tool_strict"
	// NotePrefixedToolError 工具结果带 is_error，在正文前加了 [tool_error] 标记。
	// OpenAI 的 tool 消息没有"这是失败"这个位，丢了模型会以为工具成功了。
	NotePrefixedToolError Note = "prefixed_tool_error"

	// 以下几条约只出现在**响应**侧：那边没有重试的机会（字已经生成出来了），
	// 做不到的地方只能带上并记账，不能像请求侧那样拒绝。

	// NoteDroppedExtraChoices 上游返回了多条候选，只取 index 最小的那条。
	NoteDroppedExtraChoices Note = "dropped_extra_choices"
	// NoteUnparsableToolArguments 工具调用参数解不出 JSON 对象，落成了空 input。
	// 请求侧遇到这种情况会拒绝；响应侧拒绝等于丢掉整次生成，只能落空并留下记号。
	NoteUnparsableToolArguments Note = "unparsable_tool_arguments"
	// NoteUnparsableContent 响应里的 content 形状认不出（既不是字符串也不是块数组），
	// 原样当文本带了过去。
	NoteUnparsableContent Note = "unparsable_content"
	// NoteUnknownFinishReason 上游给了认不出的 finish_reason / stop_reason，落了兜底值。
	NoteUnknownFinishReason Note = "unknown_finish_reason"
	// NoteContentFiltered 上游因内容过滤截断（content_filter → refusal）。语义能对上，
	// 但两边的档位叫法不同，记一笔免得排查时对不上号。
	NoteContentFiltered Note = "content_filtered"
	// NotePausedTurn 上游的 pause_turn（长任务暂停），OpenAI 没有这一档，落成了 stop。
	NotePausedTurn Note = "paused_turn"
	// NoteDroppedRefusal OpenAI 的 refusal 是一段独立的拒绝文本，Anthropic 没有对应块。
	NoteDroppedRefusal Note = "dropped_refusal"
	// NoteDroppedUnknownBlock 丢掉了目标协议里放不下的内容块（响应侧）。
	NoteDroppedUnknownBlock Note = "dropped_unknown_block"
	// NoteSkippedChunk 流里有一段载荷解不出 JSON，跳过了。SSE 流里常有厂商自己的
	// 心跳帧，跳过是常态，但真丢了一整段内容也只有这一条线索。
	NoteSkippedChunk Note = "skipped_chunk"
	// NoteDroppedUnknownEvent 流里出现了认不出的事件类型，跳过了。
	NoteDroppedUnknownEvent Note = "dropped_unknown_event"
	// NoteDroppedReasoning 丢掉了 reasoning_content / reasoning（DeepSeek 一类模型的
	// 思维链）。Anthropic 的 thinking 块必须带签名，这里给不出来。
	NoteDroppedReasoning Note = "dropped_reasoning"
)

// Unsupported 表示这份请求**无法在目标协议里表达**，换个说法就是"这家上游服务不了"。
//
// 它同时承担两种情形，因为调用方的处置完全一样（当作 provider 失败，换下一家重试）：
//
//   - 语义表达不了：客户端要 n=4，Anthropic 只给一份；要 response_format 的 JSON 保证，
//     Anthropic 没有 JSON 模式。这种情况**必须拒绝**——静默丢掉等于让调用方以为拿到了
//     它要的东西（Anthropic 自己的 OpenAI 兼容层就是这么做的，正是这里要避免的）。
//   - 形状转不过去：历史以 assistant 开头、空消息序列，这些 Anthropic 会直接 400。
//     与其编一条消息塞进去（模型会看见一段凭空捏造的内容），不如把话说清楚。
type Unsupported struct {
	// Field 出问题的字段，JSON 路径风格，如 "n"、"response_format"、"messages[0].role"。
	Field string
	// Reason 为什么表达不了。面向排查，用英文与仓库其余后端报错保持一致。
	Reason string
}

func (e *Unsupported) Error() string {
	return fmt.Sprintf("cannot bridge to the upstream protocol: %s: %s", e.Field, e.Reason)
}

// Options 转换层的可调项。
type Options struct {
	// MaxTokens 是 Anthropic 方向缺省 max_tokens 时用的值；<=0 表示用 DefaultMaxTokens。
	MaxTokens int
}

func (o Options) maxTokens() int {
	if o.MaxTokens > 0 {
		return o.MaxTokens
	}
	return DefaultMaxTokens
}
