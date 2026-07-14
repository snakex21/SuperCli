package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestRepairToolArguments(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" = repair must fail
	}{
		{"truncated string value", `{"path": "main.go", "content": "hello`, `{"path": "main.go", "content": "hello"}`},
		{"missing closing brace", `{"path": "main.go"`, `{"path": "main.go"}`},
		{"trailing comma", `{"a": 1,`, `{"a": 1}`},
		{"nested truncation", `{"a": {"b": [1, 2`, `{"a": {"b": [1, 2]}}`},
		{"dangling key", `{"path": "x", "cont`, `{"path": "x"}`},
		{"markdown fence", "```json\n{\"a\": 1}\n```", `{"a": 1}`},
		{"leading prose", `Sure! {"a": 1}`, `{"a": 1}`},
		{"already valid", `{"a": 1}`, `{"a": 1}`},
		{"hopeless garbage", `not json at all`, ""},
		{"empty", ``, ""},
	}
	for _, c := range cases {
		got, ok := RepairToolArguments(c.in)
		if c.want == "" {
			if ok {
				t.Errorf("%s: expected failure, got %q", c.name, got)
			}
			continue
		}
		if !ok {
			t.Errorf("%s: repair failed for %q", c.name, c.in)
			continue
		}
		if !json.Valid([]byte(got)) {
			t.Errorf("%s: result %q is not valid JSON", c.name, got)
		}
		var a, b any
		json.Unmarshal([]byte(got), &a)
		json.Unmarshal([]byte(c.want), &b)
		if !jsonEqual(a, b) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func jsonEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func TestSuggestToolName(t *testing.T) {
	known := []string{"read_file", "write_file", "bash", "search_code", "edit_line"}
	cases := []struct{ in, want string }{
		{"read_fil", "read_file"},
		{"readfile", "read_file"},
		{"Bash", "bash"},
		{"serach_code", "search_code"},
		{"completely_unrelated_tool", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := SuggestToolName(c.in, known); got != c.want {
			t.Errorf("SuggestToolName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHardenToolCallUnknownName(t *testing.T) {
	tc := llm.ToolCall{Name: "read_fil", Arguments: `{"path":"x"}`}
	msg := HardenToolCall(&tc, []string{"read_file", "bash"}, 0)
	if msg == "" {
		t.Fatal("expected an error message for unknown tool")
	}
	if !strings.Contains(msg, `"read_file"`) {
		t.Errorf("message should suggest read_file: %q", msg)
	}
	if !strings.Contains(msg, "again") {
		t.Errorf("first attempt should invite a retry: %q", msg)
	}
}

func TestHardenToolCallExplainsGoalActions(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{"complete_task", `"action":"complete_task","task_seq":1`},
		{"verify", `"action":"verify","passed":true`},
		{"mark_done", `"action":"mark_done"`},
	} {
		call := llm.ToolCall{Name: tc.name, Arguments: `{}`}
		msg := HardenToolCall(&call, []string{"tool_search", "goal"}, 0)
		if !strings.Contains(msg, `action of the "goal" tool`) || !strings.Contains(msg, tc.want) {
			t.Errorf("HardenToolCall(%q) = %q", tc.name, msg)
		}
	}
	call := llm.ToolCall{Name: "read_fil", Arguments: `{}`}
	if msg := HardenToolCall(&call, []string{"read_file", "goal"}, 0); !strings.Contains(msg, `Did you mean "read_file"`) {
		t.Fatalf("ordinary suggestion changed: %q", msg)
	}
}

func TestHardenToolCallRepairsArgs(t *testing.T) {
	tc := llm.ToolCall{Name: "read_file", Arguments: `{"path": "main.go"`}
	msg := HardenToolCall(&tc, []string{"read_file"}, 0)
	if msg != "" {
		t.Fatalf("expected repair to succeed, got %q", msg)
	}
	if tc.Arguments != `{"path": "main.go"}` {
		t.Errorf("arguments = %q", tc.Arguments)
	}
}

func TestHardenToolCallEmptyArgs(t *testing.T) {
	tc := llm.ToolCall{Name: "bash", Arguments: "  "}
	if msg := HardenToolCall(&tc, []string{"bash"}, 0); msg != "" {
		t.Fatalf("unexpected error: %q", msg)
	}
	if tc.Arguments != "{}" {
		t.Errorf("arguments = %q, want {}", tc.Arguments)
	}
}

func TestHardenToolCallRetryBudget(t *testing.T) {
	tc := llm.ToolCall{Name: "read_file", Arguments: `garbage`}
	first := HardenToolCall(&tc, []string{"read_file"}, 0)
	if !strings.Contains(first, "Call the tool again") {
		t.Errorf("attempt 0 should invite retry: %q", first)
	}
	exhausted := HardenToolCall(&tc, []string{"read_file"}, maxToolFormatRetries)
	if !strings.Contains(exhausted, "Do not retry") {
		t.Errorf("exhausted budget should stop retries: %q", exhausted)
	}
}

func TestRecentBadCallStreak(t *testing.T) {
	l := &Loop{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
		{Role: llm.RoleAssistant, Content: ""},
		{Role: llm.RoleTool, Content: badToolCallMarker + "unknown tool"},
		{Role: llm.RoleAssistant, Content: ""},
		{Role: llm.RoleTool, Content: badToolCallMarker + "bad json"},
	}}
	if got := l.recentBadCallStreak(); got != 2 {
		t.Errorf("streak = %d, want 2", got)
	}
	// A successful tool result resets the streak.
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleTool, Content: "file contents"})
	if got := l.recentBadCallStreak(); got != 0 {
		t.Errorf("streak after success = %d, want 0", got)
	}
}
