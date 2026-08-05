package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// The line editors are gone from the registry, but not from the weights of
// every model that ever saw them. The name will keep arriving, so the answer
// to it has to be the corrected call rather than a question — otherwise the
// consolidation buys a round-trip back for every model that learned the old
// names.

func TestHardenToolCall_RetiredEditorsRedirectToPatchFile(t *testing.T) {
	known := []string{"patch_file", "create_file", "read_lines", "tool_search"}
	for _, name := range []string{"edit_line", "edit_lines", "insert_after", "delete_lines"} {
		tc := llm.ToolCall{Name: name, Arguments: `{"file":"a.go","line":3,"new_content":"x"}`}
		msg := HardenToolCall(&tc, known, 0)
		if msg == "" {
			t.Fatalf("%s: expected a correction, got none", name)
		}
		if !strings.Contains(msg, "patch_file") {
			t.Errorf("%s: correction must name patch_file: %s", name, msg)
		}
		if !strings.Contains(msg, `"old"`) || !strings.Contains(msg, `"new"`) {
			t.Errorf("%s: correction must show the shorthand arguments: %s", name, msg)
		}
	}
}

// The example inside the correction has to be a call the model can send
// verbatim, not prose that merely looks like one.
func TestRetiredEditToolAdvice_ExampleIsValidJSON(t *testing.T) {
	msg := retiredEditToolAdvice("edit_line")
	open := strings.Index(msg, `{"name":"patch_file"`)
	if open < 0 {
		t.Fatalf("no example call in: %s", msg)
	}
	depth, end := 0, -1
	for i := open; i < len(msg); i++ {
		switch msg[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
		if end > 0 {
			break
		}
	}
	if end < 0 {
		t.Fatalf("unbalanced example in: %s", msg)
	}
	var call struct {
		Name      string `json:"name"`
		Arguments struct {
			Path string `json:"path"`
			Old  string `json:"old"`
			New  string `json:"new"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(msg[open:end]), &call); err != nil {
		t.Fatalf("example is not valid JSON: %v (%s)", err, msg[open:end])
	}
	if call.Name != "patch_file" || call.Arguments.Path == "" || call.Arguments.Old == "" {
		t.Errorf("example is not a usable call: %+v", call)
	}
}

// A genuine typo must still get the ordinary did-you-mean path; the redirect
// is for the four retired names only.
func TestRetiredEditToolAdvice_LeavesOtherNamesAlone(t *testing.T) {
	for _, name := range []string{"patch_file", "edit_file", "read_lines", ""} {
		if got := retiredEditToolAdvice(name); got != "" {
			t.Errorf("retiredEditToolAdvice(%q) = %q, want empty", name, got)
		}
	}
}
