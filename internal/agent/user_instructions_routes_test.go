package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestUserInstructionsSurviveLightRoutesAndDisable(t *testing.T) {
	for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify} {
		t.Run(string(route), func(t *testing.T) {
			instructions := "User preferences: preset-A"
			loop, err := NewLoop(LoopConfig{
				Provider: &stubProvider{}, Registry: tools.NewRegistry(), System: "Private coordinator tools and policy",
				UserInstructions: "startup preferences", Briefing: "Remembered user facts",
				UserInstructionsForRun: func(context.Context) (string, error) { return instructions, nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			loop.route = route
			loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleUser, Content: "Fixture conversation"})
			var lastEstimate int
			for _, next := range []string{"User preferences: preset-A", "User preferences: preset-B", ""} {
				instructions = next
				if err := loop.refreshUserInstructionsForRun(context.Background()); err != nil {
					t.Fatal(err)
				}
				messages, priced := loop.prepareProviderMessages(true)
				unpriced, _ := loop.prepareProviderMessages(false)
				if priced != llm.EstimateTokens(messages) || messages[0].Content != unpriced[0].Content {
					t.Fatal("light-route projection and context accounting differ")
				}
				prefix := messages[0].Content
				if strings.Contains(prefix, "Private coordinator") || !strings.Contains(prefix, "Remembered user facts") {
					t.Fatal("light route dropped memory or inherited the full coordinator prompt")
				}
				if next != "" && strings.Count(prefix, next) != 1 {
					t.Fatalf("selected instructions not applied exactly once: %q", prefix)
				}
				for _, old := range []string{"User preferences: preset-A", "User preferences: preset-B", "startup preferences"} {
					if old != next && strings.Contains(prefix, old) {
						t.Fatalf("obsolete instructions on %s: %q", route, prefix)
					}
				}
				estimate := loop.estimateChatRequestTokensRaw()
				if next == "" && estimate >= lastEstimate {
					t.Fatal("disabling instructions did not reduce the light-route estimate")
				}
				lastEstimate = estimate
			}
		})
	}
}

func TestUserInstructionContextSnapshotIsStableWithinRun(t *testing.T) {
	for _, value := range []string{"preset selected before run", ""} {
		t.Run(value, func(t *testing.T) {
			current := value
			var snapshots []string
			registry := tools.NewRegistry()
			registry.MustRegister(tools.Tool{
				Name: "read_snapshot", Description: "Read owning run snapshot", Schema: `{"type":"object"}`, ReadOnly: true,
				Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
					got, ok := UserInstructionsFromContext(ctx)
					if !ok {
						t.Error("tool did not inherit a run snapshot")
					}
					snapshots = append(snapshots, got)
					current = "changed during run"
					return tools.Result{Text: "Snapshot read."}, nil
				},
			})
			provider := &stubProvider{scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "one", Name: "read_snapshot", Arguments: `{"index":1}`}}},
				{{ToolCall: &llm.ToolCall{ID: "two", Name: "read_snapshot", Arguments: `{"index":2}`}}},
				{{Content: "Done.", FinishReason: "stop"}},
			}}
			loop, err := NewLoop(LoopConfig{
				Provider: provider, Registry: registry, System: "base",
				UserInstructionsForRun: func(context.Context) (string, error) { return current, nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Read two distinct snapshots.")) {
				if failure, ok := event.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			if len(snapshots) != 2 || snapshots[0] != value || snapshots[1] != value {
				t.Fatalf("snapshot changed within run: %#v", snapshots)
			}
		})
	}
	if _, ok := UserInstructionsFromContext(context.Background()); ok {
		t.Fatal("plain slash context unexpectedly has a run snapshot")
	}
}
