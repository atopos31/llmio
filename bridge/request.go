package bridge

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 请求体的互转。
//
// 两个方向各一个入口，都是纯函数：进 []byte，出 []byte + 有损改写的记录 + 错误。
// 逐字段映射表见 docs/protocol-bridge.md §3。

// OpenAIRequestToAnthropic 把一份 Chat Completions 请求体翻成 Messages 请求体的形状。
func OpenAIRequestToAnthropic(raw []byte, opt Options) ([]byte, []Note, error) {
	var req OpenAIRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, nil, &Unsupported{Field: "body", Reason: "not a JSON chat completions request: " + err.Error()}
	}
	if err := rejectUnsupportedOpenAIFields(&req); err != nil {
		return nil, nil, err
	}

	var notes []Note
	out := AnthropicRequest{
		Model:         req.Model,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		TopK:          req.TopK,
		Stream:        req.Stream,
		MaxTokens:     anthropicMaxTokens(&req, opt, &notes),
		StopSequences: parseOpenAIStop(req.Stop),
	}

	if req.ResponseFormat != nil && req.ResponseFormat.Type != "text" {
		outputConfig, err := openAIResponseFormatToOutputConfig(req.ResponseFormat)
		if err != nil {
			return nil, nil, err
		}
		if outputConfig != nil {
			out.OutputConfig = outputConfig
			notes = append(notes, NoteTranslatedOutputFormat)
		}
	}

	tools, err := anthropicTools(req.Tools, &notes)
	if err != nil {
		return nil, nil, err
	}
	choice, dropTools, err := anthropicToolChoice(req.ToolChoice)
	if err != nil {
		return nil, nil, err
	}
	if dropTools {
		// tool_choice:"none" 在 Anthropic 里没有对应的一档：不传 tools 就是它。
		// 这不是有损转换（不传工具确实等于工具被禁用），因此不记 Note。
		tools = nil
	} else {
		out.ToolChoice = choice
	}
	out.Tools = tools

	// seed / user 是"尽力而为"的提示（确定性、滥用追踪），响应契约上看不出区别，
	// 丢了不记进 error，但要记进 Note——原则见 bridge.go 顶部
	if req.Seed != nil {
		notes = append(notes, NoteDroppedSeed)
	}
	if req.User != "" {
		notes = append(notes, NoteDroppedUser)
	}

	system, messages, err := openAIMessagesToAnthropic(req.Messages, &notes)
	if err != nil {
		return nil, nil, err
	}
	if system != "" {
		out.System = stringJSON(system)
	}
	out.Messages = messages

	// 这里不可能出错：out 的字段全是 string / *float64 / []string / 已验证过的
	// json.RawMessage，没有 json 编不出来的类型
	body, _ := json.Marshal(out)
	return body, dedupeNotes(notes), nil
}

// rejectUnsupportedOpenAIFields 挑出"改了就不是调用方要的东西"的字段。
//
// 判据是**调用方能否从响应里发现差别**（docs/protocol-bridge.md §2）：
// n=4 少给三份、要 JSON 保证却拿不到，都是看得见的；而值为 0 的惩罚项、
// false 的 logprobs 是默认值，等价于没写。
func rejectUnsupportedOpenAIFields(req *OpenAIRequest) error {
	switch {
	case req.N != nil && *req.N > 1:
		return &Unsupported{Field: "n", Reason: "the upstream returns a single choice"}
	case req.PresencePenalty != nil && *req.PresencePenalty != 0:
		return &Unsupported{Field: "presence_penalty", Reason: "the upstream has no penalty parameters"}
	case req.FrequencyPenalty != nil && *req.FrequencyPenalty != 0:
		return &Unsupported{Field: "frequency_penalty", Reason: "the upstream has no penalty parameters"}
	case req.Logprobs != nil && *req.Logprobs:
		return &Unsupported{Field: "logprobs", Reason: "the upstream does not return token log probabilities"}
	case req.TopLogprobs != nil && *req.TopLogprobs > 0:
		return &Unsupported{Field: "top_logprobs", Reason: "the upstream does not return token log probabilities"}
	case req.ResponseFormat != nil && req.ResponseFormat.Type != "" &&
		req.ResponseFormat.Type != "text" &&
		req.ResponseFormat.Type != "json_schema" && req.ResponseFormat.Type != "json_object":
		// json_schema / json_object 放行——翻译成 output_config.format（见下）。
		// 其余 type 静默丢掉等于让调用方以为保证还在（Anthropic 自己的兼容层
		// 就是这么做的），仍拒绝
		return &Unsupported{Field: "response_format", Reason: fmt.Sprintf("unsupported response_format type %q", req.ResponseFormat.Type)}
	}
	return nil
}

// openAIResponseFormatToOutputConfig 把 OpenAI 的 response_format 翻成 Anthropic
// 的 output_config.format（2025-11 起原生结构化输出；现行参数是 output_config，
// 旧名 output_format 已弃用，2026-10-24 核对官方文档）。
//
//	json_schema → {"format":{"type":"json_schema","schema": <schema 原样搬运>}}
//	json_object → {"format":{"type":"json_schema","schema":{"type":"object"}}}
//	              （合成"任意 JSON 对象"——官方文档只记档了 json_schema 一档，
//	               不依赖未核实的 json 档形状）
//
// json_schema.name 是 OpenAI 侧的标识、strict 由 Anthropic 侧默认严格，都不翻译。
// 这是**保证级契约**：上游（老模型 / 兼容网关）不认而 400 时**不降级不静默丢**
// ——静默丢等于装作答应了，失败原因经收尾错误透给客户端。
func openAIResponseFormatToOutputConfig(rf *OpenAIResponseFormat) (json.RawMessage, error) {
	if rf == nil {
		return nil, nil
	}
	var schema json.RawMessage
	switch rf.Type {
	case "json_schema":
		if rf.JsonSchema == nil || len(rf.JsonSchema.Schema) == 0 {
			return nil, &Unsupported{Field: "response_format.json_schema", Reason: "json_schema without a schema"}
		}
		schema = rf.JsonSchema.Schema
	case "json_object":
		schema = json.RawMessage(`{"type":"object"}`)
	default:
		return nil, nil // text 或不认识的 type：不翻译（后者已在拒绝层拦下）
	}
	body, err := json.Marshal(map[string]any{
		"format": map[string]any{"type": "json_schema", "schema": schema},
	})
	if err != nil {
		return nil, &Unsupported{Field: "response_format", Reason: err.Error()}
	}
	return body, nil
}

