package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

func TestCompletedOperationReplayCannotBeOverriddenByReadCache(t *testing.T) {
	r := tools.NewRegistry()
	calls, checks := 0, 0
	denied := false
	r.MustRegister(tools.Tool{Name: "verify_export", Description: "observe existing artifact", ReadOnly: true, ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: reuseTestSchema,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			calls++
			return tools.Result{Text: "verified", Operation: &core.VerifiedOperation{Summary: "Existing output verified"}}, nil
		},
		ReplaySuccess: func(_ context.Context, _ json.RawMessage, prior tools.Result) (tools.Result, bool) {
			checks++
			if denied {
				return tools.Result{Err: errors.New("current access denied")}, true
			}
			prior.Inert = true
			return prior, true
		}})
	r.MarkAlwaysOn("verify_export")
	l := makeLoop(t, echoProvider("fixture"), r, "system")
	args := `{"query":"artifact"}`
	invokeOperation(l, "verify_export", "first", args)
	if got := invokeOperation(l, "verify_export", "repeat", args); got.failed || !got.inert || calls != 1 || checks != 1 {
		t.Fatalf("checked replay overwritten: %+v %d/%d", got, calls, checks)
	}
	denied = true
	if got := invokeOperation(l, "verify_export", "denied", args); !got.failed || calls != 1 || checks != 2 || l.completedOps.context() != "" {
		t.Fatalf("denial overwritten: %+v %d/%d", got, calls, checks)
	}
	if len(l.resultReuse.entries) != 0 {
		t.Fatal("effect tool admitted to read cache")
	}
}

func TestWorkerEffectsInvalidateEvidenceBeforeAndAfterPartialFailure(t *testing.T) {
	parent := &Loop{baseDir: t.TempDir()}
	key := sha256.Sum256([]byte("saved"))
	seed := func() {
		_, _, generation := parent.completedOps.lookup(key)
		parent.completedOps.record(generation, key, tools.Result{Text: "saved", Operation: &core.VerifiedOperation{Summary: "saved file"}})
		_, _, _, claim, err := parent.resultReuse.acquire(context.Background(), key, false, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		result := tools.Result{Text: "read evidence"}
		parent.resultReuse.finish(claim, &result, time.Minute, time.Now())
	}
	seed()
	parent.identicalFails.recordFailure("ctx_execute", "{}")
	parent.identicalFails.recordFailure("ctx_execute", "{}")
	parent.concreteFailure.Store(true)
	r := tools.NewRegistry()
	r.MustRegister(tools.Tool{Name: "ctx_execute", Description: "fixture command", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		if parent.completedOps.context() != "" || len(parent.resultReuse.entries) != 0 {
			t.Error("worker started effect with stale parent evidence")
		}
		// The coordinator completes another operation while the background
		// command is running; a later partial failure must invalidate it too.
		seed()
		return tools.Result{Err: errors.New("command changed files then failed")}, nil
	}})
	r.MarkAlwaysOn("ctx_execute")
	child := makeLoop(t, echoProvider("fixture"), r, "system")
	child.baseDir = parent.baseDir
	ctx := withWorkerEffectBarrier(context.Background(), parent.workerEffectBarrierObserver(context.Background()))
	result := child.invoke(ctx, llm.ToolCall{ID: "partial", Name: "ctx_execute", Arguments: `{}`}, make(chan Event, 16))
	if !result.failed || parent.completedOps.context() != "" || len(parent.resultReuse.entries) != 0 {
		t.Fatal("partial worker failure left current receipts")
	}
	if !parent.identicalFails.shouldBlock("ctx_execute", "{}") || !parent.concreteFailure.Load() {
		t.Fatal("attempted effect reported as successful repair")
	}
	seed()
	parent.workerEffectBarrierObserver(context.Background())(t.TempDir())
	if parent.completedOps.context() == "" {
		t.Fatal("separate workspace invalidated parent receipts")
	}
}

type operationFixtureEvidence struct{ path, content string }

