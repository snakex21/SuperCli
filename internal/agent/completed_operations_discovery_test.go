package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

func registerReceiptDiscovery(t *testing.T, l *Loop, preserves bool) *int {
	t.Helper()
	l.registry.MustRegister(tools.Tool{Name: "inspect_result", Description: "Inspect a saved result", ReadOnly: true, Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "Both requested results inspected."}, nil
		}})
	spec := tools.NewToolSearcher(l.registry, nil).Spec()
	if spec.ReadOnly || !spec.PreservesEvidence || spec.ReuseTTL != 0 {
		t.Fatal("standard discovery must preserve evidence without becoming parallel or cached")
	}
	// The false variant is the original dispatch behavior, and also proves a
	// custom tool with the same name does not inherit trusted capabilities.
	spec.PreservesEvidence = preserves
	calls, execute := 0, spec.Fn
	spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		calls++
		return execute(ctx, args)
	}
	l.registry.MustRegister(spec)
	l.registry.MarkAlwaysOn(spec.Name)
	return &calls
}

func TestCompletedOperationStandardDiscoveryPreservesCheckedReplay(t *testing.T) {
	for _, preserves := range []bool{false, true} {
		t.Run(map[bool]string{false: "default-barrier", true: "standard-capability"}[preserves], func(t *testing.T) {
			l, saves, checks := operationFixture(t)
			discoveries := registerReceiptDiscovery(t, l, preserves)
			invokeOperation(l, "export_asset", "first", `{"path":"one.bin"}`)
			if l.registry.IsActive("inspect_result") {
				t.Fatal("fixture inspection must start dormant")
			}
			for _, id := range []string{"discover", "discover-again"} {
				if got := invokeOperation(l, "tool_search", id, `{"query":"inspect_result"}`); got.failed {
					t.Fatal(got)
				}
			}
			if !l.registry.IsActive("inspect_result") || *discoveries != 2 {
				t.Fatal("discovery did not execute afresh or activate its schema")
			}
			got := invokeOperation(l, "export_asset", "same-output", `{"path":"one.bin"}`)
			if got.failed || len(got.followUps) != 1 || got.followUps[0].ToolCallID != "same-output" {
				t.Fatal("completed operation lost its call/result protocol")
			}
			if preserves {
				if !got.inert || *saves != 1 || *checks != 1 {
					t.Fatalf("duplicate effect not removed: saves/checks=%d/%d result=%+v", *saves, *checks, got)
				}
			} else if got.inert || *saves != 2 || *checks != 0 {
				t.Fatal("an untrusted discovery-name tool skipped the evidence barrier")
			}
			t.Logf("discovery_executions=%d effect_executions=%d live_state_checks=%d", *discoveries, *saves, *checks)
		})
	}
}

