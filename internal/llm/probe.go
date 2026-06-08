package llm

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// probeTimeout caps a single probe round trip.
// 30s matches the F10 ctx_execute hard cap; most
// providers respond in under 5s for an 8-token
// reply.
const probeTimeout = 30 * time.Second

// probeMaxBody caps the response body. A real
// 8-token reply is well under 1 KB; 1 MB is
// generous and protects us from a misbehaving
// server streaming an unbounded body.
const probeMaxBody = 1 << 20

// probeCacheTTL is how long a probe result is
// considered fresh. 7 days matches the F16 design
// (long enough that re-running the same day does
// not re-probe, short enough that provider changes
// surface within a week).
const probeCacheTTL = 7 * 24 * time.Hour

// probeCacheSchema is the model_probe_cache table.
// model_id is the primary key. The probed_at
// column is Unix nanoseconds, which is the
// monotonic-friendly choice on every Go platform
// (Windows included — the representable range
// starts at 1677-09-21, well before any modern
// model release).
const probeCacheSchema = `CREATE TABLE IF NOT EXISTS model_probe_cache (
    model_id     TEXT PRIMARY KEY,
    reasoning    INTEGER NOT NULL DEFAULT 0,
    vision       INTEGER NOT NULL DEFAULT 0,
    latency_ms   INTEGER NOT NULL DEFAULT 0,
    probed_at    INTEGER NOT NULL,
    error        TEXT NOT NULL DEFAULT ''
)`

// ProbeResult is the outcome of probing a single
// model. Error is empty on success.
//
// Vision is reserved for future probes (the
// current probe only checks reasoning via
// usage.reasoning_tokens). LatencyMS is the wall
// time of the HTTP round trip; even an error
// response records its latency, which helps the
// operator decide whether a model is dead or just
// slow.
type ProbeResult struct {
	Reasoning bool
	Vision    bool
	LatencyMS int64
	ProbedAt  time.Time
	Error     string
}

// Fresh reports whether the probe result is still
// within the TTL window at the given moment. The
// caller passes time.Now() in production; tests
// pin a specific moment.
func (r ProbeResult) Fresh(now time.Time) bool {
	return now.Sub(r.ProbedAt) <= probeCacheTTL
}

// Empty reports whether the result carries no
// information. A zero ProbeResult is what LoadProbeCache
// returns for an unknown model — callers can use
// this to skip the cache write on first probe.
func (r ProbeResult) Empty() bool {
	return r.ProbedAt.IsZero()
}

// Probe sends a minimal chat completion request
// to <baseURL>/chat/completions with
// reasoning_effort="low" and reads
// usage.reasoning_tokens from the response. A
// non-zero count means the model exposes a
// reasoning stream.
//
// The Go error is returned only for argument
// validation failures (empty baseURL or model).
// Network errors, non-2xx, and parse errors are
// recorded in ProbeResult.Error so the caller
// can cache the failure and surface it via
// --model-info. Latency is recorded on every
// response — success or failure — so a slow
// server shows up in the cache.
func Probe(ctx context.Context, baseURL, apiKey, model string) (ProbeResult, error) {
	if baseURL == "" {
		return ProbeResult{}, fmt.Errorf("llm: Probe: baseURL is empty")
	}
	if model == "" {
		return ProbeResult{}, fmt.Errorf("llm: Probe: model is empty")
	}
	start := time.Now()
	u := strings.TrimRight(baseURL, "/") + "/chat/completions"
	payload := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "user", "content": "Reply with the single token OK."},
		},
		"max_tokens":       8,
		"reasoning_effort": "low",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ProbeResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return ProbeResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := CleanAPIKey(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return ProbeResult{
			ProbedAt:  time.Now(),
			LatencyMS: time.Since(start).Milliseconds(),
			Error:     err.Error(),
		}, nil
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, probeMaxBody))
	if err != nil {
		return ProbeResult{
			ProbedAt:  time.Now(),
			LatencyMS: time.Since(start).Milliseconds(),
			Error:     err.Error(),
		}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProbeResult{
			ProbedAt:  time.Now(),
			LatencyMS: time.Since(start).Milliseconds(),
			Error:     fmt.Sprintf("status %d: %s", resp.StatusCode, raw),
		}, nil
	}
	var parsed struct {
		Usage struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ProbeResult{
			ProbedAt:  time.Now(),
			LatencyMS: time.Since(start).Milliseconds(),
			Error:     fmt.Sprintf("parse: %v", err),
		}, nil
	}
	return ProbeResult{
		Reasoning: parsed.Usage.ReasoningTokens > 0,
		LatencyMS: time.Since(start).Milliseconds(),
		ProbedAt:  time.Now(),
	}, nil
}

// LoadProbeCache reads every cached probe result.
// The map is keyed by model id. The caller
// filters by ProbeResult.Fresh when deciding
// whether a cached entry is still trustworthy.
//
// The cache table is created on demand. This is
// idempotent (CREATE TABLE IF NOT EXISTS) and
// avoids a separate Migrate step in main.go.
func LoadProbeCache(db *sql.DB) (map[string]ProbeResult, error) {
	if _, err := db.Exec(probeCacheSchema); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT model_id, reasoning, vision, latency_ms, probed_at, error FROM model_probe_cache`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]ProbeResult)
	for rows.Next() {
		var (
			id       string
			r        ProbeResult
			probedAt int64
		)
		if err := rows.Scan(&id, &r.Reasoning, &r.Vision, &r.LatencyMS, &probedAt, &r.Error); err != nil {
			return nil, err
		}
		r.ProbedAt = time.Unix(0, probedAt)
		out[id] = r
	}
	return out, rows.Err()
}

// SaveProbeCache writes a single probe result.
// The table is created on demand. Existing rows
// are overwritten via ON CONFLICT(model_id) DO
// UPDATE, so a re-probe supersedes its own stale
// entry.
func SaveProbeCache(db *sql.DB, modelID string, r ProbeResult) error {
	if _, err := db.Exec(probeCacheSchema); err != nil {
		return err
	}
	_, err := db.Exec(`
INSERT INTO model_probe_cache (model_id, reasoning, vision, latency_ms, probed_at, error)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(model_id) DO UPDATE SET
    reasoning  = excluded.reasoning,
    vision     = excluded.vision,
    latency_ms = excluded.latency_ms,
    probed_at  = excluded.probed_at,
    error      = excluded.error
`, modelID, r.Reasoning, r.Vision, r.LatencyMS, r.ProbedAt.UnixNano(), r.Error)
	return err
}
