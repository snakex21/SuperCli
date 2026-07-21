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
	Route        string // route of the last Run (chat/advisor/coordinator)
	Model        string
	Window       int    // resolved context window (tokens)
	WindowSource string // config/provider/catalog/learned/fallback
	Visible      int    // messages the model sees
	Hidden       int    // messages hidden by /clear, compaction, hide_messages

	EstimatedTokens       int    // chars/4 estimate of the visible conversation
	RequestTokens         int    // complete next request, including tools/catalog/stamp
	RawRequestTokens      int    // local estimate before provider calibration
	RequestEstimateSource string // estimate | provider+delta
	ExactRequestBase      int    // last exact provider prompt used for calibration

	// Breakdown of EstimatedTokens by message kind.
	SystemTokens     int // system prompt(s), incl. briefing/patterns
	UserTokens       int
	AssistantTokens  int
	ToolResultTokens int // RoleTool messages (tool outputs)

	// Tool exposure sent with every coordinator-route request.
	// In JSON mode every visible tool carries a full schema, counted
	// in ToolSchemaTokens. In thin mode (Thin == true) only the
	// schema-carrying core/activated tools count toward
	// ToolSchemaTokens; the dormant tail is advertised in a compact
	// catalog whose cost is CatalogTokens instead — that split is
	// where the per-turn saving shows up.
	ToolCount        int  // tools carrying a full schema this turn
	ToolSchemaTokens int  // chars/4 cost of those schemas
	Thin             bool // thin tool protocol active on this route
	CatalogTokens    int  // chars/4 cost of the catalog + call-format preamble (thin only)

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

// LastTurnStats returns observability for the most recent turn: the
// KV/prompt cache-hit percentage (cached prompt tokens / prompt tokens)
// and the number of hidden reasoning tokens the model spent. ok is
// false until a turn with provider usage has completed. Backends that
// do not report the breakdown yield zeros (still ok once a turn ran).
func (l *Loop) LastTurnStats() (cacheHitPct int, reasoning int, ok bool) {
	l.sessUsageMu.Lock()
	defer l.sessUsageMu.Unlock()
	if !l.lastTurnSet {
		return 0, 0, false
	}
	if l.lastTurnPrompt > 0 {
		cacheHitPct = l.lastTurnCached * 100 / l.lastTurnPrompt
	}
	return cacheHitPct, l.lastTurnReasoning, true
}

// LastTurnBreakdown returns the last turn's token split for
// cache-miss hunting: cached is the prompt tokens the backend served
// from its KV/prompt cache, evaluated is the prompt tokens it had to
// (re-)compute, generated is the completion tokens. ok is false until
// a turn with provider usage has completed. Backends that do not
// report the cached share yield cached==0, so the whole prompt shows
// up as evaluated — that pessimistic view is exactly the signal this
// telemetry exists to surface.
func (l *Loop) LastTurnBreakdown() (cached, evaluated, generated int, ok bool) {
	l.sessUsageMu.Lock()
	defer l.sessUsageMu.Unlock()
	if !l.lastTurnSet {
		return 0, 0, 0, false
	}
	evaluated = l.lastTurnPrompt - l.lastTurnCached
	if evaluated < 0 {
		evaluated = 0
	}
	return l.lastTurnCached, evaluated, l.lastTurnOutput, true
}

// LastTurnPromptTokens returns the prompt (input) token count the
// provider reported for the most recent turn. ok is false until a turn
// with provider usage has completed. Callers pair this with the model's
// context window to render a "context used" percentage.
func (l *Loop) LastTurnPromptTokens() (prompt int, ok bool) {
	l.sessUsageMu.Lock()
	defer l.sessUsageMu.Unlock()
	if !l.lastTurnSet {
		return 0, false
	}
	return l.lastTurnPrompt, true
}

// Route returns the route chosen for the most recent Run.
func (l *Loop) Route() RouteMode { return l.route }

