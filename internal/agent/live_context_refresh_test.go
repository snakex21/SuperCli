package agent

import (
	"context"
	"errors"
	"strings"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"testing"
)

func TestRunContextReplacesGoalWithoutGrowingHistory(t *testing.T) {
	for _, thin := range []bool{false, true} {
		name := "native"
		if thin {
			name = "sentinel"
		}
		t.Run(name, func(t *testing.T) {
			value := "[current_goal]first-goal[/current_goal]"
			provider := &stubProvider{name: "fresh-goal", scripts: [][]llm.Delta{{{Content: "Done.", FinishReason: "stop"}}, {{Content: "Done.", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), ThinTools: thin, System: "Stable base", LiveContextForRun: func(context.Context) (string, error) { return value, nil }})
			if err != nil {
				t.Fatal(err)
			}
			drainEvents(t, mustRun(t, loop, "first status"))
			value = "[current_goal]second-goal[/current_goal]"
			drainEvents(t, mustRun(t, loop, "second status"))
			for i, expected := range []string{"first-goal", "second-goal"} {
				var content strings.Builder
				for _, m := range provider.reqs[i] {
					content.WriteString(m.Content)
				}
				if !strings.Contains(content.String(), expected) || (i == 1 && strings.Contains(content.String(), "first-goal")) {
					t.Fatalf("request %d: %s", i, content.String())
				}
			}
			for _, m := range loop.Messages {
				if strings.Contains(m.Content, "[current_goal]") {
					t.Fatalf("transient goal persisted: %+v", m)
				}
			}
		})
	}
}

func TestRunContextFailureReleasesRunOwnership(t *testing.T) {
	fail := true
	provider := &stubProvider{name: "retry-context", scripts: [][]llm.Delta{{{Content: "Done.", FinishReason: "stop"}}}}
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), LiveContextForRun: func(context.Context) (string, error) {
		if fail {
			return "", errors.New("database unavailable")
		}
		return "fresh", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loop.Run(context.Background(), "first"); err == nil {
		t.Fatal("failed refresh accepted")
	}
	fail = false
	drainEvents(t, mustRun(t, loop, "retry"))
	if provider.calls != 1 {
		t.Fatalf("calls=%d", provider.calls)
	}
}
