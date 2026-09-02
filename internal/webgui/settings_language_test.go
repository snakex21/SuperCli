package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/system/config"
)

func TestUISettingsLanguageIsSharedWithTUIConfig(t *testing.T) {
	srv := newTestServer(t, false)
	post := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"ui.lang":"pl","ui.theme":"dark"}`))
	postRec := httptest.NewRecorder()
	srv.handleUISettings(postRec, post)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", postRec.Code, postRec.Body.String())
	}
	cfg, err := config.LoadToml(srv.eng.DataDir() + "/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Language != "pl" {
		t.Fatalf("config language=%q, want pl", cfg.Language)
	}

	getRec := httptest.NewRecorder()
	srv.handleUISettings(getRec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var payload struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Settings["ui.lang"] != "pl" || payload.Settings["ui.theme"] != "dark" {
		t.Fatalf("settings=%v", payload.Settings)
	}
}

func TestUISettingsRejectsUnsupportedLanguage(t *testing.T) {
	srv := newTestServer(t, false)
	rec := httptest.NewRecorder()
	srv.handleUISettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"ui.lang":"de"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

func TestUISettingsPersistsPortableComposerDrafts(t *testing.T) {
	srv := newTestServer(t, false)
	body := `{"supercli-composer-drafts-v1":{"session:s1":{"text":"niedokończona wiadomość","updated":123}}}`
	postRec := httptest.NewRecorder()
	srv.handleUISettings(postRec, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", postRec.Code, postRec.Body.String())
	}

	getRec := httptest.NewRecorder()
	srv.handleUISettings(getRec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var payload struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	var drafts map[string]struct {
		Text    string `json:"text"`
		Updated int64  `json:"updated"`
	}
	if err := json.Unmarshal(payload.Settings["supercli-composer-drafts-v1"], &drafts); err != nil {
		t.Fatal(err)
	}
	if got := drafts["session:s1"]; got.Text != "niedokończona wiadomość" || got.Updated != 123 {
		t.Fatalf("draft=%+v", got)
	}
}
