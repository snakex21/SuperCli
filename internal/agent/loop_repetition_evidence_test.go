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
)

func TestObservedWebReadsCompareActualEvidence(t *testing.T) {
	for _, name := range []string{"web_lookup", "web_search", "web_fetch"} {
		t.Run(name, func(t *testing.T) {
			call := llm.ToolCall{Name: name, Arguments: `{"url":"https://example.org/page","query":"public resources"}`}
			var p repeatProgress
			for i := 0; i < 100; i++ {
				outcome := observed(call, fmt.Sprintf("revision %d", i))
				if !outcome.observation.valid {
					t.Fatal("web evidence was not observed")
				}
				if got := p.observe([]llm.ToolCall{call}, []callOutcome{outcome}); got != repeatNone {
					t.Fatalf("changed evidence signaled repetition: i=%d signal=%v", i, got)
				}
			}
		})
	}
}

func TestObservedWebRefreshPreservesRequestedEvidenceIdentity(t *testing.T) {
	for _, name := range []string{"web_lookup", "web_search", "web_fetch"} {
		t.Run(name, func(t *testing.T) {
			base := llm.ToolCall{Name: name, Arguments: `{"url":"https://example.org/page","query":"resources"}`}
			original := observed(base, "same evidence").observation
			for _, refresh := range []string{"false", "true"} {
				call := base
				call.Arguments = fmt.Sprintf(`{"url":"https://example.org/page","query":"resources","refresh":%s}`, refresh)
				fresh := observed(call, "same evidence").observation
				if fresh.key != original.key || fresh.result != original.result {
					t.Fatalf("refresh=%s hid identical web evidence", refresh)
				}
				changed := observed(call, "new evidence").observation
				if changed.key != original.key || changed.result == original.result {
					t.Fatal("fresh changed evidence lost")
				}
				var p repeatProgress
				p.observe([]llm.ToolCall{base}, []callOutcome{observed(base, "same evidence")})
				p.observe([]llm.ToolCall{call}, []callOutcome{{failed: true}})
				if len(p.unchanged.seen) != 0 || len(p.unchanged.order) != 0 {
					t.Fatal("failed refresh retained the old successful evidence")
				}
			}
			for _, invalid := range []string{`null`, `"true"`, `1`, `{}`, `[]`} {
				call := base
				call.Arguments = fmt.Sprintf(`{"url":"https://example.org/page","query":"resources","refresh":%s}`, invalid)
				if observationCallFingerprint(call) == original.key {
					t.Fatalf("invalid refresh %s aliased a valid call", invalid)
				}
			}
			other := base
			other.Arguments = `{"url":"https://example.org/page","query":"different resources","refresh":true}`
			if observationCallFingerprint(other) == original.key {
				t.Fatal("refresh normalization ignored substantive arguments")
			}
		})
	}
	local := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"a.go","refresh":true}`}
	plain := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"a.go"}`}
	if observationCallFingerprint(local) == observationCallFingerprint(plain) {
		t.Fatal("web-only refresh normalization changed a local read")
	}
}

func TestObservedRepeatedOperationSurvivesInterleavedReads(t *testing.T) {
	for _, failedFetch := range []bool{false, true} {
		t.Run(fmt.Sprintf("failedFetch=%v", failedFetch), func(t *testing.T) {
			var p repeatProgress
			for i := 0; i < 3; i++ {
				lookup := llm.ToolCall{ID: fmt.Sprint(i), Name: "web_lookup", Arguments: `{"query":"public resources","count":5}`}
				if i%2 == 1 {
					lookup.Arguments = `{"count":5,"query":"public resources"}`
				}
				got := p.observe([]llm.ToolCall{lookup}, []callOutcome{observed(lookup, "same search hits")})
				if i < 2 && got != repeatNone {
					t.Fatalf("meaningful first/second attempt warned at %d: %v", i, got)
				}
				if i == 2 {
					if got != repeatWarn || p.unchanged.repeats != 3 || p.unchanged.tool != "web_lookup" {
						t.Fatalf("repeated lookup escaped: signal=%v progress=%+v", got, p.unchanged)
					}
					warning := p.warningText()
					for _, want := range []string{"next dependent action", "all user requests are completed and verified", "refresh=true"} {
						if !strings.Contains(warning, want) {
							t.Fatalf("warning lacks %q: %s", want, warning)
						}
					}
					break
				}
				fetch := llm.ToolCall{Name: "web_fetch", Arguments: fmt.Sprintf(`{"url":"https://example.org/page-%d"}`, i)}
				outcome := observed(fetch, fmt.Sprintf("new page %d", i))
				if failedFetch {
					outcome = callOutcome{failed: true}
				}
				if got := p.observe([]llm.ToolCall{fetch}, []callOutcome{outcome}); got != repeatNone {
					t.Fatalf("different/failed fetch counted as unchanged evidence: %v", got)
				}
			}
		})
	}
}

