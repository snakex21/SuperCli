package tui

import (
	"encoding/json"
	"fmt"
	"io"
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

	"supercli/internal/llm/providers"
	"supercli/internal/system/uilang"
)

func firstRunChoice(t *testing.T, m onboardModel, name string) (onboardModel, tea.Cmd) {
	t.Helper()
	for i, choice := range m.filteredChoices() {
		if choice.provider.Name == name || choice.kind == name {
			m.cursor = i
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			return next.(onboardModel), cmd
		}
	}
	t.Fatalf("first-run choice %q missing", name)
	return m, nil
}

func TestOnboardingSharesAllProviderTemplatesWithoutDuplicates(t *testing.T) {
	for _, detected := range [][]providers.LocalServer{nil, {
		{Name: "ollama", Label: "Ollama", Type: "openai", BaseURL: "http://localhost:11434/v1", Models: []string{"qwen"}},
		{Name: "lmstudio", Label: "LM Studio", Type: "openai", BaseURL: "http://localhost:1234/v1", Models: []string{"qwen"}},
	}} {
		for _, language := range []string{"en", "pl"} {
			choices := buildChoices(detected, language)
			byName := map[string]onboardChoice{}
			for _, choice := range choices {
				name := choice.provider.Name
				if name == "" {
					continue // offline mode is not an API template
				}
				if _, exists := byName[name]; exists {
					t.Fatalf("duplicate provider %s", name)
				}
				byName[name] = choice
			}
			for _, template := range providers.PredefinedProviders() {
				template.Desc = uilang.ProviderDescription(language, template.Name, template.Desc)
				choice, ok := byName[template.Name]
				if !ok || choice.provider != template {
					t.Fatalf("first-run diverged from GUI catalog for %s", template.Name)
				}
				m := onboardModel{step: onboardMenu, choices: choices, language: language}
				selected, cmd := firstRunChoice(t, m, template.Name)
				if selected.result.Name != template.Name || selected.result.Type != template.Type || selected.result.BaseURL != template.BaseURL {
					t.Fatalf("template fields lost for %s: %+v", template.Name, selected.result)
				}
				if choice.kind != "local-manual" && cmd != nil {
					t.Fatalf("selecting %s unexpectedly started a request", template.Name)
				}
				selected.cancelRequest()
			}
			if len(choices) != len(providers.PredefinedProviders())+2 {
				t.Fatalf("catalog size %d; want templates + custom + offline", len(choices))
			}
			for i, server := range detected {
				if choices[i].local == nil || choices[i].local.Name != server.Name {
					t.Fatal("detected local servers should be first")
				}
			}
		}
	}
}

