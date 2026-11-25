package search

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// SessionRecall ("przypomnij") searches the full saved transcript of the
// CURRENT conversation: user messages, answers, tool calls and results and
// worker reports. After compaction the model works from a state note; the
// exact older details stay one call away instead of being guessed.
//
// Read-only and scoped by the host: the conversation id comes from Session,
// never from the model, so no other conversation can be read. A worker also
// searches its own transcript (Worker).
type SessionRecall struct {
	Store *session.Store
	// Session returns the current conversation id ("" disables the tool).
	Session func(ctx context.Context) string
	// Worker returns the calling worker's id and own transcript (ok=false
	// for the coordinator).
	Worker func(ctx context.Context) (id string, msgs []llm.Message, ok bool)
}

// SessionRecallName is the tool name.
const SessionRecallName = "session_recall"

const (
	recallDefaultLimit = 8
	recallMaxLimit     = 20
	recallSnippetChars = 280
	recallReadDefault  = 6000
)

func (r *SessionRecall) Spec() Tool {
	return Tool{
		Name:     SessionRecallName,
		ReadOnly: true,
		Description: "Recall details from earlier in THIS conversation (its full saved transcript: user instructions, answers, " +
			"tool calls and results, errors, worker reports). After compaction only a state note remains in context; " +
			"use this before guessing an older detail, path, decision or error. Search with a few keywords (any match, " +
			"best first), optionally within a turn range (from_turn/to_turn) or time range (since/until, RFC3339) or one role. " +
			"Results are short snippets with turn numbers; read one turn in full with turn (and offset for long ones). " +
			"Other conversations are not reachable.",
		Schema: `{
  "type": "object",
  "properties": {
    "query":     {"type": "string",  "description": "keywords; any of them may match"},
    "from_turn": {"type": "integer", "description": "only turns >= this number"},
    "to_turn":   {"type": "integer", "description": "only turns <= this number"},
    "since":     {"type": "string",  "description": "RFC3339 lower time bound"},
    "until":     {"type": "string",  "description": "RFC3339 upper time bound"},
    "role":      {"type": "string",  "description": "user|assistant|tool"},
    "limit":     {"type": "integer", "description": "results (default 8, max 20)"},
    "turn":      {"type": "integer", "description": "read this turn in full instead of searching"},
    "worker_item": {"type": "integer", "description": "worker only: read this item of your own transcript"},
    "offset":    {"type": "integer", "description": "byte offset when reading a long turn"}
  }
}`,
		Fn: r.run,
	}
}

type sessionRecallArgs struct {
	Query      string `json:"query"`
	FromTurn   int    `json:"from_turn"`
	ToTurn     int    `json:"to_turn"`
	Since      string `json:"since"`
	Until      string `json:"until"`
	Role       string `json:"role"`
	Limit      int    `json:"limit"`
	Turn       int    `json:"turn"`
	WorkerItem int    `json:"worker_item"`
	Offset     int    `json:"offset"`
}