func TestObservedFailureInvalidatesOnlyItsOwnRead(t *testing.T) {
	a := llm.ToolCall{Name: "web_lookup", Arguments: `{"query":"resources"}`}
	b := llm.ToolCall{Name: "web_fetch", Arguments: `{"url":"https://example.org/page"}`}
	var p repeatProgress
	for i := 0; i < 2; i++ {
		p.observe([]llm.ToolCall{a}, []callOutcome{observed(a, "hits")})
		p.observe([]llm.ToolCall{b}, []callOutcome{observed(b, "body")})
	}
	p.cooldown = 0
	p.observe([]llm.ToolCall{b}, []callOutcome{{failed: true}})
	if _, retained := p.unchanged.seen[toolCallFingerprint(b.Name, b.Arguments)]; retained {
		t.Fatal("failed read retained its old successful result")
	}
	if got := p.observe([]llm.ToolCall{b}, []callOutcome{observed(b, "body")}); got != repeatNone {
		t.Fatalf("retry after failure warned: %v", got)
	}
	if got := p.observe([]llm.ToolCall{a}, []callOutcome{observed(a, "hits")}); got != repeatWarn {
		t.Fatalf("unrelated successful lookup history was lost: %v", got)
	}
	if len(p.unchanged.order) != len(p.unchanged.seen) {
		t.Fatal("failed read left duplicate history keys")
	}
}

func TestObservedBatchRetainsIndependentSuccessAcrossReadFailure(t *testing.T) {
	a := llm.ToolCall{Name: "web_lookup", Arguments: `{"query":"resources"}`}
	b := llm.ToolCall{Name: "web_fetch", Arguments: `{"url":"https://example.org/page"}`}
	var p repeatProgress
	for i := 0; i < 3; i++ {
		got := p.observe([]llm.ToolCall{a, b}, []callOutcome{observed(a, "hits"), {failed: true}})
		if i < 2 && got != repeatNone {
			t.Fatalf("failed request created a cycle at %d: %v", i, got)
		}
		if i == 2 && got != repeatWarn {
			t.Fatalf("failed unrelated read erased repeated successful evidence: %v", got)
		}
	}
	var failures repeatProgress
	for i := 0; i < repeatHardLimit-1; i++ {
		if got := failures.observe([]llm.ToolCall{b}, []callOutcome{{failed: true}}); got != repeatNone {
			t.Fatalf("failed-only calls counted as successful repetition: %v", got)
		}
	}
}

func TestObservedStableCycleStopsAfterEightUnchangedRounds(t *testing.T) {
	a := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"a.go"}`}
	b := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"b.go"}`}
	var p repeatProgress
	p.observe([]llm.ToolCall{a, b}, []callOutcome{observed(a, "a"), observed(b, "b")})
	warned := false
	for i := 0; i < unchangedRoundLimit; i++ {
		got := p.observe([]llm.ToolCall{a, b}, []callOutcome{observed(a, "a"), observed(b, "b")})
		warned = warned || got == repeatWarn
		if p.unchanged.rounds != i+1 || (got == repeatAbort) != (i == unchangedRoundLimit-1) {
			t.Fatalf("round=%d signal=%v progress=%+v", i+1, got, p.unchanged)
		}
	}
	if !warned || !strings.Contains(p.repeatAbortText(), "8 consecutive tool rounds") || strings.Contains(p.repeatAbortText(), "same tool call") {
		t.Fatalf("cycle warning/abort reason incorrect: warned=%v text=%s", warned, p.repeatAbortText())
	}
}

