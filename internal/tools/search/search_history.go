package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/storage/session"
)

// SearchHistory is the F13 tool. It lets the model query its
// own past conversations via FTS5 over messages.content.
//
// Schema:
//
//	{
//	  "query":      string (required) — FTS5 MATCH expression
//	  "session_id": string (optional) — limit to one session
//	  "role":       string (optional) — system|user|assistant|tool
//	  "since":      string (optional) — RFC3339 timestamp
//	  "until":      string (optional) — RFC3339 timestamp
//	  "limit":      int    (optional) — default 20, max 100
//	}
//
// The FTS5 query supports the MATCH operator syntax:
//   - "konspekt OR refaktoryzacja"  (boolean)
//   - "\"exact phrase\""             (exact)
//   - "prefix*"                      (prefix)
//
// Polish diacritic folding is partial: combining marks
// (ą/ę/ó) are folded, but `ł` is not. The store layer
// documents this limitation.
//
// The tool is registered opt-in (NOT MarkAlwaysOn); the
// model discovers it via tool_search when needed.
type SearchHistory struct {
	// Store is the session store to query. nil disables the tool.
	Store *session.Store
	// DefaultLimit is used when the model omits `limit`. 20.
	DefaultLimit int
	// MaxLimit caps the model's request. 100.
	MaxLimit int
	// NowFn returns the current time, overridable for tests.
	NowFn func() time.Time
}

// NewSearchHistory returns a SearchHistory bound to store.
// Default limit 20, max 100.
func NewSearchHistory(store *session.Store) *SearchHistory {
	return &SearchHistory{Store: store, DefaultLimit: 20, MaxLimit: 100, NowFn: time.Now}
}

// Spec returns the tools.Tool description for the registry.
func (s *SearchHistory) Spec() Tool {
	return Tool{
		Name:     "search_history",
		ReadOnly: true,
		Description: "Search the conversation history of all prior sessions. " +
			"Returns matching messages with their session, role, seq, and timestamp, " +
			"plus a snippet of the message text with matches highlighted in <mark>...</mark>. " +
			"Use this to recall prior decisions, findings, or context from earlier sessions. " +
			"The query is a full-text search expression (FTS5 MATCH). Supports boolean " +
			"operators (OR, AND, NOT), exact phrases (\"...\"), and prefix wildcards (word*). " +
			"Filters: session_id (limit to one session), role (system|user|assistant|tool), " +
			"since/until (RFC3339 timestamps), limit (default 20, max 100).",
		Schema: `{
			"type": "object",
			"properties": {
				"query":      {"type": "string",  "description": "FTS5 MATCH expression (required)"},
				"session_id": {"type": "string",  "description": "limit to a single session id"},
				"role":       {"type": "string",  "description": "filter by role: system|user|assistant|tool"},
				"since":      {"type": "string",  "description": "RFC3339 timestamp, inclusive lower bound"},
				"until":      {"type": "string",  "description": "RFC3339 timestamp, inclusive upper bound"},
				"limit":      {"type": "integer", "description": "max results (default 20, max 100)"}
			},
			"required": ["query"]
		}`,
		Fn: s.run,
	}
}

type searchHistoryArgs struct {
	Query     string `json:"query"`
	SessionID string `json:"session_id,omitempty"`
	Role      string `json:"role,omitempty"`
	Since     string `json:"since,omitempty"`
	Until     string `json:"until,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

func (s *SearchHistory) run(ctx context.Context, args json.RawMessage) (Result, error) {
	if s.Store == nil {
		return Result{Text: "search_history: session store not available"}, nil
	}
	var a searchHistoryArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("search_history: bad args: %w", err)}, nil
	}
	a.Query = strings.TrimSpace(a.Query)
	if a.Query == "" {
		return Result{Err: fmt.Errorf("search_history: query is empty")}, nil
	}
	limit := a.Limit
	if limit <= 0 {
		limit = s.DefaultLimit
	}
	if limit > s.MaxLimit {
		limit = s.MaxLimit
	}
	var since, until time.Time
	if a.Since != "" {
		t, err := time.Parse(time.RFC3339, a.Since)
		if err != nil {
			return Result{Err: fmt.Errorf("search_history: bad since: %w", err)}, nil
		}
		since = t
	}
	if a.Until != "" {
		t, err := time.Parse(time.RFC3339, a.Until)
		if err != nil {
			return Result{Err: fmt.Errorf("search_history: bad until: %w", err)}, nil
		}
		until = t
	}
	// Default role validation: accept only known roles; pass
	// empty string to skip the filter.
	if a.Role != "" {
		switch a.Role {
		case "system", "user", "assistant", "tool":
		default:
			return Result{Err: fmt.Errorf("search_history: invalid role %q (want system|user|assistant|tool)", a.Role)}, nil
		}
	}
	hits, err := s.Store.SearchHistory(ctx, a.Query, a.SessionID, a.Role, since, until, limit)
	if err != nil {
		return Result{Err: fmt.Errorf("search_history: %w", err)}, nil
	}
	if len(hits) == 0 {
		return Result{Text: "no matches"}, nil
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "[%s, sess=%s, role=%s, seq=%d]\n%s\n---\n",
			h.CreatedAt.Format(time.RFC3339),
			h.SessionID,
			h.Role,
			h.Seq,
			h.Snippet,
		)
	}
	return Result{Text: b.String()}, nil
}
