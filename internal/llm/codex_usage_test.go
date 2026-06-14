package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUsageEndpointURL(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		want    string
	}{
		{
			name:    "chatgpt backend uses wham path",
			backend: "https://chatgpt.com/backend-api/codex",
			want:    "https://chatgpt.com/backend-api/codex/wham/usage",
		},
		{
			name:    "trailing slash trimmed",
			backend: "https://chatgpt.com/backend-api/codex/",
			want:    "https://chatgpt.com/backend-api/codex/wham/usage",
		},
		{
			name:    "non-backend-api base uses codex api path",
			backend: "https://example.com/v1",
			want:    "https://example.com/v1/api/codex/usage",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := usageEndpointURL(tt.backend); got != tt.want {
				t.Errorf("usageEndpointURL(%q) = %q, want %q", tt.backend, got, tt.want)
			}
		})
	}
}

func TestParseCodexUsageBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want CodexRateLimits
	}{
		{
			name: "both windows",
			body: `{"plan_type":"pro","rate_limit":{"allowed":true,"limit_reached":false,
				"primary_window":{"used_percent":3,"limit_window_seconds":18000,"reset_after_seconds":1234,"reset_at":1700000000},
				"secondary_window":{"used_percent":11,"limit_window_seconds":604800,"reset_after_seconds":5678,"reset_at":1700500000}}}`,
			want: CodexRateLimits{
				PrimaryUsedPct: 3, PrimaryWindowMin: 300,
				PrimaryResetAfter: 1234, PrimaryResetAt: 1700000000,
				SecondaryUsedPct: 11, SecondaryWindowMin: 10080,
				SecondaryResetAfter: 5678, SecondaryResetAt: 1700500000,
				OK: true,
			},
		},
		{
			name: "float percent floored and clamped",
			body: `{"rate_limit":{"primary_window":{"used_percent":42.9,"limit_window_seconds":18000,"reset_at":0,"reset_after_seconds":0}}}`,
			want: CodexRateLimits{
				PrimaryUsedPct: 42, PrimaryWindowMin: 300, OK: true,
			},
		},
		{
			name: "missing rate_limit -> not ok",
			body: `{"plan_type":"pro"}`,
			want: CodexRateLimits{OK: false},
		},
		{
			name: "empty rate_limit object -> not ok",
			body: `{"rate_limit":{}}`,
			want: CodexRateLimits{OK: false},
		},
		{
			name: "garbage json -> not ok",
			body: `not json`,
			want: CodexRateLimits{OK: false},
		},
		{
			name: "empty body -> not ok",
			body: ``,
			want: CodexRateLimits{OK: false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCodexUsageBody([]byte(tt.body))
			if got != tt.want {
				t.Errorf("parseCodexUsageBody()\n got = %+v\nwant = %+v", got, tt.want)
			}
		})
	}
}

// TestFetchUsageStoresSnapshot drives FetchUsage against a stub usage
// endpoint and confirms the parsed body is stored on the provider.
func TestFetchUsageStoresSnapshot(t *testing.T) {
	var gotPath, gotAuth, gotAccount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("chatgpt-account-id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":7,"limit_window_seconds":18000,"reset_at":0,"reset_after_seconds":0},` +
			`"secondary_window":{"used_percent":22,"limit_window_seconds":604800,"reset_at":0,"reset_after_seconds":0}}}`))
	}))
	defer srv.Close()

	p, err := NewCodex(CodexConfig{BackendURL: srv.URL, Model: "gpt-5-codex", Tokens: &fakeTokens{access: "tok", accountID: "acc-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.RateLimits(); ok {
		t.Fatal("expected no snapshot before fetch")
	}

	rl, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if rl.PrimaryUsedPct != 7 || rl.SecondaryUsedPct != 22 {
		t.Errorf("unexpected snapshot: %+v", rl)
	}
	if gotPath != "/api/codex/usage" {
		// srv.URL has no /backend-api, so the codex-api path applies.
		t.Errorf("request path = %q, want /api/codex/usage", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotAccount != "acc-1" {
		t.Errorf("chatgpt-account-id = %q", gotAccount)
	}

	stored, ok := p.RateLimits()
	if !ok || stored != rl {
		t.Errorf("snapshot not stored: ok=%v stored=%+v", ok, stored)
	}
}

// TestFetchUsageRefreshesOn401 confirms a 401 triggers a single token
// refresh and retry.
func TestFetchUsageRefreshesOn401(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":5,"limit_window_seconds":18000,"reset_at":0,"reset_after_seconds":0}}}`))
	}))
	defer srv.Close()

	ft := &fakeTokens{access: "tok"}
	p, err := NewCodex(CodexConfig{BackendURL: srv.URL, Model: "gpt-5-codex", Tokens: ft})
	if err != nil {
		t.Fatal(err)
	}
	rl, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if rl.PrimaryUsedPct != 5 {
		t.Errorf("unexpected snapshot: %+v", rl)
	}
	if ft.refreshed != 1 {
		t.Errorf("expected exactly one refresh, got %d", ft.refreshed)
	}
}

// TestFetchUsageEmptyBodyErrors confirms a 200 with no usable limits
// returns an error and leaves the prior snapshot untouched (no panic,
// graceful degradation).
func TestFetchUsageEmptyBodyErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"plan_type":"pro"}`))
	}))
	defer srv.Close()

	p, err := NewCodex(CodexConfig{BackendURL: srv.URL, Model: "gpt-5-codex", Tokens: &fakeTokens{access: "tok"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FetchUsage(context.Background()); err == nil {
		t.Fatal("expected error for body with no usable limits")
	}
	if _, ok := p.RateLimits(); ok {
		t.Error("expected no snapshot stored after empty body")
	}
}