func TestObservedStreakResetsForFreshEvidenceAndBarriers(t *testing.T) {
	read := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"a.go"}`}
	for _, kind := range []string{"new", "changed", "failure", "mutation", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			var p repeatProgress
			for cycle := 0; cycle < 4; cycle++ {
				for i := 0; i < unchangedRoundLimit-1; i++ {
					if got := p.observe([]llm.ToolCall{read}, []callOutcome{observed(read, "body")}); got == repeatAbort {
						t.Fatalf("aborted before fresh evidence: cycle=%d i=%d", cycle, i)
					}
				}
				call, outcome := read, observed(read, "fresh body")
				switch kind {
				case "new":
					call.Arguments = fmt.Sprintf(`{"file":"new-%d.go"}`, cycle)
					outcome = observed(call, "body")
				case "failure":
					outcome = callOutcome{failed: true}
				case "mutation":
					call = llm.ToolCall{Name: "patch_file", Arguments: "{}"}
					outcome = callOutcome{}
				case "unknown":
					call = llm.ToolCall{Name: "custom_operation", Arguments: "{}"}
					outcome = callOutcome{}
				}
				if got := p.observe([]llm.ToolCall{call}, []callOutcome{outcome}); got == repeatAbort || p.unchanged.rounds != 0 {
					t.Fatalf("fresh evidence/barrier did not reset rounds: signal=%v rounds=%d", got, p.unchanged.rounds)
				}
			}
		})
	}
}

func TestRepeatProgressFailedCallsUseSeparateBudgetGuard(t *testing.T) {
	call := llm.ToolCall{Name: "web_fetch", Arguments: `{"url":"https://example.org/unavailable"}`}
	var p repeatProgress
	for i := 0; i < repeatHardLimit; i++ {
		call.ID = fmt.Sprint(i)
		got := p.observe([]llm.ToolCall{call}, []callOutcome{{failed: true}})
		want := repeatNone
		if i == repeatHardLimit-1 {
			want = repeatAbort
		}
		if got != want || p.unchanged.rounds != 0 {
			t.Fatalf("failure %d: signal=%v want=%v rounds=%d", i+1, got, want, p.unchanged.rounds)
		}
	}
	if !strings.Contains(p.repeatAbortText(), "failed 50 consecutive times") {
		t.Fatalf("wrong failure abort reason: %s", p.repeatAbortText())
	}
	p.observe([]llm.ToolCall{call}, []callOutcome{observed(call, "now available")})
	if p.identicalFailureStreak != 0 {
		t.Fatal("successful retry retained failure streak")
	}
	for i := 0; i < repeatHardLimit-1; i++ {
		if got := p.observe([]llm.ToolCall{call}, []callOutcome{{failed: true}}); got != repeatNone {
			t.Fatalf("failure guard did not reset after success: %v", got)
		}
	}
	other := call
	other.Arguments = `{"url":"https://example.org/different"}`
	p.observe([]llm.ToolCall{other}, []callOutcome{{failed: true}})
	if p.identicalFailureStreak != 1 {
		t.Fatal("different failed request did not reset identical failure streak")
	}
}

func TestUnobservedReadsNeverUseArgumentsAsEvidence(t *testing.T) {
	for _, name := range []string{"read_image", "code_intel", "mcp_bridge", "custom_operation"} {
		t.Run(name, func(t *testing.T) {
			call := llm.ToolCall{Name: name, Arguments: `{"path":"same"}`}
			var p repeatProgress
			for i := 0; i < 100; i++ {
				if got := p.observe([]llm.ToolCall{call}, []callOutcome{{}}); got != repeatNone {
					t.Fatalf("unobserved read guessed no progress from arguments: i=%d signal=%v", i, got)
				}
			}
		})
	}
	for _, name := range []string{"read_image", "read_pdf", "code_intel"} {
		call := llm.ToolCall{Name: name, Arguments: `{"path":"same"}`}
		var p repeatProgress
		for i := 0; i < 100; i++ {
			ob := observeToolResult(call, tools.Result{Text: "same image caption", Image: &core.ImageContent{}})
			if ob.valid || ob.comparable {
				t.Fatal("native image result became comparable textual evidence")
			}
			if got := p.observe([]llm.ToolCall{call}, []callOutcome{{observation: ob}}); got != repeatNone {
				t.Fatalf("native image result guessed repetition: tool=%s i=%d signal=%v", name, i, got)
			}
		}
	}
}

func TestOtherTextualDiscoveryGuardUsesActualResult(t *testing.T) {
	for _, name := range []string{"code_intel", "recall", "search_history"} {
		t.Run(name, func(t *testing.T) {
			call := llm.ToolCall{Name: name, Arguments: `{"query":"same"}`}
			var changed, stable repeatProgress
			for i := 0; i < repeatHardLimit; i++ {
				outcome := observed(call, fmt.Sprintf("new evidence %d", i))
				if outcome.observation.valid || !outcome.observation.comparable {
					t.Fatal("discovery accidentally became an unchanged full-round observation")
				}
				if got := changed.observe([]llm.ToolCall{call}, []callOutcome{outcome}); got != repeatNone {
					t.Fatalf("changed textual discovery counted as repetition: %v", got)
				}
				got := stable.observe([]llm.ToolCall{call}, []callOutcome{observed(call, "stable evidence")})
				if (got == repeatAbort) != (i == repeatHardLimit-1) || stable.unchanged.rounds != 0 {
					t.Fatalf("budget guard i=%d signal=%v rounds=%d", i, got, stable.unchanged.rounds)
				}
			}
		})
	}
	for _, name := range []string{"read_image", "mcp_bridge", "scratchpad", "apply_skill", "ctx_execute", "write_file"} {
		ob := observed(llm.ToolCall{Name: name, Arguments: "{}"}, "same text").observation
		if ob.valid || ob.comparable {
			t.Fatalf("image or possible side effect %s became comparable read evidence", name)
		}
	}
}

func TestObservedShortCyclesIncludeResults(t *testing.T) {
	for _, period := range []int{2, 3} {
		for _, changed := range []bool{false, true} {
			t.Run(fmt.Sprintf("period=%d/changed=%v", period, changed), func(t *testing.T) {
				var p repeatProgress
				warned := false
				for i := 0; i < period*2; i++ {
					call := llm.ToolCall{Name: "read_lines", Arguments: fmt.Sprintf(`{"file":"%d.go"}`, i%period)}
					body := fmt.Sprintf("body %d", i%period)
					if changed {
						body += fmt.Sprintf(" revision %d", i)
					}
					got := p.observe([]llm.ToolCall{call}, []callOutcome{observed(call, body)})
					warned = warned || got == repeatWarn
					if got == repeatAbort || ((changed || i < period) && got != repeatNone) {
						t.Fatalf("novel evidence signaled repetition: i=%d signal=%v", i, got)
					}
				}
				if !changed && !warned {
					t.Fatal("unchanged short cycle escaped detection")
				}
			})
		}
	}
}

func TestObservedMutationAndUnknownEffectsPermitRechecks(t *testing.T) {
	read := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"a.go"}`}
	for _, barrier := range []llm.ToolCall{
		{Name: "patch_file", Arguments: `{"file":"a.go"}`},
		{Name: "task", Arguments: `{"prompt":"update a.go"}`},
		{Name: "ctx_execute", Arguments: `{"command":["custom-command"]}`},
		{Name: "read_zip", Arguments: `{"action":"extract"}`},
	} {
		t.Run(barrier.Name, func(t *testing.T) {
			var p repeatProgress
			for i := 0; i < 6; i++ {
				if got := p.observe([]llm.ToolCall{read, barrier}, []callOutcome{observed(read, "body"), {}}); got != repeatNone {
					t.Fatalf("read after side effect counted as a cycle: i=%d signal=%v", i, got)
				}
				if len(p.recent) != 0 || len(p.unchanged.seen) != 0 {
					t.Fatal("side effect retained previous read evidence")
				}
			}
			if strings.Contains(p.warningText(), "refresh=true") {
				t.Fatal("local-work warning suggested a web-only argument")
			}
		})
	}
}

