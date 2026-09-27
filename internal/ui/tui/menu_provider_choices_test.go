package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
)

func TestProviderChooserSharesGUICatalogAndFiltersWithoutCommands(t *testing.T) {
	for _, language := range []string{"en", "pl"} {
		t.Run(language, func(t *testing.T) {
			m := New(Options{Home: t.TempDir(), Language: language, NoColor: true})
			m.input.SetValue("draft stays here")
			m.enterMenu(interactiveMenu{kind: menuProviders})
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if m.menu.kind != menuProviderPredefined {
				t.Fatal("empty provider list needs a visible Enter action")
			}
			rows := m.providerTemplateRows()
			if rows[0].Name != "custom" || !reflect.DeepEqual(rows[1:], providers.PredefinedProviders()) {
				t.Fatal("TUI templates diverged from the catalog returned by the GUI")
			}
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("LM Studio")})
			if rows := m.providerTemplateRows(); len(rows) != 1 || rows[0].Name != "lmstudio" {
				t.Fatalf("name search: %+v", rows)
			}
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if m.menu.kind != menuProviderForm || m.menu.form[2] != "http://localhost:1234/v1" {
				t.Fatal("template did not prefill the correct connection")
			}
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
			if m.menu.kind != menuProviderPredefined || m.menu.filter != "LM Studio" {
				t.Fatal("Back lost provider search")
			}
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
			if m.menu.kind != menuProviders || m.input.Value() != "draft stays here" {
				t.Fatal("Back lost provider list or draft")
			}

			for _, query := range []string{"k", "j", "a", "e", "r", "c", "d"} {
				m.enterMenu(interactiveMenu{kind: menuProviderPredefined})
				m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(query)})
				if m.menu.filter != query || m.menu.kind != menuProviderPredefined {
					t.Fatalf("%q acted as a shortcut instead of a search", query)
				}
			}
			m.menu.filter = "no-provider-has-this-name"
			m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if m.menu.kind != menuProviderPredefined {
				t.Fatal("empty search selected an unrelated provider")
			}
			m.menu.filter = "localhost"
			if rows := m.providerTemplateRows(); len(rows) != 2 {
				t.Fatalf("URL search should find local integrations: %+v", rows)
			}
			if language == "pl" {
				m.menu.filter = "wlasny"
				if rows := m.providerTemplateRows(); len(rows) != 1 || rows[0].Name != "custom" {
					t.Fatal("Polish search should also work without accents")
				}
			}
		})
	}
}

func TestProviderAddRowCannotModifyLastConfiguredProvider(t *testing.T) {
	dir := t.TempDir()
	mgr := providers.NewManager(dir)
	if err := mgr.Add("openai", "openai", "https://api.openai.com/v1", "test-key", "test-model"); err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: dir, DataDir: dir, ProviderMgr: mgr, NoColor: true})
	m.enterMenu(interactiveMenu{kind: menuProviders, cursor: 1})
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("e")},
		{Type: tea.KeyRunes, Runes: []rune("d")},
		{Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune("c")},
	} {
		m = navigateKey(t, m, key)
		if m.menu.kind != menuProviders || len(mgr.Configured()) != 1 || mgr.Configured()[0].Disabled {
			t.Fatal("Add row acted on the last configured provider")
		}
	}
	if m.cursorOnOpenAIRow() {
		t.Fatal("Add row must not inherit OpenAI actions")
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.menu.kind != menuProviderPredefined {
		t.Fatal("Add row did not open chooser")
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.menu.kind != menuProviderForm || m.menu.form[0] != "" || m.menu.form[1] != "auto" || m.menu.form[2] != "" {
		t.Fatal("custom endpoint should start with a blank URL and auto detection")
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	for _, want := range []string{"openai", "responses", "anthropic", "opencode", "auto"} {
		m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRight})
		if m.menu.form[1] != want {
			t.Fatalf("protocol = %q, want %q", m.menu.form[1], want)
		}
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("not-a-protocol")})
	if m.menu.form[1] != "auto" {
		t.Fatal("typing corrupted the protocol selector")
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.menu.form[1] != "opencode" {
		t.Fatal("left arrow should wrap")
	}
}

