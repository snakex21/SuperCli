package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func hdr(pairs map[string]string) http.Header {
	h := http.Header{}
	for k, v := range pairs {
		h.Set(k, v)
	}
	return h
}

func TestParseCodexRateLimits(t *testing.T) {
	tests := []struct {
		name string
		h    http.Header
		want CodexRateLimits
	}{
		{
			name: "full set",
			h: hdr(map[string]string{
				"X-Codex-Primary-Used-Percent":         "1",
				"X-Codex-Primary-Window-Minutes":       "300",
				"X-Codex-Primary-Reset-At":             "1700000000",
				"X-Codex-Primary-Reset-After-Seconds":  "16320",
				"X-Codex-Secondary-Used-Percent":       "11",
				"X-Codex-Secondary-Window-Minutes":     "10080",
				"X-Codex-Secondary-Reset-At":           "1700500000",
				"X-Codex-Secondary-Reset-After-Seconds": "200000",
			}),
			want: CodexRateLimits{
				PrimaryUsedPct: 1, PrimaryWindowMin: 300,
				PrimaryResetAt: 1700000000, PrimaryResetAfter: 16320,
				SecondaryUsedPct: 11, SecondaryWindowMin: 10080,
				SecondaryResetAt: 1700500000, SecondaryResetAfter: 200000,
				OK: true,
			},
		},
		{
			name: "no headers => not OK",
			h:    http.Header{},
			want: CodexRateLimits{OK: false},
		},
		{
			name: "partial: only primary percent",
			h: hdr(map[string]string{
				"X-Codex-Primary-Used-Percent": "42",
			}),
			want: CodexRateLimits{PrimaryUsedPct: 42, OK: true},
		},
		{
			name: "empty percent values => not OK",
			h: hdr(map[string]string{
				"X-Codex-Primary-Used-Percent":   "",
				"X-Codex-Secondary-Used-Percent": "",
			}),
			want: CodexRateLimits{OK: false},
		},
		{
			name: "float percent rounds down",
			h: hdr(map[string]string{
				"X-Codex-Primary-Used-Percent":   "1.9",
				"X-Codex-Secondary-Used-Percent": "11.4",
			}),
			want: CodexRateLimits{PrimaryUsedPct: 1, SecondaryUsedPct: 11, OK: true},
		},
		{
			name: "garbage percent ignored, garbage window => 0, no panic",
			h: hdr(map[string]string{
				"X-Codex-Primary-Used-Percent":     "abc",
				"X-Codex-Primary-Window-Minutes":   "xyz",
				"X-Codex-Secondary-Used-Percent":   "11",
				"X-Codex-Secondary-Window-Minutes": "10080",
			}),
			want: CodexRateLimits{
				PrimaryUsedPct: 0, PrimaryWindowMin: 0,
				SecondaryUsedPct: 11, SecondaryWindowMin: 10080,
				OK: true,
			},
		},
		{
			name: "percent clamps to 0..100",
			h: hdr(map[string]string{
				"X-Codex-Primary-Used-Percent":   "150",
				"X-Codex-Secondary-Used-Percent": "-5",
			}),
			want: CodexRateLimits{PrimaryUsedPct: 100, SecondaryUsedPct: 0, OK: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCodexRateLimits(tt.h)
			if got != tt.want {
				t.Errorf("parseCodexRateLimits()\n got = %+v\nwant = %+v", got, tt.want)
			}
		})
	}
}

func TestCodexRateLimitsFormatHUD(t *testing.T) {
	tests := []struct {
		name string
		rl   CodexRateLimits
		want string
	}{
		{
			name: "not OK => empty (tile hidden)",
			rl:   CodexRateLimits{OK: false},
			want: "",
		},
		{
			name: "two percentages with known windows",
			rl: CodexRateLimits{
				PrimaryUsedPct: 1, PrimaryWindowMin: 300,
				SecondaryUsedPct: 11, SecondaryWindowMin: 10080,
				OK: true,
			},
			want: "5h 1% · 7d 11%",
		},
		{
			name: "unknown windows fall back to 5h/7d labels",
			rl: CodexRateLimits{
				PrimaryUsedPct: 2, SecondaryUsedPct: 33, OK: true,
			},
			want: "5h 2% · 7d 33%",
		},
		{
			name: "reset-after appended to primary",
			rl: CodexRateLimits{
				PrimaryUsedPct: 1, PrimaryWindowMin: 300, PrimaryResetAfter: 16320,
				SecondaryUsedPct: 11, SecondaryWindowMin: 10080,
				OK: true,
			},
			want: "5h 1% (4h32m) · 7d 11%",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rl.FormatHUD(); got != tt.want {
				t.Errorf("FormatHUD() = %q, want %q", got, tt.want)
			}
		})
	}
}

