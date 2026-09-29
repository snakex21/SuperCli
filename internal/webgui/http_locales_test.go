package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"supercli/internal/system/uilang"
)

func TestEmbeddedGUICatalogsMatchLanguagesAndPlaceholders(t *testing.T) {
	var english map[string]string
	data, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &english); err != nil {
		t.Fatal(err)
	}
	placeholders := regexp.MustCompile(`\{[a-zA-Z0-9_]+\}`)
	tokens := func(value string) []string {
		out := placeholders.FindAllString(value, -1)
		sort.Strings(out)
		return out
	}
	for _, lang := range uilang.Languages() {
		t.Run(lang.Code, func(t *testing.T) {
			data, err := assetsFS.ReadFile("assets/locales/" + lang.Code + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var catalog map[string]string
			if err := json.Unmarshal(data, &catalog); err != nil {
				t.Fatal(err)
			}
			if len(catalog) != len(english) {
				t.Fatalf("catalog keys=%d want %d", len(catalog), len(english))
			}
			for key, source := range english {
				value, ok := catalog[key]
				if !ok || strings.TrimSpace(value) == "" {
					t.Errorf("missing key %s", key)
					continue
				}
				if !reflect.DeepEqual(tokens(source), tokens(value)) {
					t.Errorf("placeholder mismatch %s: %q", key, value)
				}
			}
		})
	}
}

func TestLocaleBootstrapShipsOnlyEnglishAndNativeMetadata(t *testing.T) {
	server := &Server{}
	rec := httptest.NewRecorder()
	server.handleLocaleBootstrap(rec, httptest.NewRequest(http.MethodGet, "/locales/en.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("catalog bootstrap must not write browser caches")
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "var I18N = {en:") || !strings.Contains(body, "var UI_LANGUAGES = ") {
		t.Fatal("missing English/metadata bootstrap")
	}
	if strings.Contains(body, "Zapisz") {
		t.Fatal("non-English catalogs were bundled into startup")
	}
	for _, language := range uilang.Languages() {
		if !strings.Contains(body, language.Name) {
			t.Errorf("missing native name %s", language.Name)
		}
	}
}
