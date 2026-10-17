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

	"supercli/internal/system/config"
	"supercli/internal/tools"
)

var mobileMenuTestSizes = []struct {
	name          string
	width, height int
}{
	{"tiny", 32, 12}, {"keyboard", 38, 14}, {"phone", 42, 20},
	{"narrow", 60, 20}, {"desktop", 110, 34},
}

func mobileMenuTestRenderer(profile termenv.Profile) *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard, termenv.WithProfile(profile))
	r.SetColorProfile(profile)
	r.SetHasDarkBackground(true)
	return r
}

func assertMobileMenuBounds(t *testing.T, view string, width, height int) {
	t.Helper()
	if !utf8.ValidString(view) {
		t.Fatal("render split a UTF-8 sequence")
	}
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Fatalf("render has %d rows, budget %d:\n%s", len(lines), height, view)
	}
	for i, line := range lines {
		if cells := ansi.StringWidth(line); cells > width {
			t.Fatalf("row %d has %d cells, budget %d: %q", i, cells, width, line)
		}
	}
}

// Join only actual visible inner rows, so a suffix wrapped at a hyphen still
// counts, but an omitted tail cannot pass by inspecting the source option.
func mobileAskVisibleText(view string) string {
	var text strings.Builder
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if start := strings.Index(line, "│ "); start >= 0 {
			text.WriteString(strings.TrimSpace(strings.TrimSuffix(line[start+len("│ "):], " │")))
		}
	}
	return text.String()
}

func assertMobileAskControls(t *testing.T, plain string) {
	t.Helper()
	if (!strings.Contains(plain, "Enter") && !strings.Contains(plain, "⏎")) || !strings.Contains(plain, "Esc") {
		t.Fatalf("submit/cancel hints disappeared:\n%s", plain)
	}
}

func TestMobileSettingsEditLine(t *testing.T) {
	for _, size := range mobileMenuTestSizes {
		for _, language := range []string{"en", "pl"} {
			for _, value := range []string{
				"provider/long-model-name-distinctive-suffix",
				strings.Repeat("模型/🚀/e\u0301/", 8) + "模型-e\u0301-suffix",
			} {
				t.Run(fmt.Sprintf("%s/%s/%t", size.name, language, strings.Contains(value, "模型")), func(t *testing.T) {
					m := mobileLayoutFixture(t, size.width, size.height, "settings", language, mobileMenuTestRenderer(termenv.TrueColor))
					m.menu.editBuf = value
					cursor, category, cfg := m.menu.cursor, m.menu.category, m.menu.settingsCfg
					view := m.View()
					assertMobileMenuBounds(t, view, size.width, size.height)
					plain := ansi.Strip(view)
					suffix := "distinctive-suffix▏"
					if strings.Contains(value, "模型") {
						suffix = "模型-e\u0301-suffix▏"
					}
					if !strings.Contains(plain, suffix) || !strings.Contains(plain, "› ") {
						t.Fatalf("edited tail/caret or selected row missing:\n%s", plain)
					}
					if ansi.StringWidth(value+"▏") <= size.width-4 && !strings.Contains(plain, value+"▏") {
						t.Fatalf("full value missing despite enough horizontal space:\n%s", plain)
					}
					if !strings.Contains(view, "\x1b[") || len(strings.Split(view, "\n")) != size.height {
						t.Fatal("fixture lost colored production rendering or stable menu frame")
					}
					if m.menu.editBuf != value || m.menu.cursor != cursor || m.menu.category != category || m.menu.settingsCfg != cfg || !reflect.DeepEqual(*cfg, config.TomlConfig{}) {
						t.Fatal("render changed editing state or configuration")
					}
					m.menu.formErr = "Invalid input"
					view = m.View()
					assertMobileMenuBounds(t, view, size.width, size.height)
					if plain = ansi.Strip(view); !strings.Contains(plain, suffix) || !strings.Contains(plain, "Invalid input") {
						t.Fatalf("validation displaced the editing line:\n%s", plain)
					}
				})
			}
		}
	}
}

