// Replay harness: deterministic loop-contract tests that replay
// RECORDED model responses (stream deltas, native/sentinel tool
// calls, structured errors) through the real agent loop — no model
// host, no network, plain `go test ./test/...`.
//
// The point is to protect "wyciskamy wydajność, ale nie pogarszamy
// modelu": any future prompt/tool-protocol change must keep these
// contracts — what reaches the provider, what gets executed, how
// many turns are spent, and what lands in history.
//
// Recordings live in test/replay/*.json (format documented in
// test/replay/README.md). Tools are defined here in Go; the JSON
// carries only what the MODEL said.
//
// TODO(live-eval): a future `-tags eval` suite of 10–15 real tasks
// against a live local host, with the same per-scenario metrics
// (success, turns, failed tool calls, tokens, tool wall time) saved
// as a baseline — see test/replay/README.md. Deliberately not built
// yet.
package test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

// ---------------------------------------------------------------------------
// Recording format (mirrors llm.Delta, see test/replay/README.md).

type replayRecording struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Steps       []replayStep `json:"steps"`
}

type replayStep struct {
	Note   string        `json:"note,omitempty"`
	Deltas []replayDelta `json:"deltas"`
}

type replayDelta struct {
	Role         string          `json:"role,omitempty"`
	Content      string          `json:"content,omitempty"`
	ToolCall     *replayToolCall `json:"tool_call,omitempty"`
	FinishReason string          `json:"finish_reason,omitempty"`
	Usage        *replayUsage    `json:"usage,omitempty"`
	Err          string          `json:"err,omitempty"`
	Notice       string          `json:"notice,omitempty"`
}

type replayToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type replayUsage struct {
	Input       int `json:"input"`
	Output      int `json:"output"`
	Total       int `json:"total"`
	CachedInput int `json:"cached_input,omitempty"`
	Reasoning   int `json:"reasoning,omitempty"`
}

func (d replayDelta) toLLM() llm.Delta {
	out := llm.Delta{
		Role:         llm.Role(d.Role),
		Content:      d.Content,
		FinishReason: d.FinishReason,
		Notice:       d.Notice,
	}
	if d.ToolCall != nil {
		out.ToolCall = &llm.ToolCall{
			ID:        d.ToolCall.ID,
			Name:      d.ToolCall.Name,
			Arguments: d.ToolCall.Arguments,
		}
	}
	if d.Usage != nil {
		out.Usage = &llm.Usage{
			Input:       d.Usage.Input,
			Output:      d.Usage.Output,
			Total:       d.Usage.Total,
			CachedInput: d.Usage.CachedInput,
			Reasoning:   d.Usage.Reasoning,
		}
	}
	if d.Err != "" {
		out.Err = fmt.Errorf("%s", d.Err)
	}
	return out
}

// ---------------------------------------------------------------------------
// replayProvider: a deterministic llm.Provider that streams recorded
// deltas — one recorded step per Complete call — and records every
// request (messages + tool names) for contract assertions.

type replayRequest struct {
	Messages  []llm.Message
	ToolNames []string
}

type replayProvider struct {
	name  string
	steps [][]llm.Delta

	mu       sync.Mutex
	requests []replayRequest
}

func (p *replayProvider) Name() string { return p.name }

func (p *replayProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	call := len(p.requests)
	req := replayRequest{Messages: append([]llm.Message(nil), msgs...)}
	for _, d := range defs {
		req.ToolNames = append(req.ToolNames, d.Name)
	}
	p.requests = append(p.requests, req)
	p.mu.Unlock()

	// Strict: a loop that needs more turns than the recording has is
	// a turn-count regression, not something to absorb silently.
	if call >= len(p.steps) {
		return nil, fmt.Errorf("replay exhausted: provider call %d, but only %d steps recorded", call+1, len(p.steps))
	}
	script := p.steps[call]
	ch := make(chan llm.Delta, len(script)+1)
	go func() {
		defer close(ch)
		for _, d := range script {
			select {
			case ch <- d:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (p *replayProvider) reqs() []replayRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]replayRequest(nil), p.requests...)
}

// loadReplayProvider reads a scenario file from test/replay/.
func loadReplayProvider(t *testing.T, file string) *replayProvider {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("replay", file))
	if err != nil {
		t.Fatalf("read recording %s: %v", file, err)
	}
	var rec replayRecording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("parse recording %s: %v", file, err)
	}
	if len(rec.Steps) == 0 {
		t.Fatalf("recording %s has no steps", file)
	}
	p := &replayProvider{name: "replay:" + rec.Name}
	for _, s := range rec.Steps {
		var deltas []llm.Delta
		for _, d := range s.Deltas {
			deltas = append(deltas, d.toLLM())
		}
		p.steps = append(p.steps, deltas)
	}
	return p
}

