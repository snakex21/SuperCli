package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func nativeReplayFixtureBlock(format, raw string) *ReasoningBlock {
	return &ReasoningBlock{Format: format, Model: "fixture", Scope: "fixture", Data: json.RawMessage(raw), Prefix: "unchanged-signature-prefix"}
}

func TestNativeReasoningValidationContracts(t *testing.T) {
	cases := []struct{ name, format, raw string }{
		{"chat", ReasoningChat, "{\"reasoning_content\":\"plan\"}"},
		{"chat alias", ReasoningChat, "{\"reasoning\":\"plan\"}"},
		{"chat second alias", ReasoningChat, "{\"reasoning_text\":\"plan\"}"},
		{"chat null text legacy accepted", ReasoningChat, "{\"reasoning\":null}"},
		{"chat empty text", ReasoningChat, "{\"reasoning\":\"\"}"},
		{"chat duplicate last wins", ReasoningChat, "{\"reasoning\":7,\"reasoning\":\"last\"}"},
		{"chat duplicate last invalid", ReasoningChat, "{\"reasoning\":\"first\",\"reasoning\":7}"},
		{"chat case exact", ReasoningChat, "{\"Reasoning_content\":\"plan\"}"},
		{"chat uppercase exact", ReasoningChat, "{\"REASONING\":\"plan\"}"},
		{"chat duplicate case distinct", ReasoningChat, "{\"reasoning\":\"plan\",\"Reasoning\":\"other\"}"},
		{"chat escaped key", ReasoningChat, "{\"reasoning_\\u0063ontent\":\"plan\"}"},
		{"chat unicode escaped text", ReasoningChat, "{\"reasoning\":\"Zażółć \\n \\uD83D\\uDE00 \\u003Cplan\\u003E\"}"},
		{"chat extra key", ReasoningChat, "{\"reasoning\":\"plan\",\"future\":true}"},
		{"chat two allowed keys", ReasoningChat, "{\"reasoning\":\"plan\",\"reasoning_text\":\"other\"}"},
		{"chat number", ReasoningChat, "{\"reasoning\":17}"},
		{"chat bool", ReasoningChat, "{\"reasoning\":true}"},
		{"chat object", ReasoningChat, "{\"reasoning\":{}}"},
		{"chat array", ReasoningChat, "{\"reasoning\":[]}"},
		{"chat no field", ReasoningChat, "{}"},
		{"empty", ReasoningChat, ""},
		{"null", ReasoningChat, "null"},
		{"array", ReasoningChat, "[]"},
		{"number", ReasoningChat, "5"},
		{"string", ReasoningChat, "\"value\""},
		{"malformed", ReasoningChat, "{\"reasoning\":\"unfinished"},
		{"trailing JSON", ReasoningChat, "{\"reasoning\":\"plan\"} {}"},
		{"trailing comma", ReasoningChat, "{\"reasoning\":\"plan\",}"},
		{"unknown format", "CHAT", "{\"reasoning\":\"plan\"}"},
		{"responses opaque", ReasoningResponses, "{\"type\":\"reasoning\",\"id\":\"fixture\",\"encrypted_content\":\"opaque\",\"future\":{\"signature\":\"unchanged\"}}"},
		{"responses wrong type", ReasoningResponses, "{\"type\":\"assistant\"}"},
		{"responses exact lowercase type", ReasoningResponses, "{\"TYPE\":\"reasoning\"}"},
		{"responses case keys distinct", ReasoningResponses, "{\"type\":\"reasoning\",\"TYPE\":\"other\"}"},
		{"responses null type", ReasoningResponses, "{\"type\":null}"},
		{"anthropic thinking", ReasoningAnthropic, "{\"type\":\"thinking\",\"thinking\":\"plan\",\"signature\":\"opaque\",\"future\":7}"},
		{"anthropic redacted", ReasoningAnthropic, "{\"type\":\"redacted_thinking\",\"data\":\"opaque\"}"},
		{"anthropic assistant", ReasoningAnthropic, "{\"type\":\"assistant\",\"content\":[{\"type\":\"thinking\",\"signature\":\"opaque\"},{\"type\":\"text\",\"text\":\"Done\"}],\"future\":{\"keep\":true}}"},
		{"anthropic invalid case type", ReasoningAnthropic, "{\"TYPE\":\"thinking\"}"},
		{"anthropic exact case type", ReasoningAnthropic, "{\"type\":\"thinking\",\"TYPE\":\"other\"}"},
		{"anthropic invalid content case", ReasoningAnthropic, "{\"type\":\"assistant\",\"Content\":[{\"type\":\"thinking\"}]}"},
		{"anthropic empty content", ReasoningAnthropic, "{\"type\":\"assistant\",\"content\":[]}"},
		{"anthropic null content", ReasoningAnthropic, "{\"type\":\"assistant\",\"content\":null}"},
		{"anthropic malformed content", ReasoningAnthropic, "{\"type\":\"assistant\",\"content\":{}}"},
		{"anthropic no thinking", ReasoningAnthropic, "{\"type\":\"assistant\",\"content\":[{\"type\":\"text\"}]}"},
		{"anthropic null child", ReasoningAnthropic, "{\"type\":\"assistant\",\"content\":[null]}"},
		{"anthropic wrong child type", ReasoningAnthropic, "{\"type\":\"assistant\",\"content\":[{\"type\":5}]}"},
		{"anthropic missing child type", ReasoningAnthropic, "{\"type\":\"assistant\",\"content\":[{\"Type\":\"thinking\"}]}"},
	}
	invalid := map[string]string{
		"chat duplicate last invalid":    "chat reasoning must be text",
		"chat case exact":                "unsupported chat field",
		"chat uppercase exact":           "unsupported chat field",
		"chat duplicate case distinct":   "expected one native chat field",
		"chat extra key":                 "expected one native chat field",
		"chat two allowed keys":          "expected one native chat field",
		"chat number":                    "chat reasoning must be text",
		"chat bool":                      "chat reasoning must be text",
		"chat object":                    "chat reasoning must be text",
		"chat array":                     "chat reasoning must be text",
		"chat no field":                  "expected one native chat field",
		"empty":                          "invalid origin or payload",
		"null":                           "payload must be an object",
		"array":                          "payload must be an object",
		"number":                         "payload must be an object",
		"string":                         "payload must be an object",
		"malformed":                      "invalid origin or payload",
		"trailing JSON":                  "invalid origin or payload",
		"trailing comma":                 "invalid origin or payload",
		"unknown format":                 "unknown format \"CHAT\"",
		"responses wrong type":           "expected reasoning item",
		"responses exact lowercase type": "expected reasoning item",
		"responses null type":            "expected reasoning item",
		"anthropic invalid case type":    "expected thinking block",
		"anthropic invalid content case": "missing native assistant content",
		"anthropic empty content":        "missing native assistant content",
		"anthropic null content":         "missing native assistant content",
		"anthropic malformed content":    "missing native assistant content",
		"anthropic no thinking":          "native assistant has no thinking",
		"anthropic null child":           "invalid content block",
		"anthropic wrong child type":     "invalid content block",
		"anthropic missing child type":   "invalid content block",
	}
	type validationCase struct {
		name, want string
		block      *ReasoningBlock
	}
	tests := []validationCase{
		{name: "nil", want: "invalid origin or payload"},
		{name: "missing model", want: "invalid origin or payload", block: nativeReplayFixtureBlock(ReasoningChat, "{\"reasoning\":\"plan\"}")},
		{name: "missing scope", want: "invalid origin or payload", block: nativeReplayFixtureBlock(ReasoningChat, "{\"reasoning\":\"plan\"}")},
	}
	tests[1].block.Model = ""
	tests[2].block.Scope = ""
	for _, tc := range cases {
		tests = append(tests, validationCase{name: tc.name, want: invalid[tc.name], block: nativeReplayFixtureBlock(tc.format, tc.raw)})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			block := tc.block
			before, _ := json.Marshal(block)
			var got string
			if err := block.Validate(); err != nil {
				got = strings.TrimPrefix(err.Error(), "reasoning part: ")
			}
			if got != tc.want {
				t.Fatalf("validation=%q want=%q", got, tc.want)
			}
			after, _ := json.Marshal(block)
			if string(before) != string(after) {
				t.Fatal("validation modified original native payload")
			}
			for _, role := range []Role{RoleAssistant, RoleUser} {
				m := Message{Role: role, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: block}}}
				var actual, expected openaiReqMsg
				applyChatReasoning(&actual, m, "fixture")
				applyLegacyChatReasoning(&expected, m, "fixture")
				if !reflect.DeepEqual(actual, expected) {
					t.Fatal("chat application differs")
				}
			}
		})
	}
	// The final native block for each field overrides earlier blocks exactly as before.
	m := Message{Role: RoleAssistant, Parts: []ContentPart{
		{Type: PartTypeReasoning, Reasoning: nativeReplayFixtureBlock(ReasoningChat, "{\"reasoning\":\"first\"}")},
		{Type: PartTypeReasoning, Reasoning: nativeReplayFixtureBlock(ReasoningChat, "{\"reasoning_content\":\"independent\"}")},
		{Type: PartTypeReasoning, Reasoning: nativeReplayFixtureBlock(ReasoningChat, "{\"reasoning\":\"last\"}")},
	}}
	var actual, expected openaiReqMsg
	applyChatReasoning(&actual, m, "fixture")
	applyLegacyChatReasoning(&expected, m, "fixture")
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("multiblock ordering differs")
	}
}

