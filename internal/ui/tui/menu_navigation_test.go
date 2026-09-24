package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/storage/session"
)

func navigateKey(t *testing.T, m Model, key tea.KeyMsg) Model {
	t.Helper()
	next, cmd := m.Update(key)
	if cmd != nil {
		t.Fatalf("navigation %q unexpectedly started an operation", key.String())
	}
	return next.(Model)
}

func TestActionNavigationRetainsSelectionFilterAndDraft(t *testing.T) {
	for _, language := range []string{"pl", "en"} {
		t.Run(language, func(t *testing.T) {
			m := New(Options{Home: t.TempDir(), NoColor: true, Language: language})
			m.input.SetValue("nie wysyłaj jeszcze\ndruga linia")
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlK})
			for i := 0; i < 2; i++ {
				m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRight})
			}
			if rows := m.filteredActionRows(); len(rows) != 6 || rows[0].id != "model" {
				t.Fatalf("Model category: %+v", rows)
			}
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("model")})
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if m.menu.kind != menuModelCatalog || m.input.Focused() {
				t.Fatal("Enter should open the selected model catalog and blur chat")
			}
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
			if m.mode != modeMenu || m.menu.kind != menuActions || m.menu.category != 2 || m.menu.cursor != 1 || m.menu.filter != "model" {
				t.Fatalf("Esc lost parent state: %+v", m.menu)
			}
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlK})
			if m.mode != modeNormal || m.menu.parent != nil || !m.input.Focused() || m.input.Value() != "nie wysyłaj jeszcze\ndruga linia" {
				t.Fatal("returning to chat lost the draft, focus or navigation reset")
			}
		})
	}
}

func TestProviderFormBackTracksActualPathWithoutProbes(t *testing.T) {
	m := New(Options{Home: t.TempDir(), NoColor: true})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Providers")})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.menu.kind != menuProviders {
		t.Fatal("providers not opened")
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	for i, p := range providers.PredefinedProviders() {
		if p.Name == "openai" {
			m.menu.cursor = i
			break
		}
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.menu.kind != menuProviderForm {
		t.Fatal("API key form not opened")
	}
	for _, want := range []menuKind{menuOpenAIAuth, menuProviderPredefined, menuProviders, menuActions} {
		m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		if m.menu.kind != want || m.mode != modeMenu {
			t.Fatalf("back reached %v, want %v", m.menu.kind, want)
		}
		if want == menuOpenAIAuth && m.menu.cursor != 1 {
			t.Fatal("auth method selection was lost")
		}
	}
	if m.menu.filter != "Providers" {
		t.Fatal("action search was lost")
	}
}

func TestEscapeCancelsInlineEditBeforeLeavingSettings(t *testing.T) {
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), NoColor: true})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Settings")})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRight})
	for i, row := range m.localizedSettingsRows() {
		if row.kind == setInt {
			m.menu.cursor = i
			break
		}
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("12")})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.menu.kind != menuSettings || m.menu.editing || m.menu.editBuf != "" {
		t.Fatal("first Esc should cancel only the unfinished value")
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.menu.kind != menuActions || m.menu.filter != "Settings" {
		t.Fatal("second Esc should restore the action centre")
	}
}

func TestModelSearchDoesNotSwallowFirstLetter(t *testing.T) {
	registry := llm.NewCapabilityRegistry()
	for _, id := range []string{"jamba", "kimi", "qwen"} {
		registry.Register(llm.ModelInfo{ID: id, Provider: "local"})
	}
	for _, kind := range []menuKind{menuModels, menuModelCatalog, menuProviderModels} {
		for _, letter := range []string{"j", "k"} {
			m := New(Options{Home: t.TempDir(), CapabilityRegistry: registry})
			m.enterMenu(interactiveMenu{kind: kind})
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(letter)})
			if m.menu.filter != letter || len(m.filteredModelRows()) != 1 {
				t.Fatalf("kind=%v letter=%s filter=%q", kind, letter, m.menu.filter)
			}
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyUp})
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
			if m.menu.filter != letter || m.menu.cursor != 0 {
				t.Fatal("arrows should navigate without changing the filter")
			}
		}
	}
}