func TestMobileSettingsEditKeyboard(t *testing.T) {
	for _, size := range mobileMenuTestSizes {
		t.Run(size.name, func(t *testing.T) {
			m := mobileLayoutFixture(t, size.width, size.height, "settings", "en", mobileMenuTestRenderer(termenv.Ascii))
			value, cursor, category := m.menu.editBuf, m.menu.cursor, m.menu.category
			key := func(msg tea.KeyMsg) {
				next, _ := m.handleMenuKey(msg)
				m = next.(Model)
				assertMobileMenuBounds(t, m.View(), size.width, size.height)
			}
			key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("界")})
			if m.menu.editBuf != value+"界" || !strings.Contains(m.View(), "界▏") {
				t.Fatal("typed Unicode character/caret was lost")
			}
			key(tea.KeyMsg{Type: tea.KeyBackspace})
			key(tea.KeyMsg{Type: tea.KeyRight})
			key(tea.KeyMsg{Type: tea.KeyDown})
			if m.menu.editBuf != value || m.menu.cursor != cursor || m.menu.category != category {
				t.Fatal("editing changed the buffer, selected row or tab unexpectedly")
			}
			key(tea.KeyMsg{Type: tea.KeyEsc})
			if m.menu.editing || m.menu.editBuf != "" || m.mode != modeMenu {
				t.Fatal("Escape must cancel editing without closing settings")
			}
			key(tea.KeyMsg{Type: tea.KeyDown})
			if m.menu.cursor != min(cursor+1, len(m.localizedSettingsRows())-1) || !reflect.DeepEqual(*m.menu.settingsCfg, config.TomlConfig{}) {
				t.Fatal("row navigation changed or cancel saved configuration")
			}
		})
	}
}

func TestMobileAskSelectedBlock(t *testing.T) {
	for _, size := range mobileMenuTestSizes {
		for _, multi := range []bool{false, true} {
			for cursor := range 4 {
				t.Run(fmt.Sprintf("%s/multi=%t/choice=%d", size.name, multi, cursor+1), func(t *testing.T) {
					m := mobileLayoutFixture(t, size.width, size.height, "ask", "en", mobileMenuTestRenderer(termenv.TrueColor))
					a := m.pendingAsk
					a.Question, a.Header = "Apply safely?", "選択 e\u0301"
					a.cursor, a.MultiSelect, a.AllowCustom = cursor, multi, true
					a.toggled[cursor] = true
					for i := range a.Options {
						a.Options[i] = tools.AskOption{
							Label: fmt.Sprintf("Choice %d 模型", i+1), Description: strings.Repeat("Earlier choice details. ", 8),
							Preview: strings.Repeat("Preview content. ", 8),
						}
					}
					a.Options[cursor].Label = "\x1b[36m" + strings.Repeat("模型 e\u0301 sprawdź ", 4) + "distinctive-suffix\x1b[0m"
					a.Options[cursor].Description = "DESC-prefix " + strings.Repeat("check safely e\u0301 模型 ", 8)
					before := append([]tools.AskOption{}, a.Options...)
					view := m.View()
					assertMobileMenuBounds(t, view, size.width, size.height)
					plain := ansi.Strip(view)
					assertMobileAskControls(t, plain)
					if !strings.Contains(mobileAskVisibleText(view), "distinctive-suffix") {
						t.Fatalf("selected label tail missing from visible rows:\n%s", plain)
					}
					for _, want := range []string{fmt.Sprintf("> %d.", cursor+1), "DESC-prefix", "[選択 e\u0301]"} {
						if !strings.Contains(plain, want) {
							t.Fatalf("selected block/control %q missing:\n%s", want, plain)
						}
					}
					check := ""
					if multi {
						check = "[x] "
						if !strings.Contains(plain, "[x]") || (!strings.Contains(plain, "Space") && !strings.Contains(plain, "toggle")) {
							t.Fatalf("multiselect state/hint missing:\n%s", plain)
						}
					}
					labelRows := len(wrap(fmt.Sprintf("> %d. %s%s", cursor+1, check, a.Options[cursor].Label), min(size.width, 92)-4))
					if labelRows+2 <= size.height-4 && !strings.Contains(plain, a.Question) {
						t.Fatalf("question hidden despite space beside focused label/description:\n%s", plain)
					}
					if a.cursor != cursor || !a.toggled[cursor] || !reflect.DeepEqual(a.Options, before) {
						t.Fatal("render mutated answer order, focus or toggles")
					}
				})
			}
		}
	}
}

