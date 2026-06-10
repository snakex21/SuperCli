package tui

import (
	"fmt"
	"strings"
)

// Marker renders inline event markers in the chat transcript.
// Each marker type has a fixed prefix and a compact format.
type Marker struct {
	p Palette
}

// NewMarker creates a Marker bound to the given palette.
func NewMarker(p Palette) Marker {
	return Marker{p: p}
}

// Draft renders: [draft: model→model, saved N tokens]
func (m Marker) Draft(draftModel, verifierModel string, savings int, decision string) string {
	if savings > 0 {
		text := fmt.Sprintf("✻ draft · %s → %s · saved %d tokens", draftModel, verifierModel, savings)
		return m.p.Marker.Render(text)
	}
	text := fmt.Sprintf("✻ draft · %s → %s · %s", draftModel, verifierModel, decision)
	return m.p.MarkerDim.Render(text)
}

// Council renders: [council: N candidate(s) → winner=X, "reason"]
func (m Marker) Council(candidateCount int, winnerProvider, reason string) string {
	text := fmt.Sprintf("✻ council · %d candidate(s) → winner=%s · %q", candidateCount, winnerProvider, reason)
	return m.p.Marker.Render(text)
}

// CouncilQuestion renders the council question (dimmed, indented).
func (m Marker) CouncilQuestion(q string) string {
	if len(q) > 60 {
		q = q[:57] + "..."
	}
	return m.p.MarkerDim.Render("    Q: " + q)
}

// CouncilAllFailed renders: [council: all samples failed]
func (m Marker) CouncilAllFailed() string {
	return m.p.MarkerDim.Render("✻ council · all samples failed")
}

// ContextHid renders: [context: hid N message(s) (reason)]
func (m Marker) ContextHid(count int, reason string) string {
	if reason == "" {
		reason = "manual"
	}
	text := fmt.Sprintf("✻ context · hid %d message(s) · %s", count, reason)
	return m.p.MarkerDim.Render(text)
}

// Reflection renders: [reflection: step N]
func (m Marker) Reflection(step int) string {
	text := fmt.Sprintf("✻ reflection · step %d", step)
	return m.p.Marker.Render(text)
}

// Goal renders: [goal: N/M tasks]
func (m Marker) Goal(done, total int) string {
	text := fmt.Sprintf("✻ goal · %d/%d tasks", done, total)
	return m.p.Marker.Render(text)
}

// Done renders: (done · N in / N out)
func (m Marker) Done(input, output int) string {
	text := fmt.Sprintf("✓ done · %d in / %d out", input, output)
	return m.p.Dim.Render(text)
}

// DoneEst renders with optional "(est.)" suffix when the
// provider didn't report token usage and we estimated it.
func (m Marker) DoneEst(input, output int, estimated bool) string {
	suffix := ""
	if estimated {
		suffix = " · est."
	}
	text := fmt.Sprintf("✓ done · %d in / %d out%s", input, output, suffix)
	return m.p.Dim.Render(text)
}

// Error renders: (error: msg)
func (m Marker) Error(err error) string {
	text := fmt.Sprintf("✗ error · %v", err)
	return m.p.Error.Render(text)
}

// ToolCall renders a compact tool chip: ▸ tool_name  args
// (collapsible — Shift+E expands the matching result block).
func (m Marker) ToolCall(name, args string) string {
	// Truncate args for readability.
	if len(args) > 80 {
		args = args[:77] + "..."
	}
	prefix := m.p.ToolName.Render("▸ " + name)
	return prefix + m.p.Dim.Render("  "+args)
}

// ToolResult renders the truncated output of a tool call.
func (m Marker) ToolResult(output string, isErr bool) string {
	if len(output) > 200 {
		output = output[:200] + "…"
	}
	if isErr {
		return m.p.ToolErr.Render("  ⎿ error · " + output)
	}
	return m.p.ToolOutput.Render("  ⎿ " + output)
}

// ToolResultFull renders tool output with the tool name header
// and up to maxLines of content. expanded=true shows more lines.
func (m Marker) ToolResultFull(toolName, output string, expanded bool) string {
	maxLines := 20
	if expanded {
		maxLines = 50
	}
	lines := strings.Split(output, "\n")
	truncated := len(lines) > maxLines
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	var b strings.Builder
	b.WriteString(m.p.ToolOutput.Render("  ⎿ ") + m.p.ToolName.Render(toolName) + m.p.Dim.Render(" · done"))
	for _, line := range lines {
		b.WriteByte('\n')
		b.WriteString(m.p.ToolOutput.Render("    " + line))
	}
	if truncated {
		b.WriteString(m.p.Dim.Render("\n    … truncated · Shift+E to expand"))
	}
	return b.String()
}

// ToolResultErr renders a tool error with the tool name.
func (m Marker) ToolResultErr(toolName, errMsg string) string {
	var b strings.Builder
	b.WriteString(m.p.ToolErr.Render("  ⎿ " + toolName + " error"))
	b.WriteByte('\n')
	b.WriteString(m.p.ToolErr.Render("    " + errMsg))
	return b.String()
}

// Running renders the "running..." indicator shown during slash commands.
func (m Marker) Running() string {
	return m.p.Dim.Render("▸ running · Ctrl+C to abort")
}

// NoAgent renders the no-agent error.
func (m Marker) NoAgent() string {
	return m.p.Error.Render("✗ no agent wired · configure SUPERCLI_LLM_API_KEY or set --echo")
}

// Mention renders: [mentions: N file(s), ~T tokens]
func (m Marker) Mention(count, tokens int) string {
	text := fmt.Sprintf("✻ mentions · %d file(s) · ~%d tokens", count, tokens)
	return m.p.MarkerDim.Render(text)
}

// PlanMode renders: [plan: mode ON] or [plan: mode OFF]
func (m Marker) PlanMode(on bool) string {
	if on {
		return m.p.Marker.Render("✻ plan · ON · read-only analysis, structured output")
	}
	return m.p.Dim.Render("✻ plan · OFF · normal execution")
}

// Diff renders the /diff output with markers.
func (m Marker) Diff(text string) string {
	return m.p.MarkerDim.Render(text)
}

// ModelInfo renders: [model: ...]
func (m Marker) ModelInfo(text string) string {
	return m.p.Marker.Render("✻ model · " + text)
}
