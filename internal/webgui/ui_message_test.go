package webgui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"supercli/internal/system/uilang"
)

// Server-authored prose cannot be translated by the browser. Every notice the
// GUI renders must travel as a catalog key that exists in every supported
// language, including the parameters required by the wire contract.
func TestUINoticeCodesExistInAllLanguageCatalogs(t *testing.T) {
	forEachEmbeddedGUILanguageCatalog(t, func(t *testing.T, catalog map[string]string) {
		for key, placeholders := range map[string][]string{
			"prov.warn.typeUnclear": nil,
			"prov.warn.scanTimeout": {"{n}", "{c}"},
			"chat.noProvider":       {"{n}"},
		} {
			value := catalog[key]
			if strings.TrimSpace(value) == "" {
				t.Errorf("missing notice key %s", key)
				continue
			}
			for _, placeholder := range placeholders {
				if !strings.Contains(value, placeholder) {
					t.Errorf("%s must interpolate %s", key, placeholder)
				}
			}
		}
	})
}

func forEachEmbeddedGUILanguageCatalog(t *testing.T, check func(*testing.T, map[string]string)) {
	t.Helper()
	languages := uilang.Languages()
	if len(languages) != 27 {
		t.Fatalf("supported GUI languages = %d, want 27", len(languages))
	}
	for _, language := range languages {
		t.Run(language.Code, func(t *testing.T) {
			data, err := assetsFS.ReadFile("assets/locales/" + language.Code + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var catalog map[string]string
			if err := json.Unmarshal(data, &catalog); err != nil {
				t.Fatal(err)
			}
			check(t, catalog)
		})
	}
}

// The concrete regression: these two notices used to be finished Polish (and
// finished English) sentences built in Go, so the language switch could not
// reach them.
func TestUINoticeProseDoesNotReturnToGo(t *testing.T) {
	for file, banned := range map[string][]string{
		"ctl_providers.go": {"was added, but it did not finish listing models", "Nie udało się jednoznacznie wykryć typu API"},
		"stream_run.go":    {"nie ma aktywnego dostawcy AI"},
	} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, phrase := range banned {
			if strings.Contains(string(source), phrase) {
				t.Errorf("%s builds UI prose in Go (%q); send a catalog code instead", file, phrase)
			}
		}
	}
}

// The wire contract the browser depends on: a blocked run reports a code, not
// a sentence, and the code is the one the catalog defines.
func TestNoActiveProviderReportsACatalogCode(t *testing.T) {
	source, err := os.ReadFile("run_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `event.ErrCode = "chat.noProvider"`) {
		t.Error("run_chat.go must map errNoActiveProvider to the chat.noProvider catalog key")
	}
	if errNoActiveProvider == nil {
		t.Fatal("errNoActiveProvider sentinel is missing")
	}
}