// ---------------------------------------------------------------------------
// Shared helpers: event collection and per-scenario metrics.

type replayMetrics struct {
	Turns       int
	ToolCalls   int
	ToolErrs    int
	Messages    int
	Done        bool
	Usage       agent.Usage
	HistTokens  int
	ToolWall    time.Duration
	ErrorEvents []error
}

func collectReplay(t *testing.T, ch <-chan agent.Event) []agent.Event {
	t.Helper()
	var out []agent.Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func replaySummary(prov *replayProvider, l *agent.Loop, events []agent.Event, toolWall time.Duration) replayMetrics {
	m := replayMetrics{Turns: len(prov.reqs()), ToolWall: toolWall}
	for _, ev := range events {
		switch e := ev.(type) {
		case agent.MessageEvent:
			m.Messages++
		case agent.ToolCallEvent:
			m.ToolCalls++
		case agent.ToolResultEvent:
			if e.Err != nil {
				m.ToolErrs++
			}
		case agent.DoneEvent:
			m.Done = true
			m.Usage = e.Usage
		case agent.ErrorEvent:
			m.ErrorEvents = append(m.ErrorEvents, e.Err)
		}
	}
	m.HistTokens = llm.EstimateTokens(l.Messages)
	return m
}

func logReplayMetrics(t *testing.T, name string, m replayMetrics) {
	t.Helper()
	t.Logf("[replay] scenario=%s success=%v turns=%d tool_calls=%d failed_tool_calls=%d usage_in=%d usage_out=%d hist_est_tokens=%d tool_wall=%s",
		name, m.Done && len(m.ErrorEvents) == 0, m.Turns, m.ToolCalls, m.ToolErrs,
		m.Usage.Input, m.Usage.Output, m.HistTokens, m.ToolWall)
}

func requireCleanDone(t *testing.T, m replayMetrics) {
	t.Helper()
	for _, err := range m.ErrorEvents {
		t.Errorf("unexpected ErrorEvent: %v", err)
	}
	if !m.Done {
		t.Error("no DoneEvent — run did not finish cleanly")
	}
}

// reqText flattens a recorded request for substring assertions.
func reqText(req replayRequest) string {
	var sb strings.Builder
	for _, m := range req.Messages {
		sb.WriteString(string(m.Role))
		sb.WriteString(": ")
		sb.WriteString(m.Content)
		for _, p := range m.Parts {
			sb.WriteString(p.Text)
		}
		for _, tc := range m.ToolCalls {
			sb.WriteString(tc.Name)
			sb.WriteString(tc.Arguments)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// timedTool wraps a tool Fn with a wall-clock accumulator.
func timedTool(wall *time.Duration, fn func(ctx context.Context, args json.RawMessage) (tools.Result, error)) func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	return func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
		start := time.Now()
		res, err := fn(ctx, args)
		*wall += time.Since(start)
		return res, err
	}
}

func newReplayLoop(t *testing.T, cfg agent.LoopConfig) *agent.Loop {
	t.Helper()
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 5
	}
	if cfg.System == "" {
		cfg.System = "You are a replay-test coordinator."
	}
	l, err := agent.NewLoop(cfg)
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	return l
}

// assertToolPairing checks the loop-history invariant every provider
// requires: each RoleTool message carries a ToolCallID that appears
// in an EARLIER assistant message's ToolCalls.
func assertToolPairing(t *testing.T, msgs []llm.Message) {
	t.Helper()
	seen := map[string]bool{}
	for i, m := range msgs {
		for _, tc := range m.ToolCalls {
			seen[tc.ID] = true
		}
		if m.Role == llm.RoleTool {
			if m.ToolCallID == "" {
				t.Errorf("message %d: RoleTool without ToolCallID", i)
			} else if !seen[m.ToolCallID] {
				t.Errorf("message %d: tool result %q has no preceding assistant tool call", i, m.ToolCallID)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario sanity: every recording in test/replay parses. Hand-added
// scenarios that drift from the format fail loudly here.

func TestReplay_AllRecordingsParse(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("replay", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no recordings found (err=%v)", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		var rec replayRecording
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Errorf("parse %s: %v", f, err)
			continue
		}
		if rec.Name == "" || len(rec.Steps) == 0 {
			t.Errorf("%s: missing name or steps", f)
		}
	}
}

// ---------------------------------------------------------------------------
// (a) Sentinel tool call, torn across stream deltas: parsed, executed,
// result returns to the model on the next step.

func TestReplay_SentinelToolCall_RoundTrip(t *testing.T) {
	prov := loadReplayProvider(t, "tool_call_sentinel.json")
	var toolWall time.Duration
	var gotArgs string
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "echo",
		Description: "echoes msg",
		Schema:      `{"type":"object","properties":{"msg":{"type":"string"}}}`,
		Fn: timedTool(&toolWall, func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			var a struct {
				Msg string `json:"msg"`
			}
			_ = json.Unmarshal(args, &a)
			gotArgs = string(args)
			return tools.Result{Text: "echoed: " + a.Msg}, nil
		}),
	})
	l := newReplayLoop(t, agent.LoopConfig{Provider: prov, Registry: reg})

	ch, err := l.Run(context.Background(), "echo hi for me")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectReplay(t, ch)
	m := replaySummary(prov, l, events, toolWall)
	logReplayMetrics(t, "tool_call_sentinel", m)
	requireCleanDone(t, m)

	// The tool was parsed out of the torn sentinel block and executed
	// with the exact recorded arguments.
	if m.ToolCalls != 1 || m.ToolErrs != 0 {
		t.Fatalf("tool_calls=%d errs=%d, want 1/0", m.ToolCalls, m.ToolErrs)
	}
	if !strings.Contains(gotArgs, `"msg":"hi"`) {
		t.Errorf("echo args = %q, want msg=hi", gotArgs)
	}
	// The prose BEFORE the sentinel block was streamed as a message.
	prose := false
	for _, ev := range events {
		if e, ok := ev.(agent.MessageEvent); ok && strings.Contains(e.Text, "Let me echo that.") {
			prose = true
		}
	}
	if !prose {
		t.Error("prose before the sentinel block was not emitted as a MessageEvent")
	}
	// History carries a properly paired tool result.
	assertToolPairing(t, l.Messages)
	foundResult := false
	for _, msg := range l.Messages {
		if msg.Role == llm.RoleTool && msg.Content == "echoed: hi" {
			foundResult = true
		}
	}
	if !foundResult {
		t.Error("tool result missing from history")
	}
	// The result actually went BACK to the model: request 2 carries it.
	reqs := prov.reqs()
	if m.Turns != 2 {
		t.Fatalf("turns = %d, want 2", m.Turns)
	}
	if !strings.Contains(reqText(reqs[1]), "echoed: hi") {
		t.Error("request 2 does not carry the tool result")
	}
}

// ---------------------------------------------------------------------------
// (b) command_failed: the structured failure summary (exit code +
// stderr tail) must REACH the model — the recorded second step cites
// the exit code and failing test, which is only consistent if it did.

func TestReplay_CommandFailedDiagnosticsReachModel(t *testing.T) {
	prov := loadReplayProvider(t, "command_failed_diag.json")
	const diag = "command_failed exit=1 (1.3s)\nstderr:\nFAIL: TestFoo asserts x != y"
	var toolWall time.Duration
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "run_tests",
		Description: "runs the tests",
		Schema:      `{"type":"object"}`,
		Fn: timedTool(&toolWall, func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: diag, Err: fmt.Errorf("exit 1")}, nil
		}),
	})
	l := newReplayLoop(t, agent.LoopConfig{Provider: prov, Registry: reg})

	ch, err := l.Run(context.Background(), "run the tests")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectReplay(t, ch)
	m := replaySummary(prov, l, events, toolWall)
	logReplayMetrics(t, "command_failed_diag", m)
	requireCleanDone(t, m)

	if m.ToolCalls != 1 || m.ToolErrs != 1 {
		t.Fatalf("tool_calls=%d errs=%d, want 1 call with 1 error result", m.ToolCalls, m.ToolErrs)
	}
	reqs := prov.reqs()
	if m.Turns != 2 {
		t.Fatalf("turns = %d, want 2", m.Turns)
	}
	req2 := reqText(reqs[1])
	// The diagnostics ARRIVED: both the fact line and the stderr tail.
	if !strings.Contains(req2, "command_failed exit=1") {
		t.Error("request 2 lost the command_failed fact line")
	}
	if !strings.Contains(req2, "FAIL: TestFoo") {
		t.Error("request 2 lost the stderr diagnostics")
	}
	// The recorded reaction (citing exit=1 / TestFoo) landed in history:
	// the model's next step depended on the error CONTENT.
	final := l.Messages[len(l.Messages)-1]
	if final.Role != llm.RoleAssistant {
		t.Fatalf("last message role = %s, want assistant", final.Role)
	}
	finalText := final.Content
	for _, p := range final.Parts {
		finalText += p.Text
	}
	if !strings.Contains(finalText, "exit=1") || !strings.Contains(finalText, "TestFoo") {
		t.Errorf("recorded reaction does not cite the diagnostics: %q", finalText)
	}
}

