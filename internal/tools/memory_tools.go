package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/storage/memory"
)

// MemoryKeeper is the subset of *memory.Store the remember /
// recall tools need. The interface lives here so test stubs do
// not need a real SQLite store.
type MemoryKeeper interface {
	Put(e memory.Entry) error
	Search(query string, k int) ([]memory.Entry, error)
}

// HybridSearcher is the optional upgrade a MemoryKeeper can
// implement (the real *memory.Store does): FTS5 + vector search
// with rank fusion. recall type-asserts for it and falls back to
// plain Search otherwise.
type HybridSearcher interface {
	HybridSearch(ctx context.Context, query string, k int) ([]memory.Entry, error)
}

// Remember is the always-on tool that saves a fact to the
// persistent memory store (SQLite + FTS5 + markdown mirror).
// The fact survives across sessions and is searchable via the
// recall tool. With both stores wired, `scope` routes the entry
// to the per-project DB (default) or the global one.
type Remember struct {
	Store  MemoryKeeper // project store (default scope)
	Global MemoryKeeper // optional global store
	// OnSave is called after every successful save. The session
	// auto-saver uses it to know the model kept its own notes.
	OnSave func()
	// Now is injected for tests. Default: time.Now.
	Now func() time.Time
}

// NewRemember wires a remember tool to a single (project) store.
func NewRemember(s MemoryKeeper) *Remember {
	return &Remember{Store: s, Now: time.Now}
}

// NewRememberDual wires a remember tool to both stores.
func NewRememberDual(project, global MemoryKeeper) *Remember {
	return &Remember{Store: project, Global: global, Now: time.Now}
}

// Spec returns the Tool descriptor.
func (r *Remember) Spec() Tool {
	return Tool{
		Name: "remember",
		Description: "Save a fact to persistent memory that survives across sessions. " +
			"Use this when you learn something worth carrying forward: user preferences " +
			"(coding style, language, tools they like), project decisions and their " +
			"rationale, environment quirks, session summaries, or anything the user " +
			"explicitly asks you to remember. After finishing a task, save a short " +
			"type=task-log note: WHAT you did, WHY, and which files you touched. " +
			"Do NOT use it for transient task state (use the goal tool), for " +
			"facts already obvious from the codebase, or for secrets/credentials. " +
			"Keep each memory short and self-contained — one fact per call.",
		Schema: `{
			"type": "object",
			"properties": {
				"text":  {"type": "string", "description": "The fact to remember. Short, self-contained, one fact per call."},
				"type":  {"type": "string", "enum": ["fact", "decision", "task-log", "preference"], "description": "Kind of memory. Default: fact. Use preference for user preferences, decision for project decisions with rationale, task-log for end-of-task summaries."},
				"scope": {"type": "string", "enum": ["project", "global"], "description": "Where to store it. Default: project. Use global for cross-project user preferences."},
				"topic": {"type": "string", "description": "Optional topic tag, e.g. 'testing', 'deploy', 'style'."}
			},
			"required": ["text"]
		}`,
		Fn: r.run,
	}
}

type rememberArgs struct {
	Text  string `json:"text"`
	Type  string `json:"type,omitempty"`
	Scope string `json:"scope,omitempty"`
	Topic string `json:"topic,omitempty"`
}

func (r *Remember) run(ctx context.Context, args json.RawMessage) (Result, error) {
	if r.Store == nil && r.Global == nil {
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
	entryScope := normalizeMemType(a.Type)
	target := r.Store
	targetName := "project"
	switch strings.ToLower(strings.TrimSpace(a.Scope)) {
	case "global":
		if r.Global != nil {
			target = r.Global
			targetName = "global"
		}
	case "", "project":
		// default
	default:
		return Result{Err: fmt.Errorf("remember: scope must be 'project' or 'global'")}, nil
	}
	if target == nil { // project store missing, global present
		target = r.Global
		targetName = "global"
	}
	// Preferences default to the global store: they describe the
	// user, not the project.
	if entryScope == memory.ScopePreference && r.Global != nil && strings.TrimSpace(a.Scope) == "" {
		target = r.Global
		targetName = "global"
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	e := memory.Entry{
		ID:      fmt.Sprintf("mem-%x", now.UnixNano()),
		Scope:   entryScope,
		Content: a.Text,
		Source:  memory.SourceAgent,
	}
	if t := strings.TrimSpace(a.Topic); t != "" {
		e.Tags = []string{t}
	}
	if err := target.Put(e); err != nil {
		return Result{Err: fmt.Errorf("remember: %w", err)}, nil
	}
	if r.OnSave != nil {
		r.OnSave()
	}
	return Result{Text: fmt.Sprintf("remembered [%s] (%s, %s): %s", e.ID, entryScope, targetName, a.Text)}, nil
}

// normalizeMemType maps the tool's `type` argument to an entry
// scope. Unknown or empty types become "fact"; the legacy
// "general" scope written by older builds keeps working on read.
func normalizeMemType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case memory.ScopeDecision:
		return memory.ScopeDecision
	case memory.ScopeTaskLog, "tasklog", "task_log", "log":
		return memory.ScopeTaskLog
	case memory.ScopePreference, "pref":
		return memory.ScopePreference
	default:
		return memory.ScopeFact
	}
}

