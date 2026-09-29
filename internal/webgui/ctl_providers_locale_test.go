package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"supercli/internal/llm/providers"
)

func TestProviderPresetLocalizationPreservesProtocolAndSource(t *testing.T) {
	source := providers.PredefinedProviders()
	srv := newTestServer(t, false)
	for _, language := range []string{"en", "pl", "uk", "unsupported"} {
		t.Run(language, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, localProviderRequest(http.MethodGet, "/api/providers?lang="+language))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
			}
			var response struct {
				Templates []providers.PredefinedProvider `json:"templates"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Templates) != len(source) {
				t.Fatal("preset count changed")
			}
			changed := false
			for i, preset := range response.Templates {
				if preset.Desc == "" {
					t.Fatalf("empty description for %s", preset.Name)
				}
				changed = changed || preset.Desc != source[i].Desc
				preset.Desc = source[i].Desc
				if preset != source[i] {
					t.Fatalf("provider protocol changed for %s", preset.Name)
				}
			}
			if (language == "pl" || language == "uk") && !changed {
				t.Fatal("localized request returned only original descriptions")
			}
			if (language == "en" || language == "unsupported") && changed {
				t.Fatal("English fallback changed original descriptions")
			}
		})
	}
	if !reflect.DeepEqual(source, providers.PredefinedProviders()) {
		t.Fatal("localization mutated the source catalog")
	}
}
