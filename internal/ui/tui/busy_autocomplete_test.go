package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/llm"
)

func TestBusySlashPaletteNavigationAndEscapePreserveRun(t *testing.T) {
	m := keyDispatchTestModel(t)
	m.busy = true
	cancelled := false
	m.cancel.Arm(cancelRun, func() { cancelled = true })
	for _, r := range "/workers" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	if m.autocomp.kind != autocompSlash || m.autocomp.query != "workers" || !strings.Contains(m.View(), "/workers") {
		t.Fatalf("busy palette missing: input=%q popup=%+v", m.input.Value(), m.autocomp)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if !m.busy || cancelled || m.autocomp.kind != autocompNone || m.input.Value() != "/workers" {
		t.Fatal("closing palette cancelled the run or lost the draft")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	if !cancelled {
		t.Fatal("Ctrl+C stopped cancelling")
	}
}

func TestBusySlashHelpStaysLocalAndMutatingCommandIsBlocked(t *testing.T) {
	m := keyDispatchTestModel(t)
	m.busy = true
	called := false
	m.commands = map[string]SlashHandler{"help": func(context.Context, string) (string, error) { called = true; return "HELP_FIXTURE", nil }}
	m.input.SetValue("/help")
	m.updateAutocompleteState()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil || m.input.Value() != "" {
		t.Fatal("help was queued or left in the draft")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !called || !m.busy {
		t.Fatal("local help ended the active run")
	}
	m.input.SetValue("/compact")
	m.updateAutocompleteState()
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !m.busy || !strings.HasPrefix(m.input.Value(), "/compact") {
		t.Fatal("blocked command lost the run/draft")
	}
}

func TestUsageOpensRecordedUsageForAnyNonSubscriptionProvider(t *testing.T) {
	for _, busy := range []bool{false, true} {
		m := keyDispatchTestModel(t)
		m.busy = busy
		m.llm, _ = llm.NewEcho("fixture")
		next, cmd := m.dispatchSlashCommand(SlashCommand{Name: "usage"})
		m = next.(Model)
		if m.mode != modeMenu || m.menu.kind != menuUsage || cmd == nil || m.busy != busy {
			t.Fatal("usage did not open the generic usage view")
		}
	}
}

type subscriptionUsageFixture struct{ llm.Provider }

func (subscriptionUsageFixture) FetchUsage(context.Context) (llm.CodexRateLimits, error) {
	return llm.CodexRateLimits{}, nil
}

func TestUsageRetainsSubscriptionRefreshButBusyRunOpensCachedLimits(t *testing.T) {
	for _, busy := range []bool{false, true} {
		m := keyDispatchTestModel(t)
		m.busy = busy
		echo, _ := llm.NewEcho("subscription-fixture")
		m.llm = subscriptionUsageFixture{echo}
		called := false
		m.commands = map[string]SlashHandler{"usage": func(context.Context, string) (string, error) {
			called = true
			return "subscription limits", nil
		}}
		next, cmd := m.dispatchSlashCommand(SlashCommand{Name: "usage"})
		m = next.(Model)
		if cmd == nil {
			t.Fatal("usage did not return a command")
		}
		if busy {
			if m.mode != modeMenu || m.menu.kind != menuUsage || m.menu.category != 2 || called || !m.busy {
				t.Fatal("busy subscription usage did not open cached account limits")
			}
		} else {
			if m.mode == modeMenu {
				t.Fatal("idle subscription usage lost its existing refresh handler")
			}
			if _, ok := cmd().(slashResultMsg); !ok || !called {
				t.Fatal("idle subscription usage did not call the refresh handler")
			}
		}
	}
}

func TestBusySlashNeverBecomesAModelInterjection(t *testing.T) {
	for _, text := range []string{"/", "/help", "/compact", "/unknown-command"} {
		t.Run(text, func(t *testing.T) {
			m := keyDispatchTestModel(t)
			m.busy = true
			a := &queueingAgent{}
			m.agent = a
			m.input.SetValue(text)
			// A dismissed popup or pasted command follows the direct input path.
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(Model)
			if len(a.queued) != 0 || !m.busy {
				t.Fatalf("slash entered the model queue or ended its run: %v", a.queued)
			}
			if text == "/" && m.autocomp.kind != autocompSlash {
				t.Fatal("bare slash did not reopen suggestions")
			}
		})
	}
}

func TestBusyUsageMenuKeepsCancelHandlerAndDraft(t *testing.T) {
	for _, name := range []string{"usage", "cost"} {
		t.Run(name, func(t *testing.T) {
			m := keyDispatchTestModel(t)
			m.busy = true
			m.llm, _ = llm.NewEcho("fixture")
			m.input.SetValue("keep this unrelated draft")
			cancelled := false
			m.cancel.Arm(cancelRun, func() { cancelled = true })
			next, _ := m.dispatchSlashCommand(SlashCommand{Name: name})
			m = next.(Model)
			if m.mode != modeMenu || m.menu.kind != menuUsage || !m.busy || cancelled {
				t.Fatal("opening usage changed the active run")
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = next.(Model)
			if m.mode != modeNormal || !m.busy || cancelled || m.input.Value() != "keep this unrelated draft" {
				t.Fatal("closing usage changed the run or composer")
			}
			m.cancel.Cancel()
			if !cancelled {
				t.Fatal("usage replaced or disarmed the original cancellation handler")
			}
		})
	}
}

func TestBusySlashSafeTextResultsKeepCancellationAndBlockRunReplacement(t *testing.T) {
	for _, name := range []string{"help", "status", "workers", "update"} {
		t.Run(name, func(t *testing.T) {
			m := keyDispatchTestModel(t)
			m.busy = true
			cancelled := false
			m.cancel.Arm(cancelRun, func() { cancelled = true })
			m.commands = map[string]SlashHandler{name: func(context.Context, string) (string, error) { return "local result", nil }}
			args := ""
			if name == "update" {
				args = "check"
			}
			next, cmd := m.dispatchSlashCommand(SlashCommand{Name: name, Args: args})
			m = next.(Model)
			if cmd == nil {
				t.Fatal("safe command did not run")
			}
			next, _ = m.Update(cmd())
			m = next.(Model)
			if !m.busy || cancelled {
				t.Fatal("local result ended the active run")
			}
			m.cancel.Cancel()
			if !cancelled {
				t.Fatal("local result disarmed the original cancellation handler")
			}
		})
	}
	for _, command := range []SlashCommand{{Name: "compact"}, {Name: "model", Args: "other"}, {Name: "providers"}, {Name: "context"}, {Name: "update", Args: "install"}} {
		t.Run("blocked_"+command.Name, func(t *testing.T) {
			m := keyDispatchTestModel(t)
			m.busy = true
			called, cancelled := false, false
			m.cancel.Arm(cancelRun, func() { cancelled = true })
			m.commands = map[string]SlashHandler{command.Name: func(context.Context, string) (string, error) { called = true; return "", nil }}
			next, _ := m.dispatchSlashCommand(command)
			m = next.(Model)
			if called || !m.busy || cancelled || m.mode != modeNormal {
				t.Fatal("unsafe command overlapped the active run")
			}
			expected := m.tr("tui.slash.busy")
			if command.Name == "update" {
				expected = m.tr("update.busy")
			}
			if m.statusOverride != expected {
				t.Fatalf("command did not take the blocking path: status=%q", m.statusOverride)
			}
			m.cancel.Cancel()
			if !cancelled {
				t.Fatal("blocked command disarmed cancellation")
			}
		})
	}
}

func TestMentionCompletionPreservesDraftAndDoesNotExecuteItsSlashPrefix(t *testing.T) {
	for _, busy := range []bool{false, true} {
		for _, key := range []tea.KeyType{tea.KeyEnter, tea.KeyTab} {
			m := keyDispatchTestModel(t)
			m.busy = busy
			writeFile(t, filepath.Join(m.home, "fixture.go"), "package fixture")
			m.input.SetValue("/help explain this\nzażółć @fix")
			m.updateAutocompleteState()
			if m.autocomp.kind != autocompMention {
				t.Fatal("mention suggestions did not open")
			}
			next, cmd := m.Update(tea.KeyMsg{Type: key})
			m = next.(Model)
			if cmd != nil || m.input.Value() != "/help explain this\nzażółć @fixture.go " || m.busy != busy {
				t.Fatalf("completion changed draft/run or executed its prefix: %q", m.input.Value())
			}
		}
	}
}
