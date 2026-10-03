package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/files"
)

func mutationFixtureDelta(call llm.ToolCall) []llm.Delta {
	return []llm.Delta{{ToolCall: &call, FinishReason: "tool_calls"}}
}
func mutationFixtureFinal(text string) []llm.Delta {
	return []llm.Delta{{Content: text, FinishReason: "stop"}}
}
func mutationFixtureCall(id, name string, args any) llm.ToolCall {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return llm.ToolCall{ID: id, Name: name, Arguments: string(raw)}
}

// Uses real Loop.Run, AgentTool, worker continuation, PatchFile and file reads.
// ctx_execute is an owned verification double; it starts no child program.
func TestDelegatedRepairUnblocksParentVerification(t *testing.T) {
	for _, mode := range []string{"task", "continuation", "no-op", "failed patch", "read-only", "separate worktree"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			childRoot := root
			target := filepath.Join(root, "source.txt")
			if err := os.WriteFile(target, []byte("broken\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "separate worktree" {
				childRoot = t.TempDir()
				if err := os.WriteFile(filepath.Join(childRoot, "source.txt"), []byte("broken\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var runs atomic.Int32
			reg := tools.NewRegistry()
			reg.MustRegister(files.NewPatchFile(root).Spec())
			reg.MustRegister(files.NewReadLines(root).Spec())
			reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "Controlled verification fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				runs.Add(1)
				data, err := os.ReadFile(target)
				if err != nil {
					return tools.Result{Err: err}, nil
				}
				if string(data) != "fixed\n" {
					return tools.Result{Text: `{"exit_code":1,"stderr":"fixture broken"}`, Err: errors.New("command_failed: fixture broken")}, nil
				}
				return tools.Result{Text: `{"exit_code":0,"stdout":"fixture passed"}`}, nil
			}})
			for _, name := range []string{"patch_file", "read_lines", "ctx_execute"} {
				reg.MarkAlwaysOn(name)
			}
			check := mutationFixtureCall("check", "ctx_execute", map[string]any{"command": []string{"go", "test", "./..."}, "workdir": root})
			first, second := check, check
			first.ID = "failed-1"
			second.ID = "failed-2"
			patch := mutationFixtureCall("repair", "patch_file", map[string]any{"path": "source.txt", "old": "broken", "new": "fixed"})
			switch mode {
			case "no-op":
				patch = mutationFixtureCall("repair", "patch_file", map[string]any{"path": "source.txt", "old": "broken", "new": "broken"})
			case "failed patch":
				patch = mutationFixtureCall("repair", "patch_file", map[string]any{"path": "source.txt", "old": "missing", "new": "fixed"})
			case "read-only":
				patch = mutationFixtureCall("read", "read_lines", map[string]any{"file": "source.txt"})
			}
			task := mutationFixtureCall("delegate", "task", map[string]any{"prompt": "Repair the fixture source"})
			scripts := [][]llm.Delta{{{ToolCall: &first}, {ToolCall: &second, FinishReason: "tool_calls"}}, mutationFixtureDelta(task)}
			if mode == "continuation" {
				scripts = append(scripts, mutationFixtureFinal("Ready to continue."), mutationFixtureDelta(mutationFixtureCall("continue", "send_message", map[string]any{"to": "worker-1", "message": "Repair source now"})))
			}
			scripts = append(scripts, mutationFixtureDelta(patch), mutationFixtureFinal("Fixture work finished."), mutationFixtureDelta(check), mutationFixtureFinal("Report the observed verification result."))
			provider := &stubProvider{name: "fixture", scripts: scripts}
			parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: root, MaxSteps: 16})
			if err != nil {
				t.Fatal(err)
			}
			specs := NewSubAgentRegistry()
			MustRegisterAll(specs, BuiltinSubAgents())
			factory := NewLoop
			if mode == "separate worktree" {
				factory = func(cfg LoopConfig) (*Loop, error) {
					isolated := tools.NewRegistry()
					isolated.MustRegister(files.NewPatchFile(childRoot).Spec())
					isolated.MarkAlwaysOn("patch_file")
					cfg.BaseDir = childRoot
					cfg.Registry = isolated
					return NewLoop(cfg)
				}
			}
			taskTool, err := NewAgentTool(specs, parent, reg, provider, nil, factory)
			if err != nil {
				t.Fatal(err)
			}
			reg.MustRegister(taskTool.Spec())
			reg.MarkAlwaysOn("task")
			reg.MustRegister(NewSendMessageTool(taskTool.Workers).Spec())
			reg.MarkAlwaysOn("send_message")
			var results []ToolResultEvent
			channel, err := parent.Run(context.Background(), "Run the controlled check twice, delegate the repair, then rerun the identical check.")
			if err != nil {
				t.Fatal(err)
			}
			for event := range channel {
				switch value := event.(type) {
				case ToolResultEvent:
					results = append(results, value)
				case ErrorEvent:
					t.Fatalf("Loop.Run failed: %v", value.Err)
				}
			}
			repaired := mode == "task" || mode == "continuation"
			wantRuns := int32(2)
			if repaired {
				wantRuns = 3
			}
			if runs.Load() != wantRuns {
				t.Fatalf("mode=%s check executions=%d want=%d (worker edit did not expire old command failures)", mode, runs.Load(), wantRuns)
			}
			var finalCheck *ToolResultEvent
			for index := range results {
				if results[index].ID == "check" {
					finalCheck = &results[index]
				}
			}
			if finalCheck == nil {
				t.Fatal("missing exact parent rerun result")
			}
			if repaired {
				if finalCheck.Err != nil {
					t.Fatalf("fixed verification stayed blocked/failed: %v", finalCheck.Err)
				}
				if parent.failedChecks.unresolved() {
					t.Fatal("passing exact check did not resolve original evidence")
				}
			} else {
				if finalCheck.Err == nil || !strings.Contains(finalCheck.Err.Error(), "blocked:") {
					t.Fatalf("non-repair forgave failures: %+v", finalCheck)
				}
				if !parent.failedChecks.unresolved() {
					t.Fatal("edit/report erased unresolved check evidence")
				}
			}
			wantRequests := int32(6)
			if mode == "continuation" {
				wantRequests = 8
			}
			if provider.calls != wantRequests {
				t.Fatalf("requests=%d want=%d", provider.calls, wantRequests)
			}
			if mode == "continuation" && len(taskTool.Workers.List()) != 1 {
				t.Fatal("continuation created a second worker")
			}
			if mode == "separate worktree" {
				data, err := os.ReadFile(filepath.Join(childRoot, "source.txt"))
				if err != nil || string(data) != "fixed\n" {
					t.Fatalf("separate worktree mutation failed: %q %v", data, err)
				}
			}
		})
	}
}

