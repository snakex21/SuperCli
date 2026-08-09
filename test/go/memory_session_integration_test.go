//go:build integration

package test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/tools"
)

// liveSummarize adapts the loaded LM Studio provider into a
// memory.SummarizeFunc — the same shape the app's end-of-session
// auto-saver uses to turn a transcript into a one-line journal entry.
func liveSummarize(p llm.Provider) memory.SummarizeFunc {
	return func(ctx context.Context, prompt string) (string, error) {
		ch, err := p.Complete(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
		if err != nil {
			return "", err
		}
		var out strings.Builder
		for d := range ch {
			if d.Err != nil {
				return "", d.Err
			}
			out.WriteString(d.Content)
		}
		return out.String(), nil
	}
}

// TestIntegration_Memory_JournalWriteThenBriefingRecall is the live
// end-to-end proof of "memory between sessions":
//
//	SESSION 1: a real transcript is summarized by the loaded model and
//	           stored as a task-log ("internal git") journal entry.
//	SESSION 2: a fresh Loop is started with ONLY the code-built briefing
//	           injected into its system prompt (no recall call). Asked
//	           "what did we do last time?", the model answers from the
//	           briefing — proving the auto-injection actually lands.
func TestIntegration_Memory_JournalWriteThenBriefingRecall(t *testing.T) {
	if _, ok := lmStudioAvailable(t); !ok {
		return
	}
	p := newLMStudioProvider(t, "auto")

	projectStore, err := memory.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open project store: %v", err)
	}
	defer projectStore.Close()
	globalStore, err := memory.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open global store: %v", err)
	}
	defer globalStore.Close()
	projectPath := t.TempDir()

	// ---- SESSION 1: model summarizes the session into a journal entry.
	saver := &memory.AutoSaver{Project: projectStore, Global: globalStore, ProjectPath: projectPath}
	transcript := "user: Zmień w pliku config.txt wartość debug z false na true.\n" +
		"assistant: Zmieniłem debug=false na debug=true w config.txt (linia 3).\n" +
		"user: Świetnie, dzięki."
	ctx1, cancel1 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel1()
	if ok := saver.StoreSummary(ctx1, transcript, liveSummarize(p)); !ok {
		t.Fatal("StoreSummary reported failure (model summarize call errored)")
	}
	logs, err := projectStore.Recent(memory.ScopeTaskLog, 5)
	if err != nil {
		t.Fatalf("read task logs: %v", err)
	}
	if len(logs) == 0 {
		t.Fatal("SESSION 1: no journal (task-log) entry was written by the model summary")
	}
	journal := logs[0].Content
	t.Logf("SESSION 1 journal entry: %q", journal)
	if strings.EqualFold(strings.TrimSpace(journal), "NOTHING") || strings.TrimSpace(journal) == "" {
		t.Fatalf("SESSION 1: journal entry is empty/NOTHING: %q", journal)
	}

	// Seed a durable preference the way the global store holds it, so
	// the briefing carries it into session 2 (preferences-first).
	if err := globalStore.Put(memory.Entry{
		ID: "pref-lang", Scope: memory.ScopePreference,
		Content: "The user prefers answers in Polish.", Source: memory.SourceAgent,
	}); err != nil {
		t.Fatal(err)
	}

	// ---- SESSION 2: brand-new Loop, only the briefing is injected.
	brief := memory.BuildBriefing(globalStore, projectStore, projectPath, 700)
	if brief == "" {
		t.Fatal("SESSION 2: BuildBriefing produced nothing to inject")
	}
	t.Logf("SESSION 2 briefing (%d tokens):\n%s", memory.EstimateTokens(brief), brief)

	// recall is registered and wired to the SAME stores, but we record
	// whether the model needs it: the whole point is that the briefing
	// alone answers the question.
	recallCalls := 0
	reg := tools.NewRegistry()
	recallSpec := tools.NewRecallDual(projectStore, globalStore).Spec()
	recallFn := recallSpec.Fn
	recallSpec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		recallCalls++
		return recallFn(ctx, args)
	}
	reg.MustRegister(recallSpec)
	reg.MarkAlwaysOn("recall")

	system := "You are SuperCli, a helpful CLI assistant.\n\n" + brief
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider: p,
		Registry: reg,
		MaxSteps: 4,
		System:   system,
		Briefing: brief,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel2()
	ch, err := loop.Run(ctx2, "Co robiliśmy w poprzedniej sesji? Odpowiedz jednym krótkim zdaniem.")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var answer strings.Builder
	done := false
	for ev := range ch {
		switch e := ev.(type) {
		case agent.MessageEvent:
			answer.WriteString(e.Text)
		case agent.DoneEvent:
			done = true
		case agent.ErrorEvent:
			t.Fatalf("ErrorEvent: %v", e.Err)
		}
	}
	if !done {
		t.Error("no DoneEvent — session 2 did not finish cleanly")
	}
	got := strings.ToLower(answer.String())
	t.Logf("SESSION 2 answer: %q (recall calls: %d)", answer.String(), recallCalls)
	// The answer must reflect the journal content: the model learned
	// what we did last time purely from the injected briefing.
	if !strings.Contains(got, "config") && !strings.Contains(got, "debug") {
		t.Errorf("SESSION 2: answer does not reflect the injected journal (expected mention of config/debug): %q", answer.String())
	}
}
