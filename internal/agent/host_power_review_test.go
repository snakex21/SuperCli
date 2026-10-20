package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	toolhostpower "supercli/internal/tools/hostpower"
)

// Only the inert check below runs in a subprocess. It inspects fixture files,
// never executes host power, calls a model, or accesses a network service.
func TestHostPowerReviewCheckFixture(t *testing.T) {
	if os.Getenv("SUPERCLI_HOST_POWER_REVIEW_FIXTURE") != "1" {
		return
	}
	if _, err := os.Stat("checked-work.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("verification-red"); err == nil {
		t.Fatal("fixture verification is red")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestHostPowerReviewFence(t *testing.T) {
	t.Setenv("SUPERCLI_HOST_POWER_REVIEW_FIXTURE", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(executable, " \t\r\n") {
		t.Fatal("fixture executable path must be one sieve argv token")
	}
	check := executable + " -test.run=^TestHostPowerReviewCheckFixture$"
	for _, kind := range []string{"unreadable", "review_error", "revise_step_limit", "takeover_step_limit", "revise_round_limit", "takeover_round_limit", "red_accept", "red_then_skipped_accept", "red_then_green_revise", "red_then_green_takeover", "green_accept"} {
		t.Run(kind, func(t *testing.T) {
			controller, clock := newHostPowerTestController(t)
			svc, id := hostPowerTestGoals(t)
			ctx, approvals := hostPowerTestApproval(t)
			root := t.TempDir()
			red := strings.HasPrefix(kind, "red_")
			var repairs atomic.Int32
			registry := tools.NewRegistry()
			registry.MustRegister(toolhostpower.Spec())
			registry.MustRegister(tools.Tool{Name: "fixture_work", Description: "Write inert work and finish the temporary goal", Schema: `{}`, Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
				if err := os.WriteFile(filepath.Join(root, "checked-work.txt"), []byte("fixture work"), 0o600); err != nil {
					return tools.Result{}, err
				}
				if red {
					if err := os.WriteFile(filepath.Join(root, "verification-red"), []byte("red"), 0o600); err != nil {
						return tools.Result{}, err
					}
				}
				completeHostPowerTestGoal(t, svc, id)
				return tools.Result{Text: "Goal is DONE/PASSED with all tasks complete; post-review still required."}, nil
			}})
			review := &PostReviewConfig{VerifyCommands: []string{check}, MaxRounds: 1}
			registry.MustRegister(tools.Tool{Name: "fixture_repair", Description: "Repair the local verification fixture", Schema: `{}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				repairs.Add(1)
				if kind == "red_then_skipped_accept" {
					// Omitting checks is not a passing rerun of a previous red sieve.
					review.VerifyCommands = nil
				} else if err := os.Remove(filepath.Join(root, "verification-red")); err != nil {
					return tools.Result{}, err
				}
				return tools.Result{Text: "Revision applied; obtain a fresh review and verification."}, nil
			}})
			verdict := func(text string) []llm.Delta { return []llm.Delta{{Content: text}, {FinishReason: "stop"}} }
			reviewer := &stubProvider{name: "offline-reviewer", scripts: [][]llm.Delta{verdict(`{"decision":"accept"}`)}}
			switch kind {
			case "unreadable":
				reviewer.scripts = [][]llm.Delta{verdict("unreadable fixture verdict")}
			case "review_error":
				reviewer.scripts = [][]llm.Delta{{{Err: errors.New("offline reviewer fixture error")}}}
			case "revise_step_limit", "revise_round_limit":
				reviewer.scripts = [][]llm.Delta{verdict(`{"decision":"revise","instruction":"fix the fixture"}`)}
			case "takeover_step_limit", "takeover_round_limit":
				reviewer.scripts = [][]llm.Delta{verdict(`{"decision":"takeover","instruction":"redo the fixture"}`)}
			case "red_then_skipped_accept", "red_then_green_revise":
				reviewer.scripts = [][]llm.Delta{verdict(`{"decision":"revise","instruction":"repair verification"}`), verdict(`{"decision":"accept"}`)}
			case "red_then_green_takeover":
				reviewer.scripts = [][]llm.Delta{verdict(`{"decision":"takeover","instruction":"repair verification"}`), verdict(`{"decision":"accept"}`)}
			}
			review.Provider = reviewer
			scripts := hostPowerIntegrationScripts(id)
			if strings.HasPrefix(kind, "red_then_") {
				scripts = append(scripts, []llm.Delta{{ToolCall: &llm.ToolCall{ID: "repair", Name: "fixture_repair", Arguments: `{}`}}, {FinishReason: "tool_calls"}}, verdict("Work revised; verification rerun required."))
			}
			main := &stubProvider{name: "offline-main", scripts: scripts}
			loop := makeLoop(t, main, registry, "system")
			loop.baseDir, loop.reviewCfg = root, review
			if strings.HasSuffix(kind, "step_limit") {
				loop.maxSteps = 3
			}
			loop.ConfigureHostPower(controller, svc, false)
			events := runHostPowerTest(t, loop, ctx)
			assertHostPowerIntegrationTools(t, events, false, false)
			if approvals.Load() != 1 {
				t.Fatalf("UI approvals = %d", approvals.Load())
			}
			if len(reviewer.reqs) == 0 {
				t.Fatal("required review did not run")
			}
			if strings.HasSuffix(kind, "round_limit") && len(reviewer.reqs) != 2 {
				t.Fatalf("review calls = %d, want round-limit review", len(reviewer.reqs))
			}
			if strings.HasPrefix(kind, "red_then_") && (repairs.Load() != 1 || len(reviewer.reqs) != 2) {
				t.Fatal("revision did not precede a fresh review")
			}
			want := "blocked"
			if kind == "green_accept" || kind == "red_then_green_revise" || kind == "red_then_green_takeover" {
				want = "countdown"
			}
			if state := controller.Snapshot(); state.Status != want {
				t.Fatalf("review %s: state = %+v, want %s", kind, state, want)
			}
			if clock.executions.Load() != 0 {
				t.Fatal("executed before fake deadline")
			}
			clock.fire()
			clock.fire()
			wantExecutions, wantTimers := int32(0), 0
			if want == "countdown" {
				wantExecutions, wantTimers = 1, 1
			}
			if clock.executions.Load() != wantExecutions || clock.timers() != wantTimers {
				t.Fatalf("executions=%d timers=%d, want %d/%d", clock.executions.Load(), clock.timers(), wantExecutions, wantTimers)
			}
		})
	}
}
