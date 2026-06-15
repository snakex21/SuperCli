package reflect

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrorRecord mirrors the JSONL line written by
// tools.ErrorLog in F4.d. Fields map 1:1; only the ones the
// extractor needs are decoded.
type ErrorRecord struct {
	Timestamp time.Time `json:"ts"`
	Tool      string    `json:"tool"`
	Category  string    `json:"category"`
	Reason    string    `json:"reason"`
	Suggest   string    `json:"suggestion,omitempty"`
	Attempt   int       `json:"attempt"`
}

// Extractor reads the JSONL error log and groups records
// by (tool, category, normalized reason) into Patterns.
// Sessions are an optional bonus signal: any pattern also
// seen in a session history bumps its confidence.
//
// Extractor is stateless — multiple goroutines may call
// Extract concurrently. The errors log is opened read-only
// on every call so external writers do not have to coordinate.
type Extractor struct {
	// ErrorsPath is the absolute path to the JSONL log
	// (F4.d default: <dataDir>/logs/tool_errors.log).
	ErrorsPath string
	// Session is an optional F2.c session reader. nil = skip.
	Session SessionReader
	// Since filters records older than this timestamp.
	// Zero value = include all.
	Since time.Time
	// MaxPatterns caps the result list (default 5, max 100).
	MaxPatterns int
}

// SessionReader lets the Extractor inspect past sessions
// for patterns that recurred across them. Implemented by
// session.Store from F2.c. The interface lives in the
// reflect package to avoid a cycle.
type SessionReader interface {
	RecentSessions(ctx context.Context, limit int) ([]SessionSummary, error)
}

// SessionSummary is a minimal description of a session
// passed to the extractor. The agent package maps a
// session.Store record into this struct.
type SessionSummary struct {
	ID    string
	Title string
	Text  string // concatenated user/assistant messages
}

