package uilang

import (
	"encoding/json"
	"testing"
)

func TestPluralPolishAndEnglish(t *testing.T) {
	for _, tc := range []struct {
		lang string
		n    int
		want string
	}{
		{"en", 1, "done · 1 line"}, {"en", 4, "done · 4 lines"},
		{"pl", 1, "gotowe · 1 linia"}, {"pl", 2, "gotowe · 2 linie"}, {"pl", 4, "gotowe · 4 linie"},
		{"pl", 5, "gotowe · 5 linii"}, {"pl", 12, "gotowe · 12 linii"}, {"pl", 22, "gotowe · 22 linie"},
	} {
		if got := Plural(tc.lang, "tui.view_markers.doneLines", tc.n); got != tc.want {
			t.Errorf("%s %d: %q want %q", tc.lang, tc.n, got, tc.want)
		}
	}
}

func TestPluralCategories(t *testing.T) {
	for _, tc := range []struct {
		lang string
		n    int
		want string
	}{
		{"ru", 21, "one"}, {"ru", 11, "many"}, {"uk", 3, "few"}, {"cs", 3, "few"}, {"cs", 5, "other"},
		{"sl", 102, "two"}, {"lt", 12, "other"}, {"lt", 22, "few"}, {"lv", 10, "zero"}, {"lv", 21, "one"},
		{"ro", 0, "few"}, {"ro", 20, "other"}, {"fr", 0, "one"}, {"hr", 25, "other"}, {"de", 0, "other"},
	} {
		if got := PluralCategory(tc.lang, tc.n); got != tc.want {
			t.Errorf("%s %d: %s want %s", tc.lang, tc.n, got, tc.want)
		}
	}
}

// Every plural message must define each category its language can produce,
// so no locale silently falls back to a wrong form.
func TestPluralMessagesCoverLanguageCategories(t *testing.T) {
	for _, language := range Languages() {
		data, err := catalogFiles.ReadFile("catalogs/" + language.Code + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var catalog map[string]string
		if err := json.Unmarshal(data, &catalog); err != nil {
			t.Fatal(err)
		}
		forms := map[string]bool{}
		for _, m := range pluralForm.FindAllStringSubmatch(catalog["tui.view_markers.doneLines"], -1) {
			forms[m[1]] = true
		}
		for n := 0; n < 120; n++ {
			if category := PluralCategory(language.Code, n); !forms[category] {
				t.Errorf("%s: no %q form (n=%d)", language.Code, category, n)
				break
			}
		}
	}
}
