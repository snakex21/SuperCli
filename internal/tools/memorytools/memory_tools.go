package memorytools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/storage/memory"
	"supercli/internal/tools/core"
)

const (
	maxRememberTextBytes = 4 * 1024
	maxRecallLimit       = 10
	maxRecallOutputBytes = 8 * 1024
	maxRecallEntryBytes  = 1800
)

// MemoryKeeper is the subset of *memory.Store the remember /
// recall tools need. The interface lives here so test stubs do
// not need a real SQLite store.
type MemoryKeeper interface {
	Put(e memory.Entry) error
	Search(query string, k int) ([]memory.Entry, error)
}

// RecentKeeper is an optional capability used when lexical search
// cannot bridge languages (for example, an English "user name"
// query for a Polish "Użytkownik ma na imię Maks" memory).
type RecentKeeper interface {
	Recent(scope string, n int) ([]memory.Entry, error)
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
			"explicitly asks you to remember. Save personal information such as the " +
			"user's name, preferences, location or habits immediately with " +
			"topic=user_profile; do not wait until task completion. After finishing a task, save a short " +
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
	if len(a.Text) > maxRememberTextBytes {
		return Result{Err: fmt.Errorf("remember: text is %d bytes, exceeds %d byte limit; summarize it into one short self-contained fact", len(a.Text), maxRememberTextBytes)}, nil
	}
	topic := strings.TrimSpace(a.Topic)
	entryScope := normalizeMemType(a.Type)
	// Models commonly save identity facts with a user_profile topic
	// but omit type=preference. Treat those as durable user
	// preferences so they default to the global store.
	if strings.TrimSpace(a.Type) == "" && isUserProfileTopic(topic) {
		entryScope = memory.ScopePreference
	}
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
	if topic != "" {
		e.Tags = []string{topic}
	}
	if err := target.Put(e); err != nil {
		return Result{Err: fmt.Errorf("remember: %w", err)}, nil
	}
	if r.OnSave != nil {
		r.OnSave()
	}
	return Result{Text: fmt.Sprintf("remembered [%s] (%s, %s): %s", e.ID, entryScope, targetName, a.Text)}, nil
}

func isUserProfileTopic(topic string) bool {
	normalized := strings.ToLower(strings.TrimSpace(topic))
	normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)
	switch normalized {
	case "user_profile", "user_identity", "profile", "identity", "personal_profile":
		return true
	default:
		return false
	}
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
		Description: "Search persistent memory for facts saved via remember in past sessions: " +
			"user preferences, project decisions, environment quirks. Use at task start for " +
			"prior context. NOT for the current conversation (use search_history) or the " +
			"codebase (use search_code / grep). Full-text query — a few keywords work best.",
		Schema: `{"type":"object","properties":{
"query":{"type":"string","description":"Search keywords"},
"scope":{"type":"string","enum":["project","global","all"],"description":"Default all"},
"limit":{"type":"integer","description":"Default 5"}
},"required":["query"]}`,
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

func recentOne(s MemoryKeeper, k int) ([]memory.Entry, error) {
	if s == nil {
		return nil, nil
	}
	if recent, ok := s.(RecentKeeper); ok {
		return recent.Recent("", k)
	}
	return nil, nil
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
	if a.Limit > maxRecallLimit {
		a.Limit = maxRecallLimit
	}
	scope := strings.ToLower(strings.TrimSpace(a.Scope))
	if scope == "" {
		scope = "all"
	}
	if scope != "project" && scope != "global" && scope != "all" {
		return Result{Err: fmt.Errorf("recall: scope must be project, global or all")}, nil
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

	fallback := false
	if len(hits) == 0 {
		var recentHits []hit
		if scope == "project" || scope == "all" {
			es, err := recentOne(r.Store, a.Limit)
			if err != nil {
				return Result{Err: fmt.Errorf("recall fallback: %w", err)}, nil
			}
			for _, e := range es {
				recentHits = append(recentHits, hit{e, "project"})
			}
		}
		if scope == "global" || scope == "all" {
			es, err := recentOne(r.Global, a.Limit)
			if err != nil {
				return Result{Err: fmt.Errorf("recall fallback: %w", err)}, nil
			}
			for _, e := range es {
				recentHits = append(recentHits, hit{e, "global"})
			}
		}
		// Identity and preference memories are the most useful
		// cross-language fallback. Keep them ahead of unrelated
		// recent project notes while preserving recency within each
		// group.
		for _, h := range recentHits {
			if h.e.Scope == memory.ScopePreference || hasUserProfileTag(h.e.Tags) {
				hits = append(hits, h)
			}
		}
		for _, h := range recentHits {
			if h.e.Scope != memory.ScopePreference && !hasUserProfileTag(h.e.Tags) {
				hits = append(hits, h)
			}
		}
		fallback = len(hits) > 0
	}
	if len(hits) == 0 {
		return Result{Text: fmt.Sprintf("recall: no memories match %q", a.Query)}, nil
	}
	if len(hits) > a.Limit {
		hits = hits[:a.Limit]
	}
	var b strings.Builder
	if fallback {
		fmt.Fprintf(&b, "No lexical match for %q; showing %d recent durable memor(ies) as a cross-language fallback:\n", a.Query, len(hits))
	} else {
		fmt.Fprintf(&b, "%d memor(ies) matching %q:\n", len(hits), a.Query)
	}
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
		content := strings.Join(strings.Fields(e.Content), " ")
		content = core.HeadTail(content, maxRecallEntryBytes-300, 300)
		fmt.Fprintf(&b, "- [%s] %s/%s%s%s %s\n", e.ID, h.where, e.Scope, date, tag, content)
	}
	return Result{Text: core.HeadTail(b.String(), maxRecallOutputBytes-1024, 1024)}, nil
}

func hasUserProfileTag(tags []string) bool {
	for _, tag := range tags {
		if isUserProfileTopic(tag) {
			return true
		}
	}
	return false
}
