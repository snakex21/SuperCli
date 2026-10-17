package tui

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func mobileAutocompletePalette(profile termenv.Profile) Palette {
	r := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(profile))
	r.SetColorProfile(profile)
	r.SetHasDarkBackground(true)
	return NewPalette(r)
}

func mobileAutocompleteFixture(kind autocompleteKind) autocomplete {
	query := strings.Repeat("模型", 8)
	ac := autocomplete{kind: kind, query: query}
	for i := range 20 {
		ac.items = append(ac.items, autocompleteItem{
			Label:    fmt.Sprintf("\x1b[35m/item%02d 👩‍💻 e\u0301 %s ustawienia zaawansowanego modelu\x1b[0m", i, query),
			Category: "Konfiguracja 模型 e\u0301",
			Desc:     "\x1b[33mOpis konfiguracji 模型 e\u0301\n" + strings.Repeat("długi opis ", 8) + "\x1b[0m",
			Hint:     "argumenty\t" + strings.Repeat("🚀 e\u0301 ", 8),
			Value:    fmt.Sprintf("/value%02d ", i),
		})
	}
	return ac
}

func assertMobileAutocompleteBounds(t *testing.T, ac *autocomplete, width, budget int, view string) {
	t.Helper()
	if !utf8.ValidString(view) || strings.Contains(ansi.Strip(view), "\x1b") {
		t.Fatal("popup split UTF-8 or an ANSI escape")
	}
	rows := 0
	if view != "" {
		lines := strings.Split(view, "\n")
		rows = len(lines)
		for i, line := range lines {
			if cells := ansi.StringWidth(line); cells > width {
				t.Fatalf("row %d has %d cells, budget %d: %q", i, cells, width, line)
			}
		}
	}
	if budget >= 0 && rows > budget {
		t.Fatalf("popup has %d rows, budget %d:\n%s", rows, budget, view)
	}
	if measured := autocompleteHeightWithin(ac, budget); measured != rows {
		t.Fatalf("height counter=%d, rendered rows=%d, budget=%d:\n%s", measured, rows, budget, view)
	}
}

func TestMobileAutocompleteWithinHeight(t *testing.T) {
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.Ascii} {
		palette := mobileAutocompletePalette(profile)
		for _, language := range []string{"en", "pl"} {
			for _, kind := range []autocompleteKind{autocompSlash, autocompMention} {
				for _, width := range []int{32, 38, 60} {
					for budget := 3; budget <= 12; budget++ {
						for _, cursor := range []int{0, 7, 19} {
							t.Run(fmt.Sprintf("%d/%s/%d/%dx%d/selected%d", profile, language, kind, width, budget, cursor), func(t *testing.T) {
								ac := mobileAutocompleteFixture(kind)
								ac.cursor, ac.scroll = cursor, 18 // stale scroll must not hide selection
								before := ac
								before.items = append([]autocompleteItem{}, ac.items...)
								view := renderAutocompleteWithinHeight(&ac, width, budget, palette, language)
								assertMobileAutocompleteBounds(t, &ac, width, budget, view)
								plain := ansi.Strip(view)
								if !strings.Contains(plain, fmt.Sprintf("> /item%02d", cursor)) || !strings.Contains(plain, "👩‍💻 e\u0301") {
									t.Fatalf("selected row or complete wide/combining grapheme missing:\n%s", plain)
								}
								if !reflect.DeepEqual(ac, before) {
									t.Fatal("measuring/rendering changed cursor, scroll, filter, values or item order")
								}
							})
						}
					}
				}
			}
		}
	}
}

