package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type requestPreparationFixture struct {
	name  string
	msgs  []Message
	tools []ToolDef
}

func requestPreparationFixtures(tb testing.TB) []requestPreparationFixture {
	tb.Helper()
	schemas := schemaResidencyBuiltins(tb)
	var tools []ToolDef
	for i, schema := range schemas[:min(24, len(schemas))] {
		tools = append(tools, ToolDef{Name: fmt.Sprintf("tool_%d", i), Description: "Source builtin schema; synthetic name.", Schema: schema})
	}
	makeMessages := func(size int) []Message {
		msgs := []Message{{Role: RoleSystem, Content: "Follow user agreements. Keep the chosen reasoning level. Preserve paths and pending work."}}
		if size < 128 {
			return append(msgs, Message{Role: RoleUser, Content: "Inspect the code and fix the failing operation."})
		}
		// 128 chronological coding messages rather than one enormous text blob.
		for i := 0; i < 32; i++ {
			id := fmt.Sprintf("call_%d", i)
			msgs = append(msgs,
				Message{Role: RoleUser, Content: fmt.Sprintf("Inspect file-%d.go; keep its API and reasoning configuration.", i)},
				Message{Role: RoleAssistant, Content: "Inspecting the source.", ToolCalls: []ToolCall{{ID: id, Name: "tool_0", Arguments: `{"path":"file.go"}`}}},
				Message{Role: RoleTool, ToolCallID: id, Content: strings.Repeat("func example() { return } // retained code\n", size/(32*42))},
				Message{Role: RoleAssistant, Content: "Keep the signature; next inspect callers and validate behavior."},
			)
		}
		return msgs
	}
	return []requestPreparationFixture{
		{name: "tiny-no-tools", msgs: makeMessages(0)},
		{name: "short-three-tools", msgs: makeMessages(0), tools: tools[:3]},
		{name: "short-24-tools", msgs: makeMessages(0), tools: tools},
		{name: "coding-128KiB", msgs: makeMessages(128 << 10), tools: tools},
		{name: "coding-1MiB", msgs: makeMessages(1 << 20), tools: tools},
	}
}

// The baseline and candidate test binaries execute this exact same receipt
// fixture against their own production builders. Compare WIRE lines verbatim.
func TestRequestPreparationWireReceipt(t *testing.T) {
	previousEffort, previousThinking := ReasoningEffort(), ThinkingEnabled()
	t.Cleanup(func() { _ = SetReasoningEffort(previousEffort); SetThinkingEnabled(previousThinking) })
	if err := SetReasoningEffort("xhigh"); err != nil {
		t.Fatal(err)
	}
	SetThinkingEnabled(true)
	fixtures := requestPreparationFixtures(t)
	fixtures = append(fixtures,
		requestPreparationFixture{name: "invalid-schema", msgs: fixtures[0].msgs, tools: []ToolDef{{Name: "broken", Schema: `{bad json`}}},
		requestPreparationFixture{name: "unicode-unions", msgs: []Message{{Role: RoleUser, Content: "Zażółć <&>\u2028"}}, tools: []ToolDef{{Name: "union", Schema: `{"oneOf":[{"properties":{"first":{"const":"Zażółć <&>"}}},{"properties":{"second":{"type":"integer"}}}]}`}}},
	)
	for _, f := range fixtures {
		for _, transport := range []string{"chat", "messages"} {
			var body []byte
			var err error
			if transport == "chat" {
				body, err = buildOpenAIRequestWithReasoningKey("fixture-model", "fixture-model", f.msgs, f.tools, true, true, openAIReasoningNone, 0, Sampling{})
			} else {
				body, err = buildAnthropicRequestWithCaching("fixture-model", f.msgs, f.tools, true, 4096, Sampling{}, true)
			}
			t.Logf("WIRE %s/%s bytes=%d sha256=%x error=%v", transport, f.name, len(body), sha256.Sum256(body), err)
		}
	}
	for _, format := range []openAIReasoningFormat{openAIReasoningEffort, openAIReasoningUnified} {
		body, err := buildOpenAIRequestWithReasoningKey("reasoning-fixture", "reasoning-fixture", fixtures[2].msgs, fixtures[2].tools, true, true, format, 8192, Sampling{})
		if err != nil || (!bytes.Contains(body, []byte(`"reasoning_effort"`)) && !bytes.Contains(body, []byte(`"reasoning"`))) {
			t.Fatalf("receipt omitted chosen thinking format=%d: %v", format, err)
		}
		t.Logf("WIRE chat/effort-%d bytes=%d sha256=%x error=%v", format, len(body), sha256.Sum256(body), err)
	}
	const model = "claude-sonnet-4"
	initial := fixtures[2].msgs
	prior, err := buildAnthropicRequestWithSampling(model, initial, fixtures[2].tools, true, 8192, Sampling{})
	if err != nil || !bytes.Contains(prior, []byte(`"thinking"`)) {
		t.Fatalf("receipt omitted configured thinking: %v", err)
	}
	t.Logf("WIRE messages/thinking bytes=%d sha256=%x error=%v", len(prior), sha256.Sum256(prior), err)
	raw := []byte(`{"type":"assistant","content":[{"type":"thinking","thinking":"synthetic fixture","signature":"opaque+/=<&>","future":{"counter":9007199254740993,"exp":1.2e3}},{"type":"text","text":"visible evidence"}]}`)
	for _, prefix := range []string{"", anthropicRequestPrefix(prior), "mismatched-prefix"} {
		native := nativeReasoning(ReasoningAnthropic, model, "https://preparation.fixture.invalid/v1", raw)
		native.Prefix = prefix
		next := append(append([]Message{}, initial...), Message{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: native}}}, Message{Role: RoleUser, Content: "continue"})
		body, err := buildAnthropicRequestWithCaching(model, next, fixtures[2].tools, true, 8192, Sampling{}, true)
		t.Logf("WIRE messages/opaque-%s bytes=%d sha256=%x error=%v", prefix, len(body), sha256.Sum256(body), err)
	}
}

