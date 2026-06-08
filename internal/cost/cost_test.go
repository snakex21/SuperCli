package cost

import (
	"strings"
	"testing"

	"supercli/internal/stats"
)

func TestRender_Basic(t *testing.T) {
	d := Dashboard{
		Turns: []stats.Turn{
			{Step: 1, TokensIn: 500, TokensOut: 200, DurationMs: 1000, Model: "gpt-4o"},
			{Step: 2, TokensIn: 800, TokensOut: 300, DurationMs: 1500, Model: "gpt-4o", Tools: []string{"file_read"}},
		},
		Total:     stats.Total{TokensIn: 1300, TokensOut: 500, Turns: 2},
		SessionIn: 1300,
		Model:     "gpt-4o",
	}
	out := Render(d)
	if !strings.Contains(out, "Cost Dashboard") {
		t.Error("missing header")
	}
	if !strings.Contains(out, "1.8k") {
		t.Error("missing total tokens 1800 = 1.8k")
	}
	if !strings.Contains(out, "Per-turn breakdown") {
		t.Error("missing per-turn section")
	}
	if !strings.Contains(out, "file_read") {
		t.Error("missing tool name")
	}
}

func TestRender_DraftSavings(t *testing.T) {
	d := Dashboard{
		Turns: []stats.Turn{
			{Step: 1, TokensIn: 500, TokensOut: 200, DurationMs: 1000, TokensSaved: 312, Model: "gpt-4o"},
		},
		Total:     stats.Total{TokensIn: 500, TokensOut: 200, Turns: 1, TokensSaved: 312},
		SessionIn: 500,
		Model:     "gpt-4o",
	}
	out := Render(d)
	if !strings.Contains(out, "draft saved") {
		t.Error("missing draft savings line")
	}
	if !strings.Contains(out, "312") {
		t.Error("missing saved token count")
	}
}

func TestRender_WithBudget(t *testing.T) {
	d := Dashboard{
		Turns: []stats.Turn{
			{Step: 1, TokensIn: 500, TokensOut: 200, DurationMs: 1000, Model: "gpt-4o"},
		},
		Total:     stats.Total{TokensIn: 500, TokensOut: 200, Turns: 1},
		SessionIn: 500,
		BudgetIn:  10000,
		Model:     "gpt-4o",
	}
	out := Render(d)
	if !strings.Contains(out, "left") {
		t.Error("missing budget left display")
	}
}

func TestRender_Projection(t *testing.T) {
	d := Dashboard{
		Turns: []stats.Turn{
			{Step: 1, TokensIn: 5000, TokensOut: 2000, DurationMs: 1000, Model: "gpt-4o"},
			{Step: 2, TokensIn: 5000, TokensOut: 2000, DurationMs: 1000, Model: "gpt-4o"},
		},
		Total:     stats.Total{TokensIn: 10000, TokensOut: 4000, Turns: 2},
		SessionIn: 10000,
		Model:     "gpt-4o",
	}
	out := Render(d)
	if !strings.Contains(out, "at this rate") {
		t.Error("missing projection")
	}
}

func TestStatusBarCost_WithBudget(t *testing.T) {
	out := StatusBarCost(1500, 10000, "gpt-4o")
	if !strings.Contains(out, "used") {
		t.Error("missing 'used'")
	}
	if !strings.Contains(out, "left") {
		t.Error("missing 'left'")
	}
	if !strings.Contains(out, "$") {
		t.Error("missing cost")
	}
}

func TestStatusBarCost_NoBudget(t *testing.T) {
	out := StatusBarCost(1500, 0, "gpt-4o")
	if !strings.Contains(out, "used") {
		t.Error("missing 'used'")
	}
	if strings.Contains(out, "left") {
		t.Error("should not show 'left' without budget")
	}
}
