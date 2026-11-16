package tui

// audit-130: switching the UI language from inside the TUI, and catalog key
// parity for the strings the TUI uses.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
)

// chrome keys visible on the idle main screen.
var audit130ChromeKeys = []string{
	"tui.model_view.d010c6a7e3", // ready mode badge
	"tui.model_view.be46ce0c28", // no model
}

func audit130SwitchLanguageByKeys(t *testing.T, m Model, target string) Model {
	t.Helper()
	m = audit130OpenSettingsByTyping(t, m)
	m = audit130MoveToSetting(t, m, "language")
	m = audit130Keys(m, audit130Enter)
	if m.menu.kind != menuLanguage {
		t.Fatalf("Enter on language row did not open the picker: %v", m.menu.kind)
	}
	m = audit130Type(m, uilang.Name(target))
	rows := m.languageRows()
	if len(rows) == 0 || rows[0].Code != target {
		t.Fatalf("filter %q did not find %s: %+v", uilang.Name(target), target, rows)
	}
	m = audit130Keys(m, audit130Enter)
	if m.language != target {
		t.Fatalf("language = %q after picking %s", m.language, target)
	}
	m = audit130Keys(m, audit130Esc) // back from settings
	if m.mode != modeNormal {
		m = audit130Keys(m, audit130Esc)
	}
	return m
}

func TestAudit130LanguageSwitchUpdatesChrome(t *testing.T) {
	for _, c := range []struct{ from, to string }{{"pl", "en"}, {"en", "pl"}} {
		t.Run(c.from+"->"+c.to, func(t *testing.T) {
			restoreRetryGlobals(t)
			dir := t.TempDir()
			m := New(Options{Home: dir, DataDir: dir, Language: c.from})
			out, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
			m = out.(Model)
			m.chat.addAssistant("hello")
			m.markTranscriptPresent()
			m.refreshTranscript()

			m = audit130SwitchLanguageByKeys(t, m, c.to)
			if cfg, _ := config.LoadToml(filepath.Join(dir, "config.toml")); cfg.Language != c.to {
				t.Fatalf("language not persisted: %q", cfg.Language)
			}
			view := ansi.Strip(m.View())
			keys := append([]string{}, audit130ChromeKeys...)
			for _, k := range keys {
				oldText, newText := textFor(c.from, k), textFor(c.to, k)
				if oldText == newText {
					continue
				}
				if !strings.Contains(view, newText) {
					t.Errorf("%s: new chrome %q missing", k, newText)
				}
				if strings.Contains(view, oldText) && !strings.Contains(newText, oldText) {
					t.Errorf("%s: old chrome %q still visible", k, oldText)
				}
			}
			// Hint line and placeholder come from the same switch.
			if hint := ansi.Strip(m.renderDefaultHintLine()); hint == ansi.Strip(func() string {
				mm := m
				mm.language = c.from
				return mm.renderDefaultHintLine()
			}()) {
				t.Errorf("hint line unchanged after switching to %s: %q", c.to, hint)
			}
			if !strings.Contains(view, ansi.Strip(m.renderHintLine())) {
				t.Errorf("rendered hint line is stale (cache):\nwant %q\nview:\n%s", ansi.Strip(m.renderHintLine()), view)
			}
			if m.input.Placeholder != textFor(c.to, "tui.menu_settings_actions.2d53d0e1de") {
				t.Errorf("placeholder %q not in %s", m.input.Placeholder, c.to)
			}
			// Settings panel reopened in the new language.
			m2 := audit130OpenSettingsByTyping(t, m)
			if sv := ansi.Strip(m2.View()); strings.Contains(sv, textFor(c.from, "tui.setting_language.8027f432a7")) &&
				textFor(c.from, "tui.setting_language.8027f432a7") != textFor(c.to, "tui.setting_language.8027f432a7") {
				t.Errorf("settings still show %s label after switch:\n%s", c.from, sv)
			}
		})
	}
}

// After switching to pl, an approval card opened next is fully Polish.
func TestAudit130LanguageSwitchThenApprovalCard(t *testing.T) {
	restoreRetryGlobals(t)
	dir := t.TempDir()
	m := New(Options{Home: dir, DataDir: dir, Language: "en"})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = audit130SwitchLanguageByKeys(t, out.(Model), "pl")
	m = audit130Begin(t, m, audit130ApprovalReq("pl"))
	view := ansi.Strip(m.View())
	for _, english := range []string{"Approval required", "details · Tab choice", "REVERSIBLE", "Tool"} {
		if strings.Contains(view, english) {
			t.Errorf("English chrome %q on pl approval card:\n%s", english, view)
		}
	}
}

