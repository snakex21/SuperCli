package app

// Live warm-KV-cache test against a real llama.cpp server. Skipped
// unless both env vars are set:
//
//	SUPERCLI_LIVE_BASEURL  e.g. http://127.0.0.1:8089/v1
//	SUPERCLI_LIVE_MODEL    e.g. qwen3.5-9b
//
// The server must run with `--slot-save-path <dir>`. The test seeds a
// loop with a sizeable fabricated history, runs one real turn (so the
// server holds the session KV), saves the slot, then resumes the same
// history twice: once cold (slot erased, no restore) and once after a
// restore (slot erased first, so the only possible source is the file).
// The restored resume must report a cache hit on the order of the whole
// history; the cold one must not.
//
// Consistency note (the invariant this feature relies on): SuperCli's
// resumed history is byte-identical append-only up to the last
// generated assistant message (the freshness stamp trails the prompt,
// system messages are demoted in place — see llm/system_demote.go), so
// the restored KV matches the replayed prefix. Any client-side change
// (compaction, model swap) just moves the divergence point earlier;
// llama.cpp re-evals from there, so restore is always safe — at worst
// less profitable.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

func liveRun(t *testing.T, l *agent.Loop, prompt string) agent.Usage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ch, err := l.Run(ctx, prompt)
	if err != nil {
		t.Fatalf("Run(%q): %v", prompt, err)
	}
	var usage agent.Usage
	for ev := range ch {
		switch e := ev.(type) {
		case agent.DoneEvent:
			usage = e.Usage
		case agent.ErrorEvent:
			t.Fatalf("Run(%q): %v", prompt, e.Err)
		}
	}
	return usage
}

// fabricatedHistory builds a deterministic multi-turn conversation body
// (~2.5k tokens) so the test never depends on live generation quality.
func fabricatedHistory() []llm.Message {
	filler := strings.Repeat(
		"Analiza wydajności pokazała, że wąskim gardłem jest prefill promptu, a nie generacja tokenów. ", 8)
	var msgs []llm.Message
	for i := 1; i <= 6; i++ {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("Notatka %d z poprzedniej sesji: %s Zapamiętaj ją.", i, filler)},
			llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("Zapamiętane: notatka %d dotyczy prefillu promptu i wąskiego gardła wydajności.", i)},
		)
	}
	return msgs
}

func TestWarmCache_SlotSaveRestore_Live(t *testing.T) {
	baseURL := os.Getenv("SUPERCLI_LIVE_BASEURL")
	model := os.Getenv("SUPERCLI_LIVE_MODEL")
	if baseURL == "" || model == "" {
		t.Skip("live test: set SUPERCLI_LIVE_BASEURL and SUPERCLI_LIVE_MODEL (llama.cpp with --slot-save-path)")
	}
	ctx := context.Background()

	// Keep generations short and deterministic: the Qwen soft switch
	// rides the trailing freshness stamp (cache-safe, append-only).
	llm.SetThinkingEnabled(false)
	t.Cleanup(func() { llm.SetThinkingEnabled(true) })

	provider, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: baseURL, Model: model})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	sc := llm.NewSlotCache(baseURL, nil)
	if sc == nil {
		t.Fatal("SlotCache not built — is the base URL local?")
	}

	const system = "You are a concise assistant. Always answer in one short sentence."
	newLoop := func(initial []llm.Message) *agent.Loop {
		l, err := agent.NewLoop(agent.LoopConfig{
			Provider:        provider,
			Registry:        tools.NewRegistry(),
			System:          system,
			MaxSteps:        2,
			InitialMessages: initial,
		})
		if err != nil {
			t.Fatalf("NewLoop: %v", err)
		}
		return l
	}

	// Session 1: seeded history + one real turn, then save the slot.
	l1 := newLoop(fabricatedHistory())
	u1 := liveRun(t, l1, "Ile notatek zapamiętałeś? Odpowiedz samą liczbą.")
	t.Logf("session 1:    in=%d cache=%d eval=%d", u1.Input, u1.Cached, u1.Input-u1.Cached)

	const sessID = "livetest-warmcache"
	if n, err := sc.Save(ctx, sessID); err != nil {
		t.Fatalf("slot save: %v (server must run with --slot-save-path)", err)
	} else {
		t.Logf("slot save: n_saved=%d", n)
	}

	// Snapshot the history WITHOUT the leading system run — the resumed
	// loop injects its own identical system prompt (mirrors resume.go).
	all := l1.AllMessages()
	i := 0
	for i < len(all) && all[i].Role == llm.RoleSystem {
		i++
	}
	history := append([]llm.Message(nil), all[i:]...)

	resumePrompt := "Podsumuj temat notatek jednym zdaniem."

	// Cold control: erase the in-RAM slot, resume WITHOUT restore.
	if err := sc.Erase(ctx); err != nil {
		t.Fatalf("slot erase: %v", err)
	}
	cold := liveRun(t, newLoop(history), resumePrompt)
	t.Logf("cold resume:  in=%d cache=%d eval=%d", cold.Input, cold.Cached, cold.Input-cold.Cached)

	// Warm path: erase again (the cold run re-filled the slot), restore
	// from the file, resume the identical history.
	if err := sc.Erase(ctx); err != nil {
		t.Fatalf("slot erase 2: %v", err)
	}
	n, err := sc.Restore(ctx, sessID)
	if err != nil {
		t.Fatalf("slot restore: %v", err)
	}
	t.Logf("slot restore: n_restored=%d", n)
	warm := liveRun(t, newLoop(history), resumePrompt)
	t.Logf("warm resume:  in=%d cache=%d eval=%d", warm.Input, warm.Cached, warm.Input-warm.Cached)

	// The warm resume must hit on the order of the whole history: at
	// least half the prompt (checkpoint granularity and the regenerated
	// final assistant message eat some), and clearly above the cold run.
	if warm.Cached < warm.Input/2 {
		t.Errorf("warm resume cache=%d of in=%d — expected a hit on the order of the whole history", warm.Cached, warm.Input)
	}
	if warm.Cached <= cold.Cached {
		t.Errorf("warm cache=%d not better than cold cache=%d — restore had no effect", warm.Cached, cold.Cached)
	}
}
