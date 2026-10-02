package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func referenceNonWhitespaceBytes(raw []byte) int {
	n := 0
	for _, b := range raw {
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			n++
		}
	}
	return n
}

func TestNonWhitespaceBytesPreservesStringByteCount(t *testing.T) {
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"nil", nil}, {"empty", []byte{}}, {"ascii whitespace", []byte(" \t\r\n")},
		{"UTF-8", []byte("Zażółć gęślą jaźń 😀\u00a0\u2003\n\t")},
		{"escaped JSON", []byte(`{"reasoning_content":"zażółć\n\t next\u0020"}`)},
		{"all byte values including invalid UTF-8", allBytes},
		{"large native JSON", []byte(strings.Repeat("payload 😀\t\r\n space ", 1600))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := bytes.Clone(tc.data)
			want := referenceNonWhitespaceBytes(tc.data)
			if got := nonWhitespaceBytes(tc.data); got != want {
				t.Fatalf("count=%d want=%d", got, want)
			}
			if got := nonWhitespaceBytes(tc.data); got != nonWhitespaceLen(string(tc.data)) {
				t.Fatalf("byte/string count mismatch: %d", got)
			}
			if !bytes.Equal(tc.data, before) {
				t.Fatal("counter rewrote the payload")
			}
		})
	}
	// Exercise both sides of the optimized-path threshold with every byte value.
	for _, length := range []int{0, 1, 63, 64, 65, 257} {
		for value := 0; value < 256; value++ {
			raw := bytes.Repeat([]byte{byte(value)}, length)
			if got, want := nonWhitespaceBytes(raw), referenceNonWhitespaceBytes(raw); got != want {
				t.Fatalf("length=%d byte=%d count=%d want=%d", length, value, got, want)
			}
		}
	}
}

func TestReasoningEstimatePreservesNativePayloadAndTokenCount(t *testing.T) {
	thinking := json.RawMessage(`{"type":"thinking","thinking":"Zażółć 😀 \t\n  text","signature":"signed bytes"}`)
	redacted := json.RawMessage(`{"type":"redacted_thinking","data":"opaque \u0020 bytes"}`)
	wrapped, err := json.Marshal(struct {
		Type    string            `json:"type"`
		Content []json.RawMessage `json:"content"`
	}{"assistant", []json.RawMessage{
		thinking, json.RawMessage(`{"type":"text","text":"visible text counted separately"}`),
		redacted, json.RawMessage(`{"type":"tool_use","id":"x","name":"read","input":{}}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, format string
		raw          []byte
		tokens, want int
	}{
		{"empty chat", ReasoningChat, nil, 0, 0},
		{"empty responses", ReasoningResponses, []byte{}, 0, 0},
		{"reported tokens win", ReasoningChat, []byte("invalid data"), 1234, 1234},
		{"chat UTF-8", ReasoningChat, thinking, 0, referenceNonWhitespaceBytes(thinking) / estBytesPerToken},
		{"responses opaque", ReasoningResponses, redacted, 0, referenceNonWhitespaceBytes(redacted) / estBytesPerToken},
		{"Anthropic thinking block", ReasoningAnthropic, thinking, 0, referenceNonWhitespaceBytes(thinking) / estBytesPerToken},
		{"Anthropic assistant excludes text and calls", ReasoningAnthropic, wrapped, 0, (referenceNonWhitespaceBytes(thinking) + referenceNonWhitespaceBytes(redacted)) / estBytesPerToken},
		{"Anthropic empty content fallback", ReasoningAnthropic, []byte(`{"type":"assistant","content":[]}`), 0, referenceNonWhitespaceBytes([]byte(`{"type":"assistant","content":[]}`)) / estBytesPerToken},
		{"invalid JSON fallback", ReasoningAnthropic, []byte(" \xff\t broken 😀\r\n"), 0, referenceNonWhitespaceBytes([]byte(" \xff\t broken 😀\r\n")) / estBytesPerToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &ReasoningBlock{Format: tc.format, Model: "fixture", Scope: "scope", Data: bytes.Clone(tc.raw), Tokens: tc.tokens, Prefix: "unchanged signed prefix"}
			before := bytes.Clone(b.Data)
			for repeat := 0; repeat < 3; repeat++ {
				if got := b.EstimateTokens(); got != tc.want {
					t.Fatalf("tokens=%d want=%d", got, tc.want)
				}
			}
			if !bytes.Equal(b.Data, before) || b.Format != tc.format || b.Tokens != tc.tokens || b.Model != "fixture" || b.Scope != "scope" || b.Prefix != "unchanged signed prefix" {
				t.Fatal("estimate modified the native block")
			}
		})
	}
	var absent *ReasoningBlock
	if got := absent.EstimateTokens(); got != 0 {
		t.Fatalf("nil estimate=%d", got)
	}
}

var nativeReasoningEstimateSink int

func TestReasoningRawEstimateDoesNotCopyPayload(t *testing.T) {
	raw := json.RawMessage(strings.Repeat("native continuation 😀 \n\t", 1600))
	for _, format := range []string{ReasoningChat, ReasoningResponses} {
		t.Run(format, func(t *testing.T) {
			b := &ReasoningBlock{Format: format, Data: raw}
			allocs := testing.AllocsPerRun(100, func() { nativeReasoningEstimateSink = b.EstimateTokens() })
			if allocs != 0 {
				t.Fatalf("raw estimate allocated %.1f times per call", allocs)
			}
			if nativeReasoningEstimateSink != referenceNonWhitespaceBytes(raw)/estBytesPerToken {
				t.Fatal("unexpected estimate")
			}
		})
	}
}

func BenchmarkNativeReasoningRawEstimate(b *testing.B) {
	for _, size := range []int{32 << 10, 512 << 10} {
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			raw := json.RawMessage(strings.Repeat("signed native payload 😀 \t\r\n", size/32))
			block := &ReasoningBlock{Format: ReasoningResponses, Data: raw}
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			b.ResetTimer()
			for b.Loop() {
				nativeReasoningEstimateSink = block.EstimateTokens()
			}
		})
	}
}
