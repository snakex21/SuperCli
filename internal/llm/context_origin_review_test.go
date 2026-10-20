package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContextOriginReviewNativeToolUsesContent(t *testing.T) {
	result := Message{
		Role: RoleTool, Name: "fixture", ToolCallID: "call_fixture",
		Content: "actual native tool result: zażółć 中文",
		Parts:   []ContentPart{{Type: PartTypeText, Text: "unused tool fallback"}},
		Origin:  &ContextOrigin{Source: SourceToolResult},
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	msgs := []Message{
		{Role: RoleUser, Content: "Run the fixture."},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_fixture", Name: "fixture", Arguments: `{}`}}},
		result,
	}
	body, err := buildOpenAIRequest("fixture", msgs, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if request.Messages[2].Role != "tool" || request.Messages[2].Content != result.Content {
		t.Fatal("fixture no longer demonstrates the native tool contract")
	}
	u := MeasureContext(msgs, nil)
	if got := u.Sources[SourceToolResult].TextBytes; got != int64(len(result.Content)) {
		t.Fatalf("native tool measurement counts unsent Parts: got=%d want=%d", got, len(result.Content))
	}
}

func TestContextOriginReviewMetadataNeverChangesNativeWire(t *testing.T) {
	plain := Message{Role: RoleUser, Parts: []ContentPart{{Type: PartTypeText, Text: "zażółć 中文 🙂"}}}
	tagged := plain
	tagged.Origin = &ContextOrigin{Source: SourceHuman}
	tagged.Parts = append([]ContentPart(nil), plain.Parts...)
	tagged.Parts[0].Source = SourceRuntime
	for _, tc := range []struct {
		name  string
		build func([]Message) ([]byte, error)
	}{
		{"openai", func(m []Message) ([]byte, error) { return buildOpenAIRequest("fixture", m, nil, false, false) }},
		{"anthropic", func(m []Message) ([]byte, error) { return buildAnthropicRequest("fixture", m, nil, false, 4096) }},
		{"codex", func(m []Message) ([]byte, error) { return buildCodexRequest("fixture", m, nil, false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := tc.build([]Message{plain})
			if err != nil {
				t.Fatal(err)
			}
			after, err := tc.build([]Message{tagged})
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) || strings.Contains(string(after), "context_origin") || strings.Contains(string(after), "context_source") {
				t.Fatal("internal provenance changed the provider payload")
			}
		})
	}
}
