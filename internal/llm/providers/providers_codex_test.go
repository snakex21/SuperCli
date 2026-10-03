package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func saveCodexLoginFixture(t *testing.T, dir, label, token string) {
	t.Helper()
	if err := codexauth.Save(codexauth.AuthFilePathFor(dir, label), &codexauth.AuthFile{LastRefresh: time.Now(), Tokens: &codexauth.TokenData{AccessToken: token, AccountID: "fixture-" + label}}); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureCodexProviderRequiresSavedLoginAndDoesNotSelectModel(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	if name, err := m.EnsureCodexProvider(); name != "" || err != nil || len(m.Configured()) != 0 {
		t.Fatalf("created a provider without login: %q/%v", name, err)
	}
	saveCodexLoginFixture(t, dir, "named-fixture", "fixture-token")
	m.SetCodexAuthOptions(codexauth.Options{BackendURL: "https://fixture.invalid/codex", Issuer: "https://issuer.invalid", ClientID: "fixture-client"})
	name, err := m.EnsureCodexProvider()
	if err != nil {
		t.Fatal(err)
	}
	entries := m.Configured()
	if name != "codex" || len(entries) != 1 || entries[0].Model != "" || entries[0].BaseURL != "https://fixture.invalid/codex" || entries[0].Type != config.ProviderCodex {
		t.Fatalf("wrong generated provider: %+v", entries)
	}
	if second, err := m.EnsureCodexProvider(); err != nil || second != name || len(m.Configured()) != 1 {
		t.Fatal("ensure duplicated the entry")
	}
}

func TestEnsureCodexProviderPreservesDisabledAndCollidingEntries(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "collision", true: "disabled"}[disabled], func(t *testing.T) {
			dir := t.TempDir()
			saveCodexLoginFixture(t, dir, "named-fixture", "fixture-token")
			m := NewManager(dir)
			typ := config.ProviderOpenAI
			if disabled {
				typ = config.ProviderCodex
			}
			if err := m.Add("codex", typ, "https://existing.invalid", "existing-fixture-key", "explicit-fixture-model"); err != nil {
				t.Fatal(err)
			}
			if disabled {
				if err := m.SetDisabled("codex", true); err != nil {
					t.Fatal(err)
				}
			}
			name, err := m.EnsureCodexProvider()
			if err != nil {
				t.Fatal(err)
			}
			entries := m.Configured()
			if entries[0].BaseURL != "https://existing.invalid" || entries[0].Model != "explicit-fixture-model" || entries[0].APIKey != "existing-fixture-key" {
				t.Fatal("existing provider overwritten")
			}
			if disabled {
				if name != "codex" || len(entries) != 1 || !entries[0].Disabled {
					t.Fatal("disabled provider reenabled")
				}
			} else if name != "codex-2" || len(entries) != 2 {
				t.Fatalf("collision mishandled: %s/%d", name, len(entries))
			}
		})
	}
}

