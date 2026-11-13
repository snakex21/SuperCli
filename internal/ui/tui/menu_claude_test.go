package tui

import (
	"context"
	"testing"
)

func TestClaudeProviderChoiceDispatchesAccountLogin(t *testing.T) {
	called := false
	m := New(Options{Home: t.TempDir(), NoColor: true, Commands: map[string]SlashHandler{
		"login": func(_ context.Context, args string) (string, error) {
			called = true
			if args != "claude" {
				t.Errorf("login args: %q", args)
			}
			return "signed in", nil
		},
	}})
	m.enterMenu(interactiveMenu{kind: menuProviderPredefined, filter: "claude"})
	rows := m.providerTemplateRows()
	for i, row := range rows {
		if row.Type == "claude" {
			m.menu.cursor = i
			break
		}
	}
	next, cmd := m.menuEnter()
	if next.(Model).menu.kind == menuProviderForm || cmd == nil {
		t.Fatal("Claude choice opened an API key form")
	}
	cmd()
	if !called {
		t.Fatal("Claude login not dispatched")
	}
}
