package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

func (p *AnthropicProvider) streamSSE(ctx context.Context, r io.Reader, out chan<- Delta) error {
	emit := func(d Delta) bool {
		select {
		case out <- d:
			return true
		case <-ctx.Done():
			return false
		}
	}
	toolAcc := make(map[int]*streamedToolCall)
	reasoningAcc := make(map[int]*anthropicReasoningAccumulator)
	completed := make(map[int]json.RawMessage)
	finishReason := ""
	var lastUsage *Usage
	sawResponse := false
	sawStop := false
	parseErr := parseSSE(r, func(eventName, data string) error {
		if isDone(data) || isSSEHeartbeatData(data) {
			return nil
		}
		var ev anthropicEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return fmt.Errorf("anthropic: malformed SSE payload: %w", err)
		}
		switch ev.Type {
		case "ping":
			return nil
		case "message_start":
			sawResponse = true
			// Input-side accounting. Fold the cache read/creation
			// tokens into Input so it means "total prompt tokens"
			// like OpenAI's prompt_tokens, keeping the cache-hit
			// denominator consistent across providers; CachedInput
			// is the portion served from Anthropic's prompt cache.
			if u := ev.Message.Usage; u.InputTokens > 0 || u.CacheReadInputTokens > 0 || u.CacheCreationInputTokens > 0 {
				in := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
				lastUsage = &Usage{Input: in, Total: in, CachedInput: u.CacheReadInputTokens}
			}
		case "content_block_start":
			sawResponse = true
			{
				var raw struct {
					Block json.RawMessage `json:"content_block"`
				}
				if json.Unmarshal([]byte(data), &raw) == nil {
					reasoningAcc[ev.Index] = newAnthropicReasoning(raw.Block)
				}
			}
			if ev.ContentBlock.Type == "tool_use" {
				args := "{}"
				if len(ev.ContentBlock.Input) > 0 {
					args = string(ev.ContentBlock.Input)
				}
				call := &streamedToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}
				call.arguments.WriteString(args)
				toolAcc[ev.Index] = call
			}
		case "content_block_delta":
			sawResponse = true
			switch ev.Delta.Type {
			case "text_delta":
				if acc := reasoningAcc[ev.Index]; acc != nil {
					acc.text.WriteString(ev.Delta.Text)
				}
				if ev.Delta.Text != "" && !emit(Delta{Content: ev.Delta.Text}) {
					return ctx.Err()
				}
			case "thinking_delta":
				if acc := reasoningAcc[ev.Index]; acc != nil {
					acc.thinking.WriteString(ev.Delta.Thinking)
				}
				if ev.Delta.Thinking != "" && !emit(Delta{Reasoning: ev.Delta.Thinking}) {
					return ctx.Err()
				}
			case "signature_delta":
				if acc := reasoningAcc[ev.Index]; acc != nil {
					acc.signature.WriteString(ev.Delta.Signature)
				}
			case "input_json_delta":
				if tc := toolAcc[ev.Index]; tc != nil {
					if tc.arguments.String() == "{}" {
						tc.arguments.Reset()
					}
					tc.arguments.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "content_block_stop":
			sawResponse = true
			if acc := reasoningAcc[ev.Index]; acc != nil {
				if tc := toolAcc[ev.Index]; tc != nil {
					acc.fields["input"] = json.RawMessage(tc.arguments.String())
				}
				completed[ev.Index] = acc.block(p.cfg.Model, p.cfg.BaseURL).Data
				delete(reasoningAcc, ev.Index)
			}
			if tc := toolAcc[ev.Index]; tc != nil {
				tcCopy := tc.snapshot()
				if !emit(Delta{ToolCall: &tcCopy}) {
					return ctx.Err()
				}
				delete(toolAcc, ev.Index)
			}
		case "message_delta":
			sawResponse = true
			finishReason = anthropicFinishReason(ev.Delta.StopReason)
			if ev.Usage.OutputTokens > 0 {
				// Merge with the input-side usage captured at
				// message_start (nil when the server never sent one).
				if lastUsage == nil {
					lastUsage = &Usage{}
				}
				lastUsage.Output = ev.Usage.OutputTokens
				lastUsage.Total = lastUsage.Input + lastUsage.Output
			}
		case "message_stop":
			sawResponse = true
			sawStop = true
			if block := finishAnthropicReasoning(ctx, completed, p.cfg.Model, p.cfg.BaseURL); block != nil {
				if !emit(Delta{NativeReasoning: block}) {
					return ctx.Err()
				}
			}
			if finishReason == "" {
				finishReason = "stop"
			}
			if !emit(Delta{FinishReason: finishReason, Usage: lastUsage}) {
				return ctx.Err()
			}
		case "error":
			message := ev.Error.Message
			if message == "" {
				message = "stream error"
			}
			return fmt.Errorf("anthropic: %s", message)
		}
		return nil
	})
	if parseErr != nil {
		return fmt.Errorf("sse: %w", parseErr)
	}
	if !sawResponse {
		return fmt.Errorf("anthropic: empty stream")
	}
	if !sawStop {
		return fmt.Errorf("anthropic: stream ended before message_stop")
	}
	return nil
}

func anthropicFinishReason(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_calls"
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	default:
		return reason
	}
}

type anthropicEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	// Message carries the message_start payload; its usage holds the
	// input-side accounting (including the prompt-cache breakdown).
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage anthropicUsage `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// anthropicUsage is the usage object of the Messages API. Anthropic
// reports input_tokens EXCLUSIVE of cache activity: prompt tokens
// served from the cache arrive in cache_read_input_tokens and tokens
// written to it in cache_creation_input_tokens. All fields are simply
// zero when the API (or a proxy) omits them — missing telemetry must
// never break the cloud path.
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func patchAnthropicThinking(body []byte, effort string) ([]byte, bool) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false
	}
	if effort == "" || effort == "none" {
		delete(req, "thinking")
	} else {
		maxTokens := 4096
		if v, ok := req["max_tokens"].(float64); ok && v > 0 {
			maxTokens = int(v)
		}
		req["thinking"] = map[string]any{"type": "enabled", "budget_tokens": reasoningBudgetTokens(effort, maxTokens)}
	}
	out, err := json.Marshal(req)
	return out, err == nil
}