func TestNativeChatReplayGeneratedParity(t *testing.T) {
	keys := []string{"reasoning", "reasoning_content", "reasoning_text", "Reasoning", "reasoning_\\u0063ontent"}
	values := []string{"\"text\"", "null", "7", "true", "[]", "{}", "\"escaped \\n \\uD800\"", "\"\\\"braces {,}\\\"\""}
	count := 0
	for _, key := range keys {
		for _, first := range values {
			for _, last := range values {
				for _, raw := range []string{
					"{\"" + key + "\":" + last + "}",
					" \n { \"" + key + "\" : " + last + " } \t",
					"{\"" + key + "\":" + first + ",\"" + key + "\":" + last + "}",
				} {
					count++
					block := nativeReplayFixtureBlock(ReasoningChat, raw)
					m := Message{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: block}}}
					var got, want openaiReqMsg
					applyChatReasoning(&got, m, "fixture")
					applyLegacyChatReasoning(&want, m, "fixture")
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("replay differs for fixture %d", count)
					}
				}
			}
		}
	}
	t.Logf("generated malformed/canonical/whitespace/duplicate-key cases=%d", count)
}

func TestNativeChatReplayTransportAndHistory(t *testing.T) {
	var requests atomic.Int32
	var wire []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		wire, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	p, err := NewOpenAI(OpenAIConfig{BaseURL: server.URL, Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"{\"reasoning_content\":\"large " + strings.Repeat("fixture; ", 4096) + "\"}",
		"{\"reasoning\":null}",
		"{\"reasoning_text\":\"Zażółć \\n \\uD83D\\uDE00\"}",
		"{\"reasoning\":\"first\",\"reasoning\":\"last\"}",
		"{\"reasoning\":7,\"reasoning\":\"last\"}",
	} {
		block := nativeReasoning(ReasoningChat, "fixture", p.cfg.BaseURL, []byte(raw))
		m := Message{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: block}, {Type: PartTypeText, Text: "previous reply"}}}
		before, _ := json.Marshal(m)
		ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "first"}, m, {Role: RoleUser, Content: "continue"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for d := range ch {
			if d.Err != nil {
				t.Fatal(d.Err)
			}
		}
		var body struct{ Messages []openaiReqMsg }
		if json.Unmarshal(wire, &body) != nil || len(body.Messages) != 3 {
			t.Fatal("invalid wire")
		}
		var expected openaiReqMsg
		applyLegacyChatReasoning(&expected, m, "fixture")
		actual := body.Messages[1]
		if !reflect.DeepEqual(actual.Reasoning, expected.Reasoning) || !reflect.DeepEqual(actual.ReasoningContent, expected.ReasoningContent) || !reflect.DeepEqual(actual.ReasoningText, expected.ReasoningText) {
			t.Fatal("native wire differed from baseline")
		}
		after, _ := json.Marshal(m)
		if string(before) != string(after) {
			t.Fatal("transport changed original history")
		}
	}
	for _, raw := range []string{"{\"REASONING\":\"not accepted\"}", "{\"reasoning\":1}", "{\"reasoning\":\"x\"} {}"} {
		before := requests.Load()
		block := nativeReasoning(ReasoningChat, "fixture", p.cfg.BaseURL, []byte(raw))
		if _, err := p.Complete(context.Background(), []Message{{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: block}}}}, nil); err == nil {
			t.Fatal("malformed payload accepted")
		}
		if requests.Load() != before {
			t.Fatal("malformed payload reached HTTP transport")
		}
	}
}

