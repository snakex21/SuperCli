package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

const reuseTestSchema = `{"type":"object","properties":{"query":{"type":"string"},"refresh":{"type":"boolean"}},"required":["query"],"additionalProperties":false}`

func reuseTestLoop(t *testing.T, fn func(context.Context, json.RawMessage) (tools.Result, error)) *Loop {
	t.Helper()
	r := tools.NewRegistry()
	r.MustRegister(tools.Tool{Name: "web_lookup", Description: "fixture", ReadOnly: true, ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: reuseTestSchema, Fn: fn})
	r.MarkAlwaysOn("web_lookup")
	return makeLoop(t, echoProvider("fixture"), r, "system")
}

func reuseInvoke(l *Loop, ctx context.Context, id, args string) toolResult {
	return l.invoke(ctx, llm.ToolCall{ID: id, Name: "web_lookup", Arguments: args}, make(chan Event, 16))
}

func TestToolResultReuseVerifiedResultsRefreshAndFailure(t *testing.T) {
	var calls atomic.Int32
	l := reuseTestLoop(t, func(context.Context, json.RawMessage) (tools.Result, error) {
		n := calls.Add(1)
		if n == 3 {
			return tools.Result{Err: errors.New("temporary outage")}, nil
		}
		return tools.Result{Text: fmt.Sprintf("result %d", n)}, nil
	})
	first := reuseInvoke(l, context.Background(), "first", `{"query":"cats"}`)
	second := reuseInvoke(l, context.Background(), "second", `{ "refresh": false, "query": "cats" }`)
	if first.failed || second.failed || calls.Load() != 1 || !strings.Contains(second.followUps[0].Content, "[reuse]") {
		t.Fatalf("not reused: calls=%d result=%+v", calls.Load(), second)
	}
	if first.observation != second.observation {
		t.Fatal("reuse hint changed observed evidence")
	}
	fresh := reuseInvoke(l, context.Background(), "fresh", `{"query":"cats","refresh":"true"}`)
	if fresh.failed || calls.Load() != 2 || strings.Contains(fresh.followUps[0].Content, "[reuse]") {
		t.Fatalf("fresh call not dispatched: %+v", fresh)
	}
	next := reuseInvoke(l, context.Background(), "next", `{"query":"cats"}`)
	if calls.Load() != 2 || !strings.Contains(next.followUps[0].Content, "result 2") {
		t.Fatal("refresh did not replace old result")
	}
	failed := reuseInvoke(l, context.Background(), "outage", `{"query":"cats","refresh":true}`)
	if !failed.failed || calls.Load() != 3 {
		t.Fatal("failed refresh concealed")
	}
	retry := reuseInvoke(l, context.Background(), "retry", `{"query":"cats"}`)
	if retry.failed || calls.Load() != 4 || strings.Contains(retry.followUps[0].Content, "[reuse]") {
		t.Fatal("old success or error replayed after failed refresh")
	}
}

