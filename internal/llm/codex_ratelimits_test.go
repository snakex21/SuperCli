package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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