func TestLoopInterleavedWebRepetitionWarnsThenAllowsDependentAction(t *testing.T) {
	for _, failedFetch := range []bool{false, true} {
		t.Run(fmt.Sprintf("failedFetch=%v", failedFetch), func(t *testing.T) {
			p := &stubProvider{name: "repeated-evidence"}
			for i := 0; i < 5; i++ {
				call := llm.ToolCall{ID: fmt.Sprint(i), Name: "web_lookup", Arguments: `{"query":"public resources"}`}
				if i%2 == 1 {
					call.Name = "web_fetch"
					call.Arguments = fmt.Sprintf(`{"url":"https://example.org/page-%d"}`, i)
				}
				p.scripts = append(p.scripts, []llm.Delta{{ToolCall: &call}, {FinishReason: "tool_calls"}})
			}
			p.scripts = append(p.scripts,
				[]llm.Delta{{ToolCall: &llm.ToolCall{ID: "next", Name: "record_evidence", Arguments: `{}`}}, {FinishReason: "tool_calls"}},
				[]llm.Delta{{Content: "Collected and recorded the evidence.", FinishReason: "stop"}},
			)
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{Name: "web_lookup", Description: "search", Schema: `{"type":"object"}`,
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					return tools.Result{Text: "same search hits"}, nil
				}})
			reg.MustRegister(tools.Tool{Name: "web_fetch", Description: "read", Schema: `{"type":"object"}`,
				Fn: func(_ context.Context, args json.RawMessage) (tools.Result, error) {
					if failedFetch {
						return tools.Result{}, errors.New("page unavailable")
					}
					return tools.Result{Text: "page " + string(args)}, nil
				}})
			var recorded int
			reg.MustRegister(tools.Tool{Name: "record_evidence", Description: "record", Schema: `{"type":"object"}`,
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					recorded++
					return tools.Result{Text: "recorded"}, nil
				}})
			for _, name := range []string{"web_lookup", "web_fetch", "record_evidence"} {
				reg.MarkAlwaysOn(name)
			}
			l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: false})
			if err != nil {
				t.Fatal(err)
			}
			ch, err := l.Run(context.Background(), "Find public resources and record the collected evidence.")
			if err != nil {
				t.Fatal(err)
			}
			events := drainEvents(t, ch)
			for _, event := range events {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if _, ok := events[len(events)-1].(DoneEvent); !ok || recorded != 1 || p.calls != 7 {
				t.Fatalf("dependent action interrupted: recorded=%d provider calls=%d events=%#v", recorded, p.calls, events)
			}
			found := false
			for _, msg := range p.reqs[5] {
				if msg.Role == llm.RoleSystem && strings.Contains(msg.Content, "[loop] web_lookup returned the same result 3 times.") {
					found = true
				}
			}
			if !found {
				t.Fatal("model did not receive the operation-specific warning before its dependent action")
			}
			for _, n := range p.toolReqs {
				if n == 0 {
					t.Fatal("warning removed tools from the next request")
				}
			}
		})
	}
}

