package agent

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
)

func TestContextProfileAccountsForEachEstimatedTokenOnce(t *testing.T) {
	native := &llm.ReasoningBlock{Format: llm.ReasoningResponses, Data: json.RawMessage(`{"type":"reasoning","id":"r"}`), Tokens: 123}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: WrapCompactSummary("Done: old work." + compactPreservedUserContext([]llm.Message{{Role: llm.RoleUser, Content: "Keep every restriction. 日本語"}}))},
		{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: native}, {Type: llm.PartTypeText, Text: "checking"}}, ToolCalls: []llm.ToolCall{{ID: "call", Name: "read", Arguments: `{"path":"one.go"}`}}},
		{Role: llm.RoleTool, ToolCallID: "call", Content: "observed content"},
	}
	before, _ := json.Marshal(msgs)
	profile := profileContextMessages(msgs, "test")
	total := 0
	for _, s := range profile.Categories {
		total += s.EstimatedTokens
	}
	if total != llm.EstimateTokens(msgs) || total != profile.EstimatedTokens {
		t.Fatal("component framing double-counted")
	}
	if profile.Categories["native_reasoning"].EstimatedTokens != 123 || profile.CarryRequests != 1 || profile.DuplicateNativeBlocks != 0 {
		t.Fatal("component classification incorrect")
	}
	after, _ := json.Marshal(msgs)
	if string(before) != string(after) {
		t.Fatal("diagnostic changed conversation")
	}
}

type contextCacheTinyReader struct{ io.Reader }

func (r contextCacheTinyReader) Read(b []byte) (int, error) {
	if len(b) > 3 {
		b = b[:3]
	}
	return r.Reader.Read(b)
}
func (r contextCacheTinyReader) Close() error { return nil }

func TestContextCacheTapPreservesStreamAndReportsOnlyNumericUsage(t *testing.T) {
	stream := "data: {\"type\":\"response.created\"}\r\n\r\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"SECRET\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"PRIVATE_TEXT\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1200,\"input_tokens_details\":{\"cached_tokens\":1152,\"absent\":null},\"output_tokens_details\":{\"reasoning_tokens\":55},\"private\":\"PRIVATE_USAGE\"}}}\n\n"
	sample := &contextCacheSample{Usage: map[string]int{}}
	body := &contextCacheBody{ReadCloser: contextCacheTinyReader{strings.NewReader(stream)}, start: time.Now().Add(-time.Second), sample: sample}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != stream {
		t.Fatal("tap changed SSE bytes")
	}
	want := map[string]int{"input_tokens": 1200, "input_tokens_details.cached_tokens": 1152, "output_tokens_details.reasoning_tokens": 55}
	if !sample.CacheFieldPresent || !reflect.DeepEqual(sample.Usage, want) {
		t.Fatalf("incorrect numeric usage: %+v", sample.Usage)
	}
	if sample.CreatedSeconds <= 0 || sample.FirstOutputItemSeconds < sample.CreatedSeconds || sample.FirstTextSeconds < sample.FirstOutputItemSeconds || sample.CompletedSeconds < sample.FirstTextSeconds {
		t.Fatal("incorrect event timing")
	}
	raw, _ := json.Marshal(sample)
	for _, private := range []string{"SECRET", "PRIVATE_TEXT", "PRIVATE_USAGE"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("content leaked into diagnostic")
		}
	}
	if contextCacheHash([]byte("one")) == contextCacheHash([]byte("two")) {
		t.Fatal("diagnostic hash collision")
	}
}

func TestContextCacheTapDistinguishesMissingCacheFromExplicitZero(t *testing.T) {
	for _, test := range []struct {
		usage   string
		present bool
	}{{`{"input_tokens":4}`, false}, {`{"input_tokens_details":{"cached_tokens":0}}`, true}} {
		s := &contextCacheSample{Usage: map[string]int{}}
		body := &contextCacheBody{start: time.Now(), sample: s}
		body.event([]byte(`data: {"type":"response.completed","response":{"usage":` + test.usage + `}}`))
		if s.CacheFieldPresent != test.present {
			t.Fatal("missing cache field became reported zero")
		}
	}
}