// ContextReport builds the /context diagnostic from the loop's
// current visible messages and tool registry.
func (l *Loop) ContextReport() ContextReport {
	window := l.windowResolution()
	r := ContextReport{
		Route:        string(l.route),
		Model:        l.modelID,
		Window:       window.Tokens,
		WindowSource: window.Source,
		Hidden:       l.HiddenCount(),
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

	// Tool exposure. In JSON mode every visible tool carries a full
	// schema. In thin mode the dormant tail is replaced by a compact
	// catalog, so we must count only the schema-carrying tools here
	// and add the catalog (+ call-format preamble) cost separately —
	// otherwise /context would hide the very saving thin mode buys.
	if l.thinTools && l.route == RouteCoordinator {
		r.Thin = true
		schema, tail := l.thinPartition()
		for _, t := range schema {
			r.ToolCount++
			st := (len(t.Name) + len(t.Description) + len(t.Schema)) / 4
			r.ToolSchemaTokens += st
			items = append(items, ContextItem{Label: "tool schema: " + t.Name, Tokens: st})
		}
		// The thin preamble (sentinel call-format instruction + the
		// dormant-tail catalog) is injected as a system message every
		// turn; charge its full chars/4 cost so the report reflects
		// what actually goes on the wire, not just the catalog body.
		r.CatalogTokens = len(l.thinToolsPreamble()) / 4
		if r.CatalogTokens > 0 {
			items = append(items, ContextItem{
				Label:  fmt.Sprintf("tool catalog (%d tail tools)", len(tail)),
				Tokens: r.CatalogTokens,
			})
		}
	} else {
		for _, t := range l.registry.Visible() {
			r.ToolCount++
			st := (len(t.Name) + len(t.Description) + len(t.Schema)) / 4
			r.ToolSchemaTokens += st
			items = append(items, ContextItem{Label: "tool schema: " + t.Name, Tokens: st})
		}
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].Tokens > items[j].Tokens })
	if len(items) > 5 {
		items = items[:5]
	}
	r.Top = items
	estimate := l.nextRequestTokenEstimate()
	r.RequestTokens = estimate.Effective
	r.RawRequestTokens = estimate.Raw
	r.RequestEstimateSource = estimate.Source
	r.ExactRequestBase = estimate.ExactBase
	return r
}

func estimateMessageTokens(m llm.Message) int {
	return llm.EstimateMessageTokens(m)
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
	fmt.Fprintf(&b, "Context (model: %s, route: %s, window source: %s)\n", r.Model, r.Route, r.WindowSource)
	pct := func(t int) string {
		if r.Window <= 0 {
			return ""
		}
		return fmt.Sprintf(" (%.1f%% of %d window)", float64(t)/float64(r.Window)*100, r.Window)
	}
	total := r.RequestTokens
	if total <= 0 {
		total = r.EstimatedTokens + r.ToolSchemaTokens + r.CatalogTokens
	}
	fmt.Fprintf(&b, "  messages: %d visible, %d hidden\n", r.Visible, r.Hidden)
	fmt.Fprintf(&b, "  estimated context: ~%d tok%s [%s]\n", total, pct(total), r.RequestEstimateSource)
	if r.RequestEstimateSource == "provider+delta" {
		fmt.Fprintf(&b, "    exact provider base:      %d tok (raw now ~%d)\n", r.ExactRequestBase, r.RawRequestTokens)
	}
	fmt.Fprintf(&b, "    system prompt + briefing: ~%d tok\n", r.SystemTokens)
	fmt.Fprintf(&b, "    user messages:            ~%d tok\n", r.UserTokens)
	fmt.Fprintf(&b, "    assistant messages:       ~%d tok\n", r.AssistantTokens)
	fmt.Fprintf(&b, "    tool results:             ~%d tok\n", r.ToolResultTokens)
	if r.Thin {
		fmt.Fprintf(&b, "    tool schemas (%d core):   ~%d tok\n", r.ToolCount, r.ToolSchemaTokens)
		fmt.Fprintf(&b, "    tool catalog (thin):      ~%d tok\n", r.CatalogTokens)
	} else {
		fmt.Fprintf(&b, "    tool schemas (%d tools):  ~%d tok\n", r.ToolCount, r.ToolSchemaTokens)
	}
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