// anthropicMaxTokens 取 Anthropic 必填的 max_tokens。
//
// 缺省时补默认值而不是拒绝：拒绝会让"客户端不填 max_tokens"这一个条件把 Anthropic
// 上游整体堵死，而截断至少是**看得见**的（stop_reason 会是 max_tokens）。
func anthropicMaxTokens(req *OpenAIRequest, opt Options, notes *[]Note) int {
	// max_completion_tokens 是 max_tokens 的新名字，两者都在时新的优先
	if req.MaxCompletionTokens != nil && *req.MaxCompletionTokens > 0 {
		return *req.MaxCompletionTokens
	}
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		return *req.MaxTokens
	}
	*notes = append(*notes, NoteDefaultedMaxTokens)
	return opt.maxTokens()
}

// parseOpenAIStop 解析 stop：协议允许字符串或字符串数组。
func parseOpenAIStop(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil && one != "" {
		return []string{one}
	}
	return nil
}

// anthropicTools 搬运工具声明。parameters 是 JSON Schema，只搬运不解析。
func anthropicTools(tools []OpenAITool, notes *[]Note) ([]AnthropicTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]AnthropicTool, 0, len(tools))
	for i, t := range tools {
		if t.Type != "" && t.Type != "function" {
			// 服务端内置工具（web_search 之类）在两边是两套东西，名字一样用途不同
			return nil, &Unsupported{
				Field:  fmt.Sprintf("tools[%d].type", i),
				Reason: fmt.Sprintf("only function tools can be bridged, got %q", t.Type),
			}
		}
		item := AnthropicTool{Name: t.Function.Name, Description: t.Function.Description}
		if len(t.Function.Parameters) > 0 && string(t.Function.Parameters) != "null" {
			item.InputSchema = t.Function.Parameters
		} else {
			// input_schema 是必填；不填上游直接 400，补一个空对象 Schema 更诚实
			item.InputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
			*notes = append(*notes, NoteDefaultedInputSchema)
		}
		if t.Function.Strict != nil {
			*notes = append(*notes, NoteDroppedToolStrict)
		}
		out = append(out, item)
	}
	return out, nil
}

// anthropicToolChoice 转换工具选择。第二个返回值表示"应当整个丢掉 tools"。
func anthropicToolChoice(raw json.RawMessage) (*AnthropicToolChoice, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "auto":
			return &AnthropicToolChoice{Type: "auto"}, false, nil
		case "required":
			return &AnthropicToolChoice{Type: "any"}, false, nil
		case "none":
			return nil, true, nil
		default:
			// 猜错会改变工具行为，属于可观测差异，不猜
			return nil, false, &Unsupported{Field: "tool_choice", Reason: fmt.Sprintf("unknown value %q", s)}
		}
	}
	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil || obj.Type != "function" || obj.Function.Name == "" {
		return nil, false, &Unsupported{Field: "tool_choice", Reason: "unrecognized shape"}
	}
	return &AnthropicToolChoice{Type: "tool", Name: obj.Function.Name}, false, nil
}

// anthropicTurn 是转换中途的"一轮"：role + 块。
//
// 中间的形态与 Anthropic 的线格式一致（同为 role + []block），单独起个名字只是为了让
// 清洗那几步（合并同角色、配平工具结果、丢掉空轮）在类型上看得清是"还没定稿"。
type anthropicTurn struct {
	role   string
	blocks []AnthropicBlock
}

