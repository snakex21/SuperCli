package uilang

import "testing"

func TestNormalize(t *testing.T) {
	for input, want := range map[string]string{
		"pl": "pl", "pl-PL": "pl", "PL_pl.UTF-8": "pl",
		"en": "en", "en-US": "en", "de-DE": "de",
		" pt_br.UTF-8 ": "pt-BR", "pt-PT": "pt-BR",
		"sr_RS@latin": "sr-Latn", "sr-Latn-RS": "sr-Latn",
		"no-NO": "nb", "nb_NO.UTF-8": "nb",
		"uk-UA": "uk", "ja-JP": "", "": "", "C.UTF-8": "",
	} {
		if got := Normalize(input); got != want {
			t.Errorf("Normalize(%q)=%q want %q", input, got, want)
		}
	}
}

func TestLanguageMetadataAndCanonicalCodes(t *testing.T) {
	want := []string{"en", "bg", "cs", "da", "de", "el", "es", "et", "fi", "fr", "hr", "hu", "it", "lt", "lv", "nb", "nl", "pl", "pt-BR", "ro", "ru", "sk", "sl", "sr-Latn", "sv", "tr", "uk"}
	got := Languages()
	if len(got) != len(want) {
		t.Fatalf("languages = %d, want %d", len(got), len(want))
	}
	for i, language := range got {
		if language.Code != want[i] || Normalize(language.Code) != language.Code || language.Name == "" || language.Dir != "ltr" {
			t.Errorf("invalid metadata at %d: %+v", i, language)
		}
		if Resolve(language.Code) != language.Code {
			t.Errorf("saved %s was not preserved", language.Code)
		}
	}
	got[0].Name = "mutated"
	if Languages()[0].Name != "English" {
		t.Fatal("metadata exposes its shared backing array")
	}
}
