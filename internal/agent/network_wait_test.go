package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// fakeNetwork is a network that is down until `back` on a fake clock. Every
// retry wait advances the clock instead of sleeping.
type fakeNetwork struct {
	mu      sync.Mutex
	elapsed time.Duration
	back    time.Duration
	base    time.Time
}

func (n *fakeNetwork) now() time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.base.Add(n.elapsed)
}

func (n *fakeNetwork) up() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.elapsed >= n.back
}

func installFakeNetwork(t *testing.T, back time.Duration) *fakeNetwork {
	t.Helper()
	n := &fakeNetwork{back: back, base: time.Unix(1_800_000_000, 0)}
	savedWait, savedClock, savedProbe, savedLimit := streamRetryWait, networkClock, networkProbe, llm.NetworkWait()
	streamRetryWait = func(ctx context.Context, d time.Duration) error {
		n.mu.Lock()
		n.elapsed += d
		n.mu.Unlock()
		return ctx.Err()
	}
	networkClock = n.now
	networkProbe = func(context.Context, string) bool { return n.up() }
	t.Cleanup(func() {
		streamRetryWait, networkClock, networkProbe = savedWait, savedClock, savedProbe
		llm.SetNetworkWait(savedLimit)
	})
	return n
}

// netProvider answers through a fake network: while it is down a call fails
// with downErr (or, with dropMidStream, streams part of the answer first and
// then breaks); once it is up the call streams `answer`.
type netProvider struct {
	net           *fakeNetwork
	downErr       error
	dropMidStream bool
	answer        []llm.Delta
	calls         atomic.Int32
}

func (*netProvider) Name() string { return "net-fixture" }

func (p *netProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	n := p.calls.Add(1)
	if !p.net.up() && !p.dropMidStream {
		return nil, p.downErr
	}
	ch := make(chan llm.Delta, 8)
	if p.dropMidStream && n == 1 {
		ch <- llm.Delta{Content: "Budowanie modu "}
		ch <- llm.Delta{Content: "zakończo"}
		ch <- llm.Delta{Err: &llm.StreamTransportError{Op: "read", Err: errors.New("wsarecv: An existing connection was forcibly closed by the remote host.")}}
		close(ch)
		return ch, nil
	}
	if !p.net.up() {
		close(ch)
		return nil, p.downErr
	}
	for _, d := range p.answer {
		ch <- d
	}
	close(ch)
	return ch, nil
}

type collected struct {
	text     string
	discards []StreamDiscardEvent
	waits    []NetworkWaitEvent
	err      error
	calls    int
}

