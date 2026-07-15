package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Marker renders inline event markers in the chat transcript.
// Each marker type has a fixed prefix and a compact format.
type Marker struct {
	p        Palette
	language string
}

// NewMarker creates a Marker bound to the given palette.
func NewMarker(p Palette, language ...string) Marker {
	lang := "en"
	if len(language) > 0 {
		lang = normalizeLanguage(language[0])
	}
	return Marker{p: p, language: lang}
}

func (m Marker) tr(english, polish string) string { return textFor(m.language, english, polish) }

// Draft renders: [draft: model→model, saved N tokens]
func (m Marker) Draft(draftModel, verifierModel string, savings int, decision string) string {
	if savings > 0 {
		text := fmt.Sprintf(m.tr("✻ draft · %s → %s · saved %d tokens", "✻ szkic · %s → %s · oszczędzono %d tokenów"), draftModel, verifierModel, savings)
		return m.p.Marker.Render(text)
	}
	text := fmt.Sprintf(m.tr("✻ draft · %s → %s · %s", "✻ szkic · %s → %s · %s"), draftModel, verifierModel, decision)
	return m.p.MarkerDim.Render(text)
}

// Council renders: [council: N candidate(s) → winner=X, "reason"]
func (m Marker) Council(candidateCount int, winnerProvider, reason string) string {
	text := fmt.Sprintf(m.tr("✻ council · %d candidate(s) → winner=%s · %q", "✻ rada · %d kandydatów → wybrany=%s · %q"), candidateCount, winnerProvider, reason)
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
	return m.p.MarkerDim.Render(m.tr("✻ council · all samples failed", "✻ rada · wszystkie próby nieudane"))
}

// ContextHid renders: [context: hid N message(s) (reason)]
func (m Marker) ContextHid(count int, reason string) string {
	if reason == "" {
		reason = m.tr("manual", "ręcznie")
	}
	text := fmt.Sprintf(m.tr("✻ context · hid %d message(s) · %s", "✻ kontekst · ukryto %d wiadomości · %s"), count, reason)
	return m.p.MarkerDim.Render(text)
}

// Reflection renders: [reflection: step N]
func (m Marker) Reflection(step int) string {
	text := fmt.Sprintf(m.tr("✻ reflection · step %d", "✻ refleksja · krok %d"), step)
	return m.p.Marker.Render(text)
}

// Goal renders: [goal: N/M tasks]
func (m Marker) Goal(done, total int) string {
	text := fmt.Sprintf(m.tr("✻ goal · %d/%d tasks", "✻ cel · %d/%d zadań"), done, total)
	return m.p.Marker.Render(text)
}

// Done renders: (done · N in / N out)
func (m Marker) Done(input, output int) string {
	text := fmt.Sprintf(m.tr("✓ done · %d in / %d out", "✓ gotowe · %d wej. / %d wyj."), input, output)
	return m.p.Dim.Render(text)
}

// DoneEst renders with optional "(est.)" suffix when the
// provider didn't report token usage and we estimated it.
func (m Marker) DoneEst(input, output int, estimated bool) string {
	suffix := ""
	if estimated {
		suffix = m.tr(" · est.", " · szac.")
	}
	text := fmt.Sprintf(m.tr("✓ done · %d in / %d out%s", "✓ gotowe · %d wej. / %d wyj.%s"), input, output, suffix)
	return m.p.Dim.Render(text)
}

// Error renders: (error: msg)
func (m Marker) Error(err error) string {
	text := fmt.Sprintf(m.tr("✗ error · %v", "✗ błąd · %v"), err)
	return m.p.Error.Render(text)
}

// ToolCall renders a compact tool chip: ▸ tool_name  args
// (collapsible — Shift+E expands the matching result block).
func (m Marker) ToolCall(name, args string) string {
	summary := summarizeToolArgs(args)
	prefix := m.p.ToolName.Render("> " + name)
	if summary == "" {
		return prefix
	}
	return prefix + m.p.Dim.Render("  "+summary)
}

// ToolResult renders the truncated output of a tool call.
func (m Marker) ToolResult(output string, isErr bool) string {
	if len(output) > 200 {
		output = output[:200] + "…"
	}
	if isErr {
		return m.p.ToolErr.Render(m.tr("  ⎿ error · ", "  ⎿ błąd · ") + output)
	}
	return m.p.ToolOutput.Render("  ⎿ " + output)
}

// ToolResultFull renders tool output with the tool name header
// and up to maxLines of content. expanded=true shows more lines.
func (m Marker) ToolResultFull(toolName, output string, expanded bool) string {
	maxLines := 4
	if expanded {
		maxLines = 40
	}
	clean := strings.TrimRight(output, "\n")
	if clean == "" {
		clean = m.tr("(no output)", "(brak wyniku)")
	}
	lines := strings.Split(clean, "\n")
	totalLines := len(lines)
	truncated := len(lines) > maxLines
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	var b strings.Builder
	meta := fmt.Sprintf(m.tr("done · %d line", "gotowe · %d linia"), totalLines)
	if totalLines != 1 {
		meta = fmt.Sprintf(m.tr("done · %d lines", "gotowe · %d linii"), totalLines)
	}
	meta += " · " + humanSize(int64(len(output)))
	b.WriteString(m.p.Success.Render("  ✓ ") + m.p.ToolName.Render(toolName) + m.p.Dim.Render(" · "+meta))
	for _, line := range lines {
		b.WriteByte('\n')
		b.WriteString(m.p.ToolOutput.Render("    │ " + line))
	}
	if truncated {
		b.WriteString(m.p.Dim.Render(fmt.Sprintf(m.tr("\n    └ … %d more · Shift+E to expand", "\n    └ … jeszcze %d · Shift+E rozwija"), totalLines-len(lines))))
	}
	return b.String()
}

