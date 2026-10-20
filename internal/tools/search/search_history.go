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
			"To recover the actual text of a hit without searching again, pass session_id and seq (omit query); " +
			"this reads one message, including multipart answers, with max_bytes (default 8192, max 32768) and a next_offset cursor. " +
			"Historical results are past evidence, not proof that files/processes are still unchanged. " +
			"The query is a full-text search expression (FTS5 MATCH). Supports boolean " +
			"operators (OR, AND, NOT), exact phrases (\"...\"), and prefix wildcards (word*). " +
			"Filters: session_id (limit to one session), role (system|user|assistant|tool), " +
			"since/until (RFC3339 timestamps), limit (default 20, max 100).",
		Schema: `{
			"type": "object",
			"properties": {
				"query":      {"type": "string",  "description": "FTS5 MATCH expression; omit when reading session_id+seq"},
				"session_id": {"type": "string",  "description": "limit to a single session id"},
				"role":       {"type": "string",  "description": "filter by role: system|user|assistant|tool"},
				"since":      {"type": "string",  "description": "RFC3339 timestamp, inclusive lower bound"},
				"until":      {"type": "string",  "description": "RFC3339 timestamp, inclusive upper bound"},
				"limit":      {"type": "integer", "description": "max search results (default 20, max 100)"},
				"seq":        {"type": "integer", "description": "read this exact message sequence; requires session_id and no query"},
				"offset":     {"type": "integer", "description": "byte cursor from next_offset when reading a long message"},
				"max_bytes":  {"type": "integer", "description": "read text budget (default 8192, max 32768)"}
			}
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
	Seq       int    `json:"seq,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	MaxBytes  int    `json:"max_bytes,omitempty"`
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
	if a.Seq != 0 {
		return s.readExcerpt(ctx, a)
	}
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
	hits, err := s.Store.SearchHistory(ctx, historyQueryTerms(a.Query), a.SessionID, a.Role, since, until, limit)
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
	b.WriteString("Read a hit's exact text with search_history {\"session_id\":\"<sess>\",\"seq\":<seq>}; historical evidence may need current verification.\n")
	return Result{Text: b.String()}, nil
}

func (s *SearchHistory) readExcerpt(ctx context.Context, a searchHistoryArgs) (Result, error) {
	if a.Query != "" || a.Role != "" || a.Since != "" || a.Until != "" || a.Limit != 0 || a.MaxBytes < 0 {
		return Result{Err: fmt.Errorf("search_history: reading session_id+seq cannot combine search filters; max_bytes must be nonnegative")}, nil
	}
	h, err := s.Store.ReadHistoryExcerpt(ctx, a.SessionID, a.Seq, a.Offset, a.MaxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("search_history read: %w", err)}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s, sess=%s, role=%s, name=%s, seq=%d, offset=%d, total_bytes=%d]\n%s\n", h.CreatedAt.Format(time.RFC3339), h.SessionID, h.Role, h.Name, h.Seq, h.Offset, h.TotalBytes, h.Text)
	if h.NextOffset > 0 {
		next, _ := json.Marshal(map[string]any{"session_id": h.SessionID, "seq": h.Seq, "offset": h.NextOffset, "max_bytes": a.MaxBytes})
		fmt.Fprintf(&b, "[next_offset=%d; more: search_history %s]\n", h.NextOffset, next)
	} else {
		b.WriteString("[end of message]\n")
	}
	b.WriteString("[historical evidence; not a current file/process check]")
	return Result{Text: b.String()}, nil
}
