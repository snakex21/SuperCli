package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestReasoningShapeDispatchDifferential(t *testing.T) {
	inputs := []json.RawMessage{
		nil, []byte(""), []byte(" \t\r\n"), []byte("null"), []byte("true"), []byte("123"), []byte("{}"), []byte("[]"),
		[]byte(`"exact ✓"`), []byte(`{"type":"reasoning.text","format":"unknown","text":"exact ✓"}`),
		[]byte(`[null,{"text":"a"},"b",false,3]`), []byte(`{"text":null,"value":"selected","a":"ignored"}`),
		[]byte(`{"text":"first","text":"second"}`), []byte(`{"text":3,"text":"second"}`),
		[]byte(`{"\u0074ext":"escaped key","text":"last"}`), []byte(`{"text":"\ud800"}`),
		[]byte(`{"text":"first","text":false,"value":["later",null,3]}`),
		[]byte(`{"signature":"hidden","encrypted_content":"hidden","unknown":"visible","id":"hidden"}`),
		[]byte(`{"TEXT":"unknown spelling","a":["a",{"thinking":"b"}]}`),
		[]byte(`{"text":{"value":["a","b"]},"reasoning":{"delta":"c"}}`),
		[]byte(`{"text":"visible","unknown":"ignored","thinking":null}`),
		[]byte(`"truncated`), []byte(`{"text":"x"`), []byte(`["x"`), []byte(`"x" []`), []byte(`{"text":"x"} []`),
		[]byte("\u00a0\"rejected whitespace\""), []byte("\xef\xbb\xbf\"rejected BOM\""),
	}
	for i := 0; i < 256; i++ {
		leaf := append([]byte{'"'}, byte(i), '"')
		inputs = append(inputs, leaf, append(append([]byte(`{"text":`), leaf...), '}'), append(append([]byte{'['}, leaf...), ']'))
	}
	seed := rand.New(rand.NewSource(14))
	for i := 0; i < 512; i++ {
		raw := make([]byte, seed.Intn(80))
		_, _ = seed.Read(raw)
		inputs = append(inputs, raw)
	}
	for _, raw := range inputs {
		before := append([]byte(nil), raw...)
		want := reasoningShapeOracleExtractStringLeaves(raw)
		got := extractStringLeaves(raw)
		if got != want {
			t.Fatalf("raw=%q got=%q want=%q", raw, got, want)
		}
		if !bytes.Equal(before, raw) {
			t.Fatal("raw input mutated")
		}
		for _, ws := range []string{" ", "\t\n\r ", "\u00a0", "\v"} {
			wrapped := append(append([]byte(ws), raw...), []byte(ws)...)
			if got, want := extractStringLeaves(wrapped), reasoningShapeOracleExtractStringLeaves(wrapped); got != want {
				t.Fatalf("whitespace parity raw=%q", wrapped)
			}
		}
	}
}

type reasoningShapeTransport func(*http.Request) (*http.Response, error)

func (f reasoningShapeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reasoningShapeFrames(mode string, n, size int) string {
	var out strings.Builder
	for i := 0; i < n; i++ {
		value := strings.Repeat("s", size)
		var delta map[string]any
		switch mode {
		case "plain":
			delta = map[string]any{"content": value}
		case "native-flat":
			delta = map[string]any{"reasoning_content": value}
		case "generic-flat":
			delta = map[string]any{"reasoning": value}
		case "structured":
			delta = map[string]any{"reasoning": map[string]any{"type": "reasoning.text", "format": "unknown", "text": value}}
		case "array":
			delta = map[string]any{"reasoning_details": []any{map[string]any{"text": value, "type": "reasoning.text", "index": i}}}
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}})
		out.WriteString("data: ")
		out.Write(raw)
		out.WriteString("\n\n")
	}
	out.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Done\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":11,\"total_tokens\":18}}\n\ndata: [DONE]\n\n")
	return out.String()
}

