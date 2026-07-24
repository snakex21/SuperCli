package llm

import (
	"strings"
	"testing"
)

// TestEstimateMessageTokens_Formula pins the calibrated formula:
// non-whitespace bytes / 3 + 16 per message. Calibrated 2026-07-05
// on live llama-server request logs (Qwen3.5-9B): mean 0.95 of the
// server-reported prompt tokens, range 0.86-1.02.
func TestEstimateMessageTokens_Formula(t *testing.T) {
	m := Message{Role: RoleUser, Content: strings.Repeat("x", 300)}
	if got := EstimateMessageTokens(m); got != 300/3+16 {
		t.Errorf("EstimateMessageTokens = %d, want %d", got, 300/3+16)
	}
	if got := EstimateMessageTokens(Message{Role: RoleUser}); got != 16 {
		t.Errorf("empty message = %d, want 16 (template framing)", got)
	}
}

// TestEstimateMessageTokens_IgnoresWhitespace: indentation and blank
// lines compress to almost nothing in BPE vocabularies; the estimate
// must not charge for them (this is what len/4 got wrong on code).
func TestEstimateMessageTokens_IgnoresWhitespace(t *testing.T) {
	compact := Message{Role: RoleTool, ToolCallID: "1", Content: strings.Repeat("y", 90)}
	indented := Message{Role: RoleTool, ToolCallID: "1", Content: indent90()}
	if EstimateMessageTokens(compact) != EstimateMessageTokens(indented) {
		t.Errorf("whitespace changed the estimate: %d vs %d",
			EstimateMessageTokens(compact), EstimateMessageTokens(indented))
	}
}

func indent90() string {
	var b strings.Builder
	for i := 0; i < 90; i++ {
		b.WriteString("\t  y\n")
	}
	return b.String()
}

// TestEstimateMessageTokens_CountsToolCalls: assistant tool-call
// arguments are prompt bytes too (a big write_file call was invisible
// to the old chars/4 helpers).
func TestEstimateMessageTokens_CountsToolCalls(t *testing.T) {
	bare := Message{Role: RoleAssistant, Content: "ok"}
	withCall := Message{Role: RoleAssistant, Content: "ok", ToolCalls: []ToolCall{
		{ID: "1", Name: "write_file", Arguments: `{"path":"a.go","content":"` + strings.Repeat("z", 3000) + `"}`},
	}}
	if EstimateMessageTokens(withCall) < EstimateMessageTokens(bare)+900 {
		t.Errorf("tool-call arguments not counted: bare=%d withCall=%d",
			EstimateMessageTokens(bare), EstimateMessageTokens(withCall))
	}
}

// TestEstimateTokens_CalibrationSample replays a miniature of the live
// calibration: a realistic mixed transcript must land within the
// calibrated band of the reference token count (reference = what the
// 2026-07-05 fit predicts, ~0.86-1.02 of true; here we only assert
// the estimator is meaningfully ABOVE the old len/4 floor that
// underestimated by 23-32%).
func TestEstimateTokens_CalibrationSample(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: strings.Repeat("You are a coding agent. ", 40)},
		{Role: RoleUser, Content: "przeczytaj plik internal/agent/loop.go i opisz pętlę"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "read_lines", Arguments: `{"path":"internal/agent/loop.go","from":1,"to":120}`}}},
		{Role: RoleTool, ToolCallID: "1", Name: "read_lines", Content: strings.Repeat("\tfor step := 0; step < l.maxSteps; step++ {\n", 60)},
		{Role: RoleAssistant, Content: strings.Repeat("The loop iterates over steps. ", 20)},
	}
	oldLen4 := 0
	for _, m := range msgs {
		oldLen4 += len(m.Content) / 4
	}
	got := EstimateTokens(msgs)
	if got <= oldLen4 {
		t.Errorf("calibrated estimate %d must exceed the old len/4 underestimate %d", got, oldLen4)
	}
}
