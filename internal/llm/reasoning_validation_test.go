package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func assertReasoningValidationParity(t testing.TB, block *ReasoningBlock) {
	t.Helper()
	_, original := block.validateParsed()
	if got := block.Validate(); fmt.Sprint(got) != fmt.Sprint(original) {
		t.Fatalf("validation changed for %+v: %v vs %v", block, got, original)
	}
}

func TestNativeChatValidationParity(t *testing.T) {
	payloads := []string{
		`{"reasoning_content":"ok"}`, `{"reasoning":""}`, `{"reasoning_text":null}`,
		`{"reasoning_content":"東京 café \u0000 😀"}`,
		" \t\r\n{\"reasoning_content\":\"ok\"}\n",
		`{"reasoning_content" : "legacy spacing"}`,
		`{"reasoning_content":true}`, `{"reasoning_content":123}`,
		`{"reasoning_content":{}}`, `{"reasoning_content":[]}`,
		`{"reasoning_content":"first","reasoning_content":"last"}`,
		`{"reasoning_content":1,"reasoning_content":"last"}`,
		`{"reasoning_content":"first","reasoning_content":null}`,
		`{"reasoning_content":"ok","other":"extra"}`,
		`{"\u0072easoning_content":"escaped key"}`, `{"REASONING":"wrong case"}`,
		`{"reasoning_content":"unterminated}`, `{"reasoning_content":"bad\q"}`,
		`null`, `[]`, `42`, `{}`,
		`{"type":"reasoning","encrypted_content":"opaque"}`,
		`{"type":"thinking","thinking":"ok"}`,
		`{"type":"assistant","content":[{"type":"redacted_thinking","data":"opaque"}]}`,
	}
	// Invalid UTF-8 inside strings is accepted as replacement text by json.
	// Unicode whitespace outside strings must remain invalid JSON.
	for _, space := range []string{"\u00a0", "\u2003", "\u2028", "\uFEFF", "\x00", "\v", "\f"} {
		for _, payload := range []string{`{"reasoning_content":"ok"}`, `{"reasoning_content":null}`} {
			payloads = append(payloads, space+payload, payload+space, `{"reasoning_content":`+space+`"ok"}`, `{"reasoning_content":"ok"`+space+"}")
		}
	}
	for i := 0; i < 256; i++ {
		payloads = append(payloads, `{"reasoning_content":"`+string([]byte{byte(i)})+`"}`)
		for _, position := range []int{0, 20, 23} {
			mutated := []byte(`{"reasoning_content":"ok"}`)
			mutated[position] = byte(i)
			payloads = append(payloads, string(mutated))
		}
	}
	rng := rand.New(rand.NewSource(20261002))
	for i := 0; i < 1000; i++ {
		raw := make([]byte, rng.Intn(96))
		rng.Read(raw)
		payloads = append(payloads, string(raw))
	}
	for _, format := range []string{ReasoningChat, ReasoningResponses, ReasoningAnthropic, "unknown"} {
		for _, payload := range payloads {
			for _, origin := range []int{0, 1, 2} {
				block := &ReasoningBlock{Format: format, Model: "fixture", Scope: "fixture", Data: json.RawMessage(payload)}
				if origin == 1 {
					block.Model = ""
				} else if origin == 2 {
					block.Scope = ""
				}
				assertReasoningValidationParity(t, block)
			}
		}
	}
	assertReasoningValidationParity(t, nil)
}

func TestNativeChatValidationKeepsWireAndPayload(t *testing.T) {
	for _, field := range []string{"reasoning_content", "reasoning", "reasoning_text"} {
		for _, rawValue := range []string{`"exact 東京 \n 😀"`, `""`, `null`} {
			raw := []byte(fmt.Sprintf("{%q:%s}", field, rawValue))
			block := nativeReasoning(ReasoningChat, "fixture", "https://fixture.invalid/v1", raw)
			message := Message{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: block}, {Type: PartTypeText, Text: "Verified answer."}}}
			before := bytes.Clone(block.Data)
			if err := message.Validate(); err != nil {
				t.Fatal(err)
			}
			body, err := buildOpenAIRequestWithReasoningKey("fixture", "fixture", []Message{{Role: RoleUser, Content: "Question"}, message}, nil, false, false, openAIReasoningNone, 0, Sampling{})
			if err != nil {
				t.Fatal(err)
			}
			var got struct{ Messages []map[string]json.RawMessage }
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			var expected, actual string
			if err := json.Unmarshal([]byte(rawValue), &expected); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(got.Messages[1][field], &actual); err != nil || actual != expected {
				t.Fatalf("%s/%s: exact native text changed (%v)", field, rawValue, err)
			}
			if !bytes.Equal(block.Data, before) {
				t.Fatal("validation or encoding changed native state")
			}
		}
	}
	// The typed-map fallback rejects an earlier wrong-typed duplicate, even
	// though validation's RawMessage map sees the final string. Preserve it.
	block := nativeReasoning(ReasoningChat, "fixture", "https://fixture.invalid/v1", []byte(`{"reasoning_content":1,"reasoning_content":"last"}`))
	if err := block.Validate(); err != nil {
		t.Fatal(err)
	}
	var encoded openaiReqMsg
	applyChatReasoning(&encoded, Message{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: block}}}, "fixture")
	if encoded.ReasoningContent != nil {
		t.Fatal("legacy duplicate-key wire behavior changed")
	}
}

func TestNativeChatCanonicalValidationNoAllocations(t *testing.T) {
	for _, field := range []string{"reasoning_content", "reasoning", "reasoning_text"} {
		raw := []byte(fmt.Sprintf("{%q:%q}", field, strings.Repeat("東京 exact native text 😀 ", 80)))
		block := nativeReasoning(ReasoningChat, "fixture", "https://fixture.invalid/v1", raw)
		if allocations := testing.AllocsPerRun(100, func() {
			if err := block.Validate(); err != nil {
				t.Fatal(err)
			}
		}); allocations != 0 {
			t.Fatalf("%s: validation allocated %g objects", field, allocations)
		}
	}
}

var nativeValidationBenchmarkError error

func BenchmarkNativeChatValidation(b *testing.B) {
	for _, size := range []int{256, 1024, 8192, 32768} {
		raw, err := json.Marshal(map[string]string{"reasoning_content": strings.Repeat("東京 exact native text 😀 ", (size+29)/30)})
		if err != nil {
			b.Fatal(err)
		}
		block := nativeReasoning(ReasoningChat, "fixture", "https://fixture.invalid/v1", raw)
		for _, variant := range []string{"parsed", "validate"} {
			b.Run(fmt.Sprintf("bytes=%d/%s", size, variant), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if variant == "parsed" {
						_, nativeValidationBenchmarkError = block.validateParsed()
					} else {
						nativeValidationBenchmarkError = block.Validate()
					}
				}
			})
		}
	}
}