// ---------------------------------------------------------------------------
// (c) http_failed with an error BODY: the body must reach the model so
// the recorded second step can act on it (missing_api_key).

func TestReplay_HTTPFailedBodyReachesModel(t *testing.T) {
	prov := loadReplayProvider(t, "http_failed_body.json")
	httpErr := "http_failed status=401 host=api.example.com content_type=application/json\nbody:\n" +
		`{"code":"missing_api_key","fix":"set EXAMPLE_API_KEY"}`
	var toolWall time.Duration
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "fetch_api",
		Description: "fetches a URL",
		Schema:      `{"type":"object","properties":{"url":{"type":"string"}}}`,
		Fn: timedTool(&toolWall, func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			// Same shape as web.httpFailedErr: self-contained error, no Text.
			return tools.Result{Err: core.SelfContainedErr(fmt.Errorf("%s", httpErr))}, nil
		}),
	})
	l := newReplayLoop(t, agent.LoopConfig{Provider: prov, Registry: reg})

	ch, err := l.Run(context.Background(), "fetch the data endpoint")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectReplay(t, ch)
	m := replaySummary(prov, l, events, toolWall)
	logReplayMetrics(t, "http_failed_body", m)
	requireCleanDone(t, m)

	if m.ToolCalls != 1 || m.ToolErrs != 1 {
		t.Fatalf("tool_calls=%d errs=%d, want 1/1", m.ToolCalls, m.ToolErrs)
	}
	reqs := prov.reqs()
	if m.Turns != 2 {
		t.Fatalf("turns = %d, want 2", m.Turns)
	}
	req2 := reqText(reqs[1])
	if !strings.Contains(req2, "http_failed status=401") {
		t.Error("request 2 lost the http_failed status line")
	}
	if !strings.Contains(req2, "missing_api_key") {
		t.Error("request 2 lost the error BODY (the actionable part)")
	}
	// Recorded reaction acts on the body content.
	final := l.Messages[len(l.Messages)-1]
	finalText := final.Content
	for _, p := range final.Parts {
		finalText += p.Text
	}
	if !strings.Contains(finalText, "missing_api_key") {
		t.Errorf("recorded reaction does not act on the body: %q", finalText)
	}
}