func runCollect(t *testing.T, l *Loop, prompt string) collected {
	t.Helper()
	events, err := l.Run(context.Background(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	var c collected
	var text strings.Builder
	for ev := range events {
		switch e := ev.(type) {
		case MessageEvent:
			text.WriteString(e.Text)
		case StreamDiscardEvent:
			c.discards = append(c.discards, e)
			// What a UI does: withdraw the shown part.
			c.text = text.String()
			text.Reset()
			text.WriteString(strings.TrimSuffix(c.text, e.Text))
		case NetworkWaitEvent:
			c.waits = append(c.waits, e)
		case ToolCallEvent:
			c.calls++
		case ErrorEvent:
			c.err = e.Err
		}
	}
	c.text = text.String()
	return c
}

// A connection that drops in the middle of an answer (cable to Wi-Fi) comes
// back after 30 s: the turn completes, the broken part is withdrawn, the
// answer is shown once and its tool call runs once.
func TestNetworkDropMidStreamRetriesWholeRequestOnce(t *testing.T) {
	network := installFakeNetwork(t, 30*time.Second)
	var runs atomic.Int32
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "run", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			runs.Add(1)
			return tools.Result{Text: "BUILD SUCCESSFUL"}, nil
		}})
	reg.MarkAlwaysOn("ctx_execute")
	p := &netProvider{net: network, dropMidStream: true, answer: []llm.Delta{
		{Content: "Budowanie modu zakończone."},
		tcall("b1", "ctx_execute", `{"command":["gradlew","build"]}`),
		{FinishReason: "tool_calls"},
	}}
	// After the tool, the model answers once more (network is up then).
	final := &netProvider{net: network, answer: say("Gotowe.")}
	multi := &sequenceProvider{steps: []llm.Provider{p, p, final}}
	l, err := NewLoop(LoopConfig{Provider: multi, Registry: reg, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	c := runCollect(t, l, "zbuduj mod")
	if c.err != nil {
		t.Fatalf("turn failed: %v", c.err)
	}
	if len(c.discards) != 1 || c.discards[0].Text != "Budowanie modu zakończo" {
		t.Fatalf("broken part not withdrawn: %+v", c.discards)
	}
	if strings.Count(c.text, "Budowanie modu") != 1 || !strings.Contains(c.text, "Budowanie modu zakończone.") {
		t.Fatalf("answer duplicated or missing: %q", c.text)
	}
	if runs.Load() != 1 || c.calls != 1 {
		t.Fatalf("tool ran %d times (%d calls)", runs.Load(), c.calls)
	}
	if len(c.waits) < 2 || !c.waits[0].Waiting || c.waits[len(c.waits)-1].Waiting {
		t.Fatalf("network wait not shown and cleared: %+v", c.waits)
	}
	for _, m := range l.Messages {
		if strings.Contains(m.Content, "zakończoBudowanie") || strings.Count(m.Content, "Budowanie modu") > 1 {
			t.Fatalf("history kept the broken part: %q", m.Content)
		}
	}
}

// sequenceProvider hands each call to the next provider in steps (the last
// one repeats).
type sequenceProvider struct {
	mu    sync.Mutex
	steps []llm.Provider
	n     int
}

func (*sequenceProvider) Name() string { return "sequence" }

func (s *sequenceProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	s.mu.Lock()
	p := s.steps[min(s.n, len(s.steps)-1)]
	s.n++
	s.mu.Unlock()
	return p.Complete(ctx, msgs, defs)
}

// DNS fails ("no such host" while offline) for two minutes, then works.
func TestNetworkDNSFailureForTwoMinutesThenOK(t *testing.T) {
	network := installFakeNetwork(t, 2*time.Minute)
	dns := &net.DNSError{Err: "no such host", Name: "api.example.test", IsNotFound: true}
	p := &netProvider{net: network, downErr: &net.OpError{Op: "dial", Net: "tcp", Err: dns}, answer: say("Jest sieć.")}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: emptyRegistry(), MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	c := runCollect(t, l, "hej")
	if c.err != nil || c.text != "Jest sieć." {
		t.Fatalf("after the outage: text=%q err=%v", c.text, c.err)
	}
	if network.elapsed < 2*time.Minute || network.elapsed > 3*time.Minute {
		t.Fatalf("waited %s for a 2-minute outage", network.elapsed)
	}
}

// After the whole wait (10 min) the run ends with a clean, resumable network
// stop.
func TestNetworkStaysDownGivesResumableNetworkStop(t *testing.T) {
	network := installFakeNetwork(t, time.Hour)
	p := &netProvider{net: network, downErr: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ENETUNREACH}}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: emptyRegistry(), MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	c := runCollect(t, l, "hej")
	if !IsNetworkStop(c.err) || !strings.Contains(c.err.Error(), "network stop") {
		t.Fatalf("want a network stop, got %v", c.err)
	}
	if network.elapsed < llm.DefaultNetworkWait || network.elapsed > llm.DefaultNetworkWait+time.Minute {
		t.Fatalf("gave up after %s", network.elapsed)
	}
	if len(c.waits) == 0 || c.waits[len(c.waits)-1].Waiting {
		t.Fatal("the waiting status was not cleared at the stop")
	}
}

