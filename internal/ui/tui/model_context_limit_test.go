package tui

import (
	"testing"

	"supercli/internal/system/config"
)

func TestContextLimitScopesSameModelByActiveProvider(t *testing.T) {
	store := config.LoadModelContextStore(t.TempDir())
	m := New(Options{
		ModelSwapper:      &testModelSwapper{current: "gpt-5.6-sol"},
		ActiveProvider:    "anyrouter",
		ModelContextStore: store,
	})

	_, cmd := m.dispatchSlashCommand(SlashCommand{Name: "context-limit", Args: "100k"})
	if cmd == nil {
		t.Fatal("context-limit returned no result command")
	}
	_ = cmd()
	if got, ok := store.Get("anyrouter", "gpt-5.6-sol"); !ok || got != 100_000 {
		t.Fatalf("AnyRouter override = %d, %v", got, ok)
	}
	if _, ok := store.Get("openai", "gpt-5.6-sol"); ok {
		t.Fatal("AnyRouter override leaked into OpenAI")
	}

	m.activeProvider = "openai"
	_, cmd = m.dispatchSlashCommand(SlashCommand{Name: "context-limit", Args: "1m"})
	_ = cmd()
	if got, ok := store.Get("openai", "gpt-5.6-sol"); !ok || got != 1_000_000 {
		t.Fatalf("OpenAI override = %d, %v", got, ok)
	}
	if got, _ := store.Get("anyrouter", "gpt-5.6-sol"); got != 100_000 {
		t.Fatalf("OpenAI edit changed AnyRouter override to %d", got)
	}

	_, cmd = m.dispatchSlashCommand(SlashCommand{Name: "context-limit", Args: "auto"})
	_ = cmd()
	if _, ok := store.Get("openai", "gpt-5.6-sol"); ok {
		t.Fatal("auto did not remove OpenAI override")
	}
	if got, _ := store.Get("anyrouter", "gpt-5.6-sol"); got != 100_000 {
		t.Fatalf("OpenAI auto changed AnyRouter override to %d", got)
	}
}