func reasoningShapeComplete(t testing.TB, base, frames string) ([]byte, []Delta) {
	t.Helper()
	var wire []byte
	p, err := NewOpenAI(OpenAIConfig{BaseURL: base, Model: "fixture", HTTPClient: &http.Client{Transport: reasoningShapeTransport(func(req *http.Request) (*http.Response, error) {
		var err error
		wire, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(frames)), Request: req}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := p.Complete(WithOpenCodeSession(context.Background(), "ses_0123456789abcdef0123456789"), []Message{{Role: RoleUser, Content: "synthetic decoder fixture"}}, []ToolDef{{Name: "fixture", Schema: `{"type":"object"}`}})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []Delta
	for d := range stream {
		deltas = append(deltas, d)
	}
	return wire, deltas
}

func TestReasoningShapeDispatchPublicComplete(t *testing.T) {
	for _, base := range []string{"https://fixture.invalid/v1", "https://opencode.ai/zen/v1"} {
		for _, mode := range []string{"plain", "native-flat", "generic-flat", "structured", "array"} {
			t.Run(base+"/"+mode, func(t *testing.T) {
				frames := reasoningShapeFrames(mode, 4, 33)
				wire, deltas := reasoningShapeComplete(t, base, frames)
				var text, reason strings.Builder
				var native *ReasoningBlock
				var usage *Usage
				for _, d := range deltas {
					if d.Err != nil {
						t.Fatal(d.Err)
					}
					text.WriteString(d.Content)
					reason.WriteString(d.Reasoning)
					if d.NativeReasoning != nil {
						native = d.NativeReasoning
					}
					if d.Usage != nil {
						usage = d.Usage
					}
				}
				wantText := "Done"
				wantReason := strings.Repeat("s", 4*33)
				if mode == "plain" {
					wantText = strings.Repeat("s", 4*33) + "Done"
					wantReason = ""
				}
				if text.String() != wantText || reason.String() != wantReason {
					t.Fatalf("content/reason parity: %q/%q", text.String(), reason.String())
				}
				if mode == "native-flat" || mode == "generic-flat" {
					if native == nil {
						t.Fatal("native history missing")
					}
					if err := native.Validate(); err != nil {
						t.Fatal(err)
					}
				} else if native != nil {
					t.Fatal("unexpected native history")
				}
				if !reflect.DeepEqual(usage, &Usage{Input: 7, Output: 11, Total: 18}) {
					t.Fatalf("usage=%+v", usage)
				}
				raw, _ := json.Marshal(deltas)
				sum := sha256.Sum256(raw)
				wireSum := sha256.Sum256(wire)
				t.Logf("mode=%s wire=%d/%s deltas=%d/%s", mode, len(wire), hex.EncodeToString(wireSum[:]), len(raw), hex.EncodeToString(sum[:]))
			})
		}
	}
}

func TestReasoningShapeDispatchMalformedControls(t *testing.T) {
	for _, raw := range []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":{"text":"wrong native type"}}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning":{"text":"x"}}}]`,
		`{"choices":[{"index":0,"delta":{"reasoning":{"text":"x"}}}]} trailing`,
	} {
		_, deltas := reasoningShapeComplete(t, "https://fixture.invalid/v1", "data: "+raw+"\n\n")
		var errorsSeen []string
		for _, d := range deltas {
			if d.Err != nil {
				errorsSeen = append(errorsSeen, d.Err.Error())
			}
		}
		if len(errorsSeen) != 1 || !strings.Contains(errorsSeen[0], errSSEUnexpected.Error()) {
			t.Fatalf("malformed handling=%v", errorsSeen)
		}
		sum := sha256.Sum256([]byte(errorsSeen[0]))
		t.Logf("error=%s", hex.EncodeToString(sum[:]))
	}
}

func BenchmarkReasoningShapeDispatchComplete(b *testing.B) {
	for _, mode := range []string{"plain", "native-flat", "generic-flat", "structured", "array"} {
		b.Run(mode, func(b *testing.B) {
			frames := reasoningShapeFrames(mode, 128, 32)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, deltas := reasoningShapeComplete(b, "https://fixture.invalid/v1", frames)
				if len(deltas) == 0 {
					b.Fatal("empty stream")
				}
			}
		})
	}
}
