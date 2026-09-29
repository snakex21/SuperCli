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

func (m Marker) tr(key string) string { return textFor(m.language, key) }

// Draft renders: [draft: model→model, saved N tokens]
func (m Marker) Draft(draftModel, verifierModel string, savings int, decision string) string {
	if savings > 0 {
		text := fmt.Sprintf(m.tr("tui.view_markers.a5f755c2fc"), draftModel, verifierModel, savings)
		return m.p.Marker.Render(text)
	}
	text := fmt.Sprintf(m.tr("tui.view_markers.e9b1792a48"), draftModel, verifierModel, decision)
	return m.p.MarkerDim.Render(text)
}

// Council renders: [council: N candidate(s) → winner=X, "reason"]
func (m Marker) Council(candidateCount int, winnerProvider, reason string) string {
	text := fmt.Sprintf(m.tr("tui.view_markers.af4fead372"), candidateCount, winnerProvider, reason)
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
	return m.p.MarkerDim.Render(m.tr("tui.view_markers.f8b56b4cda"))
}

// ContextHid renders: [context: hid N message(s) (reason)]
func (m Marker) ContextHid(count int, reason string) string {
	if reason == "" {
		reason = m.tr("tui.view_markers.36bde66f28")
	}
	text := fmt.Sprintf(m.tr("tui.view_markers.8dc2ee2163"), count, reason)
	return m.p.MarkerDim.Render(text)
}

// Reflection renders: [reflection: step N]
func (m Marker) Reflection(step int) string {
	text := fmt.Sprintf(m.tr("tui.view_markers.9ec5c6d74f"), step)
	return m.p.Marker.Render(text)
}

// Goal renders: [goal: N/M tasks]
func (m Marker) Goal(done, total int) string {
	text := fmt.Sprintf(m.tr("tui.view_markers.ce537ec691"), done, total)
	return m.p.Marker.Render(text)
}

// Done renders: (done · N in / N out)
func (m Marker) Done(input, output int) string {
	text := fmt.Sprintf(m.tr("tui.view_markers.cdf5f7c52a"), input, output)
	return m.p.Dim.Render(text)
}

// DoneEst renders with optional "(est.)" suffix when the
// provider didn't report token usage and we estimated it.
func (m Marker) DoneEst(input, output int, estimated bool) string {
	suffix := ""
	if estimated {
		suffix = m.tr("tui.view_markers.d8f4985c35")
	}
	text := fmt.Sprintf(m.tr("tui.view_markers.d61310b78b"), input, output, suffix)
	return m.p.Dim.Render(text)
}

// Error renders: (error: msg)
func (m Marker) Error(err error) string {
	text := fmt.Sprintf(m.tr("tui.view_markers.05daedcc46"), err)
	return m.p.Error.Render(text)
}

// ToolCall renders a compact tool chip: ▸ tool_name  args
// (collapsible — Shift+E expands the matching result block).
func (m Marker) ToolCall(name, args string) string {
	summary := summarizeToolArgs(args, m.language)
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
		return m.p.ToolErr.Render(m.tr("tui.view_markers.ca563f6a4a") + output)
	}
	return m.p.ToolOutput.Render("  └ " + output)
}

// ToolResultFull renders tool output with the tool name header
// and up to maxLines of content. expanded=true shows more lines.
func (m Marker) ToolResultFull(toolName, output string, expanded bool) string {
	maxLines := 4
	if expanded {
		maxLines = 40
	}
	clean := strings.TrimRight(toolDisplayOutput(output), "\n")
	if clean == "" {
		clean = m.tr("tui.view_markers.efefc15c24")
	}
	lines := strings.Split(clean, "\n")
	totalLines := len(lines)
	truncated := len(lines) > maxLines
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	var b strings.Builder
	meta := fmt.Sprintf(m.tr("tui.view_markers.0c4b4072e2"), totalLines)
	if totalLines != 1 {
		meta = fmt.Sprintf(m.tr("tui.view_markers.f501b4caf5"), totalLines)
	}
	meta += " · " + humanSize(int64(len(output)))
	b.WriteString(m.p.Success.Render("  + ") + m.p.ToolName.Render(toolName) + m.p.Dim.Render(" · "+meta))
	shortened := false
	for _, line := range lines {
		if !expanded {
			shortened = shortened || len([]rune(line)) > 180
			line = compactWorkerText(line, 180)
		}
		b.WriteByte('\n')
		b.WriteString(m.p.ToolOutput.Render("    │ " + line))
	}
	if truncated {
		b.WriteString(m.p.Dim.Render(fmt.Sprintf(m.tr("tui.view_markers.cc699dda6e"), totalLines-len(lines))))
	} else if shortened {
		b.WriteString(m.p.Dim.Render(m.tr("tui.view_markers.98fe7265cf")))
	}
	return b.String()
}