func TestColdModelPickerRemainsResponsiveDuringDiscovery(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce, startedOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": "kimi-local"}}})
	}))
	defer server.Close()
	defer unblock()
	home := t.TempDir()
	manager := providers.NewManager(home)
	if err := manager.Add("local", "openai", server.URL+"/v1", "", ""); err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: home, ProviderMgr: manager, CapabilityRegistry: llm.NewCapabilityRegistry()})
	type opened struct {
		model Model
		cmd   tea.Cmd
	}
	ready := make(chan opened, 1)
	go func() { next, cmd := m.openModelsMenu(); ready <- opened{next.(Model), cmd} }()
	var result opened
	select {
	case result = <-ready:
	case <-started:
		t.Fatal("opening a menu must not do network I/O in Update")
	case <-time.After(3 * time.Second):
		t.Fatal("opening the picker blocked")
	}
	if result.cmd == nil || !result.model.modelPickerScanning {
		t.Fatal("cold inventory should schedule discovery")
	}
	m = result.model
	done := make(chan tea.Msg, 1)
	go func() { done <- result.cmd() }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("discovery did not start")
	}
	viewReady := make(chan string, 1)
	go func(current Model) { viewReady <- current.View() }(m)
	select {
	case view := <-viewReady:
		if !strings.Contains(view, "scanning") {
			t.Fatal("pending discovery has no visible status")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rendering blocked on provider discovery")
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("kimi")})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeNormal {
		t.Fatal("Esc should leave while the provider is still responding")
	}
	next, duplicate := m.openModelCatalogMenu()
	m = next.(Model)
	if duplicate != nil {
		t.Fatal("reopening must not start a duplicate scan")
	}
	unblock()
	select {
	case msg := <-done:
		next, _ = m.Update(msg)
		m = next.(Model)
	case <-time.After(3 * time.Second):
		t.Fatal("completed discovery did not reach the UI")
	}
	if m.modelPickerScanning || len(m.filteredModelRows()) != 1 {
		t.Fatalf("models not delivered: %+v", m.filteredModelRows())
	}
	if !strings.Contains(m.View(), "kimi-local") {
		t.Fatal("the current menu did not render the discovered model")
	}
}

func TestNavigationPanelsFitPopulatedSmallTerminals(t *testing.T) {
	for _, language := range []string{"pl", "en"} {
		for _, size := range [][2]int{{48, 12}, {48, 18}, {64, 16}, {80, 24}, {120, 40}} {
			for _, kind := range []menuKind{menuActions, menuModels, menuSessions, menuSettings} {
				t.Run(fmt.Sprintf("%s/%dx%d/%d", language, size[0], size[1], kind), func(t *testing.T) {
					registry := llm.NewCapabilityRegistry()
					var sessions []session.Session
					for i := 0; i < 35; i++ {
						registry.Register(llm.ModelInfo{ID: fmt.Sprintf("long-model-name-%02d", i), Provider: "local"})
						sessions = append(sessions, session.Session{Title: fmt.Sprintf("Earlier work %02d", i), Model: strings.Repeat("model", 12), MessageCount: 25})
					}
					m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), NoColor: true, Language: language, CapabilityRegistry: registry})
					m.width, m.height = size[0], size[1]
					m.enterMenu(interactiveMenu{kind: menuActions})
					if kind != menuActions {
						m.enterMenu(interactiveMenu{kind: kind, sessions: sessions})
					}
					for _, key := range []tea.KeyType{tea.KeyHome, tea.KeyEnd} {
						m = navigateKey(t, m, tea.KeyMsg{Type: key})
						view := m.View()
						assertLinesFit(t, view, m.width)
						if lines := lipgloss.Height(view); lines > m.height {
							t.Fatalf("height=%d exceeds %d:\n%s", lines, m.height, view)
						}
						if !strings.Contains(view, "Esc") {
							t.Fatal("return shortcut is hidden")
						}
					}
				})
			}
		}
	}
}

// Completing a provider form should never leave that submitted form on Esc's
// path, which could expose an old API key or accidentally resubmit changes.
func TestProviderSaveReturnsPastCompletedForms(t *testing.T) {
	m := New(Options{Home: t.TempDir(), NoColor: true})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Providers")})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	for i, p := range providers.PredefinedProviders() {
		if p.Name != "openai" {
			m.menu.cursor = i
			break
		}
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.menu.kind != menuProviderForm {
		t.Fatal("expected provider form")
	}
	m.menu.formAt = len(m.menu.form) - 1
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.menu.kind != menuProviders {
		t.Fatal("expected provider list after submit")
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.menu.kind != menuActions || m.menu.filter != "Providers" {
		t.Fatal("completed form stayed on the return path")
	}
}