// fixedNow is the reference clock used by the reset-aware tests below.
// 1700000000 = 2023-11-14T22:13:20Z. Reset-at values are chosen
// relative to it so "before"/"after" reset are deterministic.
var fixedNow = time.Unix(1700000000, 0)

func TestEffectiveUsedPct(t *testing.T) {
	now := fixedNow
	tests := []struct {
		name      string
		usedPct   int
		resetAt   int64
		wantPct   int
		wantReset bool
	}{
		{
			name:    "before reset => unchanged",
			usedPct: 42, resetAt: now.Unix() + 3600,
			wantPct: 42, wantReset: false,
		},
		{
			name:    "after reset => ~0%",
			usedPct: 42, resetAt: now.Unix() - 3600,
			wantPct: 0, wantReset: true,
		},
		{
			name:    "exactly at reset => reset (now >= resetAt)",
			usedPct: 42, resetAt: now.Unix(),
			wantPct: 0, wantReset: true,
		},
		{
			name:    "zero resetAt => never resets",
			usedPct: 42, resetAt: 0,
			wantPct: 42, wantReset: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pct, reset := effectiveUsedPct(tt.usedPct, tt.resetAt, now)
			if pct != tt.wantPct || reset != tt.wantReset {
				t.Errorf("effectiveUsedPct(%d,%d) = (%d,%v), want (%d,%v)",
					tt.usedPct, tt.resetAt, pct, reset, tt.wantPct, tt.wantReset)
			}
		})
	}
}

