package tui

import (
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func restoreRetryGlobals(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { config.ApplyRetryGlobals(config.TomlConfig{}) })
}

func TestSettings_MaxRetriesEditValidatesPersistsAndApplies(t *testing.T) {
	restoreRetryGlobals(t)
	m := newSettingsModel(t, "")
	m = cursorForKey(m, "max_retries")
	if v, src := m.settingValueSource(m.localizedSettingsRows()[m.menu.cursor], m.menu.settingsCfg); src != "default" || !strings.Contains(v, "5") {
		t.Fatalf("default display = %q %q", v, src)
	}
	for _, tc := range []struct {
		input string
		want  int
	}{{"9", 9}, {"0", 0}, {"inf", -1}, {"-1", -1}} {
		mm, _ := m.settingsEnter()
		m = mm.(Model)
		m.menu.editBuf = tc.input
		mm, _ = m.settingsCommitText()
		m = mm.(Model)
		if m.menu.formErr != "" {
			t.Fatalf("%q rejected: %s", tc.input, m.menu.formErr)
		}
		if got := loadCfg(t, m).MaxRetriesSetting(); got != tc.want {
			t.Fatalf("%q persisted as %d", tc.input, got)
		}
		if llm.MaxRetries() != tc.want {
			t.Fatalf("%q not applied live: %d", tc.input, llm.MaxRetries())
		}
	}
	// -1 is shown as the localized unlimited value.
	if v, _ := m.settingValueSource(m.localizedSettingsRows()[m.menu.cursor], m.menu.settingsCfg); !strings.Contains(v, "∞") {
		t.Fatalf("unlimited display = %q", v)
	}
	for _, bad := range []string{"-5", "abc"} {
		mm, _ := m.settingsEnter()
		m = mm.(Model)
		m.menu.editBuf = bad
		mm, _ = m.settingsCommitText()
		m = mm.(Model)
		if m.menu.formErr == "" || loadCfg(t, m).MaxRetriesSetting() != -1 {
			t.Fatalf("%q must be rejected without changing the value", bad)
		}
	}
	// Reset removes the key.
	mm, _ := m.settingsResetCurrent()
	m = mm.(Model)
	if loadCfg(t, m).MaxRetries != nil || llm.MaxRetries() != llm.DefaultMaxRetries {
		t.Fatal("reset must restore the default")
	}
}

func TestSettings_RetryUnknownAndRequestsPerMinute(t *testing.T) {
	restoreRetryGlobals(t)
	m := newSettingsModel(t, "")
	m = cursorForKey(m, "retry_unknown_errors")
	mm, _ := m.settingsEnter()
	m = mm.(Model)
	if !loadCfg(t, m).RetryUnknownErrorsOn() || !llm.RetryUnknownErrors() {
		t.Fatal("retry_unknown_errors toggle on")
	}
	mm, _ = m.settingsEnter()
	m = mm.(Model)
	if loadCfg(t, m).RetryUnknownErrors != nil || llm.RetryUnknownErrors() {
		t.Fatal("second press resets to the default (off)")
	}
	m = cursorForKey(m, "requests_per_minute")
	mm, _ = m.settingsEnter()
	m = mm.(Model)
	m.menu.editBuf = "30"
	mm, _ = m.settingsCommitInt()
	m = mm.(Model)
	if loadCfg(t, m).RequestsPerMinute != 30 || llm.RequestsPerMinuteFor("https://any.example") != 30 {
		t.Fatal("requests_per_minute not persisted/applied")
	}
}

func TestLocalizeRetryNotices(t *testing.T) {
	m := newSettingsModel(t, "")
	m.language = "pl"
	for in, want := range map[string]string{
		"gpt-x: provider error (HTTP 503) — retrying in 8s (retry 3/5)":                 "gpt-x: błąd dostawcy AI (HTTP 503) — ponawianie za 8s (ponowienie 3/5)",
		"gpt-x: rate limited (HTTP 429) — retrying in 30s (retry 12/∞)":                 "gpt-x: limit zapytań (HTTP 429) — ponawianie za 30s (ponowienie 12/∞)",
		"Model connection unavailable — waiting 4s before retry 2/5.":                   "Brak połączenia z modelem — czekam 4s przed ponowieniem 2/5.",
		"connection to the model dropped (unexpected EOF) — retrying in 1s (retry 1/5)": "Połączenie z modelem zostało przerwane (unexpected EOF) — ponawianie za 1s (ponowienie 1/5)",
		"gw.example: request limit 50/min reached — waiting 1.2s":                       "gw.example: osiągnięto limit 50 zapytań/min — czekam 1.2s",
		"something else": "something else",
	} {
		if got := m.localizeNotice(in); got != want {
			t.Errorf("localizeNotice(%q) = %q, want %q", in, got, want)
		}
	}
	m.language = "en"
	in := "gpt-x: provider error (HTTP 503) — retrying in 8s (retry 3/5)"
	if got := m.localizeNotice(in); got != in {
		t.Fatalf("English must round-trip: %q", got)
	}
}

func TestLocalizeToolsUnsupportedNotice(t *testing.T) {
	m := newSettingsModel(t, "")
	m.language = "pl"
	in := `model "@cf/meta/llama-3.1-8b-instruct" does not support tool calling on this endpoint; continuing without tools (answers only) — pick a function-calling model for agent work`
	if got := m.localizeNotice(in); !strings.Contains(got, "nie obsługuje wywoływania narzędzi") || !strings.Contains(got, "@cf/meta/llama-3.1-8b-instruct") {
		t.Fatalf("localized = %q", got)
	}
}