// openAIMessagesToAnthropic 转换消息数组，返回 system 文本与定稿后的消息。
//
// 步骤刻意分开（每一步都只干一件事，测试才能一条条钉）：先逐条转成"轮"，再丢掉空轮、
// 合并同角色、把 tool_result 挪到最前、配平 tool_use ↔ tool_result，最后检查首条角色。
func openAIMessagesToAnthropic(msgs []OpenAIMessage, notes *[]Note) (string, []AnthropicMessage, error) {
	var system []string
	turns := make([]anthropicTurn, 0, len(msgs))

	for i, m := range msgs {
		switch m.Role {
		case "system", "developer":
			// developer 是 system 的新名字，语义相同
			text, parts, err := openAIContent(m.Content)
			if err != nil {
				return "", nil, &Unsupported{Field: fmt.Sprintf("messages[%d].content", i), Reason: err.Error()}
			}
			if parts != nil {
				if text, err = textOnlyParts(parts); err != nil {
					return "", nil, &Unsupported{Field: fmt.Sprintf("messages[%d].content[].type", i), Reason: err.Error()}
				}
			}
			// 空 system 直接不产生 system（Anthropic 拒收空串）
			if strings.TrimSpace(text) != "" {
				system = append(system, text)
			}
		case "user":
			blocks, err := openAIUserBlocks(i, m.Content, notes)
			if err != nil {
				return "", nil, err
			}
			turns = append(turns, anthropicTurn{role: "user", blocks: blocks})
		case "assistant":
			blocks, err := openAIAssistantBlocks(i, m, notes)
			if err != nil {
				return "", nil, err
			}
			turns = append(turns, anthropicTurn{role: "assistant", blocks: blocks})
		case "tool":
			text, parts, err := openAIContent(m.Content)
			if err != nil {
				return "", nil, &Unsupported{Field: fmt.Sprintf("messages[%d].content", i), Reason: err.Error()}
			}
			content := stringJSON(text)
			if parts != nil {
				// 工具结果在 OpenAI 里可以是块数组。text 拼回字符串、image 块搬成
				// Anthropic 的 image 块（tool_result.content 本就收 image 块）——
				// 曾经"索性一并拒绝"，拒的是双方协议都装得下的东西。其余类型
				// （Anthropic 侧没有对应物）仍拒绝。纯文本仍给字符串：只有真夹了
				// 图片才升级成块数组，与 openAIContentJSON 的约定同一条
				var texts []string
				var imgBlocks []AnthropicBlock
				carried := false
				for _, p := range parts {
					switch p.Type {
					case "text", "input_text":
						texts = append(texts, p.Text)
					case "image_url":
						b, err := anthropicImageBlock(p.ImageURL, notes)
						if err != nil {
							return "", nil, &Unsupported{Field: fmt.Sprintf("messages[%d].content[].image_url", i), Reason: err.Error()}
						}
						imgBlocks = append(imgBlocks, b)
						*notes = append(*notes, NoteCarriedToolResultImage)
						carried = true
					default:
						return "", nil, &Unsupported{Field: fmt.Sprintf("messages[%d].content[].type", i), Reason: fmt.Sprintf("the anthropic protocol cannot carry a %q block inside a tool result", p.Type)}
					}
				}
				if carried {
					blocks := make([]AnthropicBlock, 0, len(texts)+len(imgBlocks))
					for _, t := range texts {
						blocks = append(blocks, AnthropicBlock{Type: blockText, Text: t})
					}
					blocks = append(blocks, imgBlocks...)
					if body, err := json.Marshal(blocks); err == nil {
						content = body
					}
				} else {
					content = stringJSON(strings.Join(texts, ""))
				}
			}
			turns = append(turns, anthropicTurn{role: "user", blocks: []AnthropicBlock{{
				Type:      blockToolResult,
				ToolUseID: m.ToolCallID,
				Content:   content,
			}}})
		default:
			return "", nil, &Unsupported{
				Field:  fmt.Sprintf("messages[%d].role", i),
				Reason: fmt.Sprintf("unknown role %q", m.Role),
			}
		}
	}

	turns = dropEmptyTurns(turns, notes)
	turns = mergeSameRoleTurns(turns)
	turns = orderToolResultsFirst(turns, notes)
	turns = pairToolResults(turns, notes)

	if len(turns) == 0 {
		return "", nil, &Unsupported{Field: "messages", Reason: "no messages left after conversion"}
	}
	if turns[0].role != "user" {
		// 补一条空 user 消息要编内容（空文本块会被拒），编出来的东西模型看得见。
		// 与其塞一段凭空捏造的话，不如说清楚这份历史转不过去
		return "", nil, &Unsupported{Field: "messages[0].role", Reason: "the upstream requires the first message to be a user message"}
	}

	out := make([]AnthropicMessage, 0, len(turns))
	for _, t := range turns {
		out = append(out, AnthropicMessage{Role: t.role, Content: t.blocks})
	}
	return strings.Join(system, "\n\n"), out, nil
}

// dropEmptyTurns 丢掉转换后一块内容都不剩的消息。
//
// 顺序很要紧：必须在配平工具结果**之前**。空轮被留在中间的话，一条带 tool_use 的
// assistant 消息后面跟的就是那个空轮，配平会往空轮里补假结果，真正的结果反而成了孤儿。
func dropEmptyTurns(turns []anthropicTurn, notes *[]Note) []anthropicTurn {
	out := make([]anthropicTurn, 0, len(turns))
	for _, t := range turns {
		if len(t.blocks) == 0 {
			*notes = append(*notes, NoteDroppedEmptyMessage)
			continue
		}
		out = append(out, t)
	}
	return out
}

// mergeSameRoleTurns 合并连续的同角色消息：Anthropic 要求角色交替，OpenAI 不要求。
//
// 不记 Note：内容一块不少地落进同一个轮里，模型看到的东西完全一样，被丢掉的是消息
// 边界本身——而"两条 user 消息"这件事在 Anthropic 里本来就没有表示法。更要紧的是它
// 几乎每次工具调用都会发生（连续的 tool 消息必然合并成一条），逐次记账会把真正有损
// 的那几条淹掉。判据见 bridge.go 上 Note 的说明。
func mergeSameRoleTurns(turns []anthropicTurn) []anthropicTurn {
	out := make([]anthropicTurn, 0, len(turns))
	for _, t := range turns {
		if len(out) > 0 && out[len(out)-1].role == t.role {
			last := &out[len(out)-1]
			last.blocks = append(last.blocks, t.blocks...)
			continue
		}
		out = append(out, t)
	}
	return out
}

// orderToolResultsFirst 把 user 消息里的 tool_result 块挪到最前。
//
// Anthropic 要求 tool_result 块先于同一消息里的其他内容。只有真的换了位置才记 Note——
// 常见的形状（assistant 的 tool_use 后紧跟一条只有 tool_result 的 user 消息）本来就合规。
func orderToolResultsFirst(turns []anthropicTurn, notes *[]Note) []anthropicTurn {
	for i := range turns {
		if turns[i].role != "user" {
			continue
		}
		ordered := make([]AnthropicBlock, 0, len(turns[i].blocks))
		for _, b := range turns[i].blocks {
			if b.Type == blockToolResult {
				ordered = append(ordered, b)
			}
		}
		// 没有 tool_result 时下面这个循环会把原序原样搬过去，不产生 Note
		rest := make([]AnthropicBlock, 0, len(turns[i].blocks))
		for _, b := range turns[i].blocks {
			if b.Type != blockToolResult {
				rest = append(rest, b)
			}
		}
		if len(ordered) == 0 {
			continue
		}
		if len(rest) > 0 && turns[i].blocks[0].Type != blockToolResult {
			*notes = append(*notes, NoteReorderedToolResult)
		}
		turns[i].blocks = append(ordered, rest...)
	}
	return turns
}