func operationFixture(t *testing.T) (*Loop, *int, *int) {
	t.Helper()
	root := t.TempDir()
	saves, checks := 0, 0
	r := tools.NewRegistry()
	r.MustRegister(tools.Tool{Name: "export_asset", Description: "fixture export", Schema: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`,
		Fn: func(_ context.Context, args json.RawMessage) (tools.Result, error) {
			var a struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(args, &a)
			path := filepath.Join(root, a.Path)
			saves++
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				return tools.Result{Err: err}, nil
			}
			return tools.Result{Text: "Saved " + a.Path, Operation: &core.VerifiedOperation{Summary: "Saved " + a.Path, Evidence: operationFixtureEvidence{path, "original"}}}, nil
		},
		ReplaySuccess: func(ctx context.Context, _ json.RawMessage, prior tools.Result) (tools.Result, bool) {
			checks++
			if err := ctx.Err(); err != nil {
				return tools.Result{Err: err}, true
			}
			e := prior.Operation.Evidence.(operationFixtureEvidence)
			data, err := os.ReadFile(e.path)
			if os.IsNotExist(err) {
				return tools.Result{}, false
			}
			if err != nil || string(data) != e.content {
				return tools.Result{Err: errors.New("saved output changed")}, true
			}
			return tools.Result{Text: "Existing successful export verified", Inert: true, Operation: prior.Operation}, true
		}})
	r.MustRegister(tools.Tool{Name: "inspect_fixture", Description: "read", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "inspection completed"}, nil
	}})
	r.MustRegister(tools.Tool{Name: "modify_fixture", Description: "edit", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "changed"}, nil
	}})
	for _, name := range []string{"export_asset", "inspect_fixture", "modify_fixture"} {
		r.MarkAlwaysOn(name)
	}
	return makeLoop(t, echoProvider("fixture"), r, "system"), &saves, &checks
}

func invokeOperation(l *Loop, name, id, args string) toolResult {
	return l.invoke(context.Background(), llm.ToolCall{ID: id, Name: name, Arguments: args}, make(chan Event, 32))
}

func TestCompletedOperationReplayValidatesCurrentOutputAndProtocol(t *testing.T) {
	l, saves, checks := operationFixture(t)
	if got := invokeOperation(l, "export_asset", "first", `{"path":"one.bin"}`); got.failed {
		t.Fatal(got)
	}
	if !strings.Contains(l.contextTail(), "[completed operations]") {
		t.Fatal("completed receipt missing from request tail")
	}
	got := invokeOperation(l, "export_asset", "repeat", `{ "path": "one.bin" }`)
	if got.failed || !got.inert || *saves != 1 || *checks != 1 || len(got.followUps) != 1 || got.followUps[0].ToolCallID != "repeat" {
		t.Fatalf("duplicate: %+v saves/checks=%d/%d", got, *saves, *checks)
	}
	if got := invokeOperation(l, "export_asset", "invalid", `{"path":"one.bin","extra":1}`); !got.failed || *checks != 1 || *saves != 1 {
		t.Fatal("invalid call reused effect")
	}
	key, _ := completedOperationKey("export_asset", json.RawMessage(`{"path":"one.bin"}`))
	prior, _, _ := l.completedOps.lookup(key)
	e := prior.Operation.Evidence.(operationFixtureEvidence)
	if err := os.WriteFile(e.path, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := invokeOperation(l, "export_asset", "changed", `{"path":"one.bin"}`); !got.failed || *saves != 1 || l.completedOps.context() != "" {
		t.Fatal("modified file reported as completed or overwritten")
	}
}

func TestCompletedOperationMissingOutputAndMutationRequireNewExecution(t *testing.T) {
	l, saves, _ := operationFixture(t)
	invokeOperation(l, "export_asset", "first", `{"path":"one.bin"}`)
	key, _ := completedOperationKey("export_asset", json.RawMessage(`{"path":"one.bin"}`))
	prior, _, _ := l.completedOps.lookup(key)
	if err := os.Remove(prior.Operation.Evidence.(operationFixtureEvidence).path); err != nil {
		t.Fatal(err)
	}
	if got := invokeOperation(l, "export_asset", "missing", `{"path":"one.bin"}`); got.failed || *saves != 2 {
		t.Fatal("missing output reused")
	}
	invokeOperation(l, "modify_fixture", "mutation", `{}`)
	if l.completedOps.context() != "" {
		t.Fatal("mutation kept completion evidence")
	}
	invokeOperation(l, "export_asset", "after-edit", `{"path":"one.bin"}`)
	if *saves != 3 {
		t.Fatal("mutation did not reset effect receipt")
	}
}

func TestCompletedOperationNoEarlyFinishMultipleOutputsAndInspection(t *testing.T) {
	l, saves, checks := operationFixture(t)
	call := func(id, name, args string) []llm.Delta {
		return []llm.Delta{{ToolCall: &llm.ToolCall{ID: id, Name: name, Arguments: args}}, {FinishReason: "tool_calls"}}
	}
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		call("one", "export_asset", `{"path":"one.bin"}`),
		call("repeat", "export_asset", `{"path":"one.bin"}`),
		call("two", "export_asset", `{"path":"two.bin"}`),
		call("inspect", "inspect_fixture", `{}`),
		{{Content: "Both outputs saved and inspected."}, {FinishReason: "stop"}},
	}}
	l.provider = p
	var done bool
	for _, event := range drainEvents(t, mustRun(t, l, "Save two different files and inspect the result.")) {
		switch e := event.(type) {
		case ErrorEvent:
			t.Fatal(e.Err)
		case DoneEvent:
			done = true
		}
	}
	if !done || *saves != 2 || *checks != 1 || len(p.reqs) != 5 {
		t.Fatalf("premature/duplicate work: done=%t saves/checks/requests=%d/%d/%d", done, *saves, *checks, len(p.reqs))
	}
	var hint bool
	for _, m := range p.reqs[3] {
		hint = hint || strings.Contains(m.Content, "one.bin") && strings.Contains(m.Content, "two.bin") && strings.Contains(m.Content, "[completed operations]")
	}
	if !hint {
		t.Fatal("provider missing verified outputs")
	}
	if len(l.completedOps.entries) != 0 || l.completedOps.context() != "" {
		t.Fatal("receipts retained after Run")
	}
}

func TestCompletedOperationUntrustedMetadataFailedVerificationAndCanceledReplay(t *testing.T) {
	l, saves, checks := operationFixture(t)
	tool, _ := l.registry.Get("export_asset")
	tool.Verify = func(tools.Result) tools.VerifyVerdict {
		return tools.VerifyVerdict{OK: false, Reason: "fixture failed verification"}
	}
	r := tools.NewRegistry()
	r.MustRegister(tool)
	r.MarkAlwaysOn(tool.Name)
	l.SetRegistry(r)
	for i := 0; i < 2; i++ {
		if !invokeOperation(l, "export_asset", fmt.Sprint(i), `{"path":"one.bin"}`).failed {
			t.Fatal("verification failure accepted")
		}
	}
	if *saves != 2 || *checks != 0 || l.completedOps.context() != "" {
		t.Fatal("unverified success cached")
	}
	tool.Verify = nil
	tool.ReplaySuccess = nil
	r = tools.NewRegistry()
	r.MustRegister(tool)
	r.MarkAlwaysOn(tool.Name)
	l.SetRegistry(r)
	invokeOperation(l, "export_asset", "untrusted", `{"path":"one.bin"}`)
	if l.completedOps.context() != "" {
		t.Fatal("unregistered receipt callback gained completion authority")
	}
	l, _, checks = operationFixture(t)
	invokeOperation(l, "export_asset", "first", `{"path":"one.bin"}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := l.invoke(ctx, llm.ToolCall{ID: "cancel", Name: "export_asset", Arguments: `{"path":"one.bin"}`}, make(chan Event, 16)); !got.failed || *checks != 0 {
		t.Fatal("canceled operation replayed")
	}
}

func TestCompletedOperationBoundsAndResetRejectLateReceipts(t *testing.T) {
	var c completedOperations
	for i := 0; i < 100; i++ {
		key := sha256.Sum256([]byte(fmt.Sprint(i)))
		_, _, generation := c.lookup(key)
		c.record(generation, key, tools.Result{Text: strings.Repeat("x", 64<<10), Operation: &core.VerifiedOperation{Summary: fmt.Sprint("completed ", i)}})
	}
	if len(c.entries) > completedOperationLimit || c.bytes > completedOperationBytes || len(c.context()) > 4096 {
		t.Fatal("unbounded completed output storage/prompt")
	}
	key := sha256.Sum256([]byte("late"))
	_, _, generation := c.lookup(key)
	c.reset()
	c.record(generation, key, tools.Result{Text: "late", Operation: &core.VerifiedOperation{Summary: "late"}})
	if len(c.entries) != 0 || c.context() != "" {
		t.Fatal("old instruction resurrected receipt")
	}
	tool := tools.Tool{Name: "fixture", ReplaySuccess: func(context.Context, json.RawMessage, tools.Result) (tools.Result, bool) { panic("bad checker") }}
	if result, handled := replayCompletedOperation(context.Background(), tool, nil, tools.Result{}); !handled || result.Err == nil {
		t.Fatal("panic escaped replay boundary")
	}
}
