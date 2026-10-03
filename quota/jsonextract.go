package quota

import (
	"encoding/json"
	"strings"
)

// 从"嘈杂输出"里提取契约 JSON。
//
// 为什么需要这个：脚本作者最自然的写法是 console.log(JSON.stringify(x))，
// 而真实输出里往往夹杂别的东西——日志行、被 console.log 展开的嵌套对象、
// 库自行打印的提示。简单"取最后一行 JSON"会取错。
//
// 因此按候选打分：形状越像契约（{items:[...]}）得分越高，同等得分取跨度更长的。
// 这是原 dashboard 的做法，用户脚本依赖它，保留。

// maxCandidates 单次扫描最多考察的候选数。防止在超长输出上做平方级工作。
const maxCandidates = 80

// ExtractJSON 尝试从一段文本里提取 JSON 值。
func ExtractJSON(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}

	// 1) 整段就是 JSON
	if v, ok := tryParse(s); ok {
		return v, true
	}

	// 2) ```json 代码块
	if v, ok := extractFenced(s); ok {
		return v, true
	}

	// 3) 扫描所有 { / [ 起点，按平衡括号切出候选并打分
	var (
		best     any
		bestOK   bool
		bestKey  = -1
		bestSpan = -1
		seen     int
	)
	for i := 0; i < len(s) && seen < maxCandidates; i++ {
		if s[i] != '{' && s[i] != '[' {
			continue
		}
		end := balancedEnd(s, i)
		if end < 0 {
			continue
		}
		seen++
		cand := s[i : end+1]
		v, ok := tryParse(cand)
		if !ok {
			continue
		}
		key := scoreCandidate(v)
		span := len(cand)
		if !bestOK || key > bestKey || (key == bestKey && span > bestSpan) {
			best, bestOK, bestKey, bestSpan = v, true, key, span
		}
	}
	return best, bestOK
}

// ExtractFromLogs 从多行输出里找契约 JSON。
//
// 从**后往前**扫：脚本通常在末尾输出最终结果，
// 而前面的输出多是过程日志。先命中即返回。
func ExtractFromLogs(logs []string) (any, bool) {
	for i := len(logs) - 1; i >= 0; i-- {
		if v, ok := ExtractJSON(logs[i]); ok {
			return v, true
		}
	}
	// 逐行都不成立时，把全部输出拼起来再试一次：
	// 有些脚本把 JSON 分多次打印，单行看都不完整
	return ExtractJSON(strings.Join(logs, "\n"))
}

func tryParse(s string) (any, bool) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, false
	}
	return v, true
}

// extractFenced 取 ```json 围栏内的内容。
func extractFenced(s string) (any, bool) {
	const fence = "```"
	for {
		start := strings.Index(s, fence)
		if start < 0 {
			return nil, false
		}
		rest := s[start+len(fence):]
		// 跳过语言标记那一行
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		end := strings.Index(rest, fence)
		if end < 0 {
			return nil, false
		}
		if v, ok := tryParse(strings.TrimSpace(rest[:end])); ok {
			return v, true
		}
		// 这一段不是 JSON，继续找下一个围栏
		s = rest[end+len(fence):]
	}
}

// balancedEnd 返回与 s[start] 配对的右括号下标；不平衡时返回 -1。
//
// 必须识别字符串与转义，否则内容里出现的括号会把配对算错——
// 余量数据里出现 "{" 或 "}" 的字符串很常见（模板、JSON 字符串字段）。
func balancedEnd(s string, start int) int {
	if start >= len(s) {
		return -1
	}
	var open, close byte
	switch s[start] {
	case '{':
		open, close = '{', '}'
	case '[':
		open, close = '[', ']'
	default:
		return -1
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// scoreCandidate 给候选的"像不像契约"打分。
//
// 分数只用于排序，绝对值无意义。区分度来自形状：
// 明确的 items 数组 > data 数组 > 对象映射 > 单条含余量字段的对象 > 裸数组 > 其它对象。
func scoreCandidate(v any) int {
	switch t := v.(type) {
	case map[string]any:
		if arr, ok := t["items"].([]any); ok {
			return 100 + len(arr)
		}
		if _, ok := t["items"].(map[string]any); ok {
			return 70
		}
		if arr, ok := t["data"].([]any); ok {
			return 90 + len(arr)
		}
		if _, ok := t["data"].(map[string]any); ok {
			return 30
		}
		for _, key := range []string{"used", "total", "remaining", "balance", "limit",
			"usedAmount", "totalAmount", "已用", "总量", "剩余", "余额"} {
			if _, ok := t[key]; ok {
				return 60
			}
		}
		return 10
	case []any:
		return 40 + len(t)
	default:
		return 0
	}
}