// pairToolResults 让每个 tool_use 都有 tool_result，并丢掉没有对应 tool_use 的孤儿结果。
//
// OpenAI 两种都允许（悬空调用、孤儿结果），Anthropic 两种都拒。补的那条结果带
// is_error:true 与一句说明——模型读到的是"这个工具没返回"，而不是一个编出来的成功结果。
//
// 一趟从左到右扫完，靠 open（见过、还没等到结果的 tool_use id）判定：
//   - 走到 assistant 轮，open 换成这一轮新提出的调用；
//   - 走到 user 轮，结果能认领的留下并从 open 里划掉，认不出的一律丢掉（孤儿）；
//     这一轮结束时 open 若还非空，说明调用没等到结果，就地补上占位结果。
//
// 必须是**一趟扫全部轮次**，不能只看"assistant 后面紧跟的那条 user 消息"：连续两条
// tool 消息会被合并成一条 user 轮，只看相邻一条时，合并进来的那份结果就成了没人检查的
// 孤儿，原样发给上游直接 400。
func pairToolResults(turns []anthropicTurn, notes *[]Note) []anthropicTurn {
	out := make([]anthropicTurn, 0, len(turns))
	var open []string // 前面出现过、还没等到结果的 tool_use id
	for _, t := range turns {
		if t.role != "user" {
			out = append(out, t)
			open = toolUseIDs(t.blocks)
			continue
		}

		kept := make([]AnthropicBlock, 0, len(t.blocks))
		for _, b := range t.blocks {
			if b.Type != blockToolResult {
				kept = append(kept, b)
				continue
			}
			idx := indexOf(open, b.ToolUseID)
			if idx < 0 {
				*notes = append(*notes, NoteDroppedOrphanToolResult)
				continue
			}
			open = append(open[:idx], open[idx+1:]...)
			kept = append(kept, b)
		}
		if len(open) > 0 {
			// 没等到结果的那些补上占位结果，排在已有结果之前（tool_result 要在最前）
			*notes = append(*notes, NoteFilledMissingToolResult)
			kept = append(syntheticToolResults(open), kept...)
			open = nil
		}
		if len(kept) == 0 {
			// 整轮被掏空：原本只装着配不上的结果
			*notes = append(*notes, NoteDroppedEmptyMessage)
			continue
		}
		t.blocks = kept
		out = append(out, t)
	}
	if len(open) > 0 {
		// 历史就停在工具调用上，后面没有承接的轮次：自己补一条
		*notes = append(*notes, NoteFilledMissingToolResult)
		out = append(out, anthropicTurn{role: "user", blocks: syntheticToolResults(open)})
	}
	return out
}

// syntheticToolResults 给没有结果的 tool_use 补上 is_error 的占位结果。
// 调用方只在确实有悬空调用时才调它。
func syntheticToolResults(ids []string) []AnthropicBlock {
	blocks := make([]AnthropicBlock, 0, len(ids))
	for _, id := range ids {
		blocks = append(blocks, AnthropicBlock{
			Type:      blockToolResult,
			ToolUseID: id,
			Content:   stringJSON("The tool call was not answered; treat it as failed."),
			IsError:   boolPtr(true),
		})
	}
	return blocks
}

// openAIUserBlocks 转一条 user 消息的内容。
func openAIUserBlocks(index int, content json.RawMessage, notes *[]Note) ([]AnthropicBlock, error) {
	text, parts, err := openAIContent(content)
	if err != nil {
		return nil, &Unsupported{Field: fmt.Sprintf("messages[%d].content", index), Reason: err.Error()}
	}
	if parts == nil {
		if strings.TrimSpace(text) == "" {
			return nil, nil // 空内容由 dropEmptyTurns 记账，不在这里重复报
		}
		return []AnthropicBlock{{Type: blockText, Text: text}}, nil
	}

	blocks := make([]AnthropicBlock, 0, len(parts))
	droppedEmpty := false
	for _, p := range parts {
		switch p.Type {
		case "text", "input_text":
			if strings.TrimSpace(p.Text) == "" {
				droppedEmpty = true
				continue
			}
			blocks = append(blocks, AnthropicBlock{Type: blockText, Text: p.Text})
		case "image_url":
			if p.ImageURL == nil || p.ImageURL.URL == "" {
				return nil, &Unsupported{Field: fmt.Sprintf("messages[%d].content[].image_url", index), Reason: "image part without a url"}
			}
			block, err := anthropicImageBlock(p.ImageURL, notes)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
		default:
			return nil, &Unsupported{
				Field:  fmt.Sprintf("messages[%d].content[].type", index),
				Reason: fmt.Sprintf("unsupported content part %q", p.Type),
			}
		}
	}
	if droppedEmpty && len(blocks) > 0 {
		*notes = append(*notes, NoteDroppedEmptyText)
	}
	return blocks, nil
}