func TestWorkerMutationObserverLifecycle(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	parent := &Loop{baseDir: root}
	observe := parent.workerMutationObserver(context.Background())
	fail := func() {
		parent.identicalFails.recordFailure("ctx_execute", "{}")
		parent.identicalFails.recordFailure("ctx_execute", "{}")
	}
	blocked := func() bool { return parent.identicalFails.shouldBlock("ctx_execute", "{}") }
	fail()
	observe(other)
	if !blocked() {
		t.Fatal("different workspace expired command failure")
	}
	observe("")
	if !blocked() {
		t.Fatal("unknown workspace expired command failure")
	}
	observe(root)
	if blocked() {
		t.Fatal("same workspace did not expire command failure")
	}
	fail()
	parent.failedChecks.reset()
	observe(root)
	if !blocked() {
		t.Fatal("previous-run observer expired new-run failures")
	}
	current := parent.workerMutationObserver(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); current(root) }()
	}
	wg.Wait()
	if blocked() {
		t.Fatal("concurrent current mutation did not expire command failure")
	}
	fail()
	parent.LoadConversation([]llm.Message{{Role: llm.RoleUser, Content: "replacement"}})
	current(root)
	if !blocked() {
		t.Fatal("old conversation observer expired replacement failures")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err == nil {
		aliasObserver := parent.workerMutationObserver(context.Background())
		aliasObserver(alias)
		if blocked() {
			t.Fatal("same directory alias did not expire command failure")
		}
	} else {
		t.Logf("symlink alias control unavailable: %v", err)
	}
}

