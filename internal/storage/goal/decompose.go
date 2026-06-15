package goal

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Decomposer turns a goal title (and optional context)
// into a task list. It has two paths:
//
//   - HeuristicFromTitle: no LLM call; cheap, runs
//     locally in microseconds. Splits the title on
//     punctuation or returns a 5-step "research, design,
//     implement, test, ship" default.
//   - ModelDecompose: calls the model with a tiny prompt
//     asking for a JSON array of tasks, then parses it
//     tolerating markdown fences and surrounding prose.
//
// Both return at most MaxDecomposeTasks (8) tasks.
const MaxDecomposeTasks = 8

// HeuristicFromTitle returns a small task list from
// the title without a model call. It is intentionally
// conservative — the LLM path is preferred when an
// `llm.Provider` is available.
func HeuristicFromTitle(title string) []string {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	// Try splitting on common separators.
	for _, sep := range []string{":", "—", " - ", " — ", ";"} {
		if strings.Contains(title, sep) {
			parts := splitNonEmpty(title, sep)
			if len(parts) >= 2 && len(parts) <= MaxDecomposeTasks {
				return trimAll(parts, 120)
			}
		}
	}
	// Fall back to a generic 5-step plan.
	def := []string{
		"research and clarify scope",
		"design the approach",
		"implement the smallest viable change",
		"add tests or verify behavior",
		"ship / document / clean up",
	}
	// Special case: bug fix.
	low := strings.ToLower(title)
	if strings.Contains(low, "fix") || strings.Contains(low, "bug") {
		def = []string{
			"reproduce the bug",
			"find the root cause",
			"write a failing test",
			"fix the root cause",
			"verify the test passes",
		}
	}
	if strings.Contains(low, "refactor") {
		def = []string{
			"identify the smell",
			"snapshot current behavior with tests",
			"refactor in small steps",
			"re-run tests after each step",
		}
	}
	return def
}

// Provider is the tiny LLM interface ModelDecompose
// needs. It matches the production *llm.OpenAIProvider
// signature (Complete). Defined here as a structural
// interface to avoid a cyclic import on internal/llm.
type Provider interface {
	Complete(ctx context.Context, msgs []Message) (string, error)
}

// Message is the minimal message shape Provider
// expects. Mirrors llm.Message.
type Message struct {
	Role    string
	Content string
}

// ModelDecompose asks the model to return a JSON array
// of 3-7 short task strings for the given title and
// context. Tolerates markdown fences and surrounding
// prose. Returns at most MaxDecomposeTasks entries.
//
// The expected shape is:
//
//	{"action":"decompose","tasks":["a","b","c"]}
//
// or simply a bare array:
//
//	["a","b","c"]
func ModelDecompose(ctx context.Context, p Provider, model, title, contextDesc string) ([]string, error) {
	if p == nil {
		return nil, fmt.Errorf("goal: ModelDecompose: nil provider")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, ErrEmptyTitle
	}
	prompt := decomposePrompt(title, contextDesc)
	msgs := []Message{
		{Role: "system", Content: decomposeSystem},
		{Role: "user", Content: prompt},
	}
	raw, err := p.Complete(ctx, msgs)
	if err != nil {
		return nil, fmt.Errorf("goal: ModelDecompose: %w", err)
	}
	return parseDecomposeResponse(raw, MaxDecomposeTasks)
}

const decomposeSystem = `You are a planning assistant. Given a goal title and optional context, produce a JSON object of the form {"action":"decompose","tasks":["...","..."]}. The "tasks" array must contain 3 to 7 short imperative sentences (max 120 chars each) that, taken together, accomplish the goal. Return ONLY the JSON object. No markdown fences, no prose, no comments.`

func decomposePrompt(title, contextDesc string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", title)
	if strings.TrimSpace(contextDesc) != "" {
		fmt.Fprintf(&b, "Context: %s\n", contextDesc)
	}
	b.WriteString("Return a JSON object with action=\"decompose\" and 3-7 tasks.")
	return b.String()
}

var (
	reDecomposeObject = regexp.MustCompile(`(?s)\{[^{}]*?"action"\s*:\s*"decompose"[^{}]*?\}`)
	reTaskQuoted      = regexp.MustCompile(`"((?:\\.|[^"\\])*)"`)
)

func parseDecomposeResponse(raw string, max int) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("goal: empty model response")
	}
	// Strip markdown fences.
	raw = stripFences(raw)
	// Try direct JSON object parse first.
	var obj struct {
		Action string   `json:"action"`
		Tasks  []string `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &obj); err == nil && len(obj.Tasks) > 0 {
		return sanitizeTasks(obj.Tasks, max), nil
	}
	// Try bare array.
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err == nil && len(arr) > 0 {
		return sanitizeTasks(arr, max), nil
	}
	// Fall back to regex: find the first { ... } and
	// pull quoted strings from the "tasks" region.
	m := reDecomposeObject.FindString(raw)
	if m == "" {
		return nil, fmt.Errorf("goal: could not parse decompose response: %q", truncate(raw, 80))
	}
	// Take everything after `"tasks":`.
	idx := strings.Index(m, `"tasks"`)
	if idx < 0 {
		return nil, fmt.Errorf("goal: no tasks field in %q", truncate(m, 80))
	}
	tail := m[idx:]
	// Skip past the key itself: `"tasks":` then take from the first `[`.
	br := strings.Index(tail, "[")
	if br < 0 {
		return nil, fmt.Errorf("goal: no tasks array in %q", truncate(m, 80))
	}
	tail = tail[br:]
	quotes := reTaskQuoted.FindAllStringSubmatch(tail, -1)
	if len(quotes) == 0 {
		return nil, fmt.Errorf("goal: no task strings in %q", truncate(m, 80))
	}
	out := make([]string, 0, len(quotes))
	for _, q := range quotes {
		out = append(out, unescapeJSON(q[1]))
		if len(out) >= max {
			break
		}
	}
	return sanitizeTasks(out, max), nil
}

func sanitizeTasks(in []string, max int) []string {
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if len(t) > 120 {
			t = t[:120]
		}
		out = append(out, t)
		if len(out) >= max {
			break
		}
	}
	return out
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Drop first line (``` or ```json)
		if i := strings.Index(s, "\n"); i > 0 {
			s = s[i+1:]
		}
		// Drop closing fence.
		if i := strings.LastIndex(s, "```"); i > 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}

func unescapeJSON(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				out.WriteByte('\n')
			case 't':
				out.WriteByte('\t')
			case '"':
				out.WriteByte('"')
			case '\\':
				out.WriteByte('\\')
			default:
				out.WriteByte(s[i+1])
			}
			i++
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

func splitNonEmpty(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func trimAll(in []string, n int) []string {
	out := make([]string, len(in))
	for i, s := range in {
		s = strings.TrimSpace(s)
		if len(s) > n {
			s = s[:n]
		}
		out[i] = s
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
