package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func contextTestLoop(t *testing.T) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "dummy",
		Description: "a dummy tool for tests",
		Schema:      `{"type":"object"}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) { return tools.Result{}, nil },
	})
	reg.MarkAlwaysOn("dummy")
	l, err := NewLoop(LoopConfig{
		Provider: &stubProvider{name: "test-model"},
		Registry: reg,
		System:   strings.Repeat("system prompt ", 50),
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	return l
}

func TestContextReport_Breakdown(t *testing.T) {
	l := contextTestLoop(t)
	l.Messages = append(l.Messages,
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("user ", 100)},
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("reply ", 50)},
		llm.Message{Role: llm.RoleTool, Name: "dummy", Content: strings.Repeat("output ", 200)},
	)
	r := l.ContextReport()
	if r.Visible != 4 {
		t.Errorf("Visible = %d, want 4", r.Visible)
	}
	if r.SystemTokens == 0 || r.UserTokens == 0 || r.AssistantTokens == 0 || r.ToolResultTokens == 0 {
		t.Errorf("breakdown has zeros: %+v", r)
	}
	if r.ToolCount != 1 || r.ToolSchemaTokens == 0 {
		t.Errorf("tool schema accounting: count=%d tokens=%d", r.ToolCount, r.ToolSchemaTokens)
	}
	if len(r.Top) == 0 || len(r.Top) > 5 {
		t.Errorf("Top len = %d", len(r.Top))
	}
	// Largest item should be the tool result (~350 tok).
	if !strings.Contains(r.Top[0].Label, "tool (dummy)") {
		t.Errorf("Top[0] = %+v, want the tool result first", r.Top[0])
	}
	out := FormatContextReport(r)
	for _, want := range []string{"tool schemas", "system prompt", "top items"} {
		if !strings.Contains(out, want) {
			t.Errorf("formatted report missing %q:\n%s", want, out)
		}
	}
}
