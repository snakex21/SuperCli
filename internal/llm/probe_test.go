package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newProbeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestProbe_ReasoningPositive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-x",
			"object":  "chat.completion",
			"created": 0,
			"model":   "o1",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "OK"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6, "reasoning_tokens": 5},
		})
	}))
	defer srv.Close()
	got, err := Probe(context.Background(), srv.URL, "k", "o1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Reasoning {
		t.Errorf("Reasoning = false, want true (reasoning_tokens=5)")
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want empty", got.Error)
	}
	if got.LatencyMS < 0 {
		t.Errorf("LatencyMS = %d, want >= 0", got.LatencyMS)
	}
}

func TestProbe_ReasoningNegative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gpt-4o",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "OK"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6, "reasoning_tokens": 0},
		})
	}))
	defer srv.Close()
	got, err := Probe(context.Background(), srv.URL, "k", "gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if got.Reasoning {
		t.Errorf("Reasoning = true, want false (reasoning_tokens=0)")
	}
}

func TestProbe_NetworkError(t *testing.T) {
	// Use a server we close immediately so the
	// next request fails fast.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	got, err := Probe(context.Background(), srv.URL, "k", "gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if got.Error == "" {
		t.Errorf("Error = empty, want some network error text")
	}
}

func TestProbe_AuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer SECRET" {
			t.Errorf("auth = %q, want Bearer SECRET", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"usage": map[string]any{"reasoning_tokens": 0},
		})
	}))
	defer srv.Close()
	if _, err := Probe(context.Background(), srv.URL, "SECRET", "m"); err != nil {
		t.Fatal(err)
	}
}

func TestProbe_EmptyArgs(t *testing.T) {
	if _, err := Probe(context.Background(), "", "k", "m"); err == nil {
		t.Error("empty baseURL should error")
	}
	if _, err := Probe(context.Background(), "https://x", "k", ""); err == nil {
		t.Error("empty model should error")
	}
}

func TestProbeCache_SaveLoad(t *testing.T) {
	db := newProbeTestDB(t)
	now := time.Now()
	in := ProbeResult{Reasoning: true, Vision: false, LatencyMS: 42, ProbedAt: now, Error: ""}
	if err := SaveProbeCache(db, "o1", in); err != nil {
		t.Fatal(err)
	}
	got, err := LoadProbeCache(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	r, ok := got["o1"]
	if !ok {
		t.Fatal("missing o1 entry")
	}
	if r.Reasoning != in.Reasoning {
		t.Errorf("Reasoning = %v, want %v", r.Reasoning, in.Reasoning)
	}
	if r.LatencyMS != in.LatencyMS {
		t.Errorf("LatencyMS = %d, want %d", r.LatencyMS, in.LatencyMS)
	}
	if r.Error != in.Error {
		t.Errorf("Error = %q, want %q", r.Error, in.Error)
	}
	if !r.ProbedAt.Equal(in.ProbedAt) {
		t.Errorf("ProbedAt = %v, want %v", r.ProbedAt, in.ProbedAt)
	}
}

func TestProbeCache_Overwrite(t *testing.T) {
	db := newProbeTestDB(t)
	t0 := time.Now()
	if err := SaveProbeCache(db, "x", ProbeResult{Reasoning: false, ProbedAt: t0}); err != nil {
		t.Fatal(err)
	}
	t1 := t0.Add(time.Minute)
	if err := SaveProbeCache(db, "x", ProbeResult{Reasoning: true, ProbedAt: t1}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadProbeCache(db)
	if err != nil {
		t.Fatal(err)
	}
	r := got["x"]
	if !r.Reasoning {
		t.Error("second save should overwrite; got Reasoning=false")
	}
	if !r.ProbedAt.Equal(t1) {
		t.Errorf("ProbedAt = %v, want %v", r.ProbedAt, t1)
	}
}

func TestProbeCache_FreshnessAndEmpty(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		r     ProbeResult
		now   time.Time
		want  bool
	}{
		{"fresh_now", ProbeResult{ProbedAt: now}, now, true},
		{"fresh_6d", ProbeResult{ProbedAt: now.Add(-6 * 24 * time.Hour)}, now, true},
		{"expired_8d", ProbeResult{ProbedAt: now.Add(-8 * 24 * time.Hour)}, now, false},
		{"empty_zero_time", ProbeResult{}, now, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.r.Fresh(c.now); got != c.want {
				t.Errorf("Fresh = %v, want %v", got, c.want)
			}
		})
	}
}

func TestProbeCache_ConcurrentSafe(t *testing.T) {
	db := newProbeTestDB(t)
	const n = 50
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := SaveProbeCache(db, "x", ProbeResult{ProbedAt: time.Now()}); err != nil {
				t.Errorf("Save: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := LoadProbeCache(db); err != nil {
				t.Errorf("Load: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}

func TestProbe_400Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad model", http.StatusBadRequest)
	}))
	defer srv.Close()
	got, err := Probe(context.Background(), srv.URL, "k", "bogus")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Error, "400") {
		t.Errorf("Error = %q, want substring 400", got.Error)
	}
}