// openAIAssistantBlocks 转一条 assistant 消息：文本 + tool_calls。
func openAIAssistantBlocks(index int, m OpenAIMessage, notes *[]Note) ([]AnthropicBlock, error) {
	text, parts, err := openAIContent(m.Content)
	if err != nil {
		return nil, &Unsupported{Field: fmt.Sprintf("messages[%d].content", index), Reason: err.Error()}
	}
	var blocks []AnthropicBlock
	if parts != nil {
		// 助手消息用块数组是 OpenAI 的扩展写法，这里只认文本块
		text, err = textOnlyParts(parts)
		if err != nil {
			return nil, &Unsupported{
				Field:  fmt.Sprintf("messages[%d].content[].type", index),
				Reason: err.Error(),
			}
		}
	}
	if strings.TrimSpace(text) != "" {
		blocks = append(blocks, AnthropicBlock{Type: blockText, Text: text})
	}

	for j, tc := range m.ToolCalls {
		if tc.ID == "" {
			// 没有 id 就没法与结果配对，Anthropic 侧会直接 400
			return nil, &Unsupported{
				Field:  fmt.Sprintf("messages[%d].tool_calls[%d].id", index, j),
				Reason: "tool call without an id cannot be paired with its result",
			}
		}
		input, err := toolInput(tc.Function.Arguments)
		if err != nil {
			return nil, &Unsupported{
				Field:  fmt.Sprintf("messages[%d].tool_calls[%d].function.arguments", index, j),
				Reason: err.Error(),
			}
		}
		blocks = append(blocks, AnthropicBlock{
			Type:  blockToolUse,
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	return blocks, nil
}

// toolInput 把 OpenAI 的 arguments 字符串解成 Anthropic 的 input 对象。
//
// **解不出来就报错，不给空对象兜底**：litellm 上出过一次真事故——转换时参数被吃掉，
// tool_use.input 变成 {}，工具"跑了"但什么参数都没带，而调用方从响应里看不出来。
func toolInput(arguments string) (json.RawMessage, error) {
	s := strings.TrimSpace(arguments)
	if s == "" || s == "null" {
		return json.RawMessage(`{}`), nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return nil, fmt.Errorf("tool call arguments are not a JSON object: %s", err)
	}
	return json.RawMessage(s), nil
}

// anthropicImageBlock 把 OpenAI 的图片转成 Anthropic 的 image 块。
func anthropicImageBlock(img *OpenAIImageURL, notes *[]Note) (AnthropicBlock, error) {
	if img.Detail != "" {
		*notes = append(*notes, NoteDroppedImageDetail)
	}
	if rest, ok := strings.CutPrefix(img.URL, "data:"); ok {
		meta, payload, found := strings.Cut(rest, ",")
		if !found {
			return AnthropicBlock{}, &Unsupported{Field: "image_url.url", Reason: "malformed data URL"}
		}
		mediaType, encoding, hasEncoding := strings.Cut(meta, ";")
		// media_type 猜错比报错更难查（上游会拒一张"格式对不上"的图）
		if !hasEncoding || encoding != "base64" || mediaType == "" || payload == "" {
			return AnthropicBlock{}, &Unsupported{Field: "image_url.url", Reason: "only base64 data URLs are supported"}
		}
		return AnthropicBlock{Type: blockImage, Source: &AnthropicImageSource{
			Type:      "base64",
			MediaType: mediaType,
			Data:      payload,
		}}, nil
	}
	if strings.HasPrefix(img.URL, "http://") || strings.HasPrefix(img.URL, "https://") {
		// Anthropic 的 URL 图片源需要较新的 API 版本，记一笔让排查有线索
		*notes = append(*notes, NoteImageByURL)
		return AnthropicBlock{Type: blockImage, Source: &AnthropicImageSource{Type: "url", URL: img.URL}}, nil
	}
	return AnthropicBlock{}, &Unsupported{Field: "image_url.url", Reason: "neither a base64 data URL nor an http(s) URL"}
}

// ---------------------------------------------------------------------------
// 反向：Anthropic → OpenAI
// ---------------------------------------------------------------------------

// AnthropicRequestToOpenAI 把一份 Messages 请求体翻成 Chat Completions 的形状。
func AnthropicRequestToOpenAI(raw []byte, _ Options) ([]byte, []Note, error) {
	var req AnthropicRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, nil, &Unsupported{Field: "body", Reason: "not a JSON messages request: " + err.Error()}
	}

	var notes []Note
	out := OpenAIRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
		Stop:        openAIStopJSON(req.StopSequences),
	}
	if req.Stream {
		// Anthropic 的流一定在 message_delta 里带 usage，OpenAI 的流默认不带，得主动要
		// （stream_options.include_usage）。不注入这一条，这条链路上的 token 会全记成 0，
		// 成本统计静默失真——llmio 对 OpenAI 客户端也是这么做的，见 service.BeforerOpenAI。
		out.StreamOptions = json.RawMessage(`{"include_usage":true}`)
	}
	if req.MaxTokens > 0 {
		maxTokens := req.MaxTokens
		out.MaxTokens = &maxTokens
	}
	if req.TopK != nil {
		notes = append(notes, NoteDroppedTopK)
	}
	if len(req.Thinking) > 0 {
		// 上游给不出思考块，调用方要的那档推理没有对应物
		notes = append(notes, NoteDroppedThinking)
	}
	if len(req.Metadata) > 0 {
		notes = append(notes, NoteDroppedMetadata)
	}

	tools := openAITools(anthropicMergedTools(req.Tools, req.Messages, &notes), &notes)
	choice, err := openAIToolChoice(req.ToolChoice)
	if err != nil {
		return nil, nil, err
	}
	out.Tools = tools
	out.ToolChoice = choice

	system, err := anthropicSystemText(req.System)
	if err != nil {
		return nil, nil, err
	}
	messages := make([]OpenAIMessage, 0, len(req.Messages)+1)
	if strings.TrimSpace(system) != "" {
		messages = append(messages, OpenAIMessage{Role: "system", Content: stringJSON(system)})
	}
	converted, err := anthropicMessagesToOpenAI(req.Messages, &notes)
	if err != nil {
		return nil, nil, err
	}
	out.Messages = append(messages, converted...)

	// 同 OpenAIRequestToAnthropic：字段类型都在 json 能编的范围里
	body, _ := json.Marshal(out)
	return body, dedupeNotes(notes), nil
}

// anthropicMessagesToOpenAI 转换消息数组。
//
// tool_result 要展开成独立的 role:"tool" 消息，且必须**排在同一个 user 轮里其余内容
// 之前**：OpenAI 的 tool 消息要紧接着带 tool_calls 的 assistant 消息，中间夹一条 user
// 文本消息会把配对打断。排过序就记 Note。
func anthropicMessagesToOpenAI(msgs []AnthropicMessage, notes *[]Note) ([]OpenAIMessage, error) {
	out := make([]OpenAIMessage, 0, len(msgs))
	for i, m := range msgs {
		switch m.Role {
		case "system":
			// Anthropic 把 system 放在顶层、messages 里只准有 user/assistant，所以这个形状
			// 不合规；但它**翻得过去**——OpenAI 的 system 是一等角色，出现在哪个位置都收。
			// 判据是目标协议能不能表达，不是源协议规不规范：能搬却拒绝，等于让路由器换掉一家
			// 本来服务得了的上游，而客户端只看到一句"翻不过去"，成因还在翻译层。
			var texts []string
			var parts []OpenAIContentPart
			for _, b := range m.Content {
				switch b.Type {
				case blockText:
					if strings.TrimSpace(b.Text) == "" {
						*notes = append(*notes, NoteDroppedEmptyText)
						continue
					}
					texts = append(texts, b.Text)
				case blockImage:
					part, err := openAIImagePart(b.Source)
					if err != nil {
						return nil, err
					}
					parts = append(parts, part)
				case blockThinking, blockRedacted:
					*notes = append(*notes, NoteDroppedThinking)
				case blockToolAddition, blockToolRemoval:
					// mid-conversation tool changes（Anthropic 的 beta 扩展）只存在于
					// system 消息里，OpenAI 的消息流表示不了这两块。工具层面能表达的部分
					// 由 anthropicInlineTools 处理：内联定义并进顶层 tools，记
					// merged_tool_definition；引用型指向的工具本来就随顶层 tools 转发过，
					// 撤回则没有对应物。这里只把块本身从消息里丢掉并记账。
					*notes = append(*notes, NoteDroppedToolChange)
				default:
					return nil, &Unsupported{
						Field:  fmt.Sprintf("messages[%d].content[].type", i),
						Reason: fmt.Sprintf("unsupported content block %q in a system message", b.Type),
					}
				}
			}
			content := openAIContentJSON(texts, parts, notes)
			if content == nil {
				*notes = append(*notes, NoteDroppedEmptyMessage)
				continue
			}
			// 位置不动、也不与别的 system 合并：OpenAI 允许中途插一条 system，原样带过去
			// 最忠实。这属于纯结构搬运，一个字不多一个字不少，因此不记账
			out = append(out, OpenAIMessage{Role: "system", Content: content})
		case "user":
			var toolMessages []OpenAIMessage
			var texts []string
			var parts []OpenAIContentPart
			hasOther := false
			reordered := false

			for _, b := range m.Content {
				switch b.Type {
				case blockToolResult:
					if hasOther {
						reordered = true
					}
					content, err := anthropicToolResultContent(b.Content, notes)
					if err != nil {
						return nil, err
					}
					if b.IsError != nil && *b.IsError {
						// OpenAI 的 tool 消息没有"这是失败"这个位。丢了模型会以为工具成功了，
						// 于是接着用一份不存在的数据往下推——把失败写进正文是唯一的信息通道。
						// 字符串照旧拼前缀；块数组拼进首个 text 块（没有就插一个纯前缀块），
						// 图片块不受影响
						content = prefixToolError(content)
						*notes = append(*notes, NotePrefixedToolError)
					}
					toolMessages = append(toolMessages, OpenAIMessage{
						Role:       "tool",
						ToolCallID: b.ToolUseID,
						Content:    content,
					})
				case blockText:
					if strings.TrimSpace(b.Text) == "" {
						*notes = append(*notes, NoteDroppedEmptyText)
						continue
					}
					hasOther = true
					texts = append(texts, b.Text)
				case blockImage:
					part, err := openAIImagePart(b.Source)
					if err != nil {
						return nil, err
					}
					hasOther = true
					parts = append(parts, part)
				case blockThinking, blockRedacted:
					*notes = append(*notes, NoteDroppedThinking)
				default:
					return nil, &Unsupported{
						Field:  fmt.Sprintf("messages[%d].content[].type", i),
						Reason: fmt.Sprintf("unsupported content block %q", b.Type),
					}
				}
			}

			if reordered {
				*notes = append(*notes, NoteReorderedToolResult)
			}
			out = append(out, toolMessages...)
			content := openAIContentJSON(texts, parts, notes)
			if content != nil {
				out = append(out, OpenAIMessage{Role: "user", Content: content})
			} else if len(toolMessages) == 0 {
				// 整条消息转完什么都不剩（空文本、空数组）
				*notes = append(*notes, NoteDroppedEmptyMessage)
			}
		case "assistant":
			texts, toolCalls, err := anthropicAssistantToOpenAI(i, m, notes)
			if err != nil {
				return nil, err
			}
			if len(texts) == 0 && len(toolCalls) == 0 {
				*notes = append(*notes, NoteDroppedEmptyMessage)
				continue
			}
			if len(texts) > 0 && len(toolCalls) > 0 {
				// OpenAI 把正文与工具调用放在两个字段里，块与块的相对次序留不住
				*notes = append(*notes, NoteFlattenedContentOrder)
			}
			msg := OpenAIMessage{Role: "assistant", ToolCalls: toolCalls}
			if len(texts) > 0 {
				msg.Content = stringJSON(strings.Join(texts, ""))
			}
			out = append(out, msg)
		default:
			return nil, &Unsupported{
				Field:  fmt.Sprintf("messages[%d].role", i),
				Reason: fmt.Sprintf("unknown role %q", m.Role),
			}
		}
	}
	if len(out) == 0 {
		return nil, &Unsupported{Field: "messages", Reason: "no messages left after conversion"}
	}
	return out, nil
}

// anthropicAssistantToOpenAI 转一条 assistant 消息，返回正文段与工具调用。
func anthropicAssistantToOpenAI(index int, m AnthropicMessage, notes *[]Note) ([]string, []OpenAIToolCall, error) {
	var texts []string
	var calls []OpenAIToolCall
	for _, b := range m.Content {
		switch b.Type {
		case blockText:
			if strings.TrimSpace(b.Text) == "" {
				*notes = append(*notes, NoteDroppedEmptyText)
				continue
			}
			texts = append(texts, b.Text)
		case blockToolUse:
			input := b.Input
			if len(input) == 0 || string(input) == "null" {
				input = json.RawMessage(`{}`)
			}
			calls = append(calls, OpenAIToolCall{
				ID:   b.ID,
				Type: "function",
				Function: OpenAIFunctionCall{
					Name: b.Name,
					// input 在 Anthropic 侧本来就是 JSON 对象，原样变成字符串即可
					Arguments: string(input),
				},
			})
		case blockThinking, blockRedacted:
			// 带签名的思考内容不进上游提示（协议允许在后续轮次里丢掉历史 thinking）
			*notes = append(*notes, NoteDroppedThinking)
		default:
			return nil, nil, &Unsupported{
				Field:  fmt.Sprintf("messages[%d].content[].type", index),
				Reason: fmt.Sprintf("unsupported assistant content block %q", b.Type),
			}
		}
	}
	return texts, calls, nil
}

// anthropicToolResultContent 取 tool_result 的正文：协议允许字符串或块数组。
//
// 纯文本（字符串或全 text 块）拼成一段字符串——兼容性最好，与 openAIContentJSON
// 的约定一致：只有文本给字符串，夹了图片才给块数组。图片块搬运成 OpenAI 的
// image_url 块（tool 消息 content 支持块数组，2025 起；曾经按"装不下"拒绝，
// 前提已过期，见 NoteCarriedToolResultImage）。document / search_result 等
// OpenAI 侧没有对应物的块仍拒绝——丢掉等于让模型瞎着往下推。
func anthropicToolResultContent(raw json.RawMessage, notes *[]Note) (json.RawMessage, error) {
	if len(raw) == 0 {
		return stringJSON(""), nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return stringJSON(s), nil
	}
	var blocks []AnthropicBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, &Unsupported{Field: "tool_result.content", Reason: "neither a string nor a block array"}
	}
	var texts []string
	var parts []OpenAIContentPart
	for _, b := range blocks {
		switch b.Type {
		case blockText:
			texts = append(texts, b.Text)
		case blockImage:
			part, err := openAIImagePart(b.Source)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
			*notes = append(*notes, NoteCarriedToolResultImage)
		default:
			return nil, &Unsupported{
				Field:  "tool_result.content",
				Reason: fmt.Sprintf("the openai protocol cannot carry a %q block inside a tool result", b.Type),
			}
		}
	}
	if len(parts) == 0 {
		return stringJSON(strings.Join(texts, "")), nil
	}
	// 先文本后图片：tool 结果的正文在前，图随后
	out := make([]OpenAIContentPart, 0, len(texts)+len(parts))
	for _, t := range texts {
		out = append(out, OpenAIContentPart{Type: "text", Text: t})
	}
	out = append(out, parts...)
	body, err := json.Marshal(out)
	if err != nil {
		return nil, &Unsupported{Field: "tool_result.content", Reason: err.Error()}
	}
	return body, nil
}

