package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestSendMessageCannotBypassActiveWorkerLimit(t *testing.T) {
	provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "continued", FinishReason: "stop"}}}}
	loop := makeLoop(t, provider, tools.NewRegistry(), "")
	workers := NewWorkerRegistry()
	workers.maxActive = 1
	worker := workers.Add("general", "completed research", loop)
	markFinished(worker, "done", "retained report", "", time.Now())
	before := worker.Snapshot()
	history, _ := json.Marshal(loop.Messages)
	active, err := workers.TryAdd("general", "active task", nil)
	if err != nil {
		t.Fatal(err)
	}
	send := NewSendMessageTool(workers)
	raw, _ := json.Marshal(map[string]string{"to": worker.ID, "message": "continue"})
	result, err := send.execute(context.Background(), raw)
	if err != nil || result.Err == nil || !strings.Contains(result.Err.Error(), "worker limit reached") {
		t.Errorf("continuation bypassed the active limit: transport=%v result=%v", err, result.Err)
	}
	currentHistory, _ := json.Marshal(loop.Messages)
	if provider.calls != 0 || !reflect.DeepEqual(before, worker.Snapshot()) || string(history) != string(currentHistory) {
		t.Errorf("rejected continuation consumed model work or changed retained state: calls=%d", provider.calls)
	}
	if t.Failed() {
		return
	}
	markFinished(active, "done", "finished", "", time.Now())
	result, err = send.execute(context.Background(), raw)
	if err != nil || result.Err != nil || provider.calls != 1 {
		t.Fatalf("freed slot did not permit continuation: %v %v calls=%d", err, result.Err, provider.calls)
	}
	if workers.Counts().Running != 0 || worker.Runs != 1 {
		t.Fatal("successful continuation leaked its active slot")
	}
}

// A completion barrier keeps admitted workers active while every other caller
// finishes its capacity check. No sleeps or repeated status probes are needed.
type continuationLimitProvider struct {
	calls   atomic.Int32
	entered chan struct{}
	release <-chan struct{}
}

func (p *continuationLimitProvider) Name() string         { return "continuation-limit-fixture" }
func (p *continuationLimitProvider) SupportsVision() bool { return false }
func (p *continuationLimitProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls.Add(1)
	p.entered <- struct{}{}
	deltas := make(chan llm.Delta, 2)
	go func() {
		defer close(deltas)
		select {
		case <-ctx.Done():
			deltas <- llm.Delta{Err: ctx.Err()}
		case <-p.release:
			deltas <- llm.Delta{Content: "continued", FinishReason: "stop"}
		}
	}()
	return deltas, nil
}

func TestSendMessageConcurrentContinuationsReserveDistinctSlots(t *testing.T) {
	const total, cap = 16, 2
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	provider := &continuationLimitProvider{entered: make(chan struct{}, total), release: release}
	workers := NewWorkerRegistry()
	workers.maxActive, workers.retention = cap, total
	var ids []string
	for i := 0; i < total; i++ {
		w := workers.Add("general", "retained context", makeLoop(t, provider, tools.NewRegistry(), ""))
		markFinished(w, "done", "report", "", time.Now())
		ids = append(ids, w.ID)
	}
	send := NewSendMessageTool(workers)
	outcomes := make(chan tools.Result, total)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	for _, id := range ids {
		raw, _ := json.Marshal(map[string]string{"to": id, "message": "continue"})
		go func() {
			<-start
			result, err := send.execute(ctx, raw)
			if err != nil {
				result.Err = err
			}
			outcomes <- result
		}()
	}
	close(start)
	for i := 0; i < cap; i++ {
		select {
		case <-provider.entered:
		case <-ctx.Done():
			t.Fatal("admitted workers did not start")
		}
	}
	for i := 0; i < total-cap; i++ {
		select {
		case result := <-outcomes:
			if result.Err == nil || !strings.Contains(result.Err.Error(), "worker limit reached") {
				t.Fatalf("expected immediate capacity refusal: %v", result.Err)
			}
		case <-ctx.Done():
			t.Fatal("concurrent continuations bypassed the capacity limit or waited inside it")
		}
	}
	if got := provider.calls.Load(); got != cap {
		t.Fatalf("model requests=%d want=%d", got, cap)
	}
	if got := workers.Counts().Running; got != cap {
		t.Fatalf("active=%d want=%d", got, cap)
	}
	if _, err := workers.TryAdd("general", "new task", nil); err == nil {
		t.Fatal("new task bypassed slots reserved by continuations")
	}
	finish()
	for i := 0; i < cap; i++ {
		select {
		case result := <-outcomes:
			if result.Err != nil {
				t.Fatal(result.Err)
			}
		case <-ctx.Done():
			t.Fatal("admitted worker did not release its slot")
		}
	}
	if got := workers.Counts().Running; got != 0 {
		t.Fatalf("active slot leaked: %d", got)
	}
	if _, err := workers.TryAdd("general", "new task after completion", nil); err != nil {
		t.Fatal(err)
	}
}