func (r *SessionRecall) run(ctx context.Context, raw json.RawMessage) (Result, error) {
	var a sessionRecallArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return Result{Err: fmt.Errorf("session_recall: bad args: %w", err)}, nil
		}
	}
	sid := ""
	if r.Session != nil {
		sid = strings.TrimSpace(r.Session(ctx))
	}
	if r.Store == nil || sid == "" {
		return Result{Text: "session_recall: this conversation has no saved transcript yet"}, nil
	}
	var workerID string
	var workerMsgs []llm.Message
	if r.Worker != nil {
		if id, msgs, ok := r.Worker(ctx); ok {
			workerID, workerMsgs = id, msgs
		}
	}
	if a.WorkerItem > 0 {
		if workerID == "" {
			return Result{Err: fmt.Errorf("session_recall: worker_item is only for workers")}, nil
		}
		if a.WorkerItem > len(workerMsgs) {
			return Result{Err: fmt.Errorf("session_recall: %s has %d items", workerID, len(workerMsgs))}, nil
		}
		return Result{Text: recallReadText(fmt.Sprintf("[%s item %d, %s]", workerID, a.WorkerItem, workerMsgs[a.WorkerItem-1].Role),
			recallMessageText(workerMsgs[a.WorkerItem-1]), a.Offset)}, nil
	}
	if a.Turn > 0 {
		h, err := r.Store.ReadHistoryExcerpt(ctx, sid, a.Turn, max(a.Offset, 0), recallReadDefault)
		if err != nil {
			return Result{Err: fmt.Errorf("session_recall: turn %d: %w", a.Turn, err)}, nil
		}
		return Result{Text: fmt.Sprintf("[turn %d, %s, %s]\n%s", h.Seq, h.Role, h.CreatedAt.Format(time.RFC3339), h.Text) +
			recallMore(h.NextOffset, a.Turn)}, nil
	}
	terms := recallTerms(a.Query)
	if len(terms) == 0 {
		return Result{Err: fmt.Errorf("session_recall: give a few keywords in query, or a turn to read")}, nil
	}
	switch a.Role {
	case "", "user", "assistant", "tool":
	default:
		return Result{Err: fmt.Errorf("session_recall: role must be user, assistant or tool")}, nil
	}
	var since, until time.Time
	var err error
	if a.Since != "" {
		if since, err = time.Parse(time.RFC3339, a.Since); err != nil {
			return Result{Err: fmt.Errorf("session_recall: bad since: %w", err)}, nil
		}
	}
	if a.Until != "" {
		if until, err = time.Parse(time.RFC3339, a.Until); err != nil {
			return Result{Err: fmt.Errorf("session_recall: bad until: %w", err)}, nil
		}
	}
	limit := a.Limit
	if limit <= 0 {
		limit = recallDefaultLimit
	}
	limit = min(limit, recallMaxLimit)
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + t + `"`
	}
	// Search wider than the limit: the turn range is applied afterwards.
	hits, err := r.Store.SearchHistory(ctx, strings.Join(quoted, " OR "), sid, a.Role, since, until, 100)
	if err != nil {
		return Result{Err: fmt.Errorf("session_recall: %w", err)}, nil
	}
	var b strings.Builder
	shown := 0
	for _, h := range hits {
		if (a.FromTurn > 0 && h.Seq < a.FromTurn) || (a.ToTurn > 0 && h.Seq > a.ToTurn) {
			continue
		}
		if shown == limit {
			break
		}
		shown++
		fmt.Fprintf(&b, "turn %d · %s · %s\n%s\n", h.Seq, h.Role, h.CreatedAt.Format("2006-01-02 15:04"), recallClip(strings.NewReplacer("<mark>", "", "</mark>", "").Replace(h.Snippet), recallSnippetChars))
	}
	if workerID != "" {
		for _, it := range recallWorkerHits(workerMsgs, terms, limit) {
			shown++
			fmt.Fprintf(&b, "%s item %d · %s\n%s\n", workerID, it.index, it.role, it.snippet)
		}
	}
	if shown == 0 {
		return Result{Text: "no matches in this conversation for: " + strings.Join(terms, ", ")}, nil
	}
	b.WriteString("Read one in full with {\"turn\": N}")
	if workerID != "" {
		b.WriteString(" or {\"worker_item\": N}")
	}
	b.WriteString(". Older results are past evidence; check current files when it matters.")
	return Result{Text: b.String()}, nil
}

// recallTerms keeps letters/digits words of length >= 2, lower-cased,
// deduplicated, at most 12.
func recallTerms(q string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.'
	}) {
		w = strings.Trim(w, "-.")
		if utf8.RuneCountInString(w) < 2 || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) == 12 {
			break
		}
	}
	return out
}

type recallWorkerHit struct {
	index   int
	role    string
	snippet string
	score   int
}

func recallWorkerHits(msgs []llm.Message, terms []string, limit int) []recallWorkerHit {
	var hits []recallWorkerHit
	for i, m := range msgs {
		if m.Role == llm.RoleSystem {
			continue
		}
		text := recallMessageText(m)
		lower := strings.ToLower(text)
		score, first := 0, -1
		for _, t := range terms {
			if at := strings.Index(lower, t); at >= 0 {
				score++
				if first < 0 || at < first {
					first = at
				}
			}
		}
		if score == 0 {
			continue
		}
		start := max(first-80, 0)
		for start > 0 && !utf8.RuneStart(text[start]) {
			start--
		}
		hits = append(hits, recallWorkerHit{index: i + 1, role: string(m.Role), snippet: recallClip(text[start:], recallSnippetChars), score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	return hits[:min(len(hits), limit)]
}

func recallMessageText(m llm.Message) string {
	var b strings.Builder
	b.WriteString(m.TextOnly().Content)
	for _, tc := range m.ToolCalls {
		fmt.Fprintf(&b, "\n[tool call %s %s]", tc.Name, tc.Arguments)
	}
	return b.String()
}

func recallClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func recallReadText(header, text string, offset int) string {
	if offset > 0 && offset < len(text) {
		for offset < len(text) && !utf8.RuneStart(text[offset]) {
			offset++
		}
		text = text[offset:]
	}
	if len(text) > recallReadDefault {
		cut := recallReadDefault
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + fmt.Sprintf("\n[… %d more bytes; read on with offset]", len(text)-cut)
	}
	return header + "\n" + text
}

func recallMore(next, turn int) string {
	if next <= 0 {
		return ""
	}
	return fmt.Sprintf("\n[more: {\"turn\": %d, \"offset\": %d}]", turn, next)
}