// anthropicSystemText 取 system 的文本：协议允许字符串或块数组。
func anthropicSystemText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var blocks []AnthropicBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", &Unsupported{Field: "system", Reason: "neither a string nor a block array"}
	}
	for _, b := range blocks {
		if b.Type != blockText {
			return "", &Unsupported{Field: "system", Reason: fmt.Sprintf("unsupported system block %q", b.Type)}
		}
	}
	var sb strings.Builder
	for _, b := range blocks {
		sb.WriteString(b.Text)
	}
	return sb.String(), nil
}

// anthropicInlineTools 收集 system 消息里 tool_addition 带的内联完整定义。
//
// mid-conversation tool changes（beta）的一种用法是 addition 里带 tool_definition，
// 完整定义不出现在顶层 tools 里。OpenAI 没有"这一刻才出现"的时序，但顶层工具表本身
// 装得下它——按"能表达就表达"的判据并进去。引用型（tool_reference / MCP 引用）指向的
// 工具要么已在顶层 tools 里、要么根本没有定义可提，都不出结果；块本身的丢弃由
// system 分支统一记 dropped_tool_change，合并这一笔记 merged_tool_definition。
//
// 与顶层同名的定义不提取也不记账（addition 的语义是"没有才加"，顶层声明优先；官方也
// 保证同一定义重发是幂等的），去重属于纯结构。
func anthropicInlineTools(top []AnthropicTool, msgs []AnthropicMessage, notes *[]Note) []AnthropicTool {
	seen := map[string]bool{}
	for _, t := range top {
		seen[t.Name] = true
	}
	var out []AnthropicTool
	for _, m := range msgs {
		if m.Role != "system" {
			continue
		}
		for _, b := range m.Content {
			if b.Type != blockToolAddition || seen[b.Name] {
				continue
			}
			var tool struct {
				Type       string          `json:"type"`
				Definition json.RawMessage `json:"definition"`
			}
			if err := json.Unmarshal(b.Tool, &tool); err != nil || tool.Type != "tool_definition" {
				continue
			}
			var def AnthropicTool
			if err := json.Unmarshal(tool.Definition, &def); err != nil || def.Name == "" || seen[def.Name] {
				continue
			}
			seen[def.Name] = true
			out = append(out, def)
			*notes = append(*notes, NoteMergedToolDefinition)
		}
	}
	return out
}