func audit130LoadCatalog(t *testing.T, code string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "system", "uilang", "catalogs", code+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Only real printf verbs; "60% lokalnie" is not a verb.
var audit130Verb = regexp.MustCompile(`%[-+#0]*[0-9]*(?:\.[0-9]+)?[vTtbcdoOqxXUeEfFgGsp%]`)

func TestAudit130CatalogParityEnPl(t *testing.T) {
	en, pl := audit130LoadCatalog(t, "en"), audit130LoadCatalog(t, "pl")
	var missingPl, missingEn, verbs []string
	for k, v := range en {
		pv, ok := pl[k]
		if !ok || strings.TrimSpace(pv) == "" {
			missingPl = append(missingPl, k)
			continue
		}
		a, b := audit130Verb.FindAllString(v, -1), audit130Verb.FindAllString(pv, -1)
		if strings.Join(a, ",") != strings.Join(b, ",") {
			verbs = append(verbs, k+": en "+strings.Join(a, ",")+" pl "+strings.Join(b, ","))
		}
	}
	for k := range pl {
		if _, ok := en[k]; !ok {
			missingEn = append(missingEn, k)
		}
	}
	sort.Strings(missingPl)
	sort.Strings(missingEn)
	sort.Strings(verbs)
	t.Logf("en=%d pl=%d keys; missing in pl=%d, missing in en=%d, verb mismatches=%d", len(en), len(pl), len(missingPl), len(missingEn), len(verbs))
	if len(missingPl)+len(missingEn)+len(verbs) > 0 {
		t.Fatalf("missing pl: %v\nmissing en: %v\nverbs: %v", missingPl, missingEn, verbs)
	}
}

// Every catalog key literally referenced from TUI code exists in en and pl.
func TestAudit130TUIKeysExistInCatalogs(t *testing.T) {
	en, pl := audit130LoadCatalog(t, "en"), audit130LoadCatalog(t, "pl")
	files, _ := filepath.Glob("*.go")
	re := regexp.MustCompile(`"((?:tui|approval|update|project|language)\.[A-Za-z0-9_.\-]+)"`)
	seen := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			if !strings.HasSuffix(m[1], ".") {
				seen[m[1]] = f
			}
		}
	}
	var missing []string
	for k, f := range seen {
		if en[k] == "" {
			missing = append(missing, "en:"+k+" ("+f+")")
		}
		if pl[k] == "" {
			missing = append(missing, "pl:"+k+" ("+f+")")
		}
	}
	sort.Strings(missing)
	t.Logf("%d literal catalog keys referenced from TUI code", len(seen))
	if len(missing) > 0 {
		audit130KnownFailing(t, "TUI references catalog keys that only exist in webgui locales (project.stopRun)")
		t.Fatalf("keys used by the TUI but missing from catalogs:\n%s", strings.Join(missing, "\n"))
	}
}

// pl entries identical to en for TUI chrome: likely untranslated. Logged, and
// failing only for long sentences (short tokens such as "OK"/"Esc" are fine).
func TestAudit130PolishTUIStringsTranslated(t *testing.T) {
	en, pl := audit130LoadCatalog(t, "en"), audit130LoadCatalog(t, "pl")
	var same []string
	for k, v := range en {
		if !strings.HasPrefix(k, "tui.") && !strings.HasPrefix(k, "approval.") {
			continue
		}
		if pl[k] == v && len(strings.Fields(v)) >= 4 {
			same = append(same, k+" = "+v)
		}
	}
	sort.Strings(same)
	if len(same) > 0 {
		audit130KnownFailing(t, "some pl TUI strings are identical to English")
		t.Fatalf("%d pl TUI sentences identical to en:\n%s", len(same), strings.Join(same, "\n"))
	}
}

// All other locales: key coverage vs English (fallback hides gaps).
func TestAudit130AllLocalesCoverTUIKeys(t *testing.T) {
	en := audit130LoadCatalog(t, "en")
	var report []string
	for _, l := range uilang.Languages() {
		if l.Code == "en" {
			continue
		}
		cat := audit130LoadCatalog(t, l.Code)
		n := 0
		for k := range en {
			if cat[k] == "" {
				n++
			}
		}
		if n > 0 {
			report = append(report, l.Code+": "+strings.Repeat("", 0)+itoa(n))
		}
	}
	if len(report) > 0 {
		t.Fatalf("locales missing keys (English fallback shown): %v", report)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// "/projects <name>" while a background worker runs shows the status key
// raw because project.stopRun exists only in the web GUI locales.
func TestAudit130ProjectsDuringWorkerShowsRawKey(t *testing.T) {
	audit130KnownFailing(t, "status shows raw catalog key 'project.stopRun'")
	for _, lang := range []string{"pl", "en"} {
		dir := t.TempDir()
		m := New(Options{Home: dir, DataDir: dir, Language: lang})
		out, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		m = out.(Model)
		m.workerViews = append(m.workerViews, workerView{id: "worker-1", agent: "explore", status: "running"})
		out, _ = m.dispatchSlashCommand(SlashCommand{Name: "projects", Args: "other"})
		m = out.(Model)
		if m.statusOverride == "project.stopRun" || strings.Contains(ansi.Strip(m.View()), "project.stopRun") {
			t.Fatalf("%s: raw key shown to the user: status=%q", lang, m.statusOverride)
		}
	}
}
