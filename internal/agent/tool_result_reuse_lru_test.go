package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

func TestToolResultReuseLRUKeepsHotReadAcrossToolsAndProviders(t *testing.T) {
	for _, providerName := range []string{"arbitrary-backend-alpha", "unrelated-backend-beta"} {
		for _, toolName := range []string{"catalog_lookup", "package_reference"} {
			t.Run(providerName+"/"+toolName, func(t *testing.T) {
				var mu sync.Mutex
				counts := map[string]int{}
				reg := tools.NewRegistry()
				reg.MustRegister(tools.Tool{Name: toolName, Description: "trusted bounded lookup", ReadOnly: true,
					ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: reuseTestSchema,
					Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
						var args struct{ Query string }
						_ = json.Unmarshal(raw, &args)
						mu.Lock()
						counts[args.Query]++
						mu.Unlock()
						return tools.Result{Text: "Verified value: " + args.Query}, nil
					}})
				reg.MarkAlwaysOn(toolName)
				call := func(id, query string) llm.Delta {
					raw, _ := json.Marshal(map[string]string{"query": query})
					return llm.Delta{ToolCall: &llm.ToolCall{ID: id, Name: toolName, Arguments: string(raw)}}
				}
				one := func(id, query string) []llm.Delta { return []llm.Delta{call(id, query), {FinishReason: "tool_calls"}} }
				var siblings []llm.Delta
				for i := 1; i < resultReuseEntries; i++ {
					siblings = append(siblings, call(fmt.Sprint("sibling-", i), fmt.Sprint("other-", i)))
				}
				siblings = append(siblings, llm.Delta{FinishReason: "tool_calls"})
				p := &stubProvider{name: providerName, scripts: [][]llm.Delta{
					one("first", "active"), siblings, one("recent-hit", "active"), one("pressure", "other-16"),
					one("after-pressure", "active"), one("second-goal", "independent-result"),
					{{Content: "Active and independent results checked."}, {FinishReason: "stop"}},
				}}
				loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, System: "fixture", MaxSteps: 10})
				if err != nil {
					t.Fatal(err)
				}
				var done bool
				for _, event := range drainEvents(t, mustRun(t, loop, "Find both active and independent results.")) {
					switch e := event.(type) {
					case ErrorEvent:
						t.Fatal(e.Err)
					case DoneEvent:
						done = true
					}
				}
				if counts["active"] != 1 || counts["independent-result"] != 1 || len(counts) != 18 || p.calls != 7 || !done {
					t.Fatalf("redundant/incomplete work: active=%d independent=%d distinct=%d model=%d done=%v", counts["active"], counts["independent-result"], len(counts), p.calls, done)
				}
				if !strings.Contains(loop.Messages[len(loop.Messages)-1].TextOnly().Content, "Active and independent") {
					t.Fatal("final answer was suppressed or replaced")
				}
				var afterPressure string
				for _, message := range p.reqs[5] {
					if message.Role == llm.RoleTool && message.ToolCallID == "after-pressure" {
						afterPressure = message.Content
					}
				}
				if !strings.Contains(afterPressure, "[reuse]") || !strings.Contains(afterPressure, "Verified value: active") {
					t.Fatal("reused response lost its evidence, age label or protocol pair")
				}
			})
		}
	}
}

func TestToolResultReuseLRUPreservesLargeContentAndOutputHandles(t *testing.T) {
	const marker = "verified-middle-record-9182"
	large := "BEGIN\n" + strings.Repeat("ordinary retained line\n", 500) + marker + "\n" + strings.Repeat("remaining retained line\n", 500) + "END"
	counts := map[string]int{}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "catalog_lookup", Description: "trusted bounded lookup", ReadOnly: true,
		ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: reuseTestSchema,
		Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
			var args struct{ Query string }
			_ = json.Unmarshal(raw, &args)
			counts[args.Query]++
			if args.Query == "active" {
				return tools.Result{Text: large}, nil
			}
			return tools.Result{Text: "value for " + args.Query}, nil
		}})
	loop := makeLoop(t, echoProvider("unrelated-provider"), reg, "fixture")
	call := func(query string) (toolResult, []Event) {
		raw, _ := json.Marshal(map[string]string{"query": query})
		events := make(chan Event, 16)
		got := loop.invoke(context.Background(), llm.ToolCall{ID: query, Name: "catalog_lookup", Arguments: string(raw)}, events)
		close(events)
		return got, drainEvents(t, events)
	}
	first, firstEvents := call("active")
	for i := 1; i < resultReuseEntries; i++ {
		call(fmt.Sprint("other-", i))
	}
	call("active")
	call("pressure")
	last, lastEvents := call("active")
	if counts["active"] != 1 || first.failed || last.failed || !strings.Contains(last.followUps[0].Content, "[reuse]") {
		t.Fatalf("large result not reused: calls=%d first=%+v last=%+v", counts["active"], first, last)
	}
	for i, result := range []toolResult{first, last} {
		content := result.followUps[0].Content
		handle := handleInOutput(content)
		if handle == "" || strings.Contains(content, marker) || !strings.Contains(content, "BEGIN") || !strings.Contains(content, "END") {
			t.Fatalf("result %d lost bounded preview or output reference", i)
		}
		raw, _ := json.Marshal(map[string]string{"handle": handle, "query": marker})
		full, err := reg.Execute(context.Background(), "read_output", raw)
		if err != nil || full.Err != nil || !strings.Contains(full.Text, marker) {
			t.Fatalf("result %d output handle lost complete evidence: err=%v result=%+v", i, err, full)
		}
	}
	for i, events := range [][]Event{firstEvents, lastEvents} {
		found := false
		for _, event := range events {
			if ev, ok := event.(ToolResultEvent); ok && ev.ID == "active" && ev.Output == large {
				found = true
			}
		}
		if !found {
			t.Fatalf("result %d UI event lost full content or call identity", i)
		}
	}
}

