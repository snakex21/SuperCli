package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestWorkerFailedCheckCannotBeHiddenByACompletedReport(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, dispatch := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%v/dispatch=%v", thin, dispatch), func(t *testing.T) {
				ctx := context.Background()
				reg := tools.NewRegistry()
				var pass atomic.Bool
				reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "check", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					if !pass.Load() {
						return tools.Result{Text: `{"exit_code":1,"stderr":"test failed"}`, Err: errors.New("tests failed")}, nil
					}
					return tools.Result{Text: `{"exit_code":0,"stdout":"tests passed"}`}, nil
				}})
				reg.MustRegister(tools.Tool{Name: "read_lines", Description: "read", Schema: "{}", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					return tools.Result{Text: "source finding"}, nil
				}})
				goalCalls := 0
				reg.MustRegister(tools.Tool{Name: "goal", Description: "goal", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					goalCalls++
					return tools.Result{Text: "done"}, nil
				}})
				for _, n := range []string{"ctx_execute", "read_lines", "goal"} {
					reg.MarkAlwaysOn(n)
				}
				check := func(id string) []llm.Delta {
					return []llm.Delta{{ToolCall: &llm.ToolCall{ID: id, Name: "ctx_execute", Arguments: `{"command":["go","test","./..."]}`}, FinishReason: "tool_calls"}}
				}
				provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
					check("failed"), {{Content: "Investigation completed.", FinishReason: "stop"}},
					{{ToolCall: &llm.ToolCall{ID: "read", Name: "read_lines", Arguments: "{}"}, FinishReason: "tool_calls"}}, {{Content: "Read source.", FinishReason: "stop"}},
					check("passed"), {{Content: "Tests passed.", FinishReason: "stop"}},
				}}
				parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: t.TempDir(), ThinTools: thin, StableToolset: dispatch})
				if err != nil {
					t.Fatal(err)
				}
				specs := NewSubAgentRegistry()
				MustRegisterAll(specs, BuiltinSubAgents())
				task, err := NewAgentTool(specs, parent, reg, provider, nil, NewLoop)
				if err != nil {
					t.Fatal(err)
				}
				reg.MustRegister(task.Spec())
				reg.MarkAlwaysOn("task")
				reg.MustRegister(NewSendMessageTool(task.Workers).Spec())
				if dispatch {
					ensureWorkerDiscovery(reg)
				}
				events := make(chan Event, 128)
				invoke := func(name, args string) toolResult {
					if dispatch && (name == "task" || name == "send_message") {
						raw, _ := json.Marshal(map[string]any{"tool": name, "args": json.RawMessage(args)})
						name, args = "invoke_tool", string(raw)
					}
					return parent.invoke(ctx, llm.ToolCall{ID: name, Name: name, Arguments: args}, events)
				}
				if result := invoke("task", `{"prompt":"Investigate the failing check"}`); result.failed {
					t.Fatal("a completed investigation was mislabeled as a failed task")
				}
				if result := invoke("goal", `{"action":"complete_task","task_seq":1,"text":"before recovery"}`); !result.failed || goalCalls != 0 {
					t.Error("coordinator completed work despite a worker's failed test")
				}
				if result := invoke("send_message", `{"to":"worker-1","message":"Read the source without rerunning tests"}`); result.failed {
					t.Fatal("read continuation failed")
				}
				if result := invoke("goal", `{"action":"verify","passed":true,"text":"after unrelated read"}`); !result.failed || goalCalls != 0 {
					t.Error("an unrelated continuation erased the failed test")
				}
				pass.Store(true)
				if result := invoke("send_message", `{"to":"worker-1","message":"Rerun the exact test"}`); result.failed {
					t.Fatal("passing continuation failed")
				}
				if result := invoke("goal", `{"action":"verify","passed":true,"text":"after passing rerun"}`); result.failed || goalCalls != 1 {
					t.Error("passing worker rerun did not resolve the coordinator's failed check")
				}
				if provider.calls != 6 {
					t.Errorf("unexpected model calls: %d", provider.calls)
				}
			})
		}
	}
}