// ---------------------------------------------------------------------------
// (d) Several tool calls in ONE model turn: all execute, results are
// appended in call order (deterministic call/result pairing).

func TestReplay_MultiToolBatch_OrderedResults(t *testing.T) {
	prov := loadReplayProvider(t, "multi_tool_batch.json")
	data := map[string]string{"a": "alpha", "b": "beta", "c": "gamma"}
	var toolWall time.Duration
	var mu sync.Mutex
	var executed []string
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "lookup",
		Description: "looks up a key",
		Schema:      `{"type":"object","properties":{"key":{"type":"string"}}}`,
		Fn: timedTool(&toolWall, func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			var a struct {
				Key string `json:"key"`
			}
			_ = json.Unmarshal(args, &a)
			mu.Lock()
			executed = append(executed, a.Key)
			mu.Unlock()
			return tools.Result{Text: data[a.Key]}, nil
		}),
	})
	l := newReplayLoop(t, agent.LoopConfig{Provider: prov, Registry: reg})

	ch, err := l.Run(context.Background(), "look up a, b and c")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectReplay(t, ch)
	m := replaySummary(prov, l, events, toolWall)
	logReplayMetrics(t, "multi_tool_batch", m)
	requireCleanDone(t, m)

	if m.ToolCalls != 3 || m.ToolErrs != 0 {
		t.Fatalf("tool_calls=%d errs=%d, want 3/0", m.ToolCalls, m.ToolErrs)
	}
	if len(executed) != 3 {
		t.Fatalf("executed = %v, want 3 lookups", executed)
	}
	// Results appended in the assistant's call order.
	var order []string
	for _, msg := range l.Messages {
		if msg.Role == llm.RoleTool {
			order = append(order, msg.ToolCallID)
		}
	}
	want := []string{"call_a", "call_b", "call_c"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("tool result order = %v, want %v", order, want)
	}
	assertToolPairing(t, l.Messages)
	// All three results reached the model on the next turn.
	reqs := prov.reqs()
	req2 := reqText(reqs[1])
	for _, v := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(req2, v) {
			t.Errorf("request 2 missing result %q", v)
		}
	}
}