// anthropicMergedTools 组装发往上游的工具表：顶层声明在前，system 消息里内联的定义
// 追加在后。没有内联定义时原样返回顶层，没有 beta 扩展的请求零开销走原路。
func anthropicMergedTools(top []AnthropicTool, msgs []AnthropicMessage, notes *[]Note) []AnthropicTool {
	inline := anthropicInlineTools(top, msgs, notes)
	if len(inline) == 0 {
		return top
	}
	return append(append(make([]AnthropicTool, 0, len(top)+len(inline)), top...), inline...)
}

// openAITools 搬运工具声明到 function 形状。
func openAITools(tools []AnthropicTool, notes *[]Note) []OpenAITool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]OpenAITool, 0, len(tools))
	for _, t := range tools {
		def := OpenAIFunctionDef{Name: t.Name, Description: t.Description}
		if len(t.InputSchema) > 0 && string(t.InputSchema) != "null" {
			def.Parameters = t.InputSchema
		} else {
			def.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			*notes = append(*notes, NoteDefaultedInputSchema)
		}
		out = append(out, OpenAITool{Type: "function", Function: def})
	}
	return out
}

// openAIToolChoice 转换工具选择。Anthropic 的 any 对应 OpenAI 的 required。
func openAIToolChoice(choice *AnthropicToolChoice) (json.RawMessage, error) {
	if choice == nil {
		return nil, nil
	}
	switch choice.Type {
	case "auto":
		return json.RawMessage(`"auto"`), nil
	case "any":
		return json.RawMessage(`"required"`), nil
	case "none":
		return json.RawMessage(`"none"`), nil
	case "tool":
		if choice.Name == "" {
			return nil, &Unsupported{Field: "tool_choice.name", Reason: "tool choice without a name"}
		}
		// map[string]any 里只有字符串，编不出来是不可能的
		body, _ := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]string{"name": choice.Name},
		})
		return body, nil
	default:
		return nil, &Unsupported{Field: "tool_choice.type", Reason: fmt.Sprintf("unknown value %q", choice.Type)}
	}
}