func TestVerificationObserverRejectsStaleRunsAndOlderEvidence(t *testing.T) {
	f := &failedChecks{}
	key := [32]byte{1}
	observe := f.observer()
	failed := verificationObservation{key: key, failed: true, sequence: verificationSequence.Add(1)}
	passed := verificationObservation{key: key, sequence: verificationSequence.Add(1)}
	observe(passed)
	observe(failed)
	if f.unresolved() {
		t.Fatal("late worker failure replaced its newer passing rerun")
	}
	newerFailure := verificationObservation{key: key, failed: true, sequence: verificationSequence.Add(1)}
	f.record(newerFailure)
	observe(passed)
	if !f.unresolved() {
		t.Fatal("late passing report erased a newer failure")
	}
	f.reset()
	observe(newerFailure)
	if f.unresolved() {
		t.Fatal("previous-run worker affected the next request")
	}
	observe = f.observer()
	observe(verificationObservation{key: key, failed: true, sequence: verificationSequence.Add(1)})
	if !f.unresolved() {
		t.Fatal("current invocation observer stopped recording")
	}
}

func TestBackgroundWorkerFailureReachesCoordinatorBeforeItsReport(t *testing.T) {
	for _, scopeChange := range []string{"none", "new run", "resumed session"} {
		t.Run(scopeChange, func(t *testing.T) {
			ctx := context.Background()
			reg := tools.NewRegistry()
			checkEntered, allowCheck := make(chan struct{}), make(chan struct{})
			reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "check", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				close(checkEntered)
				<-allowCheck
				return tools.Result{Text: `{"exit_code":1}`, Err: errors.New("tests failed")}, nil
			}})
			reg.MarkAlwaysOn("ctx_execute")
			goalCalls := 0
			reg.MustRegister(tools.Tool{Name: "goal", Description: "goal", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				goalCalls++
				return tools.Result{Text: "done"}, nil
			}})
			provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "check", Name: "ctx_execute", Arguments: `{"command":["go","test","./..."]}`}, FinishReason: "tool_calls"}},
				{{Content: "Worker investigation finished.", FinishReason: "stop"}},
			}}
			parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, Writer: &recordingWriter{}, BaseDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			specs := NewSubAgentRegistry()
			MustRegisterAll(specs, BuiltinSubAgents())
			task, err := NewAgentTool(specs, parent, reg, provider, nil, NewLoop)
			if err != nil {
				t.Fatal(err)
			}
			reg.MustRegister(task.Spec())
			reg.MarkAlwaysOn("task")
			events := make(chan Event, 64)
			parent.SetExternalSink(events)
			// Async remains an embedder API, not an advertised model argument.
			// Mirror Run's ownership while invoking foreground tools.
			if scopeChange == "none" {
				parent.sessionBusy.Store(true)
			}
			invocation := withWorkerInvocation(ctx, "delegate", events, parent.failedChecks.observer())
			result, err := task.execute(invocation, json.RawMessage(`{"prompt":"Run the verification check","async":true}`))
			if err != nil || result.Err != nil {
				t.Fatalf("background delegation failed: %v %v", err, result.Err)
			}
			parent.toolEvidence.Store(true)
			<-checkEntered
			switch scopeChange {
			case "new run":
				parent.failedChecks.reset()
			case "resumed session":
				if err := parent.ResumeConversation(ctx, &recordingWriter{}, []llm.Message{{Role: llm.RoleUser, Content: "new project"}}, nil); err != nil {
					close(allowCheck)
					t.Fatal(err)
				}
			}
			close(allowCheck)
			for {
				event := <-events
				if p, ok := event.(WorkerProgressEvent); ok && p.Kind == "tool_result" {
					break
				}
			}
			wantFailure := scopeChange == "none"
			if parent.failedChecks.unresolved() != wantFailure {
				t.Errorf("scope=%s pending=%v", scopeChange, parent.failedChecks.unresolved())
			}
			if wantFailure {
				goalResult := parent.invoke(ctx, llm.ToolCall{ID: "verify", Name: "goal", Arguments: `{"action":"verify","passed":true}`}, events)
				if !goalResult.failed || goalCalls != 0 {
					t.Error("background failed check was ignored")
				}
				parent.releaseConversation()
			}
			for {
				event := <-events
				if _, ok := event.(WorkerNotificationEvent); ok {
					break
				}
			}
			if provider.calls != 2 {
				t.Errorf("extra model calls: %d", provider.calls)
			}
		})
	}
}

