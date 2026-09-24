package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func handoffHistory() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Content: "Improve the CLI. Preserve the Zen dialect."},
		{Role: llm.RoleAssistant, Content: strings.Repeat("older investigation and verified findings\n", 5000)},
		{Role: llm.RoleUser, Content: "Previous correction: keep portable data."},
		{Role: llm.RoleAssistant, Content: "The parser fix is verified."},
	}
}

func TestModelHandoffSharedRunKeepsRecentTurns(t *testing.T) {
	for _, scope := range []string{"lmstudio", "cloud"} {
		t.Run(scope, func(t *testing.T) {
			p := &stubProvider{name: "new", scripts: [][]llm.Delta{{{Content: "Done", FinishReason: "stop"}}}}
			calls := 0
			var summarized []llm.Message
			loop, err := NewLoop(LoopConfig{
				Provider: p, Registry: tools.NewRegistry(), System: "system policy", InitialMessages: handoffHistory(),
				ContextProvider: scope, WindowFor: func(string) int { return 200_000 },
				Summarizer: func(_ context.Context, _ llm.Provider, msgs []llm.Message) (string, error) {
					calls++
					summarized = append([]llm.Message(nil), msgs...)
					return WrapCompactSummary("Goal: improve CLI; preserve Zen. Done: investigation. Pending: current task."), nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			loop.contextModel = contextModelState{loaded: true, provider: "old-backend", model: "old"}
			before := loop.EstimateNextRequestTokens()
			ch, err := loop.Run(context.Background(), "Continue with the selected fix.")
			if err != nil {
				t.Fatal(err)
			}
			var compactions int
			for ev := range ch {
				if e, ok := ev.(AutoCompactEvent); ok {
					compactions++
					if e.Reason != "model-switch" {
						t.Fatalf("reason=%s", e.Reason)
					}
				}
			}
			if calls != 1 || compactions != 1 || len(p.reqs) != 1 {
				t.Fatalf("summaries=%d events=%d main calls=%d", calls, compactions, len(p.reqs))
			}
			wire := p.reqs[0]
			text := RenderCompactTranscript(wire)
			for _, want := range []string{"Previous correction: keep portable data.", "The parser fix is verified.", "Continue with the selected fix."} {
				if !strings.Contains(text, want) {
					t.Fatalf("recent content missing: %q", want)
				}
				if strings.Contains(RenderCompactTranscript(summarized), want) {
					t.Fatalf("recent turn was summarized: %q", want)
				}
			}
			after := llm.EstimateTokens(wire)
			if after >= before/4 {
				t.Fatalf("insufficient replay reduction: %d -> %d", before, after)
			}
			t.Logf("estimated replay tokens: %d -> %d; helper calls: %d", before, after, calls)
			ch, err = loop.Run(context.Background(), "One more small change.")
			if err != nil {
				t.Fatal(err)
			}
			for range ch {
			}
			if calls != 1 {
				t.Fatalf("warm continuation summarized again: %d", calls)
			}
		})
	}
}

func TestModelHandoffNoExtraCallsForShortOrSameModel(t *testing.T) {
	for _, tt := range []struct {
		name     string
		history  []llm.Message
		previous string
	}{
		{"short-switch", []llm.Message{{Role: llm.RoleUser, Content: "old"}, {Role: llm.RoleAssistant, Content: "done"}}, "old"},
		{"warm-long", handoffHistory(), "new"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &stubProvider{name: "new"}
			l, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), InitialMessages: tt.history, WindowFor: func(string) int { return 200_000 },
				Summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
					t.Fatal("unexpected summary")
					return "", nil
				}})
			if err != nil {
				t.Fatal(err)
			}
			l.contextModel = contextModelState{loaded: true, provider: prefillProfileDefaultScope, model: tt.previous}
			ch, err := l.Run(context.Background(), "next")
			if err != nil {
				t.Fatal(err)
			}
			for range ch {
			}
			if len(p.reqs) != 1 {
				t.Fatalf("main requests=%d", len(p.reqs))
			}
		})
	}
}

func TestModelHandoffFailureDoesNotHideOrRepeat(t *testing.T) {
	p := &stubProvider{name: "new"}
	count := 0
	l, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), InitialMessages: append(handoffHistory(), llm.Message{Role: llm.RoleUser, Content: "current"}),
		WindowFor: func(string) int { return 200_000 }, Summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
			count++
			return "", errors.New("offline")
		}})
	if err != nil {
		t.Fatal(err)
	}
	l.contextModel = contextModelState{loaded: true, model: "old", provider: "old"}
	before := append([]llm.Message(nil), l.Messages...)
	l.maybeModelHandoff(context.Background(), nil)
	l.maybeModelHandoff(context.Background(), nil)
	if count != 1 || !reflect.DeepEqual(before, l.VisibleMessages()) {
		t.Fatalf("failed summary changed history or repeated: calls=%d", count)
	}
}

func TestModelHandoffKeepsUnresolvedToolTail(t *testing.T) {
	p := &stubProvider{name: "new"}
	history := append(handoffHistory(),
		llm.Message{Role: llm.RoleUser, Content: "current"},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "live", Name: "shell", Arguments: `{"cmd":"test"}`}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "live", Name: "shell", Content: "FAIL: test needs repair"},
	)
	l, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), InitialMessages: history, WindowFor: func(string) int { return 200_000 },
		Summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
			return WrapCompactSummary("old work"), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	l.maybeModelHandoff(context.Background(), nil)
	if !reflect.DeepEqual(l.Messages[len(l.Messages)-3:], history[len(history)-3:]) {
		t.Fatal("active tool protocol or failure was changed")
	}
}

