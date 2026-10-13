package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// VisitUsageHistory streams every persisted model call created at or after
// since, oldest first, through the session_usage created-at index. Unlike
// ReadUsage it spans all sessions and loads only the columns needed by the
// usage-history charts (tokens, timing, identity and the frozen price
// snapshot for cost). The callback runs with rows open and must not query
// Store.
func (s *Store) VisitUsageHistory(ctx context.Context, since time.Time, visit func(UsageRecord)) error {
	rows, err := s.db.QueryContext(ctx, `SELECT
		session_id, call_seq, provider, provider_type, endpoint_host, model,
		input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
		has_cached_input, has_reasoning, ttft_ms, duration_ms, has_timing,
		source, created_at,
		(SELECT price_snapshot FROM billing_usage b
		 WHERE b.session_id = session_usage.session_id AND b.call_seq = session_usage.call_seq)
		FROM session_usage WHERE created_at >= ? ORDER BY created_at, id`, since.UnixNano())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var u UsageRecord
		var cachedKnown, reasoningKnown, timingKnown int
		var created int64
		var priceJSON sql.NullString
		if err := rows.Scan(&u.SessionID, &u.CallSeq, &u.Provider, &u.ProviderType, &u.EndpointHost, &u.Model,
			&u.Input, &u.Output, &u.CachedInput, &u.Reasoning,
			&cachedKnown, &reasoningKnown, &u.TTFTMS, &u.DurationMS, &timingKnown,
			&u.Source, &created, &priceJSON); err != nil {
			return err
		}
		u.HasCachedInput = cachedKnown != 0
		u.HasReasoning = reasoningKnown != 0
		u.HasTiming = timingKnown != 0
		u.CreatedAt = time.Unix(0, created).UTC()
		if u.PriceSnapshot, err = decodePriceSnapshot(priceJSON); err != nil {
			return err
		}
		if visit != nil {
			visit(u)
		}
	}
	return rows.Err()
}

// VisitTurnPhases streams only the creation time and decoded phase timings
// (microseconds, keyed like "tool:<name>" or "model") of turns created at or
// after since. It reads two columns through the session_turns created-at
// index instead of the full summary row.
func (s *Store) VisitTurnPhases(ctx context.Context, since time.Time, visit func(createdAt time.Time, phases map[string]int64)) error {
	rows, err := s.db.QueryContext(ctx, `SELECT created_at, phases_json FROM session_turns
		WHERE created_at >= ? AND phases_json LIKE '%"tool:%' ORDER BY created_at`, since.UnixNano())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var created int64
		var raw string
		if err := rows.Scan(&created, &raw); err != nil {
			return err
		}
		if raw == "" || raw == "{}" || raw == "null" {
			continue
		}
		var phases map[string]int64
		if json.Unmarshal([]byte(raw), &phases) != nil || len(phases) == 0 {
			continue
		}
		if visit != nil {
			visit(time.Unix(0, created).UTC(), phases)
		}
	}
	return rows.Err()
}

// UsageHistoryDay is one local calendar day of persisted model calls.
type UsageHistoryDay struct {
	Day         string // YYYY-MM-DD in the requested location
	Calls       int
	Input       int64
	CachedInput int64
	Output      int64
	Reasoning   int64
	TTFTSumMS   int64
	TTFTCalls   int
	DurationMS  int64
	ToolUS      int64
}

// Total returns input+output tokens.
func (d UsageHistoryDay) Total() int64 { return d.Input + d.Output }

// ToolTime is the cumulative wall time spent in one tool.
type ToolTime struct {
	Name string
	US   int64
}

// UsageHistory is a contiguous daily series plus all-tools wall time.
type UsageHistory struct {
	Days  []UsageHistoryDay // oldest first, one entry per calendar day
	Tools []ToolTime        // all tools, longest first
}

// HistoryWindowStart returns local midnight of the first day of a days-long
// window ending today (in loc).
func HistoryWindowStart(now time.Time, days int, loc *time.Location) time.Time {
	if days < 1 {
		days = 1
	}
	if loc == nil {
		loc = time.Local
	}
	n := now.In(loc)
	return time.Date(n.Year(), n.Month(), n.Day()-(days-1), 0, 0, 0, 0, loc)
}

// HistoryDayKeys returns the ordered YYYY-MM-DD keys of the window.
func HistoryDayKeys(start time.Time, days int) []string {
	keys := make([]string, 0, days)
	for i := 0; i < days; i++ {
		d := time.Date(start.Year(), start.Month(), start.Day()+i, 0, 0, 0, 0, start.Location())
		keys = append(keys, d.Format("2006-01-02"))
	}
	return keys
}

// ToolPhaseName returns the tool name of a "tool:<name>" phase key.
func ToolPhaseName(key string) (string, bool) {
	if !strings.HasPrefix(key, "tool:") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(key, "tool:"))
	return name, name != ""
}

// SortToolTimes orders tool totals longest first, then by name.
func SortToolTimes(totals map[string]int64) []ToolTime {
	out := make([]ToolTime, 0, len(totals))
	for name, us := range totals {
		if us > 0 {
			out = append(out, ToolTime{Name: name, US: us})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].US != out[j].US {
			return out[i].US > out[j].US
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ReadUsageHistory aggregates the last days calendar days (local to loc,
// including today) of model calls and tool phase time. It is a streaming,
// index-bounded aggregation used by the TUI usage menu; the web GUI adds
// per-model and cost breakdowns on top of the same visitors.
func (s *Store) ReadUsageHistory(ctx context.Context, now time.Time, days int, loc *time.Location) (UsageHistory, error) {
	if days < 1 {
		days = 1
	}
	if loc == nil {
		loc = time.Local
	}
	start := HistoryWindowStart(now, days, loc)
	keys := HistoryDayKeys(start, days)
	out := UsageHistory{Days: make([]UsageHistoryDay, days)}
	index := make(map[string]int, days)
	for i, k := range keys {
		out.Days[i].Day = k
		index[k] = i
	}
	err := s.VisitUsageHistory(ctx, start, func(u UsageRecord) {
		i, ok := index[u.CreatedAt.In(loc).Format("2006-01-02")]
		if !ok {
			return
		}
		d := &out.Days[i]
		d.Calls++
		d.Input += u.Input
		d.CachedInput += u.CachedInput
		d.Output += u.Output
		d.Reasoning += u.Reasoning
		if u.HasTiming {
			d.DurationMS += u.DurationMS
			if u.TTFTMS > 0 {
				d.TTFTSumMS += u.TTFTMS
				d.TTFTCalls++
			}
		}
	})
	if err != nil {
		return UsageHistory{}, err
	}
	tools := map[string]int64{}
	err = s.VisitTurnPhases(ctx, start, func(at time.Time, phases map[string]int64) {
		i, ok := index[at.In(loc).Format("2006-01-02")]
		for key, us := range phases {
			name, isTool := ToolPhaseName(key)
			if !isTool || us <= 0 {
				continue
			}
			tools[name] += us
			if ok {
				out.Days[i].ToolUS += us
			}
		}
	})
	if err != nil {
		return UsageHistory{}, err
	}
	out.Tools = SortToolTimes(tools)
	return out, nil
}
