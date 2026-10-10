package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Keep the pre-fast-path chat decoder as an independent validation reference.
// Its object map intentionally preserves duplicate-key and legacy JSON rules.
func legacyParsedChatReference(b *ReasoningBlock) (nativeChatPayload, error) {
	var chat nativeChatPayload
	if b == nil || b.Model == "" || b.Scope == "" || !json.Valid(b.Data) {
		return chat, fmt.Errorf("reasoning part: invalid origin or payload")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b.Data, &fields) != nil || fields == nil {
		return chat, fmt.Errorf("reasoning part: payload must be an object")
	}
	if len(fields) != 1 {
		return chat, fmt.Errorf("reasoning part: expected one native chat field")
	}
	for key, value := range fields {
		if key != "reasoning_content" && key != "reasoning" && key != "reasoning_text" {
			return chat, fmt.Errorf("reasoning part: unsupported chat field")
		}
		var text string
		if json.Unmarshal(value, &text) != nil {
			return chat, fmt.Errorf("reasoning part: chat reasoning must be text")
		}
		chat.field, chat.text = key, text
		chat.canonical = canonicalSingleChatPayload(b.Data, key, value)
	}
	return chat, nil
}

func TestNativeChatParsedFastPathMatchesLegacy(t *testing.T) {
	payloads := []string{
		`{"reasoning_content":"exact 東京 <>& café \u0000 😀"}`,
		`{"reasoning":""}`, `{"reasoning_text":null}`,
		`{"reasoning":"\uD800\uD83D\uDE00\n\r\t\\\""}`,
		"{\"reasoning\":\"" + string([]byte{0xff, 0xfe}) + "\"}",
		`{"reasoning":"` + strings.Repeat("same bytes ", 8192) + `"}`,
		" \n{\"reasoning\":\"legacy padding\"}\t",
		`{ "reasoning" : "legacy spacing" }`,
		`{"\u0072easoning":"escaped key"}`,
		`{"reasoning":"first","reasoning":"last"}`,
		`{"reasoning":1,"reasoning":"last"}`,
		`{"reasoning":"first","reasoning":null}`,
		`{"reasoning":"first","reasoning":true}`,
		`{"reasoning":"ok","other":"extra"}`,
		`{"Reasoning":"wrong case"}`,
		`{"reasoning":"bad\q"}`, `{"reasoning":"unterminated}`,
		`{"reasoning":true}`, `{"reasoning":{}}`, `{"reasoning":[]}`,
		`{"reasoning":"ok"} {}`, `{}`, `null`, `[]`, `42`, "",
		"\u00a0{\"reasoning\":\"invalid JSON whitespace\"}",
	}
	for i, payload := range payloads {
		for _, origin := range []string{"valid", "missing model", "missing scope"} {
			t.Run(fmt.Sprintf("payload=%d/%s", i, origin), func(t *testing.T) {
				block := nativeReplayFixtureBlock(ReasoningChat, payload)
				block.Tokens = 37
				if origin == "missing model" {
					block.Model = ""
				} else if origin == "missing scope" {
					block.Scope = ""
				}
				before, _ := json.Marshal(block)
				want, wantErr := legacyParsedChatReference(block)
				got, gotErr := block.validateParsed()
				if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
					t.Fatalf("parsed payload changed: got=%+v, %v; want=%+v, %v", got, gotErr, want, wantErr)
				}
				after, _ := json.Marshal(block)
				if !bytes.Equal(before, after) {
					t.Fatal("parsing mutated persisted native continuation")
				}
			})
		}
	}
}

func TestNativeChatParsedFastPathKeepsExactRequestBytes(t *testing.T) {
	for _, field := range []string{"reasoning_content", "reasoning", "reasoning_text"} {
		for _, value := range []string{`"same 東京 <>& 😀 \n \u2028 \uD800"`, `""`, `null`, `7`} {
			for _, shape := range []string{"canonical", "spaced", "duplicate", "escaped key", "corrupt"} {
				t.Run(field+"/"+value+"/"+shape, func(t *testing.T) {
					key, _ := json.Marshal(field)
					raw := fmt.Sprintf("{%s:%s}", key, value)
					switch shape {
					case "spaced":
						raw = fmt.Sprintf(" \n{ %s : %s } \t", key, value)
					case "duplicate":
						raw = fmt.Sprintf("{%s:1,%s:%s}", key, key, value)
					case "escaped key":
						raw = fmt.Sprintf("{\"\\u0072%s\":%s}", field[1:], value)
					case "corrupt":
						raw += " {}"
					}
					block := nativeReplayFixtureBlock(ReasoningChat, raw)
					assistant := Message{Role: RoleAssistant, Parts: []ContentPart{
						{Type: PartTypeReasoning, Reasoning: block},
						{Type: PartTypeText, Text: "visible answer"},
					}}
					messages := []Message{{Role: RoleUser, Content: "first"}, assistant, {Role: RoleUser, Content: "continue"}}
					before, _ := json.Marshal(messages)
					got, err := buildOpenAIRequestWithReasoningKey("fixture", "fixture", messages, nil, false, true, openAIReasoningNone, 8192, Sampling{})
					if err != nil {
						t.Fatal(err)
					}
					withoutNative := append([]Message(nil), messages...)
					withoutNative[1] = Message{Role: RoleAssistant, Content: "visible answer"}
					plain, err := buildOpenAIRequestWithReasoningKey("fixture", "fixture", withoutNative, nil, false, true, openAIReasoningNone, 8192, Sampling{})
					if err != nil {
						t.Fatal(err)
					}
					var expected openaiRequest
					if err := json.Unmarshal(plain, &expected); err != nil {
						t.Fatal(err)
					}
					applyLegacyChatReasoning(&expected.Messages[1], assistant, "fixture")
					want, _ := json.Marshal(expected)
					if !bytes.Equal(got, want) {
						t.Fatalf("wire bytes differ: got=%s; want=%s", got, want)
					}
					after, _ := json.Marshal(messages)
					if !reflect.DeepEqual(before, after) {
						t.Fatal("request changed full transcript or native payload")
					}
				})
			}
		}
	}
}