func TestMobileAutocompleteEmptyAndShortBudgets(t *testing.T) {
	palette := mobileAutocompletePalette(termenv.TrueColor)
	for _, ac := range []*autocomplete{
		nil, {}, {kind: autocompSlash},
		{kind: autocompSlash, items: []autocompleteItem{{Label: "/help"}}, query: "no-such-command"},
	} {
		for _, budget := range []int{-1, 0, 1, 2, 3, 12} {
			view := renderAutocompleteWithinHeight(ac, 32, budget, palette, "pl")
			assertMobileAutocompleteBounds(t, ac, 32, budget, view)
			if view != "" {
				t.Fatalf("empty/dismissed popup used rows: %q", view)
			}
		}
	}
	for _, cursor := range []int{-3, 0, 19, 99} {
		for _, budget := range []int{0, 1, 2, 3, 4, 5, 6, 7, 12} {
			ac := mobileAutocompleteFixture(autocompSlash)
			ac.cursor, ac.scroll = cursor, -5
			before := ac
			view := renderAutocompleteWithinHeight(&ac, 32, budget, palette, "en")
			assertMobileAutocompleteBounds(t, &ac, 32, budget, view)
			if budget == 0 {
				if view != "" {
					t.Fatal("zero budget must hide popup")
				}
			} else if !strings.Contains(ansi.Strip(view), fmt.Sprintf("> /item%02d", min(max(cursor, 0), 19))) {
				t.Fatalf("short popup lost selected item at budget %d:\n%s", budget, view)
			}
			if !reflect.DeepEqual(ac, before) {
				t.Fatal("local render clamping changed navigation state")
			}
		}
	}
	// Even terminal widths too small for a border must not panic or wrap.
	for width := 1; width <= 6; width++ {
		for budget := 1; budget <= 7; budget++ {
			ac := mobileAutocompleteFixture(autocompMention)
			view := renderAutocompleteWithinHeight(&ac, width, budget, palette, "pl")
			assertMobileAutocompleteBounds(t, &ac, width, budget, view)
		}
	}
}

func TestMobileAutocompleteDesktopCompatibility(t *testing.T) {
	palette := mobileAutocompletePalette(termenv.Ascii)
	for _, width := range []int{80, 110} {
		ac := autocomplete{
			kind: autocompSlash, cursor: 1,
			items: []autocompleteItem{
				{Label: "/help", Category: "system", Desc: "show help"},
				{Label: "/goal", Category: "agent", Desc: "manage goal", Hint: "<set>"},
			},
		}
		// The usual desktop box, columns, detail and footer remain unchanged.
		boxWidth := min(width-2, 86)
		inner := boxWidth - 2
		title := textFor("en", "tui.autocomplete_render.b269dc4e81")
		row := func(s string) string { return "│" + padRight(s, inner) + "│" }
		rule := "├" + strings.Repeat("─", inner) + "┤"
		want := strings.Join([]string{
			"╭─ " + title + strings.Repeat("─", boxWidth-4-ansi.StringWidth(title)) + "╮",
			row("  " + padRight("/help", 18) + "  " + padRight("system", 8) + "  show help"),
			row("> " + padRight("/goal", 18) + "  " + padRight("agent", 8) + "  manage goal"),
			rule, row("  manage goal · <set>"),
			rule, row(truncateText(textFor("en", "tui.autocomplete_render.6b1db814c4"), inner)),
			"╰" + strings.Repeat("─", inner) + "╯",
		}, "\n")
		for _, budget := range []int{-1, 8, 30} {
			view := renderAutocompleteWithinHeight(&ac, width, budget, palette, "en")
			assertMobileAutocompleteBounds(t, &ac, width, budget, view)
			if view != want {
				t.Fatalf("desktop layout changed at width=%d budget=%d:\ngot:\n%s\nwant:\n%s", width, budget, view, want)
			}
		}
		if got := renderAutocomplete(&ac, width, palette); got != want {
			t.Fatalf("legacy variadic wrapper changed desktop layout:\n%s", got)
		}
		if got := renderAutocomplete(&ac, width, palette, "en"); got != want {
			t.Fatal("legacy language argument changed desktop layout")
		}
	}
}