func TestModelSwitchResetsTokenCalibrationAndThinking(t *testing.T) {
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "a"}, Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	l.recordContextBaseline(100, 9000)
	l.lastThinking = "old private reasoning"
	l.SetModel(&stubProvider{name: "b"})
	if l.contextBaseExact != 0 || l.lastThinking != "" {
		t.Fatal("old model calibration/thinking retained")
	}
	l.recordContextBaseline(100, 9000)
	l.lastThinking = "old connection reasoning"
	l.SetContextProvider("other")
	if l.contextBaseExact != 0 || l.lastThinking != "" {
		t.Fatal("old provider calibration/thinking retained")
	}
	// Clicking back before a request does not manufacture a handoff.
	l.SetModel(&stubProvider{name: "a"})
	l.SetContextProvider("")
	if l.needsModelHandoff(context.Background()) {
		t.Fatal("unused picker change triggered handoff")
	}
}

func TestModelHandoffPersistsProjectionAndExecutedModel(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "old", "handoff")
	if err != nil {
		t.Fatal(err)
	}
	w := session.NewWriter(store, sess.ID)
	history := handoffHistory()
	for _, m := range history {
		if err := w.AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.SaveContextModel(ctx, "cloud", "old"); err != nil {
		t.Fatal(err)
	}
	p := &stubProvider{name: "new", scripts: [][]llm.Delta{{{Content: "Finished", FinishReason: "stop"}}}}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), InitialMessages: history, Writer: w, ContextProvider: "lmstudio", WindowFor: func(string) int { return 200_000 },
		Summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
			return WrapCompactSummary("Goal: preserve Zen. Done: old work."), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := l.Run(ctx, "current")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	provider, model, err := w.ReadContextModel(ctx)
	if err != nil || provider != "lmstudio" || model != "new" {
		t.Fatalf("executed identity=%q/%q, %v", provider, model, err)
	}
	full, err := store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if full[1].Content != history[1].Content {
		t.Fatal("full transcript was lost")
	}
	projected, err := store.ReadModelContext(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if llm.EstimateTokens(projected) >= llm.EstimateTokens(history)/4 {
		t.Fatal("compact projection was not saved")
	}
	resumed, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), InitialMessages: projected, Writer: w, ContextProvider: "lmstudio"})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.needsModelHandoff(ctx) {
		t.Fatal("GUI-style fresh loop treated same model as a switch")
	}
}

func TestCompactionTranscriptBoundsPatchAndKeepsFailureTail(t *testing.T) {
	patch := "HEADER path=main.go " + strings.Repeat("ę世", 20000) + " PATCH-END"
	result := "COMMAND go test " + strings.Repeat("log\n", 20000) + " FAILURE exit 1"
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "Keep this instruction verbatim."},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "patch_file", Arguments: patch}}},
		{Role: llm.RoleTool, ToolCallID: "1", Name: "patch_file", Content: result},
	}
	got := RenderCompactTranscript(msgs)
	if len(got) > 1600 || !utf8.ValidString(got) {
		t.Fatalf("bad bounded transcript: %d bytes", len(got))
	}
	for _, want := range []string{"Keep this instruction verbatim.", "HEADER path=main.go", "PATCH-END", "COMMAND go test", "FAILURE exit 1", "middle omitted"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if string(msgs[1].ToolCalls[0].Arguments) != patch || msgs[2].Content != result {
		t.Fatal("canonical history mutated")
	}
}

func TestModelHandoffProjectionKeepsCurrentImageActive(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "old", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	history := append(handoffHistory(), llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{
		{Type: llm.PartTypeText, Text: "Inspect this image."},
		{Type: llm.PartTypeImage, Image: &llm.ImageRef{MediaType: "image/png", Data: "AAAA", Active: true}},
	}})
	for _, m := range history {
		if err := writer.AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "new"}, Registry: tools.NewRegistry(), Writer: writer, InitialMessages: history, WindowFor: func(string) int { return 200_000 },
		Summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
			return WrapCompactSummary("older work"), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if !l.maybeModelHandoff(ctx, nil) {
		t.Fatal("long history was not compacted")
	}
	image := l.Messages[len(l.Messages)-1].Parts[1].Image
	if image == nil || !image.Active {
		t.Fatal("saving projection deactivated current image before inference")
	}
}

func TestModelHandoffDoesNotReintroduceHiddenInstructions(t *testing.T) {
	history := append([]llm.Message{{Role: llm.RoleUser, Content: "hidden obsolete direction"}, {Role: llm.RoleAssistant, Content: "hidden reply"}}, handoffHistory()...)
	history = append(history, llm.Message{Role: llm.RoleUser, Content: "current"})
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "new"}, Registry: tools.NewRegistry(), InitialMessages: history, WindowFor: func(string) int { return 200_000 },
		Summarizer: func(_ context.Context, _ llm.Provider, msgs []llm.Message) (string, error) {
			if strings.Contains(RenderCompactTranscript(msgs), "hidden obsolete") {
				t.Fatal("hidden history reached summarizer")
			}
			return WrapCompactSummary("current goal"), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.HideRange(0, 2); err != nil {
		t.Fatal(err)
	}
	if !l.maybeModelHandoff(context.Background(), nil) {
		t.Fatal("expected compaction")
	}
}