func TestCompletedOperationDiscoveryKeepsAuthorizationAndActualStateChecks(t *testing.T) {
	for _, change := range []string{"denied", "modified", "missing"} {
		t.Run(change, func(t *testing.T) {
			l, saves, checks := operationFixture(t)
			registerReceiptDiscovery(t, l, true)
			tool, _ := l.registry.Get("export_asset")
			denied, liveCheck := false, tool.ReplaySuccess
			tool.ReplaySuccess = func(ctx context.Context, args json.RawMessage, prior tools.Result) (tools.Result, bool) {
				if denied {
					*checks = *checks + 1
					return tools.Result{Err: errors.New("current permission denied")}, true
				}
				return liveCheck(ctx, args, prior)
			}
			// Replacing the registry before any effect establishes this tool's
			// live checker as the actual registered contract.
			r := tools.NewRegistry()
			for _, name := range l.registry.Names() {
				spec, _ := l.registry.Get(name)
				if name == tool.Name {
					spec = tool
				} else if name == "tool_search" {
					spec = tools.NewToolSearcher(r, nil).Spec()
				}
				r.MustRegister(spec)
				r.MarkAlwaysOn(name)
			}
			l.SetRegistry(r)
			invokeOperation(l, "export_asset", "first", `{"path":"one.bin"}`)
			invokeOperation(l, "tool_search", "discover", `{"query":"inspect_result"}`)
			key, _ := completedOperationKey("export_asset", json.RawMessage(`{"path":"one.bin"}`))
			prior, found, _ := l.completedOps.lookup(key)
			if !found {
				t.Fatal("discovery discarded the checked receipt")
			}
			evidence := prior.Operation.Evidence.(operationFixtureEvidence)
			switch change {
			case "denied":
				denied = true
			case "modified":
				if err := os.WriteFile(evidence.path, []byte("changed externally"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(evidence.path); err != nil {
					t.Fatal(err)
				}
			}
			got := invokeOperation(l, "export_asset", "recheck", `{"path":"one.bin"}`)
			if *checks != 1 {
				t.Fatal("discovery bypassed the live evidence/authorization check")
			}
			if change == "missing" {
				if got.failed || got.inert || *saves != 2 {
					t.Fatal("missing output was incorrectly reported as already complete")
				}
			} else if !got.failed || *saves != 1 {
				t.Fatal("denied or changed output was overwritten or reported complete")
			}
		})
	}
}

func TestCompletedOperationDiscoveryPreservesReadEvidenceAndParentReceipts(t *testing.T) {
	reads := 0
	l := reuseTestLoop(t, func(context.Context, json.RawMessage) (tools.Result, error) {
		reads++
		return tools.Result{Text: "fixture evidence"}, nil
	})
	registerReceiptDiscovery(t, l, true)
	reuseInvoke(l, context.Background(), "read", `{"query":"fixture"}`)
	parent := &Loop{baseDir: t.TempDir()}
	l.baseDir = parent.baseDir
	key := sha256.Sum256([]byte("parent-output"))
	_, _, generation := parent.completedOps.lookup(key)
	parent.completedOps.record(generation, key, tools.Result{Text: "saved", Operation: &core.VerifiedOperation{Summary: "parent output saved"}})
	ctx := withWorkerEffectBarrier(context.Background(), parent.workerEffectBarrierObserver(context.Background()))
	got := l.invoke(ctx, llm.ToolCall{ID: "discovery", Name: "tool_search", Arguments: `{"query":"inspect_result"}`}, make(chan Event, 32))
	if got.failed || parent.completedOps.context() == "" {
		t.Fatal("metadata discovery invalidated its coordinator's verified output")
	}
	if got := reuseInvoke(l, context.Background(), "reread", `{"query":"fixture"}`); got.failed || reads != 1 || !strings.Contains(got.followUps[0].Content, "[reuse]") {
		t.Fatal("metadata discovery discarded the same accepted read's freshness contract")
	}
	// A real effect continues to invalidate the parent and the local read cache.
	l.registry.MustRegister(tools.Tool{Name: "change_fixture", Description: "fixture mutation", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "fixture changed"}, nil
		}})
	l.registry.MarkAlwaysOn("change_fixture")
	l.invoke(ctx, llm.ToolCall{ID: "mutation", Name: "change_fixture", Arguments: `{}`}, make(chan Event, 32))
	if parent.completedOps.context() != "" {
		t.Fatal("effect did not invalidate coordinator evidence")
	}
	reuseInvoke(l, context.Background(), "fresh-read", `{"query":"fixture"}`)
	if reads != 2 {
		t.Fatal("effect did not invalidate read reuse")
	}
}

func TestDiscoveryPreservesEvidenceCapabilityDoesNotEnableConcurrentReads(t *testing.T) {
	l, _, _ := operationFixture(t)
	registerReceiptDiscovery(t, l, true)
	if l.allReadOnlyCalls([]llm.ToolCall{
		{Name: "tool_search", Arguments: `{"query":"inspect_result"}`},
		{Name: "inspect_fixture", Arguments: `{}`},
	}) {
		t.Fatal("metadata discovery gained read-only concurrency")
	}
	// Reuse is still opt-in and the capability alone creates no TTL.
	spec, _ := l.registry.Get("tool_search")
	if spec.ReuseTTL != 0 || spec.RefreshArg != "" || spec.ReadOnly {
		t.Fatal("evidence preservation bypassed discovery execution")
	}
}

func TestDiscoveryPreservesEvidenceCapabilityDoesNotGrantTTLReuse(t *testing.T) {
	l, _, _ := operationFixture(t)
	calls := 0
	l.registry.MustRegister(tools.Tool{Name: "catalog_metadata", Description: "fixture discovery metadata", PreservesEvidence: true,
		ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: reuseTestSchema,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			calls++
			return tools.Result{Text: "fresh metadata"}, nil
		}})
	l.registry.MarkAlwaysOn("catalog_metadata")
	for _, id := range []string{"first", "again"} {
		if got := invokeOperation(l, "catalog_metadata", id, `{"query":"fixture"}`); got.failed || strings.Contains(got.followUps[0].Content, "[reuse]") {
			t.Fatal("metadata capability accidentally enabled result reuse")
		}
	}
	if calls != 2 || len(l.resultReuse.entries) != 0 || len(l.resultReuse.pending) != 0 {
		t.Fatalf("non-readonly metadata acquired a cache entry: calls=%d entries=%d pending=%d", calls, len(l.resultReuse.entries), len(l.resultReuse.pending))
	}
}