// ToolResultErr renders a tool error with the tool name.
func (m Marker) ToolResultErr(toolName, errMsg string) string {
	var b strings.Builder
	b.WriteString(m.p.ToolErr.Render("  └ " + toolName + m.tr("tui.view_markers.fc80a47b5e")))
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
	text := fmt.Sprintf(m.tr("tui.view_markers.977fb0ffde"), calls)
	if errors > 0 {
		text += fmt.Sprintf(m.tr("tui.view_markers.6105af2ae0"), errors)
	}
	if repeats > 0 {
		text += fmt.Sprintf(m.tr("tui.view_markers.1e9649aef7"), repeats)
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
func summarizeToolArgs(args string, languages ...string) string {
	language := "en"
	if len(languages) > 0 {
		language = languages[0]
	}
	tr := func(key string) string { return textFor(language, key) }
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
			parts = append(parts, fmt.Sprintf(tr("tui.other.3740849348"), from, to))
		case hasFrom:
			parts = append(parts, fmt.Sprintf(tr("tui.other.ff28a81e61"), from))
		case hasTo:
			parts = append(parts, fmt.Sprintf(tr("tui.other.f14f2d4610"), to))
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
		if args, ok := values["command"].([]any); ok {
			var command []string
			for _, arg := range args {
				if s, ok := arg.(string); ok {
					command = append(command, s)
				}
			}
			if len(command) > 0 {
				return truncateToolText("$ "+strings.Join(command, " "), 88)
			}
		}
		return tr("tui.view_markers.34e0293ce7")
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
	return m.p.Dim.Render(m.tr("tui.view_markers.99032d7e36"))
}

// NoAgent renders the no-agent error.
func (m Marker) NoAgent() string {
	return m.p.Error.Render(m.tr("tui.view_markers.b6a8215302"))
}

// Mention renders: [mentions: N file(s), ~T tokens]
func (m Marker) Mention(count, tokens int) string {
	text := fmt.Sprintf(m.tr("tui.view_markers.d46f7e0b9b"), count, tokens)
	return m.p.MarkerDim.Render(text)
}

// PlanMode renders: [plan: mode ON] or [plan: mode OFF]
func (m Marker) PlanMode(on bool) string {
	if on {
		return m.p.Marker.Render(m.tr("tui.view_markers.5d6ec32f83"))
	}
	return m.p.Dim.Render(m.tr("tui.view_markers.4ca5c57cce"))
}

// Diff renders the /diff output with markers.
func (m Marker) Diff(text string) string {
	return m.p.MarkerDim.Render(text)
}

// ModelInfo renders: [model: ...]
func (m Marker) ModelInfo(text string) string {
	return m.p.Marker.Render(m.tr("tui.other.8b62670777") + text)
}

// toolDisplayOutput unwraps structured process results for the transcript.
// The original JSON is kept in the stored tool result and model history.
func toolDisplayOutput(output string) string {
	var result struct {
		Stdout *string `json:"stdout"`
		Stderr string  `json:"stderr"`
	}
	if json.Unmarshal([]byte(output), &result) != nil || result.Stdout == nil {
		return output
	}
	text := strings.TrimRight(*result.Stdout, "\r\n")
	if result.Stderr != "" {
		text += "\nstderr:\n" + strings.TrimRight(result.Stderr, "\r\n")
	}
	return strings.TrimSpace(text)
}