func TestWorkerMutationNotificationConditions(t *testing.T) {
	for _, mode := range []string{"success", "inert", "failure", "cancelled", "panic", "success racing cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			parent := &Loop{baseDir: root}
			parent.identicalFails.recordFailure("ctx_execute", "{}")
			parent.identicalFails.recordFailure("ctx_execute", "{}")
			parent.recordCheckResult(llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["go","test","./..."]}`}, tools.Result{Err: errors.New("fixture failure")})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = withWorkerMutationObserver(withWorkerInvocation(ctx, "fixture", nil, nil), parent.workerMutationObserver(ctx))
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{Name: "patch_file", Description: "Controlled mutation", Schema: "{}", Verify: func(result tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: result.Err == nil} }, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				switch mode {
				case "inert":
					return tools.Result{Text: "unchanged", Inert: true}, nil
				case "failure":
					return tools.Result{Err: errors.New("no mutation")}, nil
				case "cancelled":
					cancel()
					return tools.Result{Err: context.Canceled}, nil
				case "panic":
					panic("controlled fixture panic")
				case "success racing cancel":
					cancel()
				}
				return tools.Result{Text: "changed"}, nil
			}})
			reg.MarkAlwaysOn("patch_file")
			child, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg, BaseDir: root})
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					if value := recover(); value != nil && mode != "panic" {
						t.Fatal(value)
					}
				}()
				child.invoke(ctx, llm.ToolCall{ID: "mutation", Name: "patch_file", Arguments: "{}"}, make(chan Event, 16))
			}()
			wantExpired := mode == "success" || mode == "success racing cancel"
			if parent.identicalFails.shouldBlock("ctx_execute", "{}") == wantExpired {
				t.Fatalf("mode=%s expiry=%v want=%v", mode, !parent.identicalFails.shouldBlock("ctx_execute", "{}"), wantExpired)
			}
			if !parent.failedChecks.unresolved() {
				t.Fatal("mutation was treated as a successful test")
			}
		})
	}
}

func TestWorkerMutationWorkspaceIdentity(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{root, t.TempDir()}, {"", root}, {root, ""}, {file, root}, {root, file}} {
		if sameWorkerWorkspace(pair[0], pair[1]) {
			t.Fatalf("wrong workspace identity for %q %q", pair[0], pair[1])
		}
	}
	if !sameWorkerWorkspace(root, filepath.Join(root, ".")) {
		t.Fatal("normalized exact root not shared")
	}
	if sameWorkerWorkspace(filepath.Join(root, "pending"), filepath.Join(root, "PENDING")) {
		t.Fatal("case-only different missing paths bypassed directory identity")
	}
	missing := filepath.Join(root, "not-yet-created")
	if !sameWorkerWorkspace(missing, missing) {
		t.Fatal("exact-root fast path required metadata")
	}
}

// Existing async embedder path must preserve the delegation mutation observer, and
// late background completion must not expire a replacement run's failures.
func TestBackgroundWorkerMutationScope(t *testing.T) {
	for _, scope := range []string{"current", "new run", "new session"} {
		t.Run(scope, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "source.txt")
			if err := os.WriteFile(target, []byte("broken\n"), 0600); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			patch := files.NewPatchFile(root).Spec()
			realPatch := patch.Fn
			patch.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return tools.Result{Err: ctx.Err()}, nil
				}
				return realPatch(ctx, raw)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(patch)
			reg.MarkAlwaysOn("patch_file")
			provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
				mutationFixtureDelta(mutationFixtureCall("patch", "patch_file", map[string]any{"path": "source.txt", "old": "broken", "new": "fixed"})),
				mutationFixtureFinal("Background repair completed."),
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
			events := make(chan Event, 64)
			parent.SetExternalSink(events)
			parent.sessionBusy.Store(true)
			defer parent.releaseConversation()
			fail := func() {
				parent.identicalFails.recordFailure("ctx_execute", "{}")
				parent.identicalFails.recordFailure("ctx_execute", "{}")
				parent.recordCheckResult(llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["go","test","./..."]}`}, tools.Result{Err: errors.New("fixture failed")})
			}
			fail()
			ctx := withWorkerMutationObserver(withWorkerInvocation(context.Background(), "background", nil, parent.failedChecks.observer()), parent.workerMutationObserver(context.Background()))
			result, err := task.execute(ctx, json.RawMessage(`{"prompt":"Repair source","async":true}`))
			if err != nil || result.Err != nil {
				t.Fatalf("start background: %v %v", err, result.Err)
			}
			<-entered
			switch scope {
			case "new run":
				parent.failedChecks.reset()
				fail()
			case "new session":
				parent.LoadConversation([]llm.Message{{Role: llm.RoleUser, Content: "replacement"}})
				fail()
			}
			close(release)
			for event := range events {
				if _, done := event.(WorkerNotificationEvent); done {
					break
				}
			}
			wantBlocked := scope != "current"
			if parent.identicalFails.shouldBlock("ctx_execute", "{}") != wantBlocked {
				t.Fatalf("scope=%s stale/current invalidation mismatch", scope)
			}
			if !parent.failedChecks.unresolved() {
				t.Fatal("background edit resolved check without rerun")
			}
			if provider.calls != 2 {
				t.Fatalf("extra background model steps: %d", provider.calls)
			}
		})
	}
}