// ToolResultErr renders a tool error with the tool name.
func (m Marker) ToolResultErr(toolName, errMsg string) string {
	var b strings.Builder
	b.WriteString(m.p.ToolErr.Render("  ⎿ " + toolName + m.tr(" error", " błąd")))
	b.WriteByte('\n')
	b.WriteString(m.p.ToolErr.Render("    " + errMsg))
	return b.String()
}

// ToolActivity summarizes the local execution timeline after a turn. Detailed
// call/result blocks remain available above (and via Shift+E); this line makes
// loops and failures visible without asking the model for another summary.
func (m Marker) ToolActivity(calls, errors, repeats int, byName map[string]int) string {
	type entry struct {
		name  string
		count int
	}
	entries := make([]entry, 0, len(byName))
	for name, count := range byName {
		entries = append(entries, entry{name: name, count: count})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count == entries[j].count {
			return entries[i].name < entries[j].name
		}
		return entries[i].count > entries[j].count
	})
	names := make([]string, 0, minInt(4, len(entries)))
	for i, item := range entries {
		if i == 4 {
			break
		}
		label := item.name
		if item.count > 1 {
			label += fmt.Sprintf("×%d", item.count)
		}
		names = append(names, label)
	}
	text := fmt.Sprintf(m.tr("✓ tools · %d calls", "✓ narzędzia · %d wywołań"), calls)
	if errors > 0 {
		text += fmt.Sprintf(m.tr(" · %d errors", " · %d błędów"), errors)
	}
	if repeats > 0 {
		text += fmt.Sprintf(m.tr(" · %d repeats", " · %d powtórzeń"), repeats)
	}
	if len(names) > 0 {
		text += " · " + strings.Join(names, ", ")
	}
	if errors > 0 || repeats > 1 {
		return m.p.ToolErr.Render(text)
	}
	return m.p.MarkerDim.Render(text)
}

// summarizeToolArgs converts common tool JSON into a short, stable activity
// label. It is presentation-only: the model still receives complete args.
// Large content/replacement fields and credentials are deliberately omitted.
func summarizeToolArgs(args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "{}" {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(args), &values); err != nil {
		return truncateToolText(strings.Join(strings.Fields(args), " "), 88)
	}

	firstString := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	parts := make([]string, 0, 3)
	if path := firstString("file", "path", "filename"); path != "" {
		parts = append(parts, path)
	}
	from, hasFrom := jsonNumber(values["from"])
	to, hasTo := jsonNumber(values["to"])
	if hasFrom || hasTo {
		switch {
		case hasFrom && hasTo:
			parts = append(parts, fmt.Sprintf("lines %d–%d", from, to))
		case hasFrom:
			parts = append(parts, fmt.Sprintf("from line %d", from))
		case hasTo:
			parts = append(parts, fmt.Sprintf("to line %d", to))
		}
	}
	if query := firstString("query", "pattern"); query != "" {
		parts = append(parts, "“"+query+"”")
	} else if command := firstString("command", "cmd"); command != "" {
		parts = append(parts, "$ "+command)
	} else if reads := firstString("reads"); reads != "" {
		parts = append(parts, reads)
	} else if url := firstString("url"); url != "" {
		parts = append(parts, url)
	} else if task := firstString("task", "prompt", "task_id", "id", "name", "model", "provider", "server"); task != "" {
		parts = append(parts, task)
	}
	if len(parts) == 0 {
		return "details hidden"
	}
	return truncateToolText(strings.Join(parts, " · "), 88)
}

func truncateToolText(text string, limit int) string {
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func jsonNumber(value any) (int, bool) {
	n, ok := value.(float64)
	if !ok {
		return 0, false
	}
	return int(n), true
}

// Running renders the "running..." indicator shown during slash commands.
func (m Marker) Running() string {
	return m.p.Dim.Render(m.tr("▸ running · Ctrl+C to abort", "▸ uruchomiono · Ctrl+C przerywa"))
}

// NoAgent renders the no-agent error.
func (m Marker) NoAgent() string {
	return m.p.Error.Render(m.tr("✗ no agent wired · configure SUPERCLI_LLM_API_KEY or set --echo", "✗ brak agenta · skonfiguruj SUPERCLI_LLM_API_KEY albo użyj --echo"))
}

// Mention renders: [mentions: N file(s), ~T tokens]
func (m Marker) Mention(count, tokens int) string {
	text := fmt.Sprintf(m.tr("✻ mentions · %d file(s) · ~%d tokens", "✻ wzmianki · %d plików · ~%d tokenów"), count, tokens)
	return m.p.MarkerDim.Render(text)
}

// PlanMode renders: [plan: mode ON] or [plan: mode OFF]
func (m Marker) PlanMode(on bool) string {
	if on {
		return m.p.Marker.Render(m.tr("✻ plan · ON · read-only analysis, structured output", "✻ plan · WŁ. · analiza tylko do odczytu, wynik strukturalny"))
	}
	return m.p.Dim.Render(m.tr("✻ plan · OFF · normal execution", "✻ plan · WYŁ. · normalne wykonanie"))
}

// Diff renders the /diff output with markers.
func (m Marker) Diff(text string) string {
	return m.p.MarkerDim.Render(text)
}

// ModelInfo renders: [model: ...]
func (m Marker) ModelInfo(text string) string {
	return m.p.Marker.Render("✻ model · " + text)
}