func TestOnboardingSearchNavigationAndSpecialChoices(t *testing.T) {
	m := onboardModel{step: onboardMenu, choices: buildChoices(nil, "pl"), language: "pl"}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = next.(onboardModel)
	if cmd != nil || m.filter != "k" || m.step != onboardMenu {
		t.Fatal("letters should search instead of navigating")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(onboardModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("LM Studio")})
	m = next.(onboardModel)
	if rows := m.filteredChoices(); len(rows) != 1 || rows[0].provider.Name != "lmstudio" {
		t.Fatalf("search result: %+v", rows)
	}
	m.filter = "nonexistent-provider"
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || next.(onboardModel).step != onboardMenu {
		t.Fatal("empty search should not select anything")
	}
	m.filter = ""
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(onboardModel)
	if m.cursor != len(m.choices)-1 {
		t.Fatal("End did not reach final choice")
	}
	next, _ = m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if next.(onboardModel).cursor != m.cursor-1 {
		t.Fatal("mouse wheel did not navigate")
	}
	m, cmd = firstRunChoice(t, m, "custom")
	if cmd != nil || m.step != onboardURL || m.result.Type != "auto" {
		t.Fatal("custom endpoint should auto-detect its protocol")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(onboardModel)
	m, cmd = firstRunChoice(t, m, "openai")
	if cmd != nil || m.step != onboardAuthMethod {
		t.Fatal("OpenAI auth selection missing")
	}
	oauth, quit := m.chooseAuth()
	if oauth.(onboardModel).result.AuthMethod != AuthChatGPT || quit == nil {
		t.Fatal("ChatGPT sign-in should still exit to the OAuth flow")
	}
	m.cursor = 1
	api, cmd := m.chooseAuth()
	if api.(onboardModel).step != onboardKey || cmd != nil {
		t.Fatal("OpenAI API-key path missing")
	}
	m.step, m.filter = onboardMenu, ""
	echo, quit := firstRunChoice(t, m, "echo")
	if echo.result.Type != "echo" || echo.step != onboardDone || quit == nil {
		t.Fatal("offline mode missing")
	}
}

func TestOnboardingCatalogFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{120, 32}, {80, 24}, {44, 16}, {20, 7}} {
		m := onboardModel{step: onboardMenu, choices: buildChoices(nil, "pl"), language: "pl", filter: "zen"}
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(onboardModel)
		view := m.View()
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > size[0] {
				t.Fatalf("screen %v overflows: %q", size, line)
			}
		}
		if lines := len(strings.Split(view, "\n")); lines > size[1] {
			t.Fatalf("screen %v has %d lines", size, lines)
		}
		if size[0] >= 44 && (!strings.Contains(view, "OpenCode Zen") || !strings.Contains(view, "Enter")) {
			t.Fatalf("provider or navigation missing on %v", size)
		}
		if output := os.Getenv("SUPERCLI_ONBOARD_PREVIEW"); output != "" && size[0] == 120 {
			if err := os.WriteFile(filepath.Join(output, "first-run.txt"), []byte(view), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestOnboardingAnthropicUsesNativeDiscoveryAndVerification(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint("rejected=", reject), func(t *testing.T) {
			requests := make(chan string, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.Method + " " + r.URL.Path
				if r.Header.Get("x-api-key") != "fixture-key" || r.Header.Get("anthropic-version") == "" {
					t.Error("missing native Anthropic headers")
				}
				switch r.URL.Path {
				case "/v1/models":
					fmt.Fprint(w, "{\"data\":[{\"id\":\"claude-fixture\",\"type\":\"model\"}]}")
				case "/v1/messages":
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if payload["model"] != "claude-fixture" {
						t.Error("selected model not used for verification")
					}
					if reject {
						w.WriteHeader(http.StatusUnauthorized)
						fmt.Fprint(w, "{\"error\":{\"type\":\"authentication_error\",\"message\":\"invalid API key\"}}")
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\n")
					fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			m := onboardModel{step: onboardMenu, choices: buildChoices(nil)}
			m, _ = firstRunChoice(t, m, "anthropic")
			m.result.BaseURL = server.URL + "/v1"
			m.input = "fixture-key"
			if strings.Contains(m.View(), "fixture-key") {
				t.Fatal("API key is visible")
			}
			next, load := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(onboardModel)
			next, _ = m.Update(load())
			m = next.(onboardModel)
			if m.step != onboardModels || !reflect.DeepEqual(m.models, []string{"claude-fixture"}) {
				t.Fatalf("native models did not load: %s", m.errMsg)
			}
			next, verify := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(onboardModel)
			next, quit := m.Update(verify())
			m = next.(onboardModel)
			if reject {
				if m.step != onboardMenu || m.errMsg == "" || quit != nil {
					t.Fatal("rejected credentials completed onboarding")
				}
			} else if m.step != onboardDone || m.result.Type != "anthropic" || quit == nil {
				t.Fatalf("native verification failed: %s", m.errMsg)
			}
			close(requests)
			var got []string
			for request := range requests {
				got = append(got, request)
			}
			if want := []string{"GET /v1/models", "POST /v1/messages"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("requests %v; want %v", got, want)
			}
		})
	}
}

func TestOnboardingDiscoveryResolvesAutoAndFiltersFreeCatalogs(t *testing.T) {
	for _, tc := range []struct {
		name, typ string
		want      []string
	}{
		{"custom", "auto", []string{"paid-fixture", "fixture-free"}},
		{"kilo", "openai", []string{"fixture-free"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Errorf("discovery sent %s %s", r.Method, r.URL.Path)
				}
				fmt.Fprint(w, "{\"object\":\"list\",\"data\":[{\"id\":\"paid-fixture\",\"isFree\":false},{\"id\":\"fixture-free\",\"isFree\":true}]}")
			}))
			defer server.Close()
			m := onboardModel{result: OnboardResult{Name: tc.name, Type: tc.typ, BaseURL: server.URL + "/v1"}}
			next, cmd := m.startLoadModels()
			m = next.(onboardModel)
			next, _ = m.Update(cmd())
			m = next.(onboardModel)
			if m.step != onboardModels || m.result.Type != "openai" || !reflect.DeepEqual(m.models, tc.want) {
				t.Fatalf("discovery = type %s models %v error %s", m.result.Type, m.models, m.errMsg)
			}
		})
	}
}

func TestOnboardingIgnoresCancelledDiscoveryAndVerification(t *testing.T) {
	for _, verify := range []bool{false, true} {
		t.Run(fmt.Sprint("verify=", verify), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				_ = r.Body.Close()
				close(started)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			m := onboardModel{choices: buildChoices(nil), result: OnboardResult{
				Name: "old", Type: "openai", BaseURL: server.URL + "/v1", Model: "fixture",
			}}
			var next tea.Model
			var cmd tea.Cmd
			if verify {
				next, cmd = m.startVerify()
			} else {
				next, cmd = m.startLoadModels()
			}
			m = next.(onboardModel)
			completed := make(chan tea.Msg, 1)
			go func() { completed <- cmd() }()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("request did not start")
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = next.(onboardModel)
			m, _ = firstRunChoice(t, m, "echo")
			select {
			case msg := <-completed:
				next, cmd = m.Update(msg)
				m = next.(onboardModel)
				if cmd != nil || m.step != onboardDone || m.result.Type != "echo" {
					t.Fatal("cancelled request changed a later selection")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Esc did not cancel the network request")
			}
		})
	}
}

func TestOnboardingEmptyMessagesCannotAdvanceSetup(t *testing.T) {
	m := onboardModel{step: onboardMenu}
	for _, msg := range []tea.Msg{onboardModelsMsg{}, onboardVerifyMsg{}} {
		next, cmd := m.Update(msg)
		if cmd != nil || next.(onboardModel).step != onboardMenu {
			t.Fatal("a stale or missing request completed setup")
		}
	}
	// Explicit skip must still leave setup unconfigured.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !next.(onboardModel).aborted || cmd == nil {
		t.Fatal("Escape did not skip first-run setup")
	}
}
