package webgui

import (
	"os"
	"strings"
	"testing"
)

// Server-authored prose cannot be translated by the browser. Every notice the
// GUI renders must therefore travel as a catalog key, and that key must exist
// in both catalogs — otherwise one language silently falls back to the other,
// or to the raw key.
func TestUINoticeCodesExistInBothLanguageCatalogs(t *testing.T) {
	dictionary, err := os.ReadFile("assets/js/01-i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(dictionary)
	for _, key := range []string{"prov.warn.typeUnclear", "prov.warn.scanTimeout", "chat.noProvider"} {
		if got := strings.Count(text, `"`+key+`"`); got != 2 {
			t.Errorf("%s defined %d times, want 2 (en and pl)", key, got)
		}
	}
	if !strings.Contains(text, `"prov.warn.scanTimeout": "{n} was added`) {
		t.Error("English prov.warn.scanTimeout must interpolate the provider name as {n}")
	}
	if !strings.Contains(text, "within {c}") {
		t.Error("prov.warn.scanTimeout must interpolate the discovery timeout as {c}")
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
