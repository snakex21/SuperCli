package tui

// audit-130: end-to-end tool approval through the real ApprovalGate and core
// registry, answered by keys in the TUI model. No provider, no network.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"supercli/internal/tools"
	core "supercli/internal/tools/core"
	"supercli/internal/tools/interactive"
)

type audit130Clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *audit130Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *audit130Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type audit130Rig struct {
	reg   *core.Registry
	gate  *interactive.ApprovalGate
	asks  chan interactive.AskRequest
	runs  atomic.Int64
	clock *audit130Clock
}

type audit130Result struct {
	res core.Result
	err error
}

func newAudit130Rig(t *testing.T, language string) *audit130Rig {
	t.Helper()
	rig := &audit130Rig{asks: make(chan interactive.AskRequest, 4), clock: &audit130Clock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}}
	rig.gate = &interactive.ApprovalGate{Fallback: rig.asks, Now: rig.clock.Now, GrantTTL: 10 * time.Minute, Timeout: 30 * time.Second,
		Language: func() string { return language }}
	rig.reg = core.NewRegistry()
	rig.reg.MustRegister(core.Tool{
		Name:        "audit_tool",
		Description: "audit-130 synthetic risky tool",
		Schema:      `{"type":"object","properties":{"scope":{"type":"string"},"irreversible":{"type":"boolean"}},"required":["scope"]}`,
		Approval: func(_ context.Context, args json.RawMessage) (*core.ApprovalRequest, error) {
			var a struct {
				Scope        string
				Irreversible bool
			}
			_ = json.Unmarshal(args, &a)
			risk := core.RiskWriteReversible
			if a.Irreversible {
				risk = core.RiskDestructive
			}
			return &core.ApprovalRequest{Operation: "write", Risk: risk, Reversible: !a.Irreversible, Target: "target-" + a.Scope,
				Summary: "Write to " + a.Scope, Scope: []string{"audit_tool", "write", a.Scope}, NoSessionGrant: a.Irreversible}, nil
		},
		Fn: func(_ context.Context, _ json.RawMessage) (core.Result, error) {
			rig.runs.Add(1)
			return core.Result{Text: "done"}, nil
		},
	})
	return rig
}

func (r *audit130Rig) exec(session, args string) <-chan audit130Result {
	out := make(chan audit130Result, 1)
	ctx := core.WithApprovalSession(core.WithApprover(context.Background(), r.gate), session)
	go func() {
		res, err := r.reg.Execute(ctx, "audit_tool", json.RawMessage(args))
		out <- audit130Result{res, err}
	}()
	return out
}

// nextAsk waits for the gate's prompt; ok=false when the call finished
// without asking (session grant) or nothing came.
func (r *audit130Rig) nextAsk(t *testing.T, done <-chan audit130Result) (interactive.AskRequest, *audit130Result) {
	t.Helper()
	select {
	case req := <-r.asks:
		return req, nil
	case res := <-done:
		return interactive.AskRequest{}, &res
	case <-time.After(5 * time.Second):
		t.Fatal("neither an approval prompt nor a result arrived")
	}
	return interactive.AskRequest{}, nil
}

func waitResult(t *testing.T, done <-chan audit130Result) audit130Result {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("tool call did not finish after the answer")
	}
	return audit130Result{}
}

func audit130Model(t *testing.T, language string, width, height int) Model {
	t.Helper()
	dir := t.TempDir()
	m := New(Options{Home: dir, DataDir: dir, Language: language})
	out, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return out.(Model)
}

