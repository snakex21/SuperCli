package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/memory"
)

// MemoryKeeper is the subset of *memory.Store the remember /
// recall tools need. The interface lives here so test stubs do
// not need a real SQLite store.
type MemoryKeeper interface {
	Put(e memory.Entry) error
	Search(query string, k int) ([]memory.Entry, error)
}

// Remember is the always-on tool that saves a fact to the
// persistent memory store (SQLite + FTS5 + markdown mirror).
// The fact survives across sessions and is searchable via the
// recall tool.
type Remember struct {
	Store MemoryKeeper
	// Now is injected for tests. Default: time.Now.
	Now func() time.Time
}

// NewRemember wires a remember tool to a store.
func NewRemember(s MemoryKeeper) *Remember {
	return &Remember{Store: s, Now: time.Now}
}

// Spec returns the Tool descriptor.
func (r *Remember) Spec() Tool {
	return Tool{
		Name: "remember",
		Description: "Save a fact to persistent memory that survives across sessions. " +
			"Use this when you learn something worth carrying forward: user preferences " +
			"(coding style, language, tools they like), project decisions and their " +
			"rationale, environment quirks, or anything the user explicitly asks you to " +
			"remember. Do NOT use it for transient task state (use the goal tool), for " +
			"facts already obvious from the codebase, or for secrets/credentials. " +
			"Keep each memory short and self-contained — one fact per call. " +
			"The optional topic groups related memories and helps later recall.",
		Schema: `{
			"type": "object",
			"properties": {
				"text":  {"type": "string", "description": "The fact to remember. Short, self-contained, one fact per call."},
				"topic": {"type": "string", "description": "Optional topic tag, e.g. 'preferences', 'project', 'environment'."}
			},
			"required": ["text"]
		}`,
		Fn: r.run,
	}
}

type rememberArgs struct {
	Text  string `json:"text"`
	Topic string `json:"topic,omitempty"`
}

func (r *Remember) run(ctx context.Context, args json.RawMessage) (Result, error) {
	if r.Store == nil {
		return Result{Text: "remember: memory store not available"}, nil
	}
	var a rememberArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("remember: bad args: %w", err)}, nil
	}
	a.Text = strings.TrimSpace(a.Text)
	if a.Text == "" {
		return Result{Err: fmt.Errorf("remember: text is empty")}, nil
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	e := memory.Entry{
		ID:      fmt.Sprintf("mem-%x", now.UnixNano()),
		Scope:   "general",
		Content: a.Text,
		Source:  memory.SourceAgent,
	}
	if t := strings.TrimSpace(a.Topic); t != "" {
		e.Tags = []string{t}
	}
	if err := r.Store.Put(e); err != nil {
		return Result{Err: fmt.Errorf("remember: %w", err)}, nil
	}
	return Result{Text: fmt.Sprintf("remembered [%s]: %s", e.ID, a.Text)}, nil
}

// Recall is the always-on tool that searches persistent memory
// (FTS5 full-text search) for facts saved in this or earlier
// sessions.
type Recall struct {
	Store MemoryKeeper
}

// NewRecall wires a recall tool to a store.
func NewRecall(s MemoryKeeper) *Recall {
	return &Recall{Store: s}
}

// Spec returns the Tool descriptor.
func (r *Recall) Spec() Tool {
	return Tool{
		Name: "recall",
		Description: "Search persistent memory for facts saved with the remember tool in " +
			"this or earlier sessions. Use this at the start of a task to check for " +
			"relevant prior context: user preferences, past project decisions, " +
			"environment quirks, or anything the user previously asked you to remember. " +
			"Do NOT use it to search the current conversation (use search_history) or " +
			"the codebase (use search_code / grep). The query is full-text: a few " +
			"keywords work best, e.g. 'test preferences' or 'database choice'.",
		Schema: `{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Full-text search query. A few keywords work best."},
				"limit": {"type": "integer", "description": "Max results to return. Default 5."}
			},
			"required": ["query"]
		}`,
		Fn: r.run,
	}
}

type recallArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

func (r *Recall) run(ctx context.Context, args json.RawMessage) (Result, error) {
	if r.Store == nil {
		return Result{Text: "recall: memory store not available"}, nil
	}
	var a recallArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("recall: bad args: %w", err)}, nil
	}
	a.Query = strings.TrimSpace(a.Query)
	if a.Query == "" {
		return Result{Err: fmt.Errorf("recall: query is empty")}, nil
	}
	if a.Limit <= 0 {
		a.Limit = 5
	}
	entries, err := r.Store.Search(a.Query, a.Limit)
	if err != nil {
		return Result{Err: fmt.Errorf("recall: %w", err)}, nil
	}
	if len(entries) == 0 {
		return Result{Text: fmt.Sprintf("recall: no memories match %q", a.Query)}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d memor(ies) matching %q:\n", len(entries), a.Query)
	for _, e := range entries {
		tag := ""
		if len(e.Tags) > 0 {
			tag = " (" + strings.Join(e.Tags, ", ") + ")"
		}
		fmt.Fprintf(&b, "- [%s]%s %s\n", e.ID, tag, strings.TrimSpace(e.Content))
	}
	return Result{Text: b.String()}, nil
}