// openAIImagePart 把 Anthropic 的图片块转成 OpenAI 的 image_url 块。
func openAIImagePart(src *AnthropicImageSource) (OpenAIContentPart, error) {
	if src == nil {
		return OpenAIContentPart{}, &Unsupported{Field: "image.source", Reason: "image block without a source"}
	}
	switch src.Type {
	case "base64":
		if src.MediaType == "" || src.Data == "" {
			return OpenAIContentPart{}, &Unsupported{Field: "image.source", Reason: "base64 image without media_type or data"}
		}
		return OpenAIContentPart{
			Type:     "image_url",
			ImageURL: &OpenAIImageURL{URL: "data:" + src.MediaType + ";base64," + src.Data},
		}, nil
	case "url":
		if src.URL == "" {
			return OpenAIContentPart{}, &Unsupported{Field: "image.source", Reason: "url image without a url"}
		}
		return OpenAIContentPart{Type: "image_url", ImageURL: &OpenAIImageURL{URL: src.URL}}, nil
	default:
		return OpenAIContentPart{}, &Unsupported{Field: "image.source.type", Reason: fmt.Sprintf("unknown source %q", src.Type)}
	}
}

// ---------------------------------------------------------------------------
// 共用的小工具
// ---------------------------------------------------------------------------

// toolErrorPrefix 是 OpenAI 方向上表达"这个工具结果是失败的"的唯一通道。
const toolErrorPrefix = "[tool_error] "

// prefixToolError 把 is_error 前缀拼进 tool 消息正文：content 是字符串时照旧
// 前缀拼接；是块数组时拼进首个 text 块、没有 text 块就插一个纯前缀块——
// 图片块原样保留。拼错了不是提示问题：模型会拿着一份被标成"成功"的失败数据
// 继续推。
func prefixToolError(content json.RawMessage) json.RawMessage {
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return stringJSON(toolErrorPrefix + s)
	}
	var parts []OpenAIContentPart
	if err := json.Unmarshal(content, &parts); err != nil {
		// 既不是字符串也不是块数组：原样返回，让上游去报（与现状一致）
		return content
	}
	for i := range parts {
		if parts[i].Type == "text" {
			parts[i].Text = toolErrorPrefix + parts[i].Text
			out, err := json.Marshal(parts)
			if err != nil {
				return content
			}
			return out
		}
	}
	out, err := json.Marshal(append([]OpenAIContentPart{{Type: "text", Text: toolErrorPrefix}}, parts...))
	if err != nil {
		return content
	}
	return out
}

// openAIContent 解析消息的 content 字段：字符串、块数组或 null。
// 返回 (文本, 块数组)；块数组为 nil 表示"不是数组形状"。
func openAIContent(raw json.RawMessage) (string, []OpenAIContentPart, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil, nil
	}
	var parts []OpenAIContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil, fmt.Errorf("content is neither a string nor an array of parts")
	}
	return "", parts, nil
}

// textOnlyParts 把块数组里的文本拼起来；只要出现非文本块就报错。
//
// 不静默跳过：块数组里塞进来的图片是调用方要模型看的内容，跳过它模型就读不到，而调用方
// 从响应里看不出差别——按 bridge.go 的判据，这种"内容丢了"必须拒绝，由路由器换下一家，
// 而不是发一份模型少看了一张图的请求出去。
func textOnlyParts(parts []OpenAIContentPart) (string, error) {
	var sb strings.Builder
	for _, p := range parts {
		if p.Type != "text" && p.Type != "input_text" {
			return "", fmt.Errorf("unsupported content part %q", p.Type)
		}
		sb.WriteString(p.Text)
	}
	return sb.String(), nil
}

// openAIContentJSON 组装 OpenAI 的 content：只有文本时给字符串（兼容性最好），
// 夹了图片才给块数组。
//
// 多段文本并成一段不记 Note：拼起来一个字不少，模型读到的东西没变，而 Claude Code
// 这类客户端每轮都发好几个文本块，记了只会把真正有损的那几条淹掉。
func openAIContentJSON(texts []string, parts []OpenAIContentPart, notes *[]Note) json.RawMessage {
	if len(parts) == 0 {
		if len(texts) == 0 {
			return nil
		}
		return stringJSON(strings.Join(texts, ""))
	}
	out := make([]OpenAIContentPart, 0, len(parts)+1)
	if len(texts) > 0 {
		// 文本块与图片块的相对次序在拆成两个切片时就丢了，统一把文本放前面并记账
		*notes = append(*notes, NoteFlattenedContentOrder)
		out = append(out, OpenAIContentPart{Type: "text", Text: strings.Join(texts, "")})
	}
	out = append(out, parts...)
	body, _ := json.Marshal(out)
	return body
}

// openAIStopJSON 把 stop_sequences 转成 OpenAI 的 stop。空列表省略。
func openAIStopJSON(seqs []string) json.RawMessage {
	if len(seqs) == 0 {
		return nil
	}
	body, _ := json.Marshal(seqs)
	return body
}

// stringJSON 把字符串编成 JSON 字符串字面量。
func stringJSON(s string) json.RawMessage {
	body, _ := json.Marshal(s)
	return body
}

func toolUseIDs(blocks []AnthropicBlock) []string {
	var ids []string
	for _, b := range blocks {
		if b.Type == blockToolUse {
			ids = append(ids, b.ID)
		}
	}
	return ids
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}

func boolPtr(v bool) *bool { return &v }

// dedupeNotes 去掉重复的 Note 并保持出现次序——同一处改写可能发生几十次（历史里
// 十来个空文本块），逐条记下来只会把日志淹掉。
func dedupeNotes(notes []Note) []Note {
	if len(notes) == 0 {
		return nil
	}
	seen := make(map[Note]bool, len(notes))
	out := make([]Note, 0, len(notes))
	for _, n := range notes {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}