func audit130Key(m Model, key string) Model {
	var msg tea.KeyMsg
	switch key {
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "end":
		msg = tea.KeyMsg{Type: tea.KeyEnd}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	out, _ := m.Update(msg)
	return out.(Model)
}

func audit130Begin(t *testing.T, m Model, req interactive.AskRequest) Model {
	t.Helper()
	out, _ := m.beginAsk(req)
	return out.(Model)
}

func isApprovalDenied(res audit130Result) bool {
	return res.err == nil && res.res.Err != nil && errors.Is(res.res.Err, core.ErrApprovalRequired)
}

func TestAudit130ApprovalDenyKeysNeverRunTool(t *testing.T) {
	for _, key := range []string{"n", "N", "esc", "enter"} {
		t.Run(key, func(t *testing.T) {
			rig := newAudit130Rig(t, "pl")
			m := audit130Model(t, "pl", 100, 30)
			done := rig.exec("s1", `{"scope":"inbox"}`)
			req, early := rig.nextAsk(t, done)
			if early != nil {
				t.Fatalf("tool finished without a prompt: %+v", early)
			}
			m = audit130Begin(t, m, req)
			view := ansi.Strip(m.View())
			for _, want := range []string{"[n]", "[y]", "[a]"} {
				if !strings.Contains(view, want) {
					t.Fatalf("card lacks %s:\n%s", want, view)
				}
			}
			m = audit130Key(m, key)
			res := waitResult(t, done)
			if !isApprovalDenied(res) {
				t.Fatalf("key %q: want ErrApprovalRequired, got res=%+v err=%v", key, res.res, res.err)
			}
			if n := rig.runs.Load(); n != 0 {
				t.Fatalf("key %q: denied tool ran %d times", key, n)
			}
			if m.pendingAsk != nil || m.mode != modeNormal {
				t.Fatalf("card still open after %q", key)
			}
		})
	}
}

func TestAudit130ApprovalApproveOnceRunsExactlyOnce(t *testing.T) {
	for _, key := range []string{"y", "Y"} {
		t.Run(key, func(t *testing.T) {
			rig := newAudit130Rig(t, "en")
			m := audit130Model(t, "en", 100, 30)
			done := rig.exec("s1", `{"scope":"inbox"}`)
			req, _ := rig.nextAsk(t, done)
			m = audit130Begin(t, m, req)
			m = audit130Key(m, key)
			res := waitResult(t, done)
			if res.err != nil || res.res.Err != nil || rig.runs.Load() != 1 {
				t.Fatalf("approve once: runs=%d res=%+v err=%v", rig.runs.Load(), res.res, res.err)
			}
			// "once" must not create a grant: the same call prompts again.
			done = rig.exec("s1", `{"scope":"inbox"}`)
			req, early := rig.nextAsk(t, done)
			if early != nil {
				t.Fatalf("approve-once silently granted the next call: %+v", early)
			}
			m = audit130Begin(t, m, req)
			audit130Key(m, "n")
			if res := waitResult(t, done); !isApprovalDenied(res) || rig.runs.Load() != 1 {
				t.Fatalf("second call: runs=%d res=%+v", rig.runs.Load(), res.res)
			}
		})
	}
}

func TestAudit130ApprovalSimilarGrantIsScopedAndExpires(t *testing.T) {
	for _, key := range []string{"a", "A"} {
		t.Run(key, func(t *testing.T) {
			rig := newAudit130Rig(t, "pl")
			m := audit130Model(t, "pl", 100, 30)
			done := rig.exec("s1", `{"scope":"inbox"}`)
			req, _ := rig.nextAsk(t, done)
			m = audit130Begin(t, m, req)
			m = audit130Key(m, key)
			if res := waitResult(t, done); res.res.Err != nil || rig.runs.Load() != 1 {
				t.Fatalf("approve similar: runs=%d res=%+v", rig.runs.Load(), res.res)
			}
			// Same session + scope: no prompt, runs.
			if _, early := rig.nextAsk(t, rig.exec("s1", `{"scope":"inbox"}`)); early == nil || early.res.Err != nil {
				t.Fatalf("same session/scope still prompted or failed: %+v", early)
			}
			if rig.runs.Load() != 2 {
				t.Fatalf("runs=%d after granted call", rig.runs.Load())
			}
			// Different session, then different scope: both must prompt.
			for _, c := range []struct{ session, args string }{{"s2", `{"scope":"inbox"}`}, {"s1", `{"scope":"archive"}`}} {
				done := rig.exec(c.session, c.args)
				req, early := rig.nextAsk(t, done)
				if early != nil {
					t.Fatalf("%s %s ran on a foreign grant: %+v", c.session, c.args, early)
				}
				m = audit130Begin(t, m, req)
				m = audit130Key(m, "n")
				if res := waitResult(t, done); !isApprovalDenied(res) {
					t.Fatalf("%s %s: %+v", c.session, c.args, res.res)
				}
			}
			// Grant expires after GrantTTL.
			rig.clock.Advance(10*time.Minute + time.Second)
			done = rig.exec("s1", `{"scope":"inbox"}`)
			req, early := rig.nextAsk(t, done)
			if early != nil {
				t.Fatalf("grant survived its TTL: %+v", early)
			}
			m = audit130Begin(t, m, req)
			audit130Key(m, "esc")
			if res := waitResult(t, done); !isApprovalDenied(res) || rig.runs.Load() != 2 {
				t.Fatalf("after expiry: runs=%d res=%+v", rig.runs.Load(), res.res)
			}
		})
	}
}

func TestAudit130ApprovalNoSessionGrantHidesSimilar(t *testing.T) {
	for _, lang := range []string{"pl", "en"} {
		t.Run(lang, func(t *testing.T) {
			rig := newAudit130Rig(t, lang)
			m := audit130Model(t, lang, 100, 30)
			done := rig.exec("s1", `{"scope":"inbox","irreversible":true}`)
			req, _ := rig.nextAsk(t, done)
			if len(req.Options) != 2 || req.Approval.SimilarLabel != "" {
				t.Fatalf("NoSessionGrant request offers similar: %+v", req.Options)
			}
			m = audit130Begin(t, m, req)
			if view := ansi.Strip(m.View()); strings.Contains(view, "[a]") || !strings.Contains(view, "[y]") {
				t.Fatalf("irreversible card choices wrong:\n%s", view)
			}
			for _, key := range []string{"a", "A"} {
				m = audit130Key(m, key)
				if m.pendingAsk == nil {
					t.Fatalf("%q closed an irreversible card without [a]", key)
				}
			}
			select {
			case res := <-done:
				t.Fatalf("'a' answered the gate: %+v", res)
			case <-time.After(50 * time.Millisecond):
			}
			m = audit130Key(m, "y")
			if res := waitResult(t, done); res.res.Err != nil || rig.runs.Load() != 1 {
				t.Fatalf("approve irreversible once: %+v", res.res)
			}
			// No grant: the next identical call prompts again.
			done = rig.exec("s1", `{"scope":"inbox","irreversible":true}`)
			req, early := rig.nextAsk(t, done)
			if early != nil {
				t.Fatalf("irreversible op ran without a prompt: %+v", early)
			}
			m = audit130Begin(t, m, req)
			audit130Key(m, "n")
			waitResult(t, done)
		})
	}
}

// After the card closes, typed keys must reach the composer again, the
// draft typed before the card must survive, and a queued ask shows next.
func TestAudit130ApprovalFocusReturnsToComposerAndQueueAdvances(t *testing.T) {
	rig := newAudit130Rig(t, "pl")
	m := audit130Model(t, "pl", 100, 30)
	for _, r := range "abc" {
		m = audit130Key(m, string(r))
	}
	if got := m.input.Value(); got != "abc" {
		t.Fatalf("composer before card = %q", got)
	}
	doneA := rig.exec("s1", `{"scope":"first"}`)
	reqA, _ := rig.nextAsk(t, doneA)
	doneB := rig.exec("s1", `{"scope":"second"}`)
	reqB, _ := rig.nextAsk(t, doneB)
	m = audit130Begin(t, m, reqA)
	m = audit130Begin(t, m, reqB)
	if len(m.askQueue) != 1 || m.pendingAsk == nil || m.pendingAsk.ID != reqA.ID {
		t.Fatalf("second ask not queued: queue=%d pending=%v", len(m.askQueue), m.pendingAsk)
	}
	// Typing while the card is open must not leak into the draft.
	m = audit130Key(m, "x")
	if got := m.input.Value(); got != "abc" {
		t.Fatalf("key leaked into composer while card open: %q", got)
	}
	m = audit130Key(m, "n")
	if m.pendingAsk == nil || m.pendingAsk.ID != reqB.ID || m.mode != modeAsking {
		t.Fatalf("queued ask did not appear: %+v", m.pendingAsk)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "target-second") {
		t.Fatalf("second card not rendered:\n%s", view)
	}
	if m.input.Focused() {
		t.Fatal("composer focused while the second card is open")
	}
	m = audit130Key(m, "y")
	if r := waitResult(t, doneA); !isApprovalDenied(r) {
		t.Fatalf("first: %+v", r.res)
	}
	if r := waitResult(t, doneB); r.res.Err != nil || rig.runs.Load() != 1 {
		t.Fatalf("second: runs=%d %+v", rig.runs.Load(), r.res)
	}
	if m.pendingAsk != nil || m.mode != modeNormal || !m.input.Focused() {
		t.Fatalf("after last card: pending=%v mode=%d focused=%v", m.pendingAsk, m.mode, m.input.Focused())
	}
	m = audit130Key(m, "z")
	if got := m.input.Value(); got != "abcz" {
		t.Fatalf("composer after card = %q, want abcz", got)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "abcz") {
		t.Fatalf("composer text not visible after card:\n%s", view)
	}
}

// A card whose gate already gave up (timeout/cancel) must not stay on screen.
func TestAudit130ApprovalClosedAskIsDropped(t *testing.T) {
	m := audit130Model(t, "en", 100, 30)
	done := make(chan struct{})
	req := tools.AskRequest{ID: "gone", Header: "h", Question: "q", Confirmation: true, Done: done, Respond: make(chan tools.AskAnswer, 1),
		Approval: &tools.ApprovalCard{Tool: "t", Operation: "write", ApproveLabel: "Approve", DenyLabel: "Deny"},
		Options:  []tools.AskOption{{Label: "Deny"}, {Label: "Approve"}}}
	m = audit130Begin(t, m, req)
	close(done)
	out, _ := m.Update(askClosedMsg("gone"))
	m = out.(Model)
	if m.pendingAsk != nil || m.mode != modeNormal {
		t.Fatalf("stale card stays open after its gate closed: mode=%d", m.mode)
	}
}
