package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
	"supercli/internal/tools/ctxexec"
)

func TestFailedCommandCanBeInvestigatedWithoutRerun(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, outer := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%v/runtimeError=%v", thin, outer), func(t *testing.T) {
				const diagnostic = "source.go:731 undefined: missingSymbol"
				captured := ctxexec.Result{ExitCode: 1, Stdout: strings.Repeat("build output\n", 2000) + diagnostic + strings.Repeat("\nbuild output", 2000)}
				raw, _ := json.Marshal(captured)
				commandErr := core.SelfContainedErr(errors.New(captured.FailureSummary()))
				reg := tools.NewRegistry()
				calls := 0
				reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "test build", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					calls++
					result := tools.Result{Text: string(raw), Err: commandErr}
					if outer {
						result.Err = nil
						return result, commandErr
					}
					return result, nil
				}})
				reg.MarkAlwaysOn("ctx_execute")
				p := &outputReplayProvider{stubProvider: &stubProvider{name: "worker", scripts: [][]llm.Delta{
					{{ToolCall: &llm.ToolCall{ID: "build", Name: "ctx_execute", Arguments: "{}"}}},
					{{ToolCall: &llm.ToolCall{ID: "inspect", Name: "read_output", Arguments: `{"handle":"out_000001","query":"missingSymbol"}`}}},
					{{Content: "The build failed at source.go:731.", FinishReason: "stop"}},
				}}}
				l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: thin, StableToolset: true, MaxSteps: 4})
				if err != nil {
					t.Fatal(err)
				}
				events, err := l.Run(context.Background(), "Find the build failure.")
				if err != nil {
					t.Fatal(err)
				}
				sawFull := false
				for event := range events {
					if ev, ok := event.(ToolResultEvent); ok && ev.ID == "build" {
						sawFull = ev.Output == string(raw) && ev.Err != nil
					}
					if ev, ok := event.(ErrorEvent); ok {
						t.Fatalf("loop: %v", ev.Err)
					}
				}
				if calls != 1 || p.calls != 3 {
					t.Fatalf("commands=%d model calls=%d", calls, p.calls)
				}
				if !sawFull {
					t.Fatal("UI lost full failure output or error status")
				}
				var failure, found string
				for _, m := range p.reqs[2] {
					if m.ToolCallID == "build" {
						failure = m.Content
					}
					if m.ToolCallID == "inspect" {
						found = m.Content
					}
				}
				if !strings.HasPrefix(failure, "error: command_failed exit=1") || !strings.Contains(failure, "handle=out_") {
					t.Fatalf("failure status/handle missing: %s", failure)
				}
				if strings.Contains(failure, diagnostic) {
					t.Fatal("fixture diagnostic must be outside inline failure tail")
				}
				if !strings.Contains(found, diagnostic) {
					t.Fatalf("original diagnostic unavailable: %s", found)
				}
				if len(failure) > 6000 || len(found) > 1200 {
					t.Fatalf("excessive context: failure=%d search=%d", len(failure), len(found))
				}
			})
		}
	}
}