// Extract scans the log, groups records, and returns the
// top MaxPatterns by raw count. Each pattern's Confidence
// is min(1.0, count/5.0) (a single occurrence = 0.2).
//
// An empty result is a valid outcome (no errors yet) —
// callers should treat it as "nothing to store".
func (e *Extractor) Extract(ctx context.Context) ([]Pattern, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.MaxPatterns <= 0 {
		e.MaxPatterns = 5
	}
	if e.MaxPatterns > 100 {
		e.MaxPatterns = 100
	}

	records, err := e.readErrors(e.Since)
	if err != nil {
		return nil, fmt.Errorf("reflect: read errors: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}

	// Group by (tool, category, normalized reason).
	type key struct {
		Tool, Category, Reason string
	}
	groups := make(map[key][]ErrorRecord)
	for _, r := range records {
		groups[key{r.Tool, r.Category, normalizeReason(r.Reason)}] = append(groups[key{r.Tool, r.Category, normalizeReason(r.Reason)}], r)
	}

	// Build patterns; sort by count desc.
	patterns := make([]Pattern, 0, len(groups))
	for k, recs := range groups {
		p := Pattern{
			Kind:        inferKind(k.Tool, k.Category),
			Title:       buildTitle(k.Tool, k.Category, k.Reason),
			Description: buildDescription(k.Tool, k.Category, k.Reason, pickSuggestion(recs)),
			Tool:        k.Tool,
			Category:    k.Category,
			Reason:      k.Reason,
			Count:       len(recs),
			Confidence:  min1(float64(len(recs)) / 5.0),
			Suggestion:  pickSuggestion(recs),
			ObservedAt:  collectTimestamps(recs),
		}
		p.ID = HashPattern(p.Kind, p.Title)
		patterns = append(patterns, p)
	}
	sort.Slice(patterns, func(i, j int) bool {
		if patterns[i].Count != patterns[j].Count {
			return patterns[i].Count > patterns[j].Count
		}
		return patterns[i].ID < patterns[j].ID
	})
	if len(patterns) > e.MaxPatterns {
		patterns = patterns[:e.MaxPatterns]
	}

	// Cross-session bonus: bump confidence if a recent
	// session also references the tool/reason tokens.
	if e.Session != nil {
		sessions, err := e.Session.RecentSessions(ctx, 10)
		if err == nil {
			for i := range patterns {
				patterns[i].Confidence = boost(patterns[i], sessions)
			}
		}
	}
	return patterns, nil
}

// readErrors scans the JSONL log. Missing file = empty
// slice (not an error — the user may not have run any
// failed tools yet).
func (e *Extractor) readErrors(since time.Time) ([]ErrorRecord, error) {
	if e.ErrorsPath == "" {
		return nil, nil
	}
	f, err := os.Open(e.ErrorsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	// F4.d lines can be long (JSON-encoded args); bump
	// the buffer to 1 MiB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var out []ErrorRecord
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec ErrorRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			// Skip malformed lines — never abort the
			// extraction.
			continue
		}
		if !since.IsZero() && rec.Timestamp.Before(since) {
			continue
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// normalizeReason reduces a reason to a stable key by
// lowercasing, collapsing whitespace, and stripping
// path-like fragments. The same logical error ("file
// not found: /var/log/x.log") should always map to the
// same key, regardless of the actual file path.
func normalizeReason(reason string) string {
	s := strings.ToLower(strings.TrimSpace(reason))
	if s == "" {
		return ""
	}
	// Replace path-like tokens (containing a slash) with
	// a generic "<path>". A cheap heuristic: any
	// whitespace-bounded token that includes "/" or
	// starts with "/".
	tokens := strings.Fields(s)
	for i, tok := range tokens {
		if strings.Contains(tok, "/") || strings.HasPrefix(tok, "/") {
			tokens[i] = "<path>"
		}
	}
	return strings.Join(tokens, " ")
}

// inferKind maps a (tool, category) to a Pattern kind.
// Defaults to KindError — a future version can add
// "user_feedback" etc.
func inferKind(tool, category string) Kind {
	return KindError
}

// buildTitle is a short human label used in markdown.
func buildTitle(tool, category, reason string) string {
	if reason == "" {
		return fmt.Sprintf("%s: %s", tool, category)
	}
	// Trim reason to ~80 runes for readability.
	r := reason
	if len(r) > 80 {
		r = r[:77] + "..."
	}
	return fmt.Sprintf("%s: %s", tool, r)
}

// buildDescription produces a one-sentence body the model
// sees in its system prompt. Example output:
//   "search_code errors with 'rg not found' (env). Try installing ripgrep or using grep fallback."
func buildDescription(tool, category, reason, suggestion string) string {
	r := reason
	if len(r) > 80 {
		r = r[:77] + "..."
	}
	s := fmt.Sprintf("When using %s, errors with %q (%s) recurred.", tool, r, category)
	if suggestion != "" {
		s += " Suggestion: " + suggestion + "."
	}
	return s
}

// pickSuggestion returns the most common non-empty
// Suggestion across the group. Empty = group had no
// suggestions.
func pickSuggestion(recs []ErrorRecord) string {
	counts := make(map[string]int)
	for _, r := range recs {
		if r.Suggest == "" {
			continue
		}
		counts[r.Suggest]++
	}
	if len(counts) == 0 {
		return ""
	}
	best, bestN := "", 0
	for s, n := range counts {
		if n > bestN {
			best, bestN = s, n
		}
	}
	return best
}

// collectTimestamps returns up to 5 timestamps of when
// this pattern was observed, sorted ascending.
func collectTimestamps(recs []ErrorRecord) []time.Time {
	if len(recs) > 5 {
		recs = recs[len(recs)-5:]
	}
	out := make([]time.Time, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Timestamp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// boost adjusts a Pattern's confidence upward when the
// same tokens appear in any recent session text. The bump
// is proportional to the match ratio, capped at +0.1 per
// session and 1.0 total.
func boost(p Pattern, sessions []SessionSummary) float64 {
	if len(sessions) == 0 {
		return p.Confidence
	}
	tokens := tokenize(p.Tool + " " + p.Reason)
	if len(tokens) == 0 {
		return p.Confidence
	}
	maxRatio := 0.0
	for _, s := range sessions {
		sText := strings.ToLower(s.Text)
		hits := 0
		for _, t := range tokens {
			if strings.Contains(sText, t) {
				hits++
			}
		}
		ratio := float64(hits) / float64(len(tokens))
		if ratio > maxRatio {
			maxRatio = ratio
		}
	}
	bump := maxRatio * 0.1
	out := p.Confidence + bump
	if out > 1.0 {
		out = 1.0
	}
	return out
}

// tokenize returns lowercased, alphanumeric words with
// length >= 2.
func tokenize(s string) []string {
	fields := strings.Fields(strings.ToLower(s))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, ".,;:!?()[]{}\"'")
		if len(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

func min1(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Ensure DefaultErrorsPath stays in sync with tools/error_attribution.go.
// Both default to "<dataDir>/logs/tool_errors.log".
func DefaultErrorsPath(dataDir string) string {
	return filepath.Join(dataDir, "logs", "tool_errors.log")
}
