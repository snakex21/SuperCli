package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestSystemForRunReplacesPresetOncePerRunWithoutGrowingPrefix(t *testing.T) {
	for _, thin := range []bool{false, true} {
		name := "native"
		if thin {
			name = "thin-hoisted"
		}
		t.Run(name, func(t *testing.T) {
			registry := tools.NewRegistry()
			registry.MustRegister(tools.Tool{
				Name: "read_fixture", Description: "Read fixture", ReadOnly: true,
				Schema: `{"type":"object"}`,
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					return tools.Result{Text: "fixture"}, nil
				},
			})
			preset, refreshes := "preset-A", 0
			var prefixes []string
			provider := &stubProvider{name: "system-refresh", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "r", Name: "read_fixture", Arguments: `{}`}}},
				{{Content: "Done.", FinishReason: "stop"}},
			}}
			provider.onCalled = func(call int) {
				prefixes = append(prefixes, provider.reqs[call][0].Content)
				// A save during the first model step must not change the next
				// step of that run, only the following user run.
				if call == 0 {
					preset = "preset-B"
				}
			}
			loop, err := NewLoop(LoopConfig{
				Provider: provider, Registry: registry, System: "base", ThinTools: thin,
				StableToolset: thin, CatalogHoist: thin,
				UserInstructionsForRun: func(context.Context) (string, error) {
					refreshes++
					return preset, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			for run := 0; run < 3; run++ {
				if run == 2 {
					preset = ""
				}
				for _, event := range drainEvents(t, mustRun(t, loop, "Inspect fixture.")) {
					if failure, ok := event.(ErrorEvent); ok {
						t.Fatal(failure.Err)
					}
				}
			}
			if refreshes != 3 || len(prefixes) != 4 {
				t.Fatalf("refreshes=%d model calls=%d", refreshes, len(prefixes))
			}
			for i, want := range []string{"preset-A", "preset-A", "preset-B", ""} {
				if want != "" && strings.Count(prefixes[i], want) != 1 {
					t.Fatalf("request %d prefix=%q, want one %q", i, prefixes[i], want)
				}
				for _, old := range []string{"preset-A", "preset-B"} {
					if old != want && strings.Contains(prefixes[i], old) {
						t.Fatalf("request %d retained obsolete %q", i, old)
					}
				}
			}
			if loop.system != "base" || loop.Messages[0].Content != "base" {
				t.Fatal("disabled preset was not removed from the base system")
			}
			for _, msg := range loop.Messages[1:] {
				if strings.Contains(msg.Content, "preset-A") || strings.Contains(msg.Content, "preset-B") {
					t.Fatal("instruction text leaked into dialogue history")
				}
			}
		})
	}
}

func TestSystemForRunOnlyInvalidatesCachesWhenChanged(t *testing.T) {
	current := ""
	loop, err := NewLoop(LoopConfig{
		Provider: &stubProvider{}, Registry: tools.NewRegistry(), System: "base",
		UserInstructionsForRun: func(context.Context) (string, error) { return current, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleSystem, Content: "pattern injection"}, llm.Message{Role: llm.RoleUser, Content: "dialogue"})
	loop.EstimateVisibleTokens()
	epoch, count := loop.sourceEvidenceEpoch, loop.visibleEstimateCount
	if err := loop.refreshUserInstructionsForRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loop.sourceEvidenceEpoch != epoch || loop.visibleEstimateCount != count {
		t.Fatal("unchanged prompt invalidated the token cache")
	}
	current = strings.Repeat("long instructions ", 4000)
	if err := loop.refreshUserInstructionsForRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loop.sourceEvidenceEpoch == epoch || loop.visibleEstimateCount != 0 || loop.EstimateVisibleTokens() <= 1000 {
		t.Fatal("changed prompt retained a stale context estimate")
	}
	if len(loop.Messages) != 3 || loop.Messages[1].Content != "pattern injection" || loop.Messages[2].Content != "dialogue" {
		t.Fatal("refresh changed unrelated system layers or dialogue slots")
	}
	loop.LoadConversation([]llm.Message{{Role: llm.RoleUser, Content: "resumed"}})
	current = "new preset after resume"
	if err := loop.refreshUserInstructionsForRun(context.Background()); err != nil || loop.Messages[0].Content != systemWithUserInstructions("base", current) || loop.Messages[2].Content != "resumed" {
		t.Fatalf("resume refresh: %v, messages=%+v", err, loop.Messages)
	}
}

func TestSystemForRunFailureAndCancellationReleaseOwnership(t *testing.T) {
	for _, mode := range []string{"error", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			fail := true
			provider := &stubProvider{scripts: [][]llm.Delta{{{Content: "Done.", FinishReason: "stop"}}}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			loop, err := NewLoop(LoopConfig{
				Provider: provider, Registry: tools.NewRegistry(), System: "original",
				UserInstructionsForRun: func(context.Context) (string, error) {
					if fail {
						switch mode {
						case "error":
							return "", errors.New("unreadable instructions")
						case "canceled":
							cancel()
						}
					}
					return "updated", nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := loop.Run(ctx, "Inspect fixture."); err == nil {
				t.Fatal("failed refresh accepted")
			}
			if loop.sessionBusy.Load() || len(provider.reqs) != 0 || len(loop.Messages) != 1 || loop.system != "original" {
				t.Fatal("failed refresh leaked run ownership, a model call or partial prompt state")
			}
			fail = false
			drainEvents(t, mustRun(t, loop, "Retry inspection."))
			if len(provider.reqs) != 1 || loop.system != "original\n\nupdated" {
				t.Fatal("retry did not pick up the corrected prompt")
			}
		})
	}
}

func TestSystemForRunRequiresLeadingBasePrompt(t *testing.T) {
	if _, err := NewLoop(LoopConfig{
		Provider: &stubProvider{}, Registry: tools.NewRegistry(),
		UserInstructionsForRun: func(context.Context) (string, error) { return "updated", nil },
	}); err == nil {
		t.Fatal("refresh without a base system was accepted")
	}
}