func TestMobileAskOverheightLabelAndCustomInput(t *testing.T) {
	for _, size := range mobileMenuTestSizes {
		for _, custom := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/custom=%t", size.name, custom), func(t *testing.T) {
				m := mobileLayoutFixture(t, size.width, size.height, "ask", "en", mobileMenuTestRenderer(termenv.Ascii))
				a := m.pendingAsk
				value := strings.Repeat("模型 e\u0301 ", 1000) + "distinctive-suffix"
				a.cursor, a.AllowCustom, a.customMode, a.custom = 3, true, custom, value
				a.Options[3].Label, a.Options[3].Description = value, "DESC-prefix check safely"
				view := m.View()
				assertMobileMenuBounds(t, view, size.width, size.height)
				plain := ansi.Strip(view)
				want := "distinctive-suffix"
				if custom {
					want += "_"
				} else if !strings.Contains(plain, "> 4.") || !strings.Contains(plain, "DESC-prefix") {
					t.Fatalf("over-height label displaced selection/description:\n%s", plain)
				}
				assertMobileAskControls(t, plain)
				if !strings.Contains(mobileAskVisibleText(view), want) {
					t.Fatalf("over-height focus lost suffix/caret:\n%s", plain)
				}
				if a.custom != value || a.Options[3].Label != value || a.cursor != 3 {
					t.Fatal("compacting visible rows changed the original answer/input")
				}
			})
		}
	}
}

func TestMobileAskFourChoicesAndKeyboard(t *testing.T) {
	for _, size := range mobileMenuTestSizes {
		for _, mode := range []string{"arrows", "quick-pick", "multi", "custom"} {
			t.Run(size.name+"/"+mode, func(t *testing.T) {
				m := mobileLayoutFixture(t, size.width, size.height, "welcome", "en", mobileMenuTestRenderer(termenv.Ascii))
				req := tools.AskRequest{
					Question: "Choose?", Header: "Choices", AllowCustom: true, MultiSelect: mode == "multi",
					Respond: make(chan tools.AskAnswer, 1),
				}
				for i := range 4 {
					req.Options = append(req.Options, tools.AskOption{Label: fmt.Sprintf("Choice %d 模型", i+1), Description: "Safe preview"})
				}
				next, _ := m.beginAsk(req)
				m = next.(Model)
				key := func(msg tea.KeyMsg) {
					next, _ := m.Update(msg)
					m = next.(Model)
					if m.mode == modeAsking {
						assertMobileMenuBounds(t, m.View(), size.width, size.height)
					}
				}
				for range 3 {
					key(tea.KeyMsg{Type: tea.KeyDown})
				}
				key(tea.KeyMsg{Type: tea.KeyUp})
				key(tea.KeyMsg{Type: tea.KeyDown})
				if m.pendingAsk.cursor != 3 {
					t.Fatal("arrows no longer select the fourth choice")
				}
				plain := ansi.Strip(m.View())
				if size.height-4 >= 10 {
					last := -1
					for _, opt := range req.Options {
						at := strings.Index(plain, opt.Label)
						if at <= last {
							t.Fatalf("choices hidden/reordered despite sufficient budget:\n%s", plain)
						}
						last = at
					}
				}
				want := tools.AskAnswer{Selected: []string{req.Options[3].Label}}
				switch mode {
				case "quick-pick":
					key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
				case "multi":
					key(tea.KeyMsg{Type: tea.KeySpace})
					key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
					key(tea.KeyMsg{Type: tea.KeyEnter})
					want = tools.AskAnswer{MultiSelect: true, Selected: []string{req.Options[0].Label, req.Options[3].Label}}
				case "custom":
					key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
					value := strings.Repeat("模型 e\u0301 ", 20) + "distinctive-suffix"
					key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value + "界")})
					key(tea.KeyMsg{Type: tea.KeyBackspace})
					if !strings.Contains(m.View(), "distinctive-suffix_") {
						t.Fatal("custom answer suffix/caret missing after typing/backspace")
					}
					key(tea.KeyMsg{Type: tea.KeyEnter})
					want = tools.AskAnswer{Custom: value}
				default:
					key(tea.KeyMsg{Type: tea.KeyEnter})
				}
				select {
				case got := <-req.Respond:
					if !reflect.DeepEqual(got, want) || m.mode != modeNormal {
						t.Fatalf("answer/mode changed: got %#v mode=%v, want %#v", got, m.mode, want)
					}
				default:
					t.Fatal("keyboard action did not deliver an answer")
				}
			})
		}
	}
}