// Recall is the always-on tool that searches persistent memory
// (hybrid FTS5 + vector search when embeddings are available)
// for facts saved in this or earlier sessions.
type Recall struct {
	Store  MemoryKeeper // project store
	Global MemoryKeeper // optional global store
}

// NewRecall wires a recall tool to a single (project) store.
func NewRecall(s MemoryKeeper) *Recall {
	return &Recall{Store: s}
}

// NewRecallDual wires a recall tool to both stores.
func NewRecallDual(project, global MemoryKeeper) *Recall {
	return &Recall{Store: project, Global: global}
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
			"the codebase (use search_code / grep). The query is full-text (plus " +
			"semantic search when embeddings are available): a few keywords work best.",
		Schema: `{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Search query. A few keywords work best."},
				"scope": {"type": "string", "enum": ["project", "global", "all"], "description": "Which memory to search. Default: all."},
				"limit": {"type": "integer", "description": "Max results to return. Default 5."}
			},
			"required": ["query"]
		}`,
		Fn: r.run,
	}
}

type recallArgs struct {
	Query string `json:"query"`
	Scope string `json:"scope,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// searchOne runs the best available search on a single store.
func searchOne(ctx context.Context, s MemoryKeeper, query string, k int) ([]memory.Entry, error) {
	if s == nil {
		return nil, nil
	}
	if h, ok := s.(HybridSearcher); ok {
		return h.HybridSearch(ctx, query, k)
	}
	return s.Search(query, k)
}

func (r *Recall) run(ctx context.Context, args json.RawMessage) (Result, error) {
	if r.Store == nil && r.Global == nil {
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
	scope := strings.ToLower(strings.TrimSpace(a.Scope))
	if scope == "" {
		scope = "all"
	}
	type hit struct {
		e     memory.Entry
		where string
	}
	var hits []hit
	if scope == "project" || scope == "all" {
		es, err := searchOne(ctx, r.Store, a.Query, a.Limit)
		if err != nil {
			return Result{Err: fmt.Errorf("recall: %w", err)}, nil
		}
		for _, e := range es {
			hits = append(hits, hit{e, "project"})
		}
	}
	if scope == "global" || scope == "all" {
		es, err := searchOne(ctx, r.Global, a.Query, a.Limit)
		if err != nil {
			return Result{Err: fmt.Errorf("recall: %w", err)}, nil
		}
		for _, e := range es {
			hits = append(hits, hit{e, "global"})
		}
	}
	if scope != "project" && scope != "global" && scope != "all" {
		return Result{Err: fmt.Errorf("recall: scope must be project, global or all")}, nil
	}
	if len(hits) == 0 {
		return Result{Text: fmt.Sprintf("recall: no memories match %q", a.Query)}, nil
	}
	if len(hits) > a.Limit {
		hits = hits[:a.Limit]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d memor(ies) matching %q:\n", len(hits), a.Query)
	for _, h := range hits {
		e := h.e
		tag := ""
		if len(e.Tags) > 0 {
			tag = " (" + strings.Join(e.Tags, ", ") + ")"
		}
		date := ""
		if !e.UpdatedAt.IsZero() {
			date = " " + e.UpdatedAt.Format("2006-01-02")
		}
		fmt.Fprintf(&b, "- [%s] %s/%s%s%s %s\n", e.ID, h.where, e.Scope, date, tag, strings.TrimSpace(e.Content))
	}
	return Result{Text: b.String()}, nil
}