var nativeReplayRequestSink []byte

func BenchmarkNativeReasoningRequestPipeline(b *testing.B) {
	for _, size := range []int{512, 8192, 65536} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			base := "http://127.0.0.1:9999/v1"
			msgs := []Message{{Role: RoleSystem, Content: "fixture system"}}
			for i := 0; i < 8; i++ {
				raw, _ := json.Marshal(map[string]string{"reasoning_content": strings.Repeat("r", size)})
				block := nativeReasoning(ReasoningChat, "fixture", base, raw)
				msgs = append(msgs, Message{Role: RoleUser, Content: "fixture instruction"}, Message{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: block}, {Type: PartTypeText, Text: "fixture reply"}}})
			}
			msgs = append(msgs, Message{Role: RoleUser, Content: "continue"})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, m := range msgs {
					if err := m.Validate(); err != nil {
						b.Fatal(err)
					}
				}
				view := filterNativeReasoning(msgs, ReasoningChat, "fixture", base)
				request, err := buildOpenAIRequest("fixture", view, nil, true, false)
				if err != nil {
					b.Fatal(err)
				}
				nativeReplayRequestSink = request
			}
			b.StopTimer()
			b.ReportMetric(float64(len(nativeReplayRequestSink)), "wire-bytes")
			b.ReportMetric(float64(8*size), "native-text-bytes")
		})
	}
}

// The legacy replay path remains a small independent differential reference.
// In particular, typed-map decoding retains earlier duplicate-key type errors.
func applyLegacyChatReasoning(out *openaiReqMsg, m Message, model string) {
	for _, raw := range nativePayloads(m, ReasoningChat, model) {
		var fields map[string]string
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		for key, value := range fields {
			s := value
			switch key {
			case "reasoning_content":
				out.ReasoningContent = &s
			case "reasoning":
				out.Reasoning = &s
			case "reasoning_text":
				out.ReasoningText = &s
			}
		}
	}
}
