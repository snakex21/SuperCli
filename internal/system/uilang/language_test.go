package uilang

import "testing"

func TestNormalize(t *testing.T) {
	for input, want := range map[string]string{
		"pl": "pl", "pl-PL": "pl", "PL_pl.UTF-8": "pl",
		"en": "en", "en-US": "en", "de-DE": "",
	} {
		if got := Normalize(input); got != want {
			t.Errorf("Normalize(%q)=%q want %q", input, got, want)
		}
	}
}
