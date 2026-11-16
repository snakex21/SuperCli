package main

import (
	"bytes"
	"encoding/json"
	"time"
)

// streamTiming watches a forwarded response body and records when the first
// byte and the first model output (text, reasoning or tool-call delta)
// arrived, plus the last usage object the stream reported. It understands
// Chat Completions SSE (choices[].delta), Responses SSE (*.delta events) and
// Anthropic SSE (content_block_delta); anything else only gets first byte.
type streamTiming struct {
	start      time.Time
	firstByte  time.Duration
	firstDelta time.Duration
	usage      json.RawMessage
	pending    []byte
}

func newStreamTiming(start time.Time) *streamTiming { return &streamTiming{start: start} }

func (s *streamTiming) observe(chunk []byte, now time.Time) {
	if len(chunk) == 0 {
		return
	}
	if s.firstByte == 0 {
		s.firstByte = now.Sub(s.start)
	}
	s.pending = append(s.pending, chunk...)
	for {
		i := bytes.IndexByte(s.pending, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(s.pending[:i])
		s.pending = s.pending[i+1:]
		s.line(line, now)
	}
}

// finish handles a non-streamed (plain JSON) body or a last line without a
// newline.
func (s *streamTiming) finish(now time.Time) {
	rest := bytes.TrimSpace(s.pending)
	s.pending = nil
	if len(rest) == 0 {
		return
	}
	if bytes.HasPrefix(rest, []byte("{")) {
		s.event(rest, now)
		return
	}
	s.line(rest, now)
}

func (s *streamTiming) line(line []byte, now time.Time) {
	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return
	}
	s.event(data, now)
}

func (s *streamTiming) event(data []byte, now time.Time) {
	var ev struct {
		Type    string `json:"type"`
		Delta   json.RawMessage
		Choices []struct {
			Delta struct {
				Content          string            `json:"content"`
				Reasoning        string            `json:"reasoning"`
				ReasoningContent string            `json:"reasoning_content"`
				ToolCalls        []json.RawMessage `json:"tool_calls"`
			} `json:"delta"`
			Message *json.RawMessage `json:"message"`
		} `json:"choices"`
		Usage    json.RawMessage `json:"usage"`
		Response *struct {
			Usage json.RawMessage `json:"usage"`
		} `json:"response"`
		Message *struct {
			Usage json.RawMessage `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	if s.firstDelta == 0 {
		output := false
		for _, c := range ev.Choices {
			d := c.Delta
			if d.Content != "" || d.Reasoning != "" || d.ReasoningContent != "" || len(d.ToolCalls) > 0 || c.Message != nil {
				output = true
			}
		}
		if len(ev.Delta) > 0 && string(ev.Delta) != `""` && string(ev.Delta) != "null" &&
			(bytes.HasSuffix([]byte(ev.Type), []byte(".delta")) || ev.Type == "content_block_delta") {
			output = true
		}
		if output {
			s.firstDelta = now.Sub(s.start)
		}
	}
	for _, u := range []json.RawMessage{ev.Usage, usageOf(ev.Response), usageOfMsg(ev.Message)} {
		if len(u) > 0 && string(u) != "null" {
			s.usage = append(json.RawMessage(nil), u...)
		}
	}
}

func usageOf(r *struct {
	Usage json.RawMessage `json:"usage"`
}) json.RawMessage {
	if r == nil {
		return nil
	}
	return r.Usage
}

func usageOfMsg(m *struct {
	Usage json.RawMessage `json:"usage"`
}) json.RawMessage {
	if m == nil {
		return nil
	}
	return m.Usage
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