func TestLoopStopsUnchangedCycleWithErrorAndRetainsWork(t *testing.T) {
	p := &stubProvider{name: "unchanged-cycle"}
	const calls = 2 + unchangedRoundLimit // two fresh reads, then unchanged rounds
	for i := 0; i < calls; i++ {
		call := llm.ToolCall{ID: fmt.Sprint(i), Name: "read_lines", Arguments: fmt.Sprintf(`{"file":"%d.go"}`, i%2)}
		p.scripts = append(p.scripts, []llm.Delta{{ToolCall: &call}, {FinishReason: "tool_calls"}})
	}
	p.scripts = append(p.scripts, []llm.Delta{{Content: "This must not be reported as completed.", FinishReason: "stop"}})
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "read_lines", Description: "read", Schema: `{"type":"object"}`,
		Fn: func(_ context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "unchanged file " + string(args)}, nil
		}})
	reg.MarkAlwaysOn("read_lines")
	l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: false, MaxSteps: 0})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := l.Run(context.Background(), "Inspect the files and complete the task.")
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, ch)
	warned := false
	for _, event := range events {
		if n, ok := event.(NoticeEvent); ok && strings.Contains(n.Text, "loop warning") {
			warned = true
		}
		if _, ok := event.(DoneEvent); ok {
			t.Fatal("interrupted cycle claimed task completion")
		}
	}
	last, ok := events[len(events)-1].(ErrorEvent)
	if !ok || !strings.Contains(last.Err.Error(), "8 consecutive tool rounds") || strings.Contains(last.Err.Error(), "same tool call") {
		t.Fatalf("wrong termination event: %#v", events[len(events)-1])
	}
	if !warned || p.calls != calls || last.Steps != calls {
		t.Fatalf("guard did not warn then stop at bound: warned=%v calls=%d steps=%d", warned, p.calls, last.Steps)
	}
	results := 0
	for _, message := range l.Messages {
		if message.Role == llm.RoleTool {
			results++
		}
	}
	if results != calls {
		t.Fatalf("partial work was lost: retained tool results=%d want=%d", results, calls)
	}
}