// An explicit embedder factory enables one nested worker for this fixture.
// Built-in restricted registries still structurally prohibit nested delegation.
func TestNestedDelegatedRepairUnblocksBothVerificationGates(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "source.txt")
	if err := os.WriteFile(target, []byte("broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var runs atomic.Int32
	reg := tools.NewRegistry()
	reg.MustRegister(files.NewPatchFile(root).Spec())
	reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "Controlled verification fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		runs.Add(1)
		data, err := os.ReadFile(target)
		if err != nil {
			return tools.Result{Err: err}, nil
		}
		if string(data) != "fixed\n" {
			return tools.Result{Text: `{"exit_code":1,"stderr":"fixture broken"}`, Err: errors.New("command_failed: fixture broken")}, nil
		}
		return tools.Result{Text: `{"exit_code":0,"stdout":"fixture passed"}`}, nil
	}})
	for _, name := range []string{"ctx_execute", "patch_file"} {
		reg.MarkAlwaysOn(name)
	}
	args := map[string]any{"command": []string{"go", "test", "./..."}, "workdir": root}
	failedPair := func(prefix string) []llm.Delta {
		first := mutationFixtureCall(prefix+"-1", "ctx_execute", args)
		second := mutationFixtureCall(prefix+"-2", "ctx_execute", args)
		return []llm.Delta{{ToolCall: &first}, {ToolCall: &second, FinishReason: "tool_calls"}}
	}
	provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		failedPair("root-failed"),
		mutationFixtureDelta(mutationFixtureCall("root-task", "task", map[string]any{"prompt": "Delegate the fixture repair"})),
		failedPair("middle-failed"),
		mutationFixtureDelta(mutationFixtureCall("middle-task", "task", map[string]any{"prompt": "Repair the fixture source"})),
		mutationFixtureDelta(mutationFixtureCall("repair", "patch_file", map[string]any{"path": "source.txt", "old": "broken", "new": "fixed"})),
		mutationFixtureFinal("Grandchild repair finished."),
		mutationFixtureDelta(mutationFixtureCall("middle-rerun", "ctx_execute", args)),
		mutationFixtureFinal("Middle verification finished."),
		mutationFixtureDelta(mutationFixtureCall("root-rerun", "ctx_execute", args)),
		mutationFixtureFinal("Root verification finished."),
	}}
	parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: root, MaxSteps: 16})
	if err != nil {
		t.Fatal(err)
	}
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	var middle *Loop
	var factory LoopFactory
	factory = func(cfg LoopConfig) (*Loop, error) {
		child, err := NewLoop(cfg)
		if err != nil {
			return nil, err
		}
		if middle == nil {
			middle = child
			nested, err := NewAgentTool(specs, child, child.registry, provider, nil, factory)
			if err != nil {
				return nil, err
			}
			child.registry.MustRegister(nested.Spec())
			child.registry.MarkAlwaysOn("task")
		}
		return child, nil
	}
	task, err := NewAgentTool(specs, parent, reg, provider, nil, factory)
	if err != nil {
		t.Fatal(err)
	}
	reg.MustRegister(task.Spec())
	reg.MarkAlwaysOn("task")
	stream, err := parent.Run(context.Background(), "Check twice, delegate a nested repair, and rerun the identical verification.")
	if err != nil {
		t.Fatal(err)
	}
	var rerun *ToolResultEvent
	for event := range stream {
		switch value := event.(type) {
		case ToolResultEvent:
			if value.ID == "root-rerun" {
				copy := value
				rerun = &copy
			}
		case ErrorEvent:
			t.Fatalf("Loop.Run failed: %v", value.Err)
		}
	}
	if got := runs.Load(); got != 6 {
		t.Fatalf("nested verified check executions=%d want=6: both middle and root must reexecute after the grandchild mutation", got)
	}
	if rerun == nil || rerun.Err != nil {
		t.Fatalf("original parent rerun failed/blocked: %+v", rerun)
	}
	if middle == nil || middle.failedChecks.unresolved() || parent.failedChecks.unresolved() {
		t.Fatal("passing reruns did not resolve their original check evidence")
	}
	if provider.calls != 10 {
		t.Fatalf("extra provider steps: %d want=10", provider.calls)
	}
}

func TestOrdinaryWorkerInvocationRepresentation(t *testing.T) {
	shape := reflect.TypeOf(workerInvocation{})
	if shape.NumField() != 3 || shape.Size() != 4*reflect.TypeOf(uintptr(0)).Size() {
		t.Fatalf("ordinary worker invocation grew: fields=%d bytes=%d", shape.NumField(), shape.Size())
	}
	ctx := withWorkerInvocation(context.Background(), "ordinary", nil, nil)
	if ctx.Value(workerMutationKey{}) != nil {
		t.Fatal("ordinary invocation allocated a delegation mutation observer")
	}
}
