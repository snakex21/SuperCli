package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestImportedCodexLoginAppearsAndCatalogRefreshesWithoutStaticReseed(t *testing.T) {
	var hits, generation atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/models" || r.Header.Get("Authorization") != "Bearer synthetic-token" || r.Header.Get("ChatGPT-Account-Id") != "synthetic-account" {
			http.Error(w, "wrong model discovery request", http.StatusBadRequest)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if generation.Load() == 0 {
			_, _ = w.Write([]byte("{\"models\":[{\"slug\":\"future-codex-a\",\"visibility\":\"list\",\"context_window\":123456,\"input_modalities\":[\"text\",\"image\"],\"supported_reasoning_levels\":[{\"effort\":\"low\"},{\"effort\":\"high\"}]},{\"slug\":\"hidden-model\",\"visibility\":\"hide\"}]}"))
		} else {
			_, _ = w.Write([]byte("{\"models\":[{\"slug\":\"future-codex-b\",\"visibility\":\"list\",\"input_modalities\":[\"text\"],\"supported_reasoning_levels\":[]}]}"))
		}
	}))
	defer upstream.Close()
	srv := newTestServer(t, false)
	tc := config.TomlConfig{CodexAuth: config.CodexAuthConf{BackendURL: upstream.URL + "/codex"}}
	if err := config.SaveToml(filepath.Join(srv.eng.dataDir, "config.toml"), tc); err != nil {
		t.Fatal(err)
	}
	if err := codexauth.Save(codexauth.AuthFilePathFor(srv.eng.dataDir, "work"), &codexauth.AuthFile{LastRefresh: time.Now(), Tokens: &codexauth.TokenData{AccessToken: "synthetic-token", AccountID: "synthetic-account"}}); err != nil {
		t.Fatal(err)
	}
	srv.eng.caps.Register(llm.ModelInfo{ID: "gpt-5.5", Provider: "codex", Source: llm.SourceCatalog})
	read := func() modelsResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.handleModels(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("models: %d %s", rec.Code, rec.Body.String())
		}
		var out modelsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	check := func(out modelsResponse, want string) {
		t.Helper()
		var found bool
		for _, m := range out.Models {
			if m.Provider != "codex" {
				continue
			}
			if m.ID != want {
				t.Fatalf("unexpected static or hidden Codex model: %+v", m)
			}
			found = true
		}
		if !found {
			t.Fatalf("saved login did not expose %s: %+v", want, out)
		}
	}
	check(read(), "future-codex-a")
	check(read(), "future-codex-a")
	if hits.Load() != 1 {
		t.Fatalf("cached picker repeated remote discovery %d times", hits.Load())
	}
	tcAfter, err := config.LoadToml(filepath.Join(srv.eng.dataDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if tcAfter.DefaultModel != "" || tcAfter.DefaultProvider != "" || srv.eng.ModelName() != echoConfig().Model {
		t.Fatal("catalog discovery changed active/default model")
	}
	generation.Store(1)
	rec := httptest.NewRecorder()
	srv.handleProviderDiagnostics(rec, httptest.NewRequest(http.MethodGet, "/api/provider/diagnostics?name=codex", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "future-codex-b") {
		t.Fatalf("explicit native catalog refresh: %d %s", rec.Code, rec.Body.String())
	}
	check(read(), "future-codex-b")
	if hits.Load() != 2 {
		t.Fatalf("explicit refresh/TTL made %d requests", hits.Load())
	}
}