func requestPreparationProvider(tb testing.TB, transport string) Provider {
	tb.Helper()
	if transport == "messages" {
		return newAnthropicOwnedBodyFixture(tb, "https://preparation.fixture.invalid/v1", func(req *http.Request) (*http.Response, error) {
			if _, err := io.Copy(io.Discard, req.Body); err != nil {
				return nil, err
			}
			return anthropicOwnedBodyResponse(req, 200, anthropicOwnedBodySSE), nil
		})
	}
	p, err := NewOpenAI(OpenAIConfig{
		Model: "fixture-model", BaseURL: "https://preparation.fixture.invalid/v1",
		HTTPClient: &http.Client{Transport: anthropicOwnedBodyTransport(func(req *http.Request) (*http.Response, error) {
			if _, err := io.Copy(io.Discard, req.Body); err != nil {
				return nil, err
			}
			body := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
		})},
	})
	if err != nil {
		tb.Fatal(err)
	}
	return p
}

func completeRequestPreparationFixture(tb testing.TB, p Provider, f requestPreparationFixture) {
	tb.Helper()
	stream, err := p.Complete(context.Background(), f.msgs, f.tools)
	if err != nil {
		tb.Fatal(err)
	}
	finished := false
	for d := range stream {
		if d.Err != nil {
			tb.Fatal(d.Err)
		}
		finished = finished || d.FinishReason != ""
	}
	if !finished {
		tb.Fatal("fixture completion did not finish")
	}
}

// Includes the real builder, HTTP request preparation, deterministic in-process
// transport, response parser and stream drain. No network/model inference.
func BenchmarkRequestPreparationComplete(b *testing.B) {
	for _, f := range requestPreparationFixtures(b) {
		for _, transport := range []string{"chat", "messages"} {
			b.Run(transport+"/"+f.name, func(b *testing.B) {
				p := requestPreparationProvider(b, transport)
				completeRequestPreparationFixture(b, p, f) // warm schemas/capabilities
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					completeRequestPreparationFixture(b, p, f)
				}
			})
		}
	}
}

// Use -benchtime=1x. Cold cache setup is outside the measured Complete.
func BenchmarkRequestPreparationColdComplete(b *testing.B) {
	f := requestPreparationFixtures(b)[2]
	for _, transport := range []string{"chat", "messages"} {
		b.Run(transport, func(b *testing.B) {
			p := requestPreparationProvider(b, transport)
			resetSchemaResidencyCache()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				completeRequestPreparationFixture(b, p, f)
			}
		})
	}
}

func TestRequestPreparationFixtureBodyIndependentOfSchemas(t *testing.T) {
	f := requestPreparationFixtures(t)[2]
	body, err := buildOpenAIRequest("fixture-model", f.msgs, f.tools, true, true)
	if err != nil || !json.Valid(body) {
		t.Fatalf("body invalid: %v", err)
	}
	want := bytes.Clone(body)
	for i := range body {
		body[i] = 'x'
	}
	again, err := buildOpenAIRequest("fixture-model", f.msgs, f.tools, true, true)
	if err != nil || !bytes.Equal(again, want) {
		t.Fatalf("schema-heavy body is shared with a request/cache: %v", err)
	}
}
