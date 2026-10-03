package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/buildinfo"
)

// All names and credentials in these fixtures are synthetic, not a catalog
// assertion about any real ChatGPT account.
func TestListCodexModelsUsesAccountAndPreservesMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/backend/models" || r.URL.Query().Get("client_version") != "1.2.3" {
			t.Errorf("unexpected discovery request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-token" || r.Header.Get("ChatGPT-Account-Id") != "fixture-account" {
			t.Error("discovery did not use the selected account")
		}
		fmt.Fprint(w, `{"models":[{"slug":"future-fixture","display_name":"Future Fixture","description":"fixture","visibility":"list","context_window":123456,"input_modalities":["text","image"],"default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low","description":"brief"},{"effort":"high"}]},{"slug":"hidden-fixture","visibility":"hide"},{"slug":"future-fixture","visibility":"list"},{"slug":"","visibility":"list"}]}`)
	}))
	defer srv.Close()
	models, err := ListCodexModels(context.Background(), CodexCatalogConfig{BackendURL: srv.URL + "/backend", Tokens: &fakeTokens{access: "fixture-token", accountID: "fixture-account"}, ClientVersion: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Slug != "future-fixture" || models[0].DisplayName != "Future Fixture" || models[0].ContextWindow != 123456 || models[0].DefaultReasoningLevel != "high" || len(models[0].SupportedReasoningLevels) != 2 || len(models[0].InputModalities) != 2 {
		t.Fatalf("metadata lost or fabricated: %+v", models)
	}
}

type rotatingCatalogTokens struct {
	calls, refreshes int
	rejected         bool
}

func (f *rotatingCatalogTokens) Token(context.Context) (string, string, error) {
	f.calls++
	if f.refreshes > 0 {
		return "new-fixture-token", "new-fixture-account", nil
	}
	return "old-fixture-token", "old-fixture-account", nil
}
func (f *rotatingCatalogTokens) Refresh(context.Context) (string, error) {
	f.refreshes++
	return "new-fixture-token", nil
}

func TestListCodexModelsRefreshesOnceAndReacquiresAccount(t *testing.T) {
	for _, rejectAgain := range []bool{false, true} {
		t.Run(fmt.Sprint(rejectAgain), func(t *testing.T) {
			tokens := &rotatingCatalogTokens{}
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 || rejectAgain {
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprint(w, `{"error":"fixture-secret-should-not-leak"}`)
					return
				}
				if r.Header.Get("Authorization") != "Bearer new-fixture-token" || r.Header.Get("ChatGPT-Account-Id") != "new-fixture-account" {
					t.Error("stale identity after refresh")
				}
				fmt.Fprint(w, `{"models":[]}`)
			}))
			defer srv.Close()
			models, err := ListCodexModels(context.Background(), CodexCatalogConfig{BackendURL: srv.URL, Tokens: tokens})
			if tokens.refreshes != 1 || requests != 2 || tokens.calls != 2 {
				t.Fatalf("refresh not bounded: %d/%d/%d", tokens.refreshes, requests, tokens.calls)
			}
			if rejectAgain {
				if err == nil || strings.Contains(err.Error(), "fixture-secret") {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err != nil || len(models) != 0 {
				t.Fatalf("empty catalog must be authoritative: models=%v err=%v", models, err)
			}
		})
	}
}

func TestListCodexModelsRejectsInvalidPayloadAndCanceledContext(t *testing.T) {
	for _, body := range []string{`{}`, `{"models":null}`, `{"models":{}}`, `not json`, strings.Repeat("x", (4<<20)+1)} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		_, err := ListCodexModels(context.Background(), CodexCatalogConfig{BackendURL: srv.URL, Tokens: &fakeTokens{access: "fixture"}})
		srv.Close()
		if err == nil {
			t.Fatal("invalid payload accepted")
		}
	}
	tokens := &rotatingCatalogTokens{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ListCodexModels(ctx, CodexCatalogConfig{BackendURL: "https://unused.invalid", Tokens: tokens}); err != context.Canceled || tokens.calls != 0 {
		t.Fatalf("canceled discovery started: err=%v calls=%d", err, tokens.calls)
	}
}