func TestResultReuseLRUHitsDoNotExtendTTLOrMutateCachedResult(t *testing.T) {
	var cache toolResultReuse
	start := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	now := start
	clock := func() time.Time { return now }
	first, second := sha256.Sum256([]byte("first")), sha256.Sum256([]byte("second"))
	for _, key := range [][32]byte{first, second} {
		_, _, _, claim, _ := cache.acquire(context.Background(), key, false, clock)
		value := tools.Result{Text: "complete UI content", ModelText: "complete model content"}
		cache.finish(claim, &value, time.Minute, now)
	}
	originalBytes := cache.bytes
	now = start.Add(59 * time.Second)
	got, age, hit, _, _ := cache.acquire(context.Background(), first, false, clock)
	if !hit || age != 59*time.Second || cache.order[len(cache.order)-1] != first || cache.bytes != originalBytes {
		t.Fatal("LRU hit changed age, body accounting or ordering")
	}
	got.Text, got.ModelText = "caller replacement", "caller replacement"
	again, _, hit, _, _ := cache.acquire(context.Background(), first, false, clock)
	if !hit || again.Text != "complete UI content" || again.ModelText != "complete model content" {
		t.Fatal("returned result container aliases the cached container")
	}
	now = start.Add(time.Minute)
	_, _, hit, claim, _ := cache.acquire(context.Background(), first, false, clock)
	if hit {
		t.Fatal("recent accesses extended observation lifetime")
	}
	cache.finish(claim, nil, 0, now)
}

func TestToolResultReuseDoesNotShareOperationEvidenceThroughTTL(t *testing.T) {
	// Evidence is opaque to the TTL cache. It cannot defensively copy a future
	// tool's reference-shaped payload, so effect results must stay out entirely.
	evidence := map[string]string{"value": "original"}
	operation := &core.VerifiedOperation{Summary: "effect verified", Evidence: evidence}
	calls := 0
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "catalog_lookup", Description: "misconfigured observation carrying an effect", ReadOnly: true,
		ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: reuseTestSchema,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			calls++
			return tools.Result{Text: evidence["value"], Operation: operation}, nil
		}})
	loop := makeLoop(t, echoProvider("unrelated-provider"), reg, "fixture")
	call := llm.ToolCall{ID: "lookup", Name: "catalog_lookup", Arguments: `{"query":"active"}`}
	first := loop.invoke(context.Background(), call, make(chan Event, 16))
	if first.failed || len(loop.resultReuse.entries) != 0 || len(loop.resultReuse.pending) != 0 {
		t.Fatal("effect metadata retained in observation TTL cache")
	}
	evidence["value"] = "updated"
	operation.Summary = "updated effect"
	second := loop.invoke(context.Background(), call, make(chan Event, 16))
	if second.failed || calls != 2 || strings.Contains(second.followUps[0].Content, "[reuse]") || second.followUps[0].Content != "updated" {
		t.Fatalf("mutable effect evidence reused through TTL: calls=%d result=%+v", calls, second)
	}
}

func TestResultReuseLRUByteLimitEvictsLeastRecentlyUsed(t *testing.T) {
	var cache toolResultReuse
	now := time.Now()
	clock := func() time.Time { return now }
	keys := make([][32]byte, 9)
	for i := range keys {
		keys[i] = sha256.Sum256([]byte(fmt.Sprint(i)))
		if i == 8 {
			_, _, hit, _, _ := cache.acquire(context.Background(), keys[0], false, clock)
			if !hit {
				t.Fatal("fixture hot entry missing")
			}
		}
		_, _, _, claim, _ := cache.acquire(context.Background(), keys[i], false, clock)
		value := tools.Result{Text: strings.Repeat("x", resultReuseEntryBytes)}
		cache.finish(claim, &value, time.Minute, now)
	}
	if len(cache.entries) != 8 || len(cache.order) != 8 || cache.bytes != resultReuseBytes {
		t.Fatalf("changed cache bounds: entries/order/bytes=%d/%d/%d", len(cache.entries), len(cache.order), cache.bytes)
	}
	if _, found := cache.entries[keys[0]]; !found {
		t.Fatal("recently touched result evicted under byte pressure")
	}
	if _, found := cache.entries[keys[1]]; found {
		t.Fatal("least recently used result survived byte pressure")
	}
}