// ---------------------------------------------------------------------------
// (e) Preflight addon: rides the USER message (never system), is
// one-shot, and does not disturb the tool round-trip. The noop-gate is
// app-level and zero-LLM by construction (a gated run never calls the
// provider), so it is pinned by internal/app/noopgate_test.go instead.

func TestReplay_PreflightAddon_UserSideOneShot(t *testing.T) {
	prov := loadReplayProvider(t, "preflight_addon.json")
	const repoBlock = "Repo state (auto-collected):\nbranch: main\nHEAD: abc1234 fix"
	var toolWall time.Duration
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "lookup",
		Description: "looks up a key",
		Schema:      `{"type":"object","properties":{"key":{"type":"string"}}}`,
		Fn: timedTool(&toolWall, func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "alpha"}, nil
		}),
	})
	l := newReplayLoop(t, agent.LoopConfig{Provider: prov, Registry: reg})
	l.SetNextUserAddon(repoBlock)

	ch, err := l.Run(context.Background(), "what changed recently?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectReplay(t, ch)
	m := replaySummary(prov, l, events, toolWall)
	requireCleanDone(t, m)
	if m.ToolCalls != 1 || m.ToolErrs != 0 {
		t.Fatalf("run 1 tool_calls=%d errs=%d, want 1/0 (addon broke the round-trip?)", m.ToolCalls, m.ToolErrs)
	}

	// Second run: the addon must not repeat.
	ch, err = l.Run(context.Background(), "anything else?")
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	events2 := collectReplay(t, ch)
	m2 := replaySummary(prov, l, events2, toolWall)
	logReplayMetrics(t, "preflight_addon", m2)
	requireCleanDone(t, m2)

	reqs := prov.reqs()
	if len(reqs) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(reqs))
	}
	// Addon in the first USER message, never in a system message.
	foundInUser := false
	for _, msg := range reqs[0].Messages {
		has := strings.Contains(msg.Content, repoBlock)
		if msg.Role == llm.RoleUser && has {
			foundInUser = true
			if !strings.Contains(msg.Content, "what changed recently?") {
				t.Error("addon must ride the same message as the user's prompt")
			}
		}
		if msg.Role == llm.RoleSystem && has {
			t.Error("preflight block leaked into a system message (KV-prefix killer)")
		}
	}
	if !foundInUser {
		t.Fatal("preflight block not found in any user message of request 1")
	}
	// One-shot: run 2's user message carries no block.
	for _, msg := range reqs[2].Messages {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "anything else?") &&
			strings.Contains(msg.Content, repoBlock) {
			t.Error("addon repeated on the second run")
		}
	}
}

// ---------------------------------------------------------------------------
// (f) Prune mid-session: with a small window, old tool results are
// replaced by markers BEFORE the provider call; the freshest result
// survives, pairing stays intact, and the next request strictly
// appends to the previous one (stable prefix).