func TestRegisterCodexModelsOverridesStaleCapabilitiesAndEfforts(t *testing.T) {
	clearReasoningEffortSupport()
	t.Cleanup(clearReasoningEffortSupport)
	r := NewCapabilityRegistry()
	r.Register(ModelInfo{ID: "future-fixture", Vision: true, Reasoning: true, ContextLength: 999, Source: SourceProbe})
	models := []CodexModel{{Slug: "future-fixture", Visibility: "list", InputModalities: []string{"text"}, ContextWindow: 123, SupportedReasoningLevels: []CodexReasoningLevel{{Effort: "low"}, {Effort: "high"}}}}
	ids := RegisterCodexModels(r, "chatgpt-fixture", "https://fixture.invalid/codex", models)
	info, ok := r.Get("future-fixture")
	if !ok || len(ids) != 1 || info.Provider != "chatgpt-fixture" || info.ContextLength != 123 || info.Vision || !info.VisionKnown || !info.Reasoning || !info.ReasoningKnown || !info.ToolUse || !info.Stream {
		t.Fatalf("incorrect registration: %+v", info)
	}
	if levels, ok := SupportedReasoningEfforts("future-fixture"); !ok || strings.Join(levels, ",") != "low,high" {
		t.Fatalf("efforts lost: %v/%v", levels, ok)
	}
	models[0].SupportedReasoningLevels = []CodexReasoningLevel{}
	RegisterCodexModels(r, "chatgpt-fixture", "https://fixture.invalid/codex", models)
	if levels, ok := SupportedReasoningEfforts("future-fixture"); !ok || len(levels) != 0 {
		t.Fatalf("explicit no-reasoning lost: %v/%v", levels, ok)
	}
}

func TestCodexModelCacheIsPortableIdentityBoundAndPreservesKnownEmptyMetadata(t *testing.T) {
	dir := t.TempDir()
	models := []CodexModel{{Slug: "future-fixture", Visibility: "list", InputModalities: []string{}, SupportedReasoningLevels: []CodexReasoningLevel{}}}
	if err := SaveCodexModelCache(dir, "https://fixture.invalid/codex", "login-one", models); err != nil {
		t.Fatal(err)
	}
	got, stamp, ok := ReadCodexModelCache(dir, "https://fixture.invalid/codex", "login-one")
	if !ok || stamp.IsZero() || len(got) != 1 || got[0].InputModalities == nil || got[0].SupportedReasoningLevels == nil {
		t.Fatalf("empty metadata lost: %+v", got)
	}
	for _, scope := range [][2]string{{"https://fixture.invalid/codex", "login-two"}, {"https://other.invalid/codex", "login-one"}} {
		if _, _, ok := ReadCodexModelCache(dir, scope[0], scope[1]); ok {
			t.Fatal("cache crossed login/endpoint boundary")
		}
	}
	if available, known := CodexModelCacheAvailability(dir, "https://fixture.invalid/codex", "login-one", "missing-fixture"); available || !known {
		t.Fatal("fresh absence not retained")
	}
	path := codexCatalogCachePath(dir, "https://fixture.invalid/codex", "login-one")
	data, _ := json.Marshal(codexCatalogCache{SavedAt: time.Now().Add(-CodexCatalogTTL), Models: models})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, known := CodexModelCacheAvailability(dir, "https://fixture.invalid/codex", "login-one", "missing-fixture"); known {
		t.Fatal("stale absence treated as entitlement evidence")
	}
}

func TestNativeCodexCatalogAutomaticallyIncludesClientVersion(t *testing.T) {
	client := &http.Client{Transport: responsesRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("client_version") != strings.TrimPrefix(buildinfo.Version, "v") {
			t.Fatal("native discovery omitted current client version")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{\"models\":[]}")), Header: make(http.Header)}, nil
	})}
	if _, err := ListCodexModels(context.Background(), CodexCatalogConfig{BackendURL: "https://chatgpt.com/backend-api/codex", Tokens: &fakeTokens{access: "synthetic"}, HTTPClient: client}); err != nil {
		t.Fatal(err)
	}
}
