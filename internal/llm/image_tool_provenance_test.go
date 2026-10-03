package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func TestToolImageProvenanceMetadataAndShape(t *testing.T) {
	type originOnly struct {
		URL, MediaType, Data, Path, ID, Name string
		Active                               bool
		SourceToolCallID                     string
	}
	if unsafe.Sizeof(ImageRef{}) != unsafe.Sizeof(originOnly{}) {
		t.Fatalf("carrier flag failed to use existing padding: actual=%d origin-only=%d", unsafe.Sizeof(ImageRef{}), unsafe.Sizeof(originOnly{}))
	}
	ordinary := ImageRef{MediaType: "image/png", Data: "cGl4ZWxz", ID: "img_fixture", Name: "image"}
	raw, _ := json.Marshal(ordinary)
	if bytes.Contains(raw, []byte("source_tool_call_id")) || bytes.Contains(raw, []byte("tool_output_carrier")) {
		t.Fatalf("ordinary image gained stored provenance fields: %s", raw)
	}
	marked := ordinary
	marked.SourceToolCallID = "call-exact"
	marked.ToolOutputCarrier = true
	marked.Active = true
	msg := Message{Role: RoleUser, Parts: []ContentPart{{Type: PartTypeImage, Image: &marked}}}
	dormant := msg.DormantImages()
	if dormant.Parts[0].Image == &marked || dormant.Parts[0].Image.Active || !marked.Active ||
		dormant.Parts[0].Image.SourceToolCallID != marked.SourceToolCallID || !dormant.Parts[0].Image.ToolOutputCarrier {
		t.Fatal("dormancy lost host provenance or mutated the active original")
	}
}

func TestToolImageProvenanceDoesNotChangeProviderRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.png")
	if err := os.WriteFile(path, []byte("synthetic pixels"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, vision := range []bool{false, true} {
		for _, dialect := range []string{"chat", "anthropic", "codex", "responses", "zen-responses"} {
			t.Run(dialect+map[bool]string{true: "/vision", false: "/text"}[vision], func(t *testing.T) {
				format, raw := ReasoningChat, `{"reasoning_content":"plan"}`
				if dialect == "anthropic" {
					format, raw = ReasoningAnthropic, `{"type":"thinking","thinking":"plan","signature":"exact-signed-state","future":{"keep":true}}`
				}
				if dialect == "codex" || dialect == "responses" || dialect == "zen-responses" {
					format, raw = ReasoningResponses, `{"type":"reasoning","id":"rs_fixture","encrypted_content":"exact-encrypted-state","summary":[],"future":{"keep":true}}`
				}
				reasoning := nativeReasoning(format, "fixture", "https://fixture.invalid", []byte(raw))
				msgs := []Message{
					{Role: RoleUser, Content: "Read the image"},
					{Role: RoleAssistant, Parts: []ContentPart{{Type: PartTypeReasoning, Reasoning: reasoning}}, ToolCalls: []ToolCall{{ID: "call-exact", Name: "read_image", Arguments: "{}"}}},
					{Role: RoleTool, ToolCallID: "call-exact", Name: "read_image", Content: "read"},
					{Role: RoleUser, Parts: []ContentPart{
						{Type: PartTypeText, Text: "Host image carrier"},
						{Type: PartTypeImage, Image: &ImageRef{MediaType: "image/png", Path: path, ID: "img_file", Name: "file", Active: true}},
						{Type: PartTypeImage, Image: &ImageRef{MediaType: "image/png", Data: "cGl4ZWxz", ID: "img_data", Name: "inline"}},
						{Type: PartTypeImage, Image: &ImageRef{URL: "https://fixture.invalid/remote.png", ID: "img_url", Name: "remote"}},
					}},
				}
				marked := append([]Message(nil), msgs...)
				marked[3].Parts = append([]ContentPart(nil), msgs[3].Parts...)
				for i := 1; i < len(marked[3].Parts); i++ {
					ref := *marked[3].Parts[i].Image
					ref.SourceToolCallID, ref.ToolOutputCarrier = "call-exact", true
					marked[3].Parts[i].Image = &ref
				}
				before, _ := json.Marshal(marked)
				build := func(history []Message) ([]byte, error) {
					switch dialect {
					case "chat":
						return buildOpenAIRequest("fixture", history, nil, vision, false)
					case "anthropic":
						return buildAnthropicRequest("fixture", history, nil, vision, 256)
					case "codex":
						return buildCodexRequestWithEffort("fixture", history, nil, vision, "none")
					default:
						req, err := assembleCodexRequestWithEffort("fixture", history, nil, vision, "none")
						if err != nil {
							return nil, err
						}
						if dialect == "responses" {
							return prepareOwnedStandardResponsesRequest(req, "fixture-cache", false, Sampling{})
						}
						body, err := json.Marshal(req)
						if err != nil {
							return nil, err
						}
						return prepareOpenCodeZenResponsesRequest(body, "ses_fixture")
					}
				}
				oldBody, oldErr := build(msgs)
				body, err := build(marked)
				if oldErr != nil || err != nil {
					t.Fatalf("request errors: %v / %v", oldErr, err)
				}
				if !bytes.Equal(oldBody, body) {
					t.Fatalf("host provenance changed %s wire", dialect)
				}
				if bytes.Contains(body, []byte("source_tool_call_id")) || bytes.Contains(body, []byte("tool_output_carrier")) {
					t.Fatal("host provenance leaked to provider")
				}
				if toolCallHistoryNeedsRepair(msgs) || toolCallHistoryNeedsRepair(marked) {
					t.Fatal("healthy image carrier entered repair slow path")
				}
				after, _ := json.Marshal(marked)
				if !bytes.Equal(before, after) || !reflect.DeepEqual(marked[1].Parts[0].Reasoning, reasoning) {
					t.Fatal("request encoding mutated image or signed/native state")
				}
			})
		}
	}
}

func TestToolImageProvenanceDoesNotChangeEcho(t *testing.T) {
	echo, _ := NewEcho("fixture")
	read := func(ref ImageRef) string {
		ch, err := echo.Complete(context.Background(), []Message{{Role: RoleUser, Parts: []ContentPart{{Type: PartTypeText, Text: "same text"}, {Type: PartTypeImage, Image: &ref}}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		for d := range ch {
			if d.Err != nil {
				t.Fatal(d.Err)
			}
			output.WriteString(d.Content)
		}
		return output.String()
	}
	ref := ImageRef{MediaType: "image/png", Data: "cGl4ZWxz"}
	old := read(ref)
	ref.SourceToolCallID, ref.ToolOutputCarrier = "call-exact", true
	if got := read(ref); got != old {
		t.Fatalf("echo changed: %q / %q", old, got)
	}
}