func TestReplay_PruneMidSession_CoherentHistory(t *testing.T) {
	prov := loadReplayProvider(t, "prune_mid_session.json")

	// Seed history: 4 tool turns with big results (~1.3k est tokens
	// each) so a 4000-token window forces a prune of the oldest ones.
	filler := strings.Repeat("data0123 ", 500)
	var hist []llm.Message
	hist = append(hist, llm.Message{Role: llm.RoleUser, Content: "analyze the four data files"})
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("call_%d", i)
		hist = append(hist,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
				ID: id, Name: "read_data", Arguments: fmt.Sprintf(`{"file":"data%d.txt"}`, i),
			}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: id, Name: "read_data",
				Content: fmt.Sprintf("TOOLRESULT-%d %s", i, filler)},
		)
	}

	reg := tools.NewRegistry()
	l := newReplayLoop(t, agent.LoopConfig{
		Provider:           prov,
		Registry:           reg,
		InitialMessages:    hist,
		WindowFor:          func(string) int { return 4000 },
		PruneProtectTokens: 512,
	})

	ch, err := l.Run(context.Background(), "summarize what we learned")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectReplay(t, ch)
	m := replaySummary(prov, l, events, 0)
	requireCleanDone(t, m)

	pruned := false
	for _, ev := range events {
		if pe, ok := ev.(agent.ToolResultsPrunedEvent); ok {
			pruned = true
			t.Logf("prune: %d results, ~%d tokens reclaimed (est %d / window %d)",
				pe.Pruned, pe.Reclaimed, pe.Estimated, pe.Window)
		}
	}
	if !pruned {
		t.Fatal("no ToolResultsPrunedEvent — prune did not fire")
	}

	reqs := prov.reqs()
	req1 := reqText(reqs[0])
	// Old results replaced by markers; the freshest survives verbatim.
	if !strings.Contains(req1, "[tool result pruned") {
		t.Error("request 1 carries no prune marker")
	}
	if strings.Contains(req1, "TOOLRESULT-1") {
		t.Error("oldest tool result was NOT pruned")
	}
	if !strings.Contains(req1, "TOOLRESULT-4") {
		t.Error("freshest tool result was pruned (protected tail violated)")
	}
	// Coherent history: every tool result still pairs with its call.
	assertToolPairing(t, reqs[0].Messages)
	assertToolPairing(t, l.Messages)

	// Second run: the request must be an append-only extension of the
	// first one (modulo the trailing per-request stamp) — the stable
	// prefix the KV cache depends on.
	ch, err = l.Run(context.Background(), "continue")
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	events2 := collectReplay(t, ch)
	m2 := replaySummary(prov, l, events2, 0)
	logReplayMetrics(t, "prune_mid_session", m2)
	requireCleanDone(t, m2)

	reqs = prov.reqs()
	if len(reqs) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(reqs))
	}
	prefix := reqs[0].Messages[:len(reqs[0].Messages)-1] // strip trailing stamp
	if len(reqs[1].Messages) < len(prefix) {
		t.Fatalf("request 2 shorter than request 1's prefix")
	}
	for i, want := range prefix {
		got := reqs[1].Messages[i]
		if got.Role != want.Role || got.Content != want.Content || got.ToolCallID != want.ToolCallID ||
			len(got.ToolCalls) != len(want.ToolCalls) {
			t.Errorf("prefix diverged at message %d:\n got %+v\nwant %+v", i, got, want)
			break
		}
	}
}

// ---------------------------------------------------------------------------
// (g) Truncated tool result: the [... omitted_bytes=N ...] marker
// produced by core.HeadTailBuffer survives all the way into the
// provider request, so the model KNOWS the output was cut.

func TestReplay_TruncatedToolResult_MarkerVisible(t *testing.T) {
	prov := loadReplayProvider(t, "truncated_result.json")
	var toolWall time.Duration
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "big_output",
		Description: "produces a huge output, capped head+tail",
		Schema:      `{"type":"object"}`,
		Fn: timedTool(&toolWall, func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			buf := core.NewHeadTailBuffer(2048, 2048)
			fmt.Fprint(buf, "HEAD-START ")
			for i := 0; i < 4000; i++ {
				fmt.Fprintf(buf, "line %06d filler filler filler\n", i)
			}
			fmt.Fprint(buf, " TAIL-END")
			return tools.Result{Text: buf.String()}, nil
		}),
	})
	l := newReplayLoop(t, agent.LoopConfig{Provider: prov, Registry: reg})

	ch, err := l.Run(context.Background(), "show me everything")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectReplay(t, ch)
	m := replaySummary(prov, l, events, toolWall)
	logReplayMetrics(t, "truncated_result", m)
	requireCleanDone(t, m)

	if m.ToolCalls != 1 || m.ToolErrs != 0 {
		t.Fatalf("tool_calls=%d errs=%d, want 1/0", m.ToolCalls, m.ToolErrs)
	}
	reqs := prov.reqs()
	if m.Turns != 2 {
		t.Fatalf("turns = %d, want 2", m.Turns)
	}
	req2 := reqText(reqs[1])
	if !strings.Contains(req2, "[... omitted_bytes=") {
		t.Error("request 2 lost the omission marker — the model cannot know the output was cut")
	}
	if !strings.Contains(req2, "HEAD-START") {
		t.Error("request 2 lost the head of the output")
	}
	if !strings.Contains(req2, "TAIL-END") {
		t.Error("request 2 lost the tail of the output")
	}
	// The capped result is bounded: head+tail+marker, never megabytes.
	for _, msg := range reqs[1].Messages {
		if msg.Role == llm.RoleTool && len(msg.Content) > 8192 {
			t.Errorf("tool result not bounded: %d bytes", len(msg.Content))
		}
	}
}
