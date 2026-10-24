package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atopos31/llmio/bridge"
	"github.com/atopos31/llmio/models"
)

func usageStream(start string, deltas ...string) string {
	var s strings.Builder
	fmt.Fprintf(&s, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"model\":\"test\",\"usage\":%s}}\n\n", start)
	for i, usage := range deltas {
		stop := "null"
		if i == len(deltas)-1 {
			stop = `"end_turn"`
		}
		fmt.Fprintf(&s, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%s},\"usage\":%s}\n\n", stop, usage)
	}
	s.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return s.String()
}

func wantUsage(prompt, completion, cached int64) models.Usage {
	return models.Usage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion,
		PromptTokensDetails: models.PromptTokensDetails{CachedTokens: cached}}
}

func checkUsage(t *testing.T, processor Processer, body string, stream bool, want models.Usage) {
	t.Helper()
	log, output, err := processor(context.Background(), strings.NewReader(body), stream, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if log.Usage != want {
		t.Fatalf("usage = %+v; want %+v", log.Usage, want)
	}
	if !stream && output.OfString != body {
		t.Fatal("non-streaming response was changed")
	}
	if stream {
		var payloads []string
		for _, line := range strings.Split(body, "\n") {
			if data, ok := strings.CutPrefix(line, "data: "); ok && data != "[DONE]" {
				payloads = append(payloads, data)
			}
		}
		if !slices.Equal(output.OfStringArray, payloads) {
			t.Fatal("streaming response payloads were changed")
		}
	}
}

func TestProcesserAnthropicUsage(t *testing.T) {
	for _, tc := range []struct {
		name, start string
		deltas      []string
		want        models.Usage
	}{
		{"standard", `{"input_tokens":11,"output_tokens":1}`, []string{`{"output_tokens":8}`}, wantUsage(11, 8, 0)},
		{"cache buckets", `{"input_tokens":11,"cache_creation_input_tokens":5,"cache_read_input_tokens":7,"output_tokens":1}`, []string{`{"output_tokens":8}`}, wantUsage(23, 8, 7)},
		{"cumulative deltas", `{"input_tokens":11,"output_tokens":1}`, []string{`{"output_tokens":3}`, `{"output_tokens":8}`}, wantUsage(11, 8, 0)},
		{"explicit zero overrides", `{"input_tokens":11,"cache_creation_input_tokens":5,"cache_read_input_tokens":7,"output_tokens":1}`, []string{`{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}`}, wantUsage(0, 0, 0)},
		{"full final usage", `{"input_tokens":0,"output_tokens":0}`, []string{`{"input_tokens":11,"cache_creation_input_tokens":5,"cache_read_input_tokens":7,"output_tokens":8}`}, wantUsage(23, 8, 7)},
		{"null final usage", `{"input_tokens":11,"cache_read_input_tokens":7,"output_tokens":1}`, []string{`null`}, wantUsage(18, 1, 7)},
		{"empty final usage", `{"input_tokens":11,"cache_read_input_tokens":7,"output_tokens":1}`, []string{`{}`}, wantUsage(18, 1, 7)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := usageStream(tc.start, tc.deltas...)
			t.Run("native", func(t *testing.T) { checkUsage(t, ProcesserAnthropic, body, true, tc.want) })
			t.Run("bridged", func(t *testing.T) {
				converter := bridge.NewAnthropicToOpenAIStream()
				converted, err := converter.Write([]byte(body))
				if err != nil {
					t.Fatal(err)
				}
				end, err := converter.Close()
				if err != nil {
					t.Fatal(err)
				}
				checkUsage(t, ProcesserOpenAI, string(append(converted, end...)), true, tc.want)
			})
		})
	}
	const response = `{"type":"message","id":"msg_test","model":"test","role":"assistant","content":[],"stop_reason":"end_turn","usage":{"input_tokens":11,"cache_creation_input_tokens":5,"cache_read_input_tokens":7,"output_tokens":8}}`
	t.Run("nonstream native", func(t *testing.T) { checkUsage(t, ProcesserAnthropic, response, false, wantUsage(23, 8, 7)) })
	t.Run("nonstream bridged", func(t *testing.T) {
		body, _, err := bridge.AnthropicResponseToOpenAI([]byte(response))
		if err != nil {
			t.Fatal(err)
		}
		checkUsage(t, ProcesserOpenAI, string(body), false, wantUsage(23, 8, 7))
	})
}

func TestProcesserOpenAIUsage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		usages []string
		want   models.Usage
	}{
		{"final only", []string{`null`, `{"prompt_tokens":11,"completion_tokens":8,"total_tokens":19}`}, wantUsage(11, 8, 0)},
		{"cumulative", []string{`{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}`, `{"prompt_tokens":11,"completion_tokens":8,"total_tokens":19}`}, wantUsage(11, 8, 0)},
		{"explicit zero final", []string{`{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}`, `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`}, wantUsage(0, 0, 0)},
		{"null is absent", []string{`{"prompt_tokens":11,"completion_tokens":8,"total_tokens":19}`, `null`}, wantUsage(11, 8, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body strings.Builder
			for _, usage := range tc.usages {
				fmt.Fprintf(&body, "data: {\"choices\":[],\"usage\":%s}\n\n", usage)
			}
			body.WriteString("data: [DONE]\n\n")
			checkUsage(t, ProcesserOpenAI, body.String(), true, tc.want)
		})
	}
}
