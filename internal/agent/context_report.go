package agent

import (
	"fmt"
	"sort"
	"strings"

	"supercli/internal/llm"
)

// ContextItem is one row of the /context "largest items" list.
type ContextItem struct {
	Label  string
	Tokens int
}

// ContextReport is the data behind the /context command: where the
// input tokens of the current session go.
type ContextReport struct {
	Route   string // route of the last Run (chat/advisor/coordinator)
	Model   string
	Window  int // resolved context window (tokens)
	Visible int // messages the model sees
	Hidden  int // messages hidden by /clear, compaction, hide_messages

	EstimatedTokens int // chars/4 estimate of the visible conversation

	// Breakdown of EstimatedTokens by message kind.
	SystemTokens     int // system prompt(s), incl. briefing/patterns
	UserTokens       int
	AssistantTokens  int
	ToolResultTokens int // RoleTool messages (tool outputs)

	// Tool schemas sent with every coordinator-route request.
	ToolCount        int
	ToolSchemaTokens int

	// Provider-reported cumulative usage for this session.
	UsageIn  int
	UsageOut int

	Top []ContextItem // largest individual context items
}

// SessionUsage returns the cumulative provider-reported usage.
func (l *Loop) SessionUsage() Usage {
	l.sessUsageMu.Lock()
	defer l.sessUsageMu.Unlock()
	return l.sessUsage
}

// Route returns the route chosen for the most recent Run.
func (l *Loop) Route() RouteMode { return l.route }

// ContextReport builds the /context diagnostic from the loop's
// current visible messages and tool registry.
func (l *Loop) ContextReport() ContextReport {
	r := ContextReport{
		Route:  string(l.route),
		Model:  l.modelID,
		Window: l.window(),
		Hidden: l.HiddenCount(),
	}
	u := l.SessionUsage()
	r.UsageIn, r.UsageOut = u.Input, u.Output

	visible := l.VisibleMessages()
	r.Visible = len(visible)

	var items []ContextItem
	for i, m := range visible {
		t := estimateMessageTokens(m)
		r.EstimatedTokens += t
		switch m.Role {
		case llm.RoleSystem:
			r.SystemTokens += t
		case llm.RoleUser:
			r.UserTokens += t
		case llm.RoleAssistant:
			r.AssistantTokens += t
		case llm.RoleTool:
			r.ToolResultTokens += t
		}
		label := fmt.Sprintf("#%d %s", i, m.Role)
		if m.Role == llm.RoleTool && m.Name != "" {
			label += " (" + m.Name + ")"
		}
		label += ": " + firstWords(m.Content, 8)
		items = append(items, ContextItem{Label: label, Tokens: t})
	}

	for _, t := range l.registry.Visible() {
		r.ToolCount++
		st := (len(t.Name) + len(t.Description) + len(t.Schema)) / 4
		r.ToolSchemaTokens += st
		items = append(items, ContextItem{Label: "tool schema: " + t.Name, Tokens: st})
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].Tokens > items[j].Tokens })
	if len(items) > 5 {
		items = items[:5]
	}
	r.Top = items
	return r
}

func estimateMessageTokens(m llm.Message) int {
	n := len(m.Content) / 4
	for _, p := range m.Parts {
		if p.Type == llm.PartTypeText {
			n += len(p.Text) / 4
		}
	}
	return n
}

func firstWords(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	words := strings.SplitN(s, " ", n+1)
	if len(words) > n {
		return strings.Join(words[:n], " ") + "…"
	}
	return s
}

// FormatContextReport renders the report for the TUI transcript.
func FormatContextReport(r ContextReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Context (model: %s, route: %s)\n", r.Model, r.Route)
	pct := func(t int) string {
		if r.Window <= 0 {
			return ""
		}
		return fmt.Sprintf(" (%.1f%% of %d window)", float64(t)/float64(r.Window)*100, r.Window)
	}
	fmt.Fprintf(&b, "  messages: %d visible, %d hidden\n", r.Visible, r.Hidden)
	fmt.Fprintf(&b, "  estimated context: ~%d tok%s\n", r.EstimatedTokens+r.ToolSchemaTokens, pct(r.EstimatedTokens+r.ToolSchemaTokens))
	fmt.Fprintf(&b, "    system prompt + briefing: ~%d tok\n", r.SystemTokens)
	fmt.Fprintf(&b, "    user messages:            ~%d tok\n", r.UserTokens)
	fmt.Fprintf(&b, "    assistant messages:       ~%d tok\n", r.AssistantTokens)
	fmt.Fprintf(&b, "    tool results:             ~%d tok\n", r.ToolResultTokens)
	fmt.Fprintf(&b, "    tool schemas (%d tools):  ~%d tok\n", r.ToolCount, r.ToolSchemaTokens)
	fmt.Fprintf(&b, "  provider-reported usage: %d in / %d out tok\n", r.UsageIn, r.UsageOut)
	if len(r.Top) > 0 {
		b.WriteString("  top items:\n")
		for _, it := range r.Top {
			fmt.Fprintf(&b, "    ~%6d tok  %s\n", it.Tokens, it.Label)
		}
	}
	b.WriteString("  shrink it: /compact, /clear, or hide_messages\n")
	return b.String()
}