func TestWorkerVerificationIdentityAllowsMatchingForegroundRecovery(t *testing.T) {
	root := t.TempDir()
	parent, worker := &Loop{baseDir: root}, &Loop{baseDir: root}
	worker.verificationObserver = parent.failedChecks.observer()
	command := llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["go","test","./..."],"workdir":"pkg","env_extra":["MODE=full"]}`}
	worker.recordCheckResult(command, tools.Result{Err: errors.New("failed")})
	for _, args := range []string{
		`{"command":["go","test","./..."],"workdir":"elsewhere","env_extra":["MODE=full"]}`,
		`{"command":["go","test","./other"],"workdir":"pkg","env_extra":["MODE=full"]}`,
		`{"command":["go","test","./..."],"workdir":"pkg","env_extra":["MODE=quick"]}`,
	} {
		parent.recordCheckResult(llm.ToolCall{Name: "ctx_execute", Arguments: args}, tools.Result{Text: "passed"})
		if !parent.failedChecks.unresolved() {
			t.Fatalf("different check cleared worker failure: %s", args)
		}
	}
	parent.recordCheckResult(command, tools.Result{Text: "passed"})
	if parent.failedChecks.unresolved() {
		t.Fatal("matching foreground recovery did not resolve worker check")
	}
	parent.recordCheckResult(command, tools.Result{Err: errors.New("failed in parent")})
	worker.recordCheckResult(command, tools.Result{Text: "passed"})
	if parent.failedChecks.unresolved() {
		t.Fatal("worker recovery did not resolve foreground check")
	}
}

func TestWorkerVerificationForwardingDoesNotChangeModelRequests(t *testing.T) {
	root := t.TempDir()
	run := func(forward bool) *stubProvider {
		reg := tools.NewRegistry()
		reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "check", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: `{"exit_code":1,"stderr":"failed assertion"}`, Err: errors.New("tests failed")}, nil
		}})
		reg.MarkAlwaysOn("ctx_execute")
		provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
			{{ToolCall: &llm.ToolCall{ID: "check", Name: "ctx_execute", Arguments: `{"command":["go","test","./..."]}`}, FinishReason: "tool_calls"}},
			{{Content: "Investigation complete; tests failed.", FinishReason: "stop"}},
		}}
		parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: root})
		if err != nil {
			t.Fatal(err)
		}
		specs := NewSubAgentRegistry()
		MustRegisterAll(specs, BuiltinSubAgents())
		task, err := NewAgentTool(specs, parent, reg, provider, nil, NewLoop)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if forward {
			ctx = withWorkerInvocation(ctx, "check-forwarding", nil, parent.failedChecks.observer())
		}
		result, err := task.execute(ctx, json.RawMessage(`{"prompt":"Investigate the failing test"}`))
		if err != nil || result.Err != nil {
			t.Fatalf("%v %v", err, result.Err)
		}
		if parent.failedChecks.unresolved() != forward {
			t.Fatalf("forward=%v unresolved=%v", forward, parent.failedChecks.unresolved())
		}
		return provider
	}
	baseline, current := run(false), run(true)
	if baseline.calls != 2 || current.calls != 2 || !reflect.DeepEqual(baseline.reqs, current.reqs) || !reflect.DeepEqual(baseline.toolReqs, current.toolReqs) {
		t.Fatal("internal check forwarding changed the model payload or added a request")
	}
}
