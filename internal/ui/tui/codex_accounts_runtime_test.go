package tui

import (
	"errors"
	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"testing"
)

type accountBoundarySwapper struct {
	current                  string
	accountCalls, modelCalls int
	fail                     error
}

func (s *accountBoundarySwapper) CurrentModel() string    { return s.current }
func (s *accountBoundarySwapper) SetModel(p llm.Provider) { s.modelCalls++; s.current = p.Name() }
func (s *accountBoundarySwapper) SetAccountProvider(p llm.Provider) error {
	s.accountCalls++
	if s.fail != nil {
		return s.fail
	}
	s.current = p.Name()
	return nil
}
func TestCodexAuthChangeRebuildsOnceWithoutModelHandoff(t *testing.T) {
	before, _ := llm.NewCodex(llm.CodexConfig{Model: "future-codex", Tokens: codexauth.NewManager(t.TempDir(), codexauth.Options{})})
	after, _ := llm.NewCodex(llm.CodexConfig{Model: "future-codex", Tokens: codexauth.NewManager(t.TempDir(), codexauth.Options{})})
	// These transports are never used for inference; the test only checks wiring.
	if before == nil || after == nil {
		t.Fatal("test provider construction")
	}
	swapper := &accountBoundarySwapper{current: before.Name()}
	m := New(Options{NoColor: true})
	m.llm = before
	m.modelSwapper = swapper
	m.activeProvider = "codex"
	m.codexAccountsChanged = true
	var builds int
	m.modelSwapFn = func(model, provider string) (llm.Provider, error) {
		builds++
		if model != "future-codex" || provider != "codex" {
			t.Fatalf("rebuild target %s/%s", provider, model)
		}
		return after, nil
	}
	m.chat.addUser("retained question")
	m.chat.addAssistant("retained answer")
	history := m.chat.render(m.palette)
	if err := m.refreshCodexAccounts(); err != nil {
		t.Fatal(err)
	}
	if m.llm != after || m.codexAccountsChanged || builds != 1 || swapper.accountCalls != 1 || swapper.modelCalls != 0 {
		t.Fatal("auth refresh did not use same-model account boundary")
	}
	if m.chat.render(m.palette) != history {
		t.Fatal("auth refresh changed history")
	}
	if err := m.refreshCodexAccounts(); err != nil {
		t.Fatal(err)
	}
	if builds != 1 {
		t.Fatal("ordinary turn rebuilt account pool")
	}
}
func TestCodexAuthRebuildErrorRetainsSubmittedDraft(t *testing.T) {
	before, _ := llm.NewCodex(llm.CodexConfig{Model: "future-codex", Tokens: codexauth.NewManager(t.TempDir(), codexauth.Options{})})
	m := New(Options{NoColor: true, Agent: scriptedAgent{}})
	m.llm = before
	m.modelSwapper = &accountBoundarySwapper{current: before.Name()}
	m.codexAccountsChanged = true
	m.modelSwapFn = func(string, string) (llm.Provider, error) { return nil, errors.New("no accounts available") }
	next, cmd := m.startPrompt("recover this draft")
	if cmd == nil {
		t.Fatal("expected normal rejected-run recovery")
	}
	started, ok := cmd().(runStartMsg)
	if !ok || started.err == nil {
		t.Fatalf("rebuild error not returned: %+v", started)
	}
	out, _ := next.(Model).Update(started)
	got := out.(Model)
	if got.busy || got.llm != before || !got.codexAccountsChanged || got.input.Value() != "recover this draft" {
		t.Fatal("rebuild error lost draft or ran stale account")
	}
}