func TestCompletedOperationDiscoveryStillCompletesMultipleOutputsAndInspection(t *testing.T) {
	l, saves, checks := operationFixture(t)
	l.maxSteps = 8
	discoveries := registerReceiptDiscovery(t, l, true)
	call := func(id, name, args string) []llm.Delta {
		return []llm.Delta{{ToolCall: &llm.ToolCall{ID: id, Name: name, Arguments: args}}, {FinishReason: "tool_calls"}}
	}
	p := &stubProvider{name: "discovery-fixture", scripts: [][]llm.Delta{
		call("first", "export_asset", `{"path":"one.bin"}`),
		call("discover", "tool_search", `{"query":"inspect_result"}`),
		call("repeat", "export_asset", `{"path":"one.bin"}`),
		call("second", "export_asset", `{"path":"two.bin"}`),
		call("inspect", "inspect_result", `{}`),
		{{Content: "Both requested outputs saved and inspected."}, {FinishReason: "stop"}},
	}}
	l.provider = p
	done := false
	for _, event := range drainEvents(t, mustRun(t, l, "Save two different outputs and inspect both.")) {
		switch e := event.(type) {
		case ErrorEvent:
			t.Fatal(e.Err)
		case DoneEvent:
			done = true
		}
	}
	if !done || *saves != 2 || *checks != 1 || *discoveries != 1 || p.calls != 6 {
		t.Fatalf("incorrect completion: done=%v saves/checks/discoveries/modelrequests=%d/%d/%d/%d", done, *saves, *checks, *discoveries, p.calls)
	}
	if !strings.Contains(l.Messages[len(l.Messages)-1].TextOnly().Content, "Both requested outputs saved and inspected") {
		t.Fatal("final answer was suppressed or replaced by a heuristic stop")
	}
	if len(l.completedOps.entries) != 0 {
		t.Fatal("completed receipts escaped their Run")
	}
}

func TestCompletedOperationDiscoveryStillInvalidatesAfterChanges(t *testing.T) {
	for _, boundary := range []string{"mutation", "instruction", "new-run", "registry-replacement"} {
		t.Run(boundary, func(t *testing.T) {
			l, saves, checks := operationFixture(t)
			registerReceiptDiscovery(t, l, true)
			invokeOperation(l, "export_asset", "first", `{"path":"one.bin"}`)
			invokeOperation(l, "tool_search", "discover", `{"query":"inspect_result"}`)
			switch boundary {
			case "mutation":
				invokeOperation(l, "modify_fixture", "edit", `{}`)
			case "instruction":
				l.InjectUserMessage(context.Background(), "Export the same path again with the new instruction.")
			case "new-run":
				l.provider = &stubProvider{name: "new-instruction", scripts: [][]llm.Delta{
					{{ToolCall: &llm.ToolCall{ID: "new-export", Name: "export_asset", Arguments: `{"path":"one.bin"}`}}},
					{{Content: "New export complete.", FinishReason: "stop"}},
				}}
				for _, event := range drainEvents(t, mustRun(t, l, "Export the same path again.")) {
					if failure, ok := event.(ErrorEvent); ok {
						t.Fatal(failure.Err)
					}
				}
			case "registry-replacement":
				r := tools.NewRegistry()
				tool, _ := l.registry.Get("export_asset")
				r.MustRegister(tool)
				r.MarkAlwaysOn(tool.Name)
				l.SetRegistry(r)
			}
			if l.completedOps.context() != "" {
				t.Fatal("old receipt survived a new instruction or effect boundary")
			}
			if boundary != "new-run" {
				invokeOperation(l, "export_asset", "fresh", `{"path":"one.bin"}`)
			}
			if *saves != 2 || *checks != 0 {
				t.Fatalf("boundary reused old effect: %d saves/%d checks", *saves, *checks)
			}
		})
	}
}
