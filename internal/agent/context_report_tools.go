package agent

import (
	"fmt"
	"strings"

	"supercli/internal/llm"
)

// contextGlyphs/contextLabels draw the plain-text window map; distinct
// letters keep it readable without terminal colors.
var contextGlyphs = map[string]string{
	ContextCatSystem: "S", ContextCatTools: "T", ContextCatUser: "U",
	ContextCatAssistant: "A", ContextCatToolCalls: "C", ContextCatToolResults: "R",
	ContextCatOther: "o", ContextCatFree: "·", ContextCatBuffer: "░",
}

var contextLabels = map[string]string{
	ContextCatSystem: "system prompt", ContextCatTools: "tool defs", ContextCatUser: "user msgs",
	ContextCatAssistant: "assistant", ContextCatToolCalls: "tool calls", ContextCatToolResults: "tool results",
	ContextCatOther: "other", ContextCatFree: "free", ContextCatBuffer: "compact buf",
}

func compactTok(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// writeContextMap appends a 50-cell window map with a legend and a bar per
// tool showing which tool results occupy the context.
func writeContextMap(b *strings.Builder, r ContextReport) {
	cats := r.Categories()
	total := 0
	for _, c := range cats {
		total += c.Tokens
	}
	if total <= 0 {
		return
	}
	cells := ContextCells(cats, 50)
	b.WriteString("  window map: [")
	for i, c := range cats {
		b.WriteString(strings.Repeat(contextGlyphs[c.Key], cells[i]))
	}
	b.WriteString("]\n")
	for _, c := range cats {
		if c.Tokens <= 0 {
			continue
		}
		fmt.Fprintf(b, "    %s %-13s %7s %5.1f%%\n", contextGlyphs[c.Key], contextLabels[c.Key],
			compactTok(c.Tokens), float64(c.Tokens)*100/float64(total))
	}
	if len(r.ToolResultsByTool) == 0 {
		return
	}
	peak := 0
	for _, u := range r.ToolResultsByTool {
		peak = max(peak, u.Tokens)
	}
	b.WriteString("  tool results by tool:\n")
	for _, u := range r.ToolResultsByTool {
		name := u.Name
		if name == "" {
			name = "other"
		}
		bar := 0
		if peak > 0 {
			bar = (u.Tokens*20 + peak - 1) / peak
		}
		fmt.Fprintf(b, "    %-16.16s %-20s %7s  ×%d\n", name, strings.Repeat("█", bar), compactTok(u.Tokens), u.Count)
	}
}

// ContextToolBreakdownLimit caps the per-tool result list; the remainder is
// folded into one entry with an empty Name ("other").
const ContextToolBreakdownLimit = 8

// ContextToolUsage is the estimated prompt cost of one tool's results.
type ContextToolUsage = llm.ToolUsage

// Context category keys shared by the TUI map and the web GUI payload.
const (
	ContextCatSystem      = "system"
	ContextCatTools       = "tools"
	ContextCatUser        = "user"
	ContextCatAssistant   = "assistant"
	ContextCatToolCalls   = "tool_calls"
	ContextCatToolResults = "tool_results"
	ContextCatOther       = "other"
	ContextCatFree        = "free"
	ContextCatBuffer      = "buffer"
)

// ContextCategory is one slice of the context window map.
type ContextCategory struct {
	Key    string
	Tokens int
}

// Categories splits the window into the slices drawn by /context: occupied
// categories (system prompt, tool definitions, user messages, assistant text,
// tool-call arguments, tool results, request framing), then free space and
// the autocompact buffer (window minus the compaction threshold). Occupancy
// beyond the threshold eats into the buffer; neither goes negative.
func (r ContextReport) Categories() []ContextCategory {
	assistant := r.AssistantTokens - r.ToolCallTokens
	if assistant < 0 {
		assistant = 0
	}
	cats := []ContextCategory{
		{ContextCatSystem, r.SystemTokens},
		{ContextCatTools, r.ToolSchemaTokens + r.CatalogTokens},
		{ContextCatUser, r.UserTokens},
		{ContextCatAssistant, assistant},
		{ContextCatToolCalls, r.ToolCallTokens},
		{ContextCatToolResults, r.ToolResultTokens},
	}
	used := 0
	for _, c := range cats {
		used += c.Tokens
	}
	// Request-only framing (freshness stamp, chat-template overhead) that the
	// per-message estimate does not see.
	other := 0
	if r.RequestTokens > used {
		other = r.RequestTokens - used
		used = r.RequestTokens
	}
	cats = append(cats, ContextCategory{ContextCatOther, other})
	free, buffer := ContextFreeAndBuffer(r.Window, r.CompactThreshold, used)
	return append(cats, ContextCategory{ContextCatFree, free}, ContextCategory{ContextCatBuffer, buffer})
}

// ContextFreeAndBuffer returns free space and the remaining autocompact
// buffer for a window with the given compaction threshold and occupancy.
func ContextFreeAndBuffer(window, threshold, used int) (free, buffer int) {
	if window <= 0 {
		return 0, 0
	}
	if threshold <= 0 || threshold > window {
		threshold = window
	}
	buffer = window - threshold
	free = threshold - used
	if free < 0 {
		buffer += free
		free = 0
	}
	if buffer < 0 {
		buffer = 0
	}
	return free, buffer
}

// ContextCells distributes cells (e.g. 100 for a 10×10 map) over the
// categories proportionally (largest remainder). Every non-empty category
// gets at least one cell when there are enough cells.
func ContextCells(cats []ContextCategory, cells int) []int {
	out := make([]int, len(cats))
	total := 0
	for _, c := range cats {
		if c.Tokens > 0 {
			total += c.Tokens
		}
	}
	if total <= 0 || cells <= 0 {
		return out
	}
	type rem struct {
		i int
		r int64
	}
	rems := make([]rem, 0, len(cats))
	assigned := 0
	for i, c := range cats {
		if c.Tokens <= 0 {
			continue
		}
		num := int64(c.Tokens) * int64(cells)
		out[i] = int(num / int64(total))
		assigned += out[i]
		rems = append(rems, rem{i, num % int64(total)})
	}
	for k := 0; k < len(rems); k++ { // stable selection by remainder
		best := k
		for j := k + 1; j < len(rems); j++ {
			if rems[j].r > rems[best].r {
				best = j
			}
		}
		rems[k], rems[best] = rems[best], rems[k]
	}
	for k := 0; assigned < cells && k < len(rems); k++ {
		out[rems[k].i]++
		assigned++
	}
	// Guarantee visibility of tiny non-empty categories.
	for i, c := range cats {
		if c.Tokens <= 0 || out[i] > 0 {
			continue
		}
		largest := -1
		for j := range out {
			if out[j] > 1 && (largest < 0 || out[j] > out[largest]) {
				largest = j
			}
		}
		if largest < 0 {
			break
		}
		out[largest]--
		out[i]++
	}
	return out
}