// TestFormatHUDAtResetAware exercises the reset-aware tile rendering
// with a fixed clock, covering each window independently.
func TestFormatHUDAtResetAware(t *testing.T) {
	now := fixedNow
	tests := []struct {
		name string
		rl   CodexRateLimits
		want string
	}{
		{
			name: "both fresh (resets in the future) => no zeroing",
			rl: CodexRateLimits{
				PrimaryUsedPct: 5, PrimaryWindowMin: 300, PrimaryResetAt: now.Unix() + 3600,
				SecondaryUsedPct: 11, SecondaryWindowMin: 10080, SecondaryResetAt: now.Unix() + 100000,
				OK: true,
			},
			want: "5h 5% (1h0m) · 7d 11%",
		},
		{
			name: "primary (5h) reset, secondary fresh",
			rl: CodexRateLimits{
				PrimaryUsedPct: 80, PrimaryWindowMin: 300, PrimaryResetAt: now.Unix() - 1000,
				SecondaryUsedPct: 11, SecondaryWindowMin: 10080, SecondaryResetAt: now.Unix() + 100000,
				OK: true,
			},
			// next primary reset ≈ (now-1000) + 300*60 = now+17000 = 4h43m20s.
			want: "5h ~0% (4h43m) · 7d 11%",
		},
		{
			name: "secondary (weekly) reset, primary fresh",
			rl: CodexRateLimits{
				PrimaryUsedPct: 5, PrimaryWindowMin: 300, PrimaryResetAt: now.Unix() + 3600,
				SecondaryUsedPct: 33, SecondaryWindowMin: 10080, SecondaryResetAt: now.Unix() - 1000,
				OK: true,
			},
			want: "5h 5% (1h0m) · 7d ~0%",
		},
		{
			name: "both reset",
			rl: CodexRateLimits{
				PrimaryUsedPct: 80, PrimaryWindowMin: 60, PrimaryResetAt: now.Unix() - 1000,
				SecondaryUsedPct: 33, SecondaryWindowMin: 10080, SecondaryResetAt: now.Unix() - 1000,
				OK: true,
			},
			// next primary reset ≈ (now-1000) + 60*60 = now+2600 = 43m20s.
			want: "1h ~0% (43m) · 7d ~0%",
		},
		{
			name: "primary reset but window unknown => no time annotation, no negative",
			rl: CodexRateLimits{
				PrimaryUsedPct: 80, PrimaryWindowMin: 0, PrimaryResetAt: now.Unix() - 1000,
				SecondaryUsedPct: 11, SecondaryWindowMin: 10080, SecondaryResetAt: now.Unix() + 100000,
				OK: true,
			},
			want: "5h ~0% · 7d 11%",
		},
		{
			name: "mixed labels and percents, one reset",
			rl: CodexRateLimits{
				PrimaryUsedPct: 80, PrimaryWindowMin: 300, PrimaryResetAt: now.Unix() - 1000,
				SecondaryUsedPct: 11, SecondaryWindowMin: 10080,
				OK: true,
			},
			want: "5h ~0% (4h43m) · 7d 11%",
		},
		{
			name: "no reset-at on either => behaves like today (no panic, no zeroing)",
			rl: CodexRateLimits{
				PrimaryUsedPct: 1, PrimaryWindowMin: 300,
				SecondaryUsedPct: 11, SecondaryWindowMin: 10080,
				OK: true,
			},
			want: "5h 1% · 7d 11%",
		},
		{
			name: "fresh snapshot with reset-after still annotates (unchanged behavior)",
			rl: CodexRateLimits{
				PrimaryUsedPct: 1, PrimaryWindowMin: 300, PrimaryResetAfter: 16320,
				SecondaryUsedPct: 11, SecondaryWindowMin: 10080,
				OK: true,
			},
			want: "5h 1% (4h32m) · 7d 11%",
		},
		{
			name: "not OK => empty",
			rl:   CodexRateLimits{OK: false},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rl.formatHUDAt(now); got != tt.want {
				t.Errorf("formatHUDAt() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShortDuration(t *testing.T) {
	cases := map[string]string{
		"45s":   shortDuration(45_000_000_000),       // 45s
		"12m":   shortDuration(12 * 60_000_000_000),  // 12m
		"4h32m": shortDuration((4*60 + 32) * 60_000_000_000),
	}
	for want, got := range cases {
		if got != want {
			t.Errorf("shortDuration => %q, want %q", got, want)
		}
	}
}

// TestCodexProviderCapturesRateLimitHeaders confirms the snapshot is
// populated from the 200 response headers and surfaced via RateLimits.
func TestCodexProviderCapturesRateLimitHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Codex-Primary-Used-Percent", "3")
		w.Header().Set("X-Codex-Primary-Window-Minutes", "300")
		w.Header().Set("X-Codex-Secondary-Used-Percent", "11")
		w.Header().Set("X-Codex-Secondary-Window-Minutes", "10080")
		codexSSE(w,
			`{"type":"response.output_text.delta","delta":"hi"}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		)
	}))
	defer srv.Close()

	p, err := NewCodex(CodexConfig{BackendURL: srv.URL, Model: "gpt-5-codex", Tokens: &fakeTokens{access: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.RateLimits(); ok {
		t.Fatal("expected no snapshot before first request")
	}

	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range ch { // drain the stream
	}

	rl, ok := p.RateLimits()
	if !ok {
		t.Fatal("expected rate-limit snapshot after request")
	}
	if rl.PrimaryUsedPct != 3 || rl.SecondaryUsedPct != 11 {
		t.Fatalf("unexpected snapshot: %+v", rl)
	}
	if got := rl.FormatHUD(); got != "5h 3% · 7d 11%" {
		t.Errorf("FormatHUD() = %q", got)
	}
}

// TestCodexRateLimitsSaveLoadRoundTrip confirms a saved snapshot is
// read back identically and stays scoped to its account.
func TestCodexRateLimitsSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := CodexRateLimits{
		PrimaryUsedPct: 3, PrimaryWindowMin: 300,
		PrimaryResetAt: 1700000000, PrimaryResetAfter: 16320,
		SecondaryUsedPct: 11, SecondaryWindowMin: 10080,
		OK: true,
	}
	if err := saveCodexRateLimits(dir, "acc-1", want); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Same account => loads.
	got, ok := loadCodexRateLimits(dir, "acc-1")
	if !ok {
		t.Fatal("expected snapshot to load for matching account")
	}
	if got != want {
		t.Errorf("round-trip mismatch\n got = %+v\nwant = %+v", got, want)
	}

	// Unscoped load (empty account) matches anything.
	if _, ok := loadCodexRateLimits(dir, ""); !ok {
		t.Error("expected unscoped load to succeed")
	}

	// Different account => discarded.
	if _, ok := loadCodexRateLimits(dir, "acc-2"); ok {
		t.Error("expected snapshot for a different account to be discarded")
	}
}

// TestClearCodexRateLimits confirms the snapshot file is removed and
// that clearing a missing file (or empty dataDir) is not an error.
func TestClearCodexRateLimits(t *testing.T) {
	dir := t.TempDir()
	if err := saveCodexRateLimits(dir, "acc", CodexRateLimits{PrimaryUsedPct: 5, OK: true}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, ok := loadCodexRateLimits(dir, "acc"); !ok {
		t.Fatal("precondition: snapshot should load")
	}
	if err := ClearCodexRateLimits(dir); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, ok := loadCodexRateLimits(dir, "acc"); ok {
		t.Error("expected snapshot to be gone after clear")
	}
	// Clearing again (now missing) is a no-op.
	if err := ClearCodexRateLimits(dir); err != nil {
		t.Errorf("clearing missing snapshot should be nil, got %v", err)
	}
	// Empty dataDir is a no-op.
	if err := ClearCodexRateLimits(""); err != nil {
		t.Errorf("empty dataDir should be nil, got %v", err)
	}
}

// TestCodexRateLimitsSaveSkipsNonOK confirms a non-OK snapshot is not
// written (nothing useful to persist) and no file is created.
func TestCodexRateLimitsSaveSkipsNonOK(t *testing.T) {
	dir := t.TempDir()
	if err := saveCodexRateLimits(dir, "acc", CodexRateLimits{OK: false}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, codexRateLimitsFileName)); !os.IsNotExist(err) {
		t.Errorf("expected no file for non-OK snapshot, stat err = %v", err)
	}
	if _, ok := loadCodexRateLimits(dir, "acc"); ok {
		t.Error("expected load to report not-ok when nothing was written")
	}
}

// TestCodexRateLimitsLoadMissingFile confirms a missing file is a
// graceful (ok=false) miss, not a panic or error.
func TestCodexRateLimitsLoadMissingFile(t *testing.T) {
	dir := t.TempDir() // empty; no snapshot file
	if _, ok := loadCodexRateLimits(dir, "acc"); ok {
		t.Error("expected ok=false for a missing snapshot file")
	}
}

// TestCodexRateLimitsLoadCorruptJSON confirms corrupt JSON is handled
// gracefully (ok=false, no panic).
func TestCodexRateLimitsLoadCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, codexRateLimitsFileName)
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	if _, ok := loadCodexRateLimits(dir, "acc"); ok {
		t.Error("expected ok=false for corrupt JSON")
	}
}

// TestCodexRateLimitsEmptyDataDir confirms persistence is a no-op when
// no data dir is configured (e.g. unit tests without disk wiring).
func TestCodexRateLimitsEmptyDataDir(t *testing.T) {
	if err := saveCodexRateLimits("", "acc", CodexRateLimits{PrimaryUsedPct: 5, OK: true}); err != nil {
		t.Errorf("save with empty dir should be a no-op, got %v", err)
	}
	if _, ok := loadCodexRateLimits("", "acc"); ok {
		t.Error("expected ok=false when no data dir is configured")
	}
}

// TestNewCodexSeedsFromDisk confirms NewCodex restores a persisted
// snapshot so RateLimits() returns it before any request is made.
func TestNewCodexSeedsFromDisk(t *testing.T) {
	dir := t.TempDir()
	saved := CodexRateLimits{PrimaryUsedPct: 7, SecondaryUsedPct: 22, OK: true}
	if err := saveCodexRateLimits(dir, "acc", saved); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, err := NewCodex(CodexConfig{Model: "gpt-5-codex", Tokens: &fakeTokens{access: "t"}, DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	rl, ok := p.RateLimits()
	if !ok {
		t.Fatal("expected seeded snapshot to be available immediately")
	}
	if rl.PrimaryUsedPct != 7 || rl.SecondaryUsedPct != 22 {
		t.Errorf("unexpected seeded snapshot: %+v", rl)
	}
}