func TestToolResultReuseLRUStillHonorsCurrentBoundaries(t *testing.T) {
	for _, boundary := range []string{"refresh", "effect", "instruction", "new-run", "registry", "cancel"} {
		t.Run(boundary, func(t *testing.T) {
			counts := map[string]int{}
			denied := false
			lookup := tools.Tool{Name: "catalog_lookup", Description: "trusted bounded lookup", ReadOnly: true, ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: reuseTestSchema,
				Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
					var args struct{ Query string }
					_ = json.Unmarshal(raw, &args)
					counts[args.Query]++
					if denied {
						return tools.Result{Err: errors.New("current access denied")}, nil
					}
					return tools.Result{Text: "current value: " + args.Query}, nil
				}}
			reg := tools.NewRegistry()
			reg.MustRegister(lookup)
			reg.MarkAlwaysOn(lookup.Name)
			loop := makeLoop(t, echoProvider("unrelated-provider"), reg, "fixture")
			call := func(ctx context.Context, query string, fresh bool) toolResult {
				args, _ := json.Marshal(map[string]any{"query": query, "refresh": fresh})
				return loop.invoke(ctx, llm.ToolCall{ID: query, Name: lookup.Name, Arguments: string(args)}, make(chan Event, 16))
			}
			call(context.Background(), "active", false)
			for i := 1; i < resultReuseEntries; i++ {
				call(context.Background(), fmt.Sprint("other-", i), false)
			}
			call(context.Background(), "active", false)
			call(context.Background(), "pressure", false)
			if counts["active"] != 1 {
				t.Fatal("fixture repeated the hot lookup")
			}
			ctx := context.Background()
			fresh := false
			switch boundary {
			case "refresh":
				fresh, denied = true, true
			case "effect":
				reg.MustRegister(tools.Tool{Name: "change_access", Description: "controlled state change", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					denied = true
					return tools.Result{Err: errors.New("state changed before failure")}, nil
				}})
				loop.invoke(ctx, llm.ToolCall{ID: "change", Name: "change_access", Arguments: "{}"}, make(chan Event, 16))
			case "instruction":
				loop.openInterjections(ctx)
				if !loop.QueueInterjection("observe the changed state") {
					t.Fatal("new instruction rejected")
				}
				loop.drainInterjections(ctx, make(chan Event, 16), true, false)
				denied = true
			case "new-run":
				drainEvents(t, mustRun(t, loop, "new independent instruction"))
				denied = true
			case "registry":
				current := tools.NewRegistry()
				current.MustRegister(lookup)
				current.MarkAlwaysOn(lookup.Name)
				loop.SetRegistry(current)
				denied = true
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got := call(ctx, "active", fresh)
			wantCalls := 2
			if boundary == "cancel" {
				wantCalls = 1
			}
			if !got.failed || counts["active"] != wantCalls || strings.Contains(got.followUps[0].Content, "[reuse]") {
				t.Fatalf("stale reuse bypassed current boundary: calls=%d result=%+v", counts["active"], got)
			}
			if boundary != "cancel" && !strings.Contains(got.followUps[0].Content, "current access denied") {
				t.Fatal("current access failure hidden by prior success")
			}
		})
	}
}

func TestToolResultReuseLRUPureRegistryVisibilityPreservesObservation(t *testing.T) {
	calls := 0
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "catalog_lookup", Description: "trusted bounded lookup", ReadOnly: true,
		ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: reuseTestSchema,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			calls++
			return tools.Result{Text: "immutable verified value"}, nil
		}})
	loop := makeLoop(t, echoProvider("arbitrary-provider"), reg, "fixture")
	call := llm.ToolCall{ID: "lookup", Name: "catalog_lookup", Arguments: `{"query":"active"}`}
	first := loop.invoke(context.Background(), call, make(chan Event, 16))
	reg.Activate("catalog_lookup")
	reg.Deactivate("catalog_lookup")
	reg.Activate("catalog_lookup")
	second := loop.invoke(context.Background(), call, make(chan Event, 16))
	if first.failed || second.failed || calls != 1 || !strings.Contains(second.followUps[0].Content, "[reuse]") {
		t.Fatal("visibility-only changes invalidated an unchanged observation contract")
	}
}