func TestToolResultReuseValidationCancellationAndFailureEvidence(t *testing.T) {
	var calls int
	l := reuseTestLoop(t, func(context.Context, json.RawMessage) (tools.Result, error) {
		calls++
		return tools.Result{Text: "evidence"}, nil
	})
	reuseInvoke(l, context.Background(), "first", `{"query":"cats"}`)
	for _, args := range []string{`{"query":"cats","extra":1}`, `{"query":"cats","refresh":3}`, `{"query":false}`, `{"query":"cats"} {}`} {
		got := reuseInvoke(l, context.Background(), "invalid", args)
		if !got.failed || calls != 1 {
			t.Fatalf("invalid args reused or executed: %s %+v", args, got)
		}
	}
	l.concreteFailure.Store(true)
	reuseInvoke(l, context.Background(), "reused", `{"query":"cats"}`)
	if !l.concreteFailure.Load() {
		t.Fatal("reused evidence cleared outstanding failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := reuseInvoke(l, ctx, "cancelled", `{"query":"cats"}`)
	if calls != 1 || !got.failed {
		t.Fatal("cancelled request replayed success")
	}
}

func TestToolResultReuseVerifierFailureIsNotCached(t *testing.T) {
	var calls int
	r := tools.NewRegistry()
	r.MustRegister(tools.Tool{Name: "web_lookup", Description: "fixture", ReadOnly: true, ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: reuseTestSchema,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			calls++
			return tools.Result{Text: "unverified"}, nil
		},
		Verify: func(tools.Result) tools.VerifyVerdict {
			return tools.VerifyVerdict{OK: false, Reason: "not trustworthy"}
		}})
	l := makeLoop(t, echoProvider("fixture"), r, "system")
	for i := 0; i < 2; i++ {
		if got := reuseInvoke(l, context.Background(), fmt.Sprint(i), `{"query":"cats"}`); !got.failed {
			t.Fatal("verifier ignored")
		}
	}
	if calls != 2 {
		t.Fatal("failed verification cached")
	}
}

func TestToolResultReuseStillValidatesRequiredRefresh(t *testing.T) {
	var calls int
	r := tools.NewRegistry()
	r.MustRegister(tools.Tool{Name: "web_lookup", Description: "fixture", ReadOnly: true, ReuseTTL: time.Minute, RefreshArg: "refresh",
		Schema: `{"type":"object","properties":{"query":{"type":"string"},"refresh":{"type":"boolean","enum":[true]}},"required":["query","refresh"],"additionalProperties":false}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			calls++
			return tools.Result{Text: "accepted"}, nil
		}})
	l := makeLoop(t, echoProvider("fixture"), r, "system")
	reuseInvoke(l, context.Background(), "first", `{"query":"cats","refresh":true}`)
	for _, args := range []string{`{"query":"cats"}`, `{"query":"cats","refresh":false}`} {
		if got := reuseInvoke(l, context.Background(), "invalid", args); !got.failed || calls != 1 {
			t.Fatalf("cached hit bypassed contract: %+v calls=%d", got, calls)
		}
	}
}

func TestToolResultReuseBarriersAndRunScope(t *testing.T) {
	var calls int
	l := reuseTestLoop(t, func(context.Context, json.RawMessage) (tools.Result, error) {
		calls++
		return tools.Result{Text: "evidence"}, nil
	})
	call := func() { reuseInvoke(l, context.Background(), "read", `{"query":"cats"}`) }
	call()
	call()
	l.registry.MustRegister(tools.Tool{Name: "fixture_write", Description: "changes state", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "changed"}, nil
	}})
	l.invoke(context.Background(), llm.ToolCall{ID: "write", Name: "fixture_write", Arguments: `{}`}, make(chan Event, 16))
	call()
	if calls != 2 {
		t.Fatal("mutation did not invalidate")
	}
	l.openInterjections(context.Background())
	if !l.QueueInterjection("fetch again") {
		t.Fatal("steering rejected")
	}
	l.drainInterjections(context.Background(), make(chan Event, 16), true, false)
	call()
	if calls != 3 {
		t.Fatal("steering did not invalidate")
	}
	for i := 0; i < 2; i++ {
		drainEvents(t, mustRun(t, l, "hello"))
		call()
	}
	if calls != 5 {
		t.Fatal("reuse leaked across runs")
	}
	l.SetRegistry(l.registry)
	call()
	if calls != 6 {
		t.Fatal("registry swap retained cached result")
	}
	l.appendBackgroundMessages(context.Background(), []backgroundMessage{{content: "Fetch a fresh result for the changed request"}})
	call()
	if calls != 7 {
		t.Fatal("background instruction retained cached evidence")
	}
}

func TestToolResultReuseParallelDuplicateCompletion(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	l := reuseTestLoop(t, func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return tools.Result{Text: "same public result"}, nil
		case <-ctx.Done():
			return tools.Result{Err: ctx.Err()}, nil
		}
	})
	done := make(chan []callOutcome, 1)
	go func() {
		_, results := l.invokeToolCalls(context.Background(), []llm.ToolCall{{ID: "a", Name: "web_lookup", Arguments: `{"query":"cats"}`}, {ID: "b", Name: "web_lookup", Arguments: `{"query":"cats"}`}}, make(chan Event, 16))
		done <- results
	}()
	<-started
	close(release)
	results := <-done
	if calls.Load() != 1 || len(results) != 2 || results[0].failed || results[1].failed || results[0].observation != results[1].observation {
		t.Fatalf("duplicates not coalesced: calls=%d result=%+v", calls.Load(), results)
	}
	last := l.Messages[len(l.Messages)-2:]
	if last[0].ToolCallID != "a" || last[1].ToolCallID != "b" {
		t.Fatal("protocol pairing changed")
	}
}

func TestResultReuseWaitCancellationAndExcludedResults(t *testing.T) {
	var cache toolResultReuse
	now := time.Now()
	clock := func() time.Time { return now }
	key := sha256.Sum256([]byte("key"))
	_, _, _, owner, _ := cache.acquire(context.Background(), key, false, clock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, _, _, err := cache.acquire(ctx, key, false, clock); done <- err }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("duplicate did not respect cancellation")
	}
	cache.finish(owner, nil, 0, now)
	for _, value := range []tools.Result{
		{Err: errors.New("failed")}, {Text: "inert", Inert: true}, {Text: "retained", RetainedText: "full body"},
		{Text: "pixels", Image: &tools.ImageContent{Data: []byte{1}}}, {Text: strings.Repeat("x", resultReuseEntryBytes+1)}, {},
	} {
		_, _, _, claim, _ := cache.acquire(context.Background(), key, false, clock)
		cache.finish(claim, &value, time.Minute, now)
		if len(cache.entries) != 0 || len(cache.pending) != 0 {
			t.Fatalf("excluded result retained: %+v", value)
		}
	}
}

func TestLoopReusesRepeatedSearchWithoutEndingMixedWork(t *testing.T) {
	var searches, saves int
	r := tools.NewRegistry()
	r.MustRegister(tools.Tool{Name: "web_lookup", Description: "fixture", ReadOnly: true, ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: reuseTestSchema,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			searches++
			return tools.Result{Text: "Selected public page https://example.org/page"}, nil
		}})
	r.MustRegister(tools.Tool{Name: "web_fetch", Description: "fixture", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "Two declared asset URLs"}, nil
	}})
	r.MustRegister(tools.Tool{Name: "fixture_save", Description: "fixture", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		saves++
		return tools.Result{Text: "Saved and verified"}, nil
	}})
	for _, name := range []string{"web_lookup", "web_fetch", "fixture_save"} {
		r.MarkAlwaysOn(name)
	}
	call := func(id, name, args string) []llm.Delta {
		return []llm.Delta{{ToolCall: &llm.ToolCall{ID: id, Name: name, Arguments: args}}, {FinishReason: "tool_calls"}}
	}
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		call("1", "web_lookup", `{"query":"two public assets"}`), call("2", "web_fetch", `{"url":"https://example.org/page"}`),
		call("3", "web_lookup", `{"query":"two public assets"}`), call("4", "web_lookup", `{"query":"two public assets"}`),
		call("5", "fixture_save", `{"path":"first.bin"}`), call("6", "fixture_save", `{"path":"second.bin"}`),
		{{Content: "Both requested assets saved and verified."}, {FinishReason: "stop"}},
	}}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: r, System: "fixture", MaxSteps: 10})
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, mustRun(t, l, "Save two different public assets, then verify both."))
	if searches != 1 || saves != 2 {
		t.Fatalf("searches=%d saves=%d", searches, saves)
	}
	var done, warning bool
	for _, event := range events {
		switch e := event.(type) {
		case DoneEvent:
			done = true
		case ErrorEvent:
			t.Fatal(e.Err)
		case NoticeEvent:
			warning = warning || strings.Contains(e.Text, "loop warning")
		}
	}
	if !done || !warning {
		t.Fatalf("completion=%t warning=%t", done, warning)
	}
	var hint bool
	for _, request := range p.reqs {
		for _, m := range request {
			hint = hint || strings.Contains(m.Content, "[reuse]")
		}
	}
	if !hint {
		t.Fatal("model did not learn that repeated request used existing evidence")
	}
	if len(l.resultReuse.entries) != 0 {
		t.Fatal("result bodies retained after completed Run")
	}
}

func TestResultReuseBoundsExpiryAndRefreshRace(t *testing.T) {
	var cache toolResultReuse
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	key := sha256.Sum256([]byte("key"))
	_, _, _, old, err := cache.acquire(context.Background(), key, false, clock)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, fresh, _ := cache.acquire(context.Background(), key, true, clock)
	latest := tools.Result{Text: "latest"}
	stale := tools.Result{Text: "stale"}
	cache.finish(fresh, &latest, time.Minute, now)
	cache.finish(old, &stale, time.Minute, now)
	got, _, hit, _, _ := cache.acquire(context.Background(), key, false, clock)
	if !hit || got.Text != "latest" {
		t.Fatal("older completion overwrote refresh")
	}
	now = now.Add(time.Minute)
	_, _, hit, claim, _ := cache.acquire(context.Background(), key, false, clock)
	if hit {
		t.Fatal("expired evidence reused")
	}
	cache.reset()
	cache.finish(claim, &stale, time.Minute, now)
	if len(cache.entries) != 0 {
		t.Fatal("late result survived reset")
	}
	for i := 0; i < 32; i++ {
		k := sha256.Sum256([]byte(fmt.Sprint(i)))
		_, _, _, claim, _ := cache.acquire(context.Background(), k, false, clock)
		value := tools.Result{Text: strings.Repeat("x", resultReuseEntryBytes)}
		cache.finish(claim, &value, time.Hour, now)
	}
	if len(cache.entries) > resultReuseEntries || cache.bytes > resultReuseBytes {
		t.Fatalf("unbounded memory: %d/%d", len(cache.entries), cache.bytes)
	}
	for _, entry := range cache.entries {
		if entry.ttl > resultReuseMaxTTL {
			t.Fatal("TTL unbounded")
		}
	}
}

func TestReusableToolKeyNeverDropsDataOrEnablesUntrustedTools(t *testing.T) {
	tool := tools.Tool{Name: "fixture", ReadOnly: true, ReuseTTL: time.Minute, RefreshArg: "refresh"}
	a, _, ok := reusableToolKey(tool, json.RawMessage(`{"n":9007199254740992,"nested":{"b":2,"a":1}}`))
	if !ok {
		t.Fatal("valid args rejected")
	}
	b, _, _ := reusableToolKey(tool, json.RawMessage(`{"nested":{"a":1,"b":2},"n":9007199254740993}`))
	if a == b {
		t.Fatal("number precision lost")
	}
	for _, change := range []func(*tools.Tool){func(t *tools.Tool) { t.ReadOnly = false }, func(t *tools.Tool) { t.ReuseTTL = 0 }, func(t *tools.Tool) { t.RefreshArg = "" }} {
		target := tool
		change(&target)
		if _, _, eligible := reusableToolKey(target, json.RawMessage(`{}`)); eligible {
			t.Fatal("untrusted tool eligible")
		}
	}
}