func TestMobileAutocompleteChromeReduction(t *testing.T) {
	palette := mobileAutocompletePalette(termenv.Ascii)
	ac := autocomplete{kind: autocompSlash, items: []autocompleteItem{{Label: "/help", Desc: "DETAIL-prefix", Hint: "topic"}}}
	for _, budget := range []int{1, 2, 3, 4, 5, 6, 7, 12} {
		view := renderAutocompleteWithinHeight(&ac, 32, budget, palette, "en")
		assertMobileAutocompleteBounds(t, &ac, 32, budget, view)
		if !strings.Contains(view, "> /help") {
			t.Fatal("chrome displaced the sole selectable row")
		}
		if strings.Contains(view, "DETAIL-prefix") != (budget >= 7) {
			t.Fatalf("detail reduction incorrect at budget %d:\n%s", budget, view)
		}
		if strings.Contains(view, "╭") != (budget >= 3) || strings.Contains(view, "╰") != (budget >= 3) {
			t.Fatalf("box reduction incorrect at budget %d:\n%s", budget, view)
		}
		footer := ansi.Truncate(textFor("en", "tui.autocomplete_render.6b1db814c4"), 28, "…")
		if strings.Contains(view, footer) != (budget >= 4) {
			t.Fatalf("footer reduction incorrect at budget %d:\n%s", budget, view)
		}
	}
}

func TestMobileAutocompleteKeyboardFilterAndDrop(t *testing.T) {
	palette := mobileAutocompletePalette(termenv.Ascii)
	for _, width := range []int{32, 38, 60} {
		t.Run(fmt.Sprintf("width%d", width), func(t *testing.T) {
			m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), NoColor: true, Language: "pl"})
			m.input.SetValue("/")
			m.updateAutocompleteState()
			items := filterItems(m.autocomp.items, m.autocomp.query)
			for i := range items {
				if i > 0 {
					next, _ := m.handleAutocompleteKey(tea.KeyMsg{Type: tea.KeyDown})
					m = next.(Model)
				}
				before := m.autocomp
				budget := 3 + i%10
				view := renderAutocompleteWithinHeight(&m.autocomp, width, budget, palette, m.language)
				assertMobileAutocompleteBounds(t, &m.autocomp, width, budget, view)
				if m.autocomp.cursor != i || !reflect.DeepEqual(before, m.autocomp) || !strings.Contains(view, "> "+items[i].Label) {
					t.Fatalf("keyboard selection hidden/changed at index %d:\n%s", i, view)
				}
			}
			m.input.SetValue("/mo")
			m.updateAutocompleteState()
			filtered := append([]autocompleteItem{}, filterItems(m.autocomp.items, m.autocomp.query)...)
			if len(filtered) < 2 {
				t.Fatal("fixture requires multiple filtered commands")
			}
			for _, key := range []tea.KeyType{tea.KeyDown, tea.KeyUp, tea.KeyDown} {
				next, _ := m.handleAutocompleteKey(tea.KeyMsg{Type: key})
				m = next.(Model)
				view := renderAutocompleteWithinHeight(&m.autocomp, width, 3, palette, m.language)
				assertMobileAutocompleteBounds(t, &m.autocomp, width, 3, view)
			}
			if m.autocomp.cursor != 1 || !reflect.DeepEqual(filtered, filterItems(m.autocomp.items, m.autocomp.query)) {
				t.Fatal("resizing changed filter order or navigation index")
			}
			next, _ := m.handleAutocompleteKey(tea.KeyMsg{Type: tea.KeyTab})
			m = next.(Model)
			if m.autocomp.kind != autocompNone || m.input.Value() != filtered[1].Value {
				t.Fatalf("Tab accepted a different value or failed to dismiss: %q", m.input.Value())
			}
			m.input.SetValue("/")
			m.updateAutocompleteState()
			next, _ = m.handleAutocompleteKey(tea.KeyMsg{Type: tea.KeyEsc})
			m = next.(Model)
			if view := renderAutocompleteWithinHeight(&m.autocomp, width, 5, palette, m.language); view != "" || autocompleteHeightWithin(&m.autocomp, 5) != 0 {
				t.Fatal("Escape did not drop the popup")
			}
			m.input.SetValue("/")
			m.updateAutocompleteState()
			m.input.SetValue("ordinary text")
			m.updateAutocompleteState()
			if m.autocomp.kind != autocompNone || autocompleteHeightWithin(&m.autocomp, 5) != 0 {
				t.Fatal("removing the trigger did not drop the popup")
			}
		})
	}
}