func TestSendMessageCannotHijackAnUnstartedTask(t *testing.T) {
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "original task", FinishReason: "stop"}}}}
	workers := NewWorkerRegistry()
	workers.maxActive = 2
	w := workers.Add("general", "original prompt", makeLoop(t, p, tools.NewRegistry(), ""))
	raw, _ := json.Marshal(map[string]string{"to": w.ID, "message": "replace the prompt"})
	result, err := NewSendMessageTool(workers).execute(context.Background(), raw)
	if err != nil || result.Err == nil || !strings.Contains(result.Err.Error(), "already running") || p.calls != 0 || w.status() != "created" {
		t.Fatalf("queued task was hijacked: %v %v calls=%d status=%s", err, result.Err, p.calls, w.status())
	}
	if _, err := runWorkerLoop(context.Background(), w, "original prompt"); err != nil || p.calls != 1 {
		t.Fatalf("original task was lost: %v", err)
	}
}

func TestSendMessageStoppedContinuationReleasesItsSlot(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	p := &continuationLimitProvider{entered: make(chan struct{}, 1), release: release}
	workers := NewWorkerRegistry()
	workers.maxActive = 1
	w := workers.Add("general", "stoppable continuation", makeLoop(t, p, tools.NewRegistry(), ""))
	markFinished(w, "done", "report", "", time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, _ := json.Marshal(map[string]string{"to": w.ID, "message": "continue"})
	result := make(chan tools.Result, 1)
	go func() { r, _ := NewSendMessageTool(workers).execute(ctx, raw); result <- r }()
	select {
	case <-p.entered:
	case <-ctx.Done():
		t.Fatal("continuation did not start")
	}
	if err := workers.Stop(w.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-result:
		if r.Err == nil || w.status() != "stopped" {
			t.Fatalf("stop failed: %v status=%s", r.Err, w.status())
		}
	case <-ctx.Done():
		t.Fatal("stopped continuation did not finish")
	}
	if _, err := workers.TryAdd("general", "replacement task", nil); err != nil {
		t.Fatal("stopped continuation leaked its slot:", err)
	}
}

func TestContinuationDoesNotRestartAnEvictedWorkerReference(t *testing.T) {
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "unused", FinishReason: "stop"}}}}
	workers := NewWorkerRegistry()
	workers.retention = 1
	old := workers.Add("general", "old context", makeLoop(t, p, tools.NewRegistry(), ""))
	markFinished(old, "done", "kept summary", "", time.Now().Add(-time.Hour))
	recent := workers.Add("general", "new context", nil)
	markFinished(recent, "done", "recent summary", "", time.Now())
	workers.Add("general", "retention sweep", nil)
	// Model Get-before-eviction / resume-after-eviction without a timing race.
	_, err := runWorkerLoopInRegistry(context.Background(), old, "continue", workers)
	if err == nil || !strings.Contains(err.Error(), "evicted") || !strings.Contains(err.Error(), "kept summary") || p.calls != 0 || old.status() != "done" {
		t.Fatalf("stale reference restarted: %v calls=%d status=%s", err, p.calls, old.status())
	}
}

func TestContinuationAdmissionDoesNotChangeModelRequests(t *testing.T) {
	providers := []*stubProvider{}
	for _, registered := range []bool{false, true} {
		p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "report", FinishReason: "stop"}}}}
		workers := NewWorkerRegistry()
		w := workers.Add("general", "research", makeLoop(t, p, tools.NewRegistry(), ""))
		markFinished(w, "done", "prior report", "", time.Now())
		if registered {
			raw, _ := json.Marshal(map[string]string{"to": w.ID, "message": "continue"})
			result, err := NewSendMessageTool(workers).execute(context.Background(), raw)
			if err != nil || result.Err != nil {
				t.Fatalf("%v %v", err, result.Err)
			}
		} else if _, err := runWorkerLoop(context.Background(), w, "continue"); err != nil {
			t.Fatal(err)
		}
		providers = append(providers, p)
	}
	if providers[0].calls != 1 || providers[1].calls != 1 || !reflect.DeepEqual(providers[0].reqs, providers[1].reqs) || !reflect.DeepEqual(providers[0].toolReqs, providers[1].toolReqs) {
		t.Fatal("admission changed model context, schemas or call count")
	}
}
