package webgui

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	core "supercli/internal/tools/core"
	"supercli/internal/tools/interactive"
)

// audit-130: GUI tool approval end to end. A synthetic risky tool runs in a
// real core.Registry behind the real ApprovalGate; the gate's prompt is
// registered with the GUI engine exactly like a run's question, rendered by
// the real card in headless Chrome, and answered with a key press that posts
// to the real /api/question/answer handler. A denied tool must not run; "a"
// grants only the same session and scope.

type audit130GUIRig struct {
	reg  *core.Registry
	gate *interactive.ApprovalGate
	asks chan interactive.AskRequest
	runs atomic.Int64
}

func newAudit130GUIRig() *audit130GUIRig {
	rig := &audit130GUIRig{asks: make(chan interactive.AskRequest, 4)}
	rig.gate = &interactive.ApprovalGate{Fallback: rig.asks, GrantTTL: 10 * time.Minute, Timeout: 30 * time.Second, Language: func() string { return "pl" }}
	rig.reg = core.NewRegistry()
	rig.reg.MustRegister(core.Tool{
		Name:        "audit_gui_tool",
		Description: "audit-130 synthetic risky tool",
		Schema:      `{"type":"object","properties":{"scope":{"type":"string"}},"required":["scope"]}`,
		Approval: func(_ context.Context, args json.RawMessage) (*core.ApprovalRequest, error) {
			var a struct{ Scope string }
			_ = json.Unmarshal(args, &a)
			return &core.ApprovalRequest{Operation: "write", Risk: core.RiskWriteReversible, Reversible: true, Target: "target-" + a.Scope,
				Summary: "Write to " + a.Scope, Scope: []string{"audit_gui_tool", "write", a.Scope}}, nil
		},
		Fn: func(context.Context, json.RawMessage) (core.Result, error) {
			rig.runs.Add(1)
			return core.Result{Text: "done"}, nil
		},
	})
	return rig
}

type audit130GUIResult struct {
	res core.Result
	err error
}

func (r *audit130GUIRig) exec(session, scope string) <-chan audit130GUIResult {
	out := make(chan audit130GUIResult, 1)
	ctx := core.WithApprovalSession(core.WithApprover(context.Background(), r.gate), session)
	args, _ := json.Marshal(map[string]string{"scope": scope})
	go func() {
		res, err := r.reg.Execute(ctx, "audit_gui_tool", args)
		out <- audit130GUIResult{res, err}
	}()
	return out
}

// answerInBrowser renders the gate's prompt with the real card and presses key.
func (r *audit130GUIRig) answerInBrowser(t *testing.T, app *audit130App, done <-chan audit130GUIResult, key string) audit130GUIResult {
	t.Helper()
	var req interactive.AskRequest
	select {
	case req = <-r.asks:
	case res := <-done:
		t.Fatalf("tool finished without asking: %+v", res)
	case <-time.After(10 * time.Second):
		t.Fatal("approval prompt never arrived")
	}
	wire, err := json.Marshal(app.srv.eng.registerQuestion(req))
	if err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Focus   string `json:"focus"`
		Similar bool   `json:"similar"`
	}
	app.eval(`(async()=>{showApprovalCard(`+string(wire)+`);await new Promise(r=>setTimeout(r,120));const c=[...document.querySelectorAll('.approval-card')].pop();
 const f=document.activeElement;f.dispatchEvent(new KeyboardEvent('keydown',{key:`+jsString(key)+`,bubbles:true,cancelable:true}));
 for(let i=0;i<60&&c.querySelector('.approval-actions');i++)await new Promise(r=>setTimeout(r,50));
 return {focus:f.className,similar:!!c.querySelector('.approval-similar')||c.classList.contains('answered')};})()`, &shown)
	select {
	case res := <-done:
		return res
	case <-time.After(10 * time.Second):
		t.Fatalf("tool call did not finish after pressing %q in the GUI (focus was on %q)", key, shown.Focus)
	}
	return audit130GUIResult{}
}

func TestAudit130GUIApprovalEndToEnd(t *testing.T) {
	app := audit130StartApp(t, 1280, 900)
	app.setLanguage("pl")
	for _, key := range []string{"n", "N", "Escape"} {
		rig := newAudit130GUIRig()
		res := rig.answerInBrowser(t, app, rig.exec("gui-s1", "deny-"+key), key)
		if rig.runs.Load() != 0 {
			t.Fatalf("%q: denied tool ran %d time(s)", key, rig.runs.Load())
		}
		if res.err == nil && res.res.Err == nil {
			t.Fatalf("%q: denied call returned no error: %+v", key, res.res)
		}
		if err := errors.Join(res.err, res.res.Err); !errors.Is(err, core.ErrApprovalRequired) {
			t.Fatalf("%q: denial error does not wrap ErrApprovalRequired: %v", key, err)
		}
	}
	for _, key := range []string{"y", "Y"} {
		rig := newAudit130GUIRig()
		res := rig.answerInBrowser(t, app, rig.exec("gui-s1", "once"), key)
		if res.err != nil || res.res.Err != nil || rig.runs.Load() != 1 {
			t.Fatalf("%q: approve once: runs=%d err=%v/%v", key, rig.runs.Load(), res.err, res.res.Err)
		}
		// Approve-once leaves no grant: the same call asks again.
		done := rig.exec("gui-s1", "once")
		select {
		case <-rig.asks:
		case r := <-done:
			t.Fatalf("%q: approve-once was remembered: %+v", key, r)
		case <-time.After(10 * time.Second):
			t.Fatal("second prompt never arrived")
		}
	}
	rig := newAudit130GUIRig()
	res := rig.answerInBrowser(t, app, rig.exec("gui-s1", "grant"), "a")
	if res.err != nil || res.res.Err != nil || rig.runs.Load() != 1 {
		t.Fatalf("approve similar: runs=%d err=%v/%v", rig.runs.Load(), res.err, res.res.Err)
	}
	// Same session + scope: runs without a prompt.
	select {
	case r := <-rig.exec("gui-s1", "grant"):
		if r.err != nil || r.res.Err != nil || rig.runs.Load() != 2 {
			t.Fatalf("granted repeat failed: runs=%d %v %v", rig.runs.Load(), r.err, r.res.Err)
		}
	case <-rig.asks:
		t.Fatal("approve similar was not remembered for the same session and scope")
	case <-time.After(10 * time.Second):
		t.Fatal("granted repeat hung")
	}
	// Another session or another scope still asks.
	for _, c := range []struct{ session, scope string }{{"gui-s2", "grant"}, {"gui-s1", "other"}} {
		done := rig.exec(c.session, c.scope)
		select {
		case <-rig.asks:
		case r := <-done:
			t.Fatalf("grant leaked to session %q scope %q: %+v", c.session, c.scope, r)
		case <-time.After(10 * time.Second):
			t.Fatal("prompt never arrived")
		}
	}
	if rig.runs.Load() != 2 {
		t.Fatalf("unexpected runs: %d", rig.runs.Load())
	}
}