func TestProviderAutoDetectionRunsOffUIAndSavesResolvedType(t *testing.T) {
	requests := make(chan string, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{}})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	dir := t.TempDir()
	mgr := providers.NewManager(dir)
	m := New(Options{Home: dir, DataDir: dir, ProviderMgr: mgr, CapabilityRegistry: llm.NewCapabilityRegistry()})
	m.enterMenu(interactiveMenu{kind: menuProviders})
	m.enterMenu(interactiveMenu{kind: menuProviderForm, form: []string{"", "auto", server.URL + "/v1", "", ""}, formAt: 4})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || m.menu.formErr == "" || m.menu.formAt != 0 || len(mgr.Names()) != 0 {
		t.Fatal("blank provider name should stay in the form without a request")
	}
	m.menu.form[0], m.menu.formAt = "custom-local", 4
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil || m.menu.providerDetection == nil || len(mgr.Names()) != 0 || len(requests) != 0 {
		t.Fatal("detection must be deferred, without saving auto as a transport")
	}
	detected := cmd().(providerProtocolDetectedMsg)
	if detected.err != nil || detected.typ != "openai" {
		t.Fatalf("passive detection failed: %v, %q", detected.err, detected.typ)
	}
	next, cmd = m.Update(detected)
	m = next.(Model)
	if m.menu.kind != menuProviders || len(mgr.Configured()) != 1 || mgr.Configured()[0].Type != "openai" {
		t.Fatal("resolved provider was not saved")
	}
	var drain func(tea.Cmd)
	drain = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, sub := range msg {
				drain(sub)
			}
		case providerSavedMsg:
			if msg.err != nil {
				t.Fatal(msg.err)
			}
		}
	}
	drain(cmd)
	close(requests)
	for request := range requests {
		if !strings.HasPrefix(request, "GET ") {
			t.Fatalf("empty catalog should not start inference: %s", request)
		}
	}
	mgr.Reload()
	if mgr.Configured()[0].Type != "openai" {
		t.Fatal("resolved transport did not persist")
	}
}

func TestProviderDetectionCancelledFormDoesNotSaveLateResult(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	dir := t.TempDir()
	mgr := providers.NewManager(dir)
	m := New(Options{Home: dir, DataDir: dir, ProviderMgr: mgr})
	m.enterMenu(interactiveMenu{kind: menuProviders})
	m.enterMenu(interactiveMenu{kind: menuProviderForm, form: []string{"cancelled", "auto", server.URL + "/v1", "", ""}, formAt: 4})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("detection did not start")
	}
	m = navigateKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	select {
	case msg := <-result:
		next, cmd = m.Update(msg)
		m = next.(Model)
		if cmd != nil || m.menu.kind != menuProviders || len(mgr.Names()) != 0 {
			t.Fatal("late detection saved or reopened an abandoned form")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Esc did not cancel the pending detection")
	}
}

func TestProviderChooserFitsTerminalAndSupportsMouseWheel(t *testing.T) {
	for _, size := range [][2]int{{120, 32}, {80, 24}, {44, 16}} {
		m := New(Options{Home: t.TempDir(), NoColor: true, Language: "pl"})
		m.width, m.height = size[0], size[1]
		m.enterMenu(interactiveMenu{kind: menuProviderPredefined})
		m.menu.filter = "zen"
		view := m.renderMenuView()
		for _, want := range []string{"Wybierz dostawcę", "OpenCode Zen", "zen", "Enter"} {
			if !strings.Contains(view, want) {
				t.Fatalf("%v missing %q", size, want)
			}
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > size[0] {
				t.Fatalf("%v overflow: %q", size, line)
			}
		}
		if lines := len(strings.Split(view, "\n")); lines > size[1] {
			t.Fatalf("%v renders %d lines", size, lines)
		}
		if output := os.Getenv("SUPERCLI_TUI_PROVIDER_PREVIEW"); output != "" && size[0] == 120 {
			if err := os.WriteFile(filepath.Join(output, "chooser.txt"), []byte(view), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		m.menu.filter = ""
		next, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
		if cmd != nil || next.(Model).menu.cursor != 1 {
			t.Fatal("wheel should move provider selection without starting an operation")
		}
	}
}