// A 4xx is the provider's answer, not a lost network: no waiting, no retry.
func TestHTTP4xxIsNotRetriedAsNetwork(t *testing.T) {
	network := installFakeNetwork(t, time.Hour)
	p := &netProvider{net: network, downErr: &llm.HTTPResponseError{Status: 400, Body: `{"error":{"message":"bad request: network field invalid"}}`}}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: emptyRegistry(), MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	c := runCollect(t, l, "hej")
	if c.err == nil || IsNetworkStop(c.err) || len(c.waits) != 0 || p.calls.Load() != 1 {
		t.Fatalf("4xx handled as network: err=%v waits=%d calls=%d", c.err, len(c.waits), p.calls.Load())
	}
	for _, e := range []error{io.EOF, context.Canceled, &llm.HTTPResponseError{Status: 429}} {
		if e != io.EOF && isNetworkDrop(e) {
			t.Fatalf("%v classified as a network drop", e)
		}
	}
}

// A worker survives a one-minute outage and reports normally; it is not
// failed while it waits.
func TestWorkerSurvivesOneMinuteOutage(t *testing.T) {
	network := installFakeNetwork(t, time.Minute)
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "code", Description: "code", MaxSteps: 2})
	child := &netProvider{net: network, downErr: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, answer: say("Worker: gotowe.")}
	factory := func(cfg LoopConfig) (*Loop, error) {
		return NewLoop(LoopConfig{Provider: child, Registry: cfg.Registry, MaxSteps: 2})
	}
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), child, nil, factory)
	res, _ := at.execute(context.Background(), json.RawMessage(`{"agent":"code","prompt":"zbuduj"}`))
	if res.Err != nil || !strings.Contains(res.Text, "Worker: gotowe.") {
		t.Fatalf("worker did not survive the outage: %v %q", res.Err, res.Text)
	}
	for _, w := range at.Workers.List() {
		if s := w.Snapshot(); s.Status != "done" {
			t.Fatalf("worker status %q after the outage", s.Status)
		}
	}
}

// A worker whose network never returns ends interrupted (resumable), not
// failed.
func TestWorkerNetworkStopIsResumable(t *testing.T) {
	installFakeNetwork(t, time.Hour)
	llm.SetNetworkWait(2 * time.Minute)
	reg := NewSubAgentRegistry()
	reg.MustRegister(SubAgent{Name: "code", Description: "code", MaxSteps: 2})
	child := &netProvider{net: &fakeNetwork{back: time.Hour}, downErr: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ENETUNREACH}}
	factory := func(cfg LoopConfig) (*Loop, error) {
		return NewLoop(LoopConfig{Provider: child, Registry: cfg.Registry, MaxSteps: 2})
	}
	at, _ := NewAgentTool(reg, nil, newTestBaseRegistry(), child, nil, factory)
	_, _ = at.execute(context.Background(), json.RawMessage(`{"agent":"code","prompt":"zbuduj"}`))
	list := at.Workers.List()
	if len(list) != 1 || list[0].Snapshot().Status != WorkerStatusInterrupted || !strings.Contains(list[0].Snapshot().LastError, "network stop") {
		t.Fatalf("worker after a network stop: %+v", list[0].Snapshot())
	}
}

// A server that keeps failing while the network is up is not an outage: no
// ten-minute wait, the ordinary error handling ends the call soon.
func TestNetworkUpButServerFailingIsNotAnOutage(t *testing.T) {
	network := installFakeNetwork(t, 0)
	p := &netProvider{net: &fakeNetwork{back: time.Hour}, downErr: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: emptyRegistry(), MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	c := runCollect(t, l, "hej")
	if c.err == nil || IsNetworkStop(c.err) {
		t.Fatalf("want the server error, got %v", c.err)
	}
	if network.elapsed > 3*time.Minute {
		t.Fatalf("waited %s although the network was up", network.elapsed)
	}
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	if isNetworkDrop(refused) {
		t.Fatal("a refused connection is a server that is down, not a lost network")
	}
}