func TestCodexScanUsesNamedAccountsRefreshesAndCachesMetadata(t *testing.T) {
	dir := t.TempDir()
	saveCodexLoginFixture(t, dir, "one-fixture", "token-one")
	saveCodexLoginFixture(t, dir, "two-fixture", "token-two")
	requests := 0
	version := "initial-fixture"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Errorf("wrong request path: %s", r.URL.Path)
		}
		if r.Header.Get("ChatGPT-Account-Id") == "" {
			t.Error("missing selected account")
		}
		if r.Header.Get("Authorization") == "Bearer replaced-fixture" {
			version = "newly-available-fixture"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []llm.CodexModel{{Slug: version, Visibility: "list", DisplayName: "Fixture", ContextWindow: 98765, InputModalities: []string{"text"}, SupportedReasoningLevels: []llm.CodexReasoningLevel{{Effort: "low"}, {Effort: "high"}}}}})
	}))
	defer srv.Close()
	m := NewManager(dir)
	m.SetCodexAuthOptions(codexauth.Options{BackendURL: srv.URL})
	if _, err := m.EnsureCodexProvider(); err != nil {
		t.Fatal(err)
	}
	caps := llm.NewCapabilityRegistry()
	res := m.ScanProvider("codex", caps)
	if res.Err != nil || len(res.Models) != 1 || res.Models[0] != "initial-fixture" || requests != 2 {
		t.Fatalf("initial scan: %+v, requests=%d", res, requests)
	}
	m.RefreshCodexModels(context.Background(), caps)
	if requests != 2 {
		t.Fatalf("fresh TTL should avoid new requests, got %d", requests)
	}
	version = "refreshed-fixture"
	res = m.ScanProvider("codex", caps)
	if res.Err != nil || res.Models[0] != "refreshed-fixture" || requests != 4 {
		t.Fatalf("explicit refresh: %+v/%d", res, requests)
	}
	saveCodexLoginFixture(t, dir, "one-fixture", "replaced-fixture")
	m.RefreshCodexModels(context.Background(), caps)
	if requests != 5 {
		t.Fatalf("replaced login must invalidate only its cache, got %d", requests)
	}
	fresh := NewManager(dir)
	fresh.Reload()
	fresh.SetCodexAuthOptions(m.CodexAuthOptions())
	if fresh.LoadCodexModels(caps) < 1 || requests != 5 {
		t.Fatal("offline load made a network request or lost models")
	}
	info, ok := caps.Get("newly-available-fixture")
	if !ok || info.ContextLength != 98765 || !info.VisionKnown || info.Vision || !info.ReasoningKnown || !info.Reasoning {
		t.Fatalf("metadata not retained on restart: %+v", info)
	}
	if caps.Provider("gpt-5.5") != "" {
		t.Fatal("static models guessed during discovery")
	}
}

func TestLoadCodexModelsDoesNotTreatStaticInventoryAsAccountEntitlement(t *testing.T) {
	dir := t.TempDir()
	saveCodexLoginFixture(t, dir, "named-fixture", "fixture-token")
	m := NewManager(dir)
	if err := m.Add("codex", config.ProviderCodex, "https://unused.invalid/codex", "", ""); err != nil {
		t.Fatal(err)
	}
	m.cacheProviderModels("codex", []string{"retired-fixture"})
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "retired-fixture", Provider: "codex", Source: llm.SourceCatalog})
	if n := m.LoadCodexModels(caps); n != 0 {
		t.Fatalf("unverified static models loaded: %d", n)
	}
	if models := m.ListConfigured(caps)[0].Models; len(models) != 0 {
		t.Fatalf("static row leaked into picker: %+v", models)
	}
}

func TestCodexAutomaticDiscoveryFailureIsBoundedAndExplicitScanRetries(t *testing.T) {
	dir := t.TempDir()
	saveCodexLoginFixture(t, dir, "named-fixture", "fixture-token")
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(http.StatusServiceUnavailable) }))
	defer srv.Close()
	m := NewManager(dir)
	m.SetCodexAuthOptions(codexauth.Options{BackendURL: srv.URL})
	if _, err := m.EnsureCodexProvider(); err != nil {
		t.Fatal(err)
	}
	caps := llm.NewCapabilityRegistry()
	m.RefreshCodexModels(context.Background(), caps)
	fresh := NewManager(dir)
	fresh.Reload()
	fresh.SetCodexAuthOptions(m.CodexAuthOptions())
	fresh.RefreshCodexModels(context.Background(), caps)
	if requests != 1 {
		t.Fatalf("failed automatic discovery repeated across picker managers: %d", requests)
	}
	if res := fresh.ScanProvider("codex", caps); res.Err == nil || requests != 2 {
		t.Fatalf("explicit scan did not retry: %v/%d", res.Err, requests)
	}
	if len(caps.All()) != 0 {
		t.Fatal("failed catalog fabricated models")
	}
}
