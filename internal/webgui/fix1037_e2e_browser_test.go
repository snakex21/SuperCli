package webgui

// 1.0.37: full-app browser checks for background workers and the prompt
// queue. The real GUI (every embedded script, real handlers, real agent loop
// and worker registry) runs against an in-process scripted provider in an
// isolated temp data dir; nothing touches the user's data or a paid model.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

// fix1037Provider scripts the coordinator and its async worker.
//   - user "SPAWN ..."  -> task{agent:explore, async:true, prompt:"WORKER-LIVE ..."}
//   - user "HOLD ..."   -> waits for hold, then answers
//   - automatic wake    -> short acknowledgement
//   - worker            -> each request waits for one token on step; the
//     first `toolSteps` answers are list_dir calls, then the final report.
type fix1037Provider struct {
	step      chan struct{}
	hold      chan struct{}
	toolSteps int32
	holdWake  atomic.Bool
	// slowQueued delays the first token of a "QUEUED..." turn (nanoseconds).
	slowQueued atomic.Int64

	parentCalls, workerCalls atomic.Int32
	mu                       sync.Mutex
	trace                    []string
}

func newFix1037Provider(toolSteps int) *fix1037Provider {
	return &fix1037Provider{step: make(chan struct{}, 64), hold: make(chan struct{}, 16), toolSteps: int32(toolSteps)}
}

func (*fix1037Provider) Name() string { return "fix1037-fixture" }

func (p *fix1037Provider) note(s string) {
	p.mu.Lock()
	p.trace = append(p.trace, s)
	p.mu.Unlock()
}

func (p *fix1037Provider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	parent := false
	for _, def := range defs {
		parent = parent || def.Name == "task"
	}
	if !parent {
		select {
		case <-p.step:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		n := p.workerCalls.Add(1)
		p.note(fmt.Sprintf("worker %d", n))
		if n <= p.toolSteps {
			return workerSteeringWireCall(fmt.Sprintf("w-call-%d", n), "list_dir", map[string]any{"path": "."})
		}
		return workerSteeringWireDeltas(llm.Delta{Content: "WORKER-REPORT: fixture inspected", FinishReason: "stop"}), nil
	}
	p.parentCalls.Add(1)
	last := msgs[len(msgs)-1]
	user, wake := "", false
	for i := len(msgs) - 1; i >= 0; i-- {
		if llm.IsConversationUserTurn(msgs[i]) {
			user = msgs[i].Content
			break
		}
		wake = wake || strings.Contains(msgs[i].Content, "<task-notification>") && strings.Contains(msgs[i].Content, "WORKER-REPORT")
	}
	p.note(fmt.Sprintf("parent %s wake=%v %s", last.Role, wake, clipFix1037(user)))
	if last.Role == llm.RoleTool {
		if strings.Contains(user, "HOLD") {
			select {
			case <-p.hold:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return workerSteeringWireDeltas(llm.Delta{Content: "Spawned a background worker.", FinishReason: "stop"}), nil
	}
	if wake && last.Role != llm.RoleTool {
		if p.holdWake.Load() {
			select {
			case <-p.hold:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return workerSteeringWireDeltas(llm.Delta{Content: "Wake acknowledged.", FinishReason: "stop"}), nil
	}
	if d := p.slowQueued.Load(); d > 0 && strings.HasPrefix(user, "QUEUED") && last.Role != llm.RoleTool && !wake {
		select { // slow first token
		case <-time.After(time.Duration(d)):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	switch {
	case strings.HasPrefix(user, "SPAWN"):
		return workerSteeringWireCall(fmt.Sprintf("spawn-%d", p.parentCalls.Load()), "task", map[string]any{"agent": "explore", "prompt": "WORKER-LIVE inspect the fixture", "async": true})
	case strings.HasPrefix(user, "HOLD"):
		select {
		case <-p.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return workerSteeringWireDeltas(llm.Delta{Content: "REPLY to " + clipFix1037(user), FinishReason: "stop"}), nil
}

func clipFix1037(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 40 {
		return s[:40]
	}
	return s
}

func fix1037StartApp(t *testing.T, p *fix1037Provider) *audit130App {
	t.Helper()
	app := audit130StartApp(t, 1280, 900)
	if err := os.WriteFile(filepath.Join(app.srv.eng.DataDir(), "config.toml"), []byte("orchestrator = true\npreflight_repo = false\nbackground_wake_debounce_ms = 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.srv.eng.mu.Lock()
	app.srv.eng.prov = p
	app.srv.eng.mu.Unlock()
	app.setLanguage("pl")
	return app
}

// waitJS polls a boolean expression in the page.
func (a *audit130App) waitJS(what, expr string, timeout time.Duration) {
	a.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var ok bool
		a.eval(`(()=>{try{return !!(`+expr+`);}catch(e){return false;}})()`, &ok)
		if ok {
			return
		}
		if time.Now().After(deadline) {
			var dump string
			a.eval(`(()=>{const s=document.querySelector('#run-status');return 'status='+(s&&s.textContent)+' stream='+document.querySelector('#stream').textContent.slice(-600);})()`, &dump)
			a.t.Fatalf("timed out waiting for %s; %s", what, dump)
		}
		time.Sleep(80 * time.Millisecond)
	}
}

// cleanShot hides the fixture-only toast (the scripted provider is not a
// catalog model) and optionally opens the sessions sidebar, then shoots.
func (a *audit130App) cleanShot(name string, sidebar bool) {
	a.t.Helper()
	a.eval(`(()=>{const t=document.querySelector('#toast');if(t)t.classList.remove('show');
 if(`+fmt.Sprint(sidebar)+`){if(ui.sidebarHidden)toggleSidebar();document.querySelector('#side-tabs button[data-tab=sessions]').click();}return true;})()`, nil)
	a.settle()
	a.shot(name)
}

func (a *audit130App) send(text string) {
	a.t.Helper()
	a.eval(`(()=>{const p=document.querySelector('#prompt');p.value=`+jsString(text)+`;p.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('#send-btn').click();return true;})()`, nil)
}

// fix1037Observe records, every animation frame and on every DOM mutation,
// whether the queued message is visible (bubble or queue row) and whether the
// worker panel is shown, so a short disappearance cannot slip between polls.
const fix1037Observe = `(()=>{window.__obs={gaps:[],panelGaps:[],seenQueued:false,seenPanel:false,log:[]};
function queuedVisible(){const t=window.__queuedText;if(!t)return true;
 const inStream=[...document.querySelectorAll('#stream .msg-user')].some(n=>n.isConnected&&n.textContent.includes(t));
 const inQueue=[...document.querySelectorAll('#prompt-queue .queue-text')].some(n=>n.textContent.includes(t));return inStream||inQueue;}
function check(){const o=window.__obs;const v=queuedVisible();if(window.__queuedText){if(v)o.seenQueued=true;else if(o.seenQueued)o.gaps.push(performance.now());}
 const p=document.querySelector('#worker-overview');const shown=p&&!p.hidden&&document.querySelector('#worker-overview-list').children.length>0;
 if(shown)o.seenPanel=true;else if(o.seenPanel&&window.__watchPanel)o.panelGaps.push(performance.now());}
new MutationObserver(check).observe(document.body,{subtree:true,childList:true,attributes:true,characterData:true});
(function tick(){check();requestAnimationFrame(tick);})();return true;})()`

// Large conversations load their saved history slowly; model that latency.
const fix1037SlowHistory = `(()=>{const f=window.fetch;window.fetch=function(u,o){const s=String(u&&u.url||u);
 if(s.indexOf('/api/transcript')>=0){window.__obs&&window.__obs.log.push('transcript '+performance.now().toFixed(0));return new Promise(r=>setTimeout(r,1200)).then(()=>f.call(this,u,o));}
 return f.call(this,u,o);};return true;})()`

// Item 3: a message queued with "Add to queue" during a turn must stay
// visible when it gets its normal turn (queue row -> chat bubble, no gap).
func TestFix1037QueuedMessageStaysVisibleOnNormalTurn(t *testing.T) {
	p := newFix1037Provider(0)
	app := fix1037StartApp(t, p)
	app.eval(fix1037Observe, nil)
	app.eval(fix1037SlowHistory, nil)
	app.send("first message")
	app.waitJS("first reply", `!streaming&&document.querySelector('#stream').textContent.includes('REPLY to first')`, 10*time.Second)
	app.send("HOLD first turn")
	app.waitJS("first turn running", `streaming===true&&document.querySelector('#stream .msg-user')`, 10*time.Second)
	app.eval(`window.__queuedText='QUEUED-SECOND-MESSAGE';true`, nil)
	app.send("QUEUED-SECOND-MESSAGE please")
	app.waitJS("queue row", `[...document.querySelectorAll('#prompt-queue .queue-text')].some(n=>n.textContent.includes('QUEUED-SECOND'))`, 10*time.Second)
	app.shot("1037_queue_before")
	p.hold <- struct{}{}
	app.waitJS("queued reply", `document.querySelector('#stream').textContent.includes('REPLY to QUEUED-SECOND')`, 20*time.Second)
	// Let any late refresh of the finished turn land.
	time.Sleep(1500 * time.Millisecond)
	app.shot("1037_queue_after")
	var obs struct {
		Gaps    []float64 `json:"gaps"`
		Bubbles int       `json:"bubbles"`
	}
	app.eval(`(()=>({gaps:window.__obs.gaps,bubbles:[...document.querySelectorAll('#stream .msg-user')].filter(n=>n.textContent.includes('QUEUED-SECOND')).length}))()`, &obs)
	if len(obs.Gaps) > 0 {
		t.Fatalf("queued message disappeared for %d observation(s)", len(obs.Gaps))
	}
	if obs.Bubbles != 1 {
		t.Fatalf("queued message bubbles = %d, want 1", obs.Bubbles)
	}
}

// Item 3, automatic-turn variant: the message is queued while a server-started
// wake-up turn (a remote run in this view) is running.
func TestFix1037QueuedMessageStaysVisibleAfterWakeTurn(t *testing.T) {
	p := newFix1037Provider(0)
	p.holdWake.Store(true)
	app := fix1037StartApp(t, p)
	app.eval(fix1037Observe, nil)
	app.eval(fix1037SlowHistory, nil)
	app.send("SPAWN one worker")
	app.waitJS("spawn turn done", `!streaming&&document.querySelector('#stream').textContent.includes('Spawned a background')`, 15*time.Second)
	p.step <- struct{}{} // worker final report -> automatic wake turn (held)
	app.waitJS("wake turn running", `streaming===true&&activeRun&&activeRun.remote&&activeRun.auto`, 15*time.Second)
	app.eval(`window.__queuedText='QUEUED-DURING-WAKE';true`, nil)
	app.send("QUEUED-DURING-WAKE message")
	app.waitJS("queue row", `[...document.querySelectorAll('#prompt-queue .queue-text')].some(n=>n.textContent.includes('QUEUED-DURING-WAKE'))`, 10*time.Second)
	p.hold <- struct{}{}
	app.waitJS("queued reply", `document.querySelector('#stream').textContent.includes('REPLY to QUEUED-DURING-WAKE')`, 20*time.Second)
	time.Sleep(1500 * time.Millisecond)
	app.shot("1037_queue_wake_after")
	var obs struct {
		Gaps    []float64 `json:"gaps"`
		Bubbles int       `json:"bubbles"`
	}
	app.eval(`(()=>({gaps:window.__obs.gaps,bubbles:[...document.querySelectorAll('#stream .msg-user')].filter(n=>n.textContent.includes('QUEUED-DURING-WAKE')).length}))()`, &obs)
	if len(obs.Gaps) > 0 {
		t.Fatalf("queued message disappeared for %d observation(s) (%.0f..%.0f ms)", len(obs.Gaps), obs.Gaps[0], obs.Gaps[len(obs.Gaps)-1])
	}
	if obs.Bubbles != 1 {
		t.Fatalf("queued message bubbles = %d, want 1", obs.Bubbles)
	}
}

// Item 3 with a coordinator that delegated a worker in the same turn: the
// queued message gets its normal turn while the worker reports progress.
func TestFix1037QueuedMessageStaysVisibleWithWorkers(t *testing.T) {
	p := newFix1037Provider(2)
	app := fix1037StartApp(t, p)
	app.eval(fix1037Observe, nil)
	app.eval(fix1037SlowHistory, nil)
	app.send("first message")
	app.waitJS("first reply", `!streaming&&document.querySelector('#stream').textContent.includes('REPLY to first')`, 10*time.Second)
	app.send("SPAWN and HOLD")
	app.waitJS("worker card", `streaming===true&&document.querySelector('#stream .task-row')`, 10*time.Second)
	app.eval(`window.__queuedText='QUEUED-WITH-WORKER';true`, nil)
	app.send("QUEUED-WITH-WORKER message")
	app.waitJS("queue row", `[...document.querySelectorAll('#prompt-queue .queue-text')].some(n=>n.textContent.includes('QUEUED-WITH-WORKER'))`, 10*time.Second)
	p.step <- struct{}{}
	p.hold <- struct{}{}
	p.step <- struct{}{}
	app.waitJS("queued reply", `document.querySelector('#stream').textContent.includes('REPLY to QUEUED-WITH-WORKER')`, 20*time.Second)
	p.step <- struct{}{}
	time.Sleep(2500 * time.Millisecond)
	app.shot("1037_queue_worker_after")
	var obs struct {
		Gaps    []float64 `json:"gaps"`
		Bubbles int       `json:"bubbles"`
		Log     []string  `json:"log"`
	}
	app.eval(`(()=>({gaps:window.__obs.gaps,log:window.__obs.log,bubbles:[...document.querySelectorAll('#stream .msg-user')].filter(n=>n.textContent.includes('QUEUED-WITH-WORKER')).length}))()`, &obs)
	t.Logf("log=%v", obs.Log)
	if len(obs.Gaps) > 0 {
		t.Fatalf("queued message disappeared for %d observation(s) (%.0f..%.0f ms)", len(obs.Gaps), obs.Gaps[0], obs.Gaps[len(obs.Gaps)-1])
	}
	if obs.Bubbles != 1 {
		t.Fatalf("queued message bubbles = %d, want 1", obs.Bubbles)
	}
}

// The chat HTTP stream of the next turn ends early (WebView/proxy cut):
// the browser follows that turn through sync instead.
const fix1037CutChat = `(()=>{const f=window.fetch;window.fetch=function(u,o){const s=String(u&&u.url||u);
 if(s==='/api/chat'&&window.__cutChat&&o&&o.method==='POST'){window.__cutChat=false;return f.call(this,u,o).then(resp=>{
  const reader=resp.body.getReader();let seen=false;const body=new ReadableStream({async pull(c){if(seen){c.close();reader.cancel();return;}
   const r=await reader.read();if(r.done){c.close();return;}const txt=new TextDecoder().decode(r.value);if(txt.indexOf('session_activity')>=0)seen=true;c.enqueue(r.value);}});
  return new Response(body,{status:resp.status,headers:resp.headers});});}
 return f.call(this,u,o);};return true;})()`

func TestFix1037QueuedMessageStaysVisibleWhenFollowedBySync(t *testing.T) {
	p := newFix1037Provider(0)
	app := fix1037StartApp(t, p)
	app.eval(fix1037Observe, nil)
	app.eval(fix1037SlowHistory, nil)
	app.eval(fix1037CutChat, nil)
	app.send("first message")
	app.waitJS("first reply", `!streaming&&document.querySelector('#stream').textContent.includes('REPLY to first')`, 10*time.Second)
	app.send("HOLD second turn")
	app.waitJS("second turn running", `streaming===true`, 10*time.Second)
	app.eval(`window.__queuedText='QUEUED-FOLLOWED';window.__cutChat=true;true`, nil)
	app.send("QUEUED-FOLLOWED message HOLD")
	app.waitJS("queue row", `[...document.querySelectorAll('#prompt-queue .queue-text')].some(n=>n.textContent.includes('QUEUED-FOLLOWED'))`, 10*time.Second)
	p.hold <- struct{}{}
	time.Sleep(2500 * time.Millisecond)
	app.shot("1037_queue_followed_mid")
	p.hold <- struct{}{}
	app.waitJS("queued reply", `document.querySelector('#stream').textContent.includes('REPLY to QUEUED-FOLLOWED')`, 20*time.Second)
	time.Sleep(2500 * time.Millisecond)
	var obs struct {
		Gaps    []float64 `json:"gaps"`
		Bubbles int       `json:"bubbles"`
		Log     []string  `json:"log"`
	}
	app.eval(`(()=>({gaps:window.__obs.gaps,log:window.__obs.log,bubbles:[...document.querySelectorAll('#stream .msg-user')].filter(n=>n.textContent.includes('QUEUED-FOLLOWED')).length}))()`, &obs)
	t.Logf("log=%v", obs.Log)
	if len(obs.Gaps) > 0 {
		t.Fatalf("queued message disappeared for %d observation(s) (%.0f..%.0f ms)", len(obs.Gaps), obs.Gaps[0], obs.Gaps[len(obs.Gaps)-1])
	}
	if obs.Bubbles != 1 {
		t.Fatalf("queued message bubbles = %d, want 1", obs.Bubbles)
	}
}

// The native window is hidden and shown again (another app focused) while a
// turn runs: the browser then follows the turn through sync.
const fix1037HideShow = `(async()=>{if(!window.__hiddenPatched){window.__hiddenPatched=true;window.__hidden=false;Object.defineProperty(document,'hidden',{configurable:true,get:()=>window.__hidden});}
 window.__hidden=true;document.dispatchEvent(new Event('visibilitychange'));await new Promise(r=>setTimeout(r,150));
 window.__hidden=false;document.dispatchEvent(new Event('visibilitychange'));return true;})()`

func TestFix1037QueuedMessageStaysVisibleAfterWindowRefocus(t *testing.T) {
	p := newFix1037Provider(0)
	app := fix1037StartApp(t, p)
	app.eval(fix1037Observe, nil)
	app.eval(fix1037SlowHistory, nil)
	app.send("first message")
	app.waitJS("first reply", `!streaming&&document.querySelector('#stream').textContent.includes('REPLY to first')`, 10*time.Second)
	app.send("HOLD second turn")
	app.waitJS("second turn running", `streaming===true`, 10*time.Second)
	app.eval(`window.__queuedText='QUEUED-REFOCUS';true`, nil)
	app.send("QUEUED-REFOCUS message")
	app.waitJS("queue row", `[...document.querySelectorAll('#prompt-queue .queue-text')].some(n=>n.textContent.includes('QUEUED-REFOCUS'))`, 10*time.Second)
	app.eval(fix1037HideShow, nil)
	app.waitJS("followed run", `activeRun&&activeRun.remote&&streaming`, 10*time.Second)
	p.hold <- struct{}{}
	app.waitJS("queued reply", `document.querySelector('#stream').textContent.includes('REPLY to QUEUED-REFOCUS')`, 20*time.Second)
	time.Sleep(2500 * time.Millisecond)
	app.shot("1037_queue_refocus_after")
	var obs struct {
		Gaps    []float64 `json:"gaps"`
		Bubbles int       `json:"bubbles"`
		Log     []string  `json:"log"`
	}
	app.eval(`(()=>({gaps:window.__obs.gaps,log:window.__obs.log,bubbles:[...document.querySelectorAll('#stream .msg-user')].filter(n=>n.textContent.includes('QUEUED-REFOCUS')).length}))()`, &obs)
	t.Logf("log=%v gaps=%d", obs.Log, len(obs.Gaps))
	if len(obs.Gaps) > 0 {
		t.Fatalf("queued message disappeared for %d observation(s) (%.0f..%.0f ms)", len(obs.Gaps), obs.Gaps[0], obs.Gaps[len(obs.Gaps)-1])
	}
	if obs.Bubbles != 1 {
		t.Fatalf("queued message bubbles = %d, want 1", obs.Bubbles)
	}
}

// Two views of one conversation (native window + browser tab): both see the
// turn end and both try to start the queued message; the one that loses the
// durable claim must keep showing the message until the winner's run appears.
func TestFix1037QueuedMessageStaysVisibleInSecondView(t *testing.T) {
	p := newFix1037Provider(0)
	app := fix1037StartApp(t, p)
	app.send("first message")
	app.waitJS("first reply", `!streaming&&document.querySelector('#stream').textContent.includes('REPLY to first')`, 10*time.Second)
	var sid string
	app.eval(`activeSessionID`, &sid)
	second, err := app.browser.NewPage(app.ctx, app.api.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	b := &audit130App{t: t, ctx: app.ctx, srv: app.srv, api: app.api, browser: app.browser, page: second, dataDir: app.dataDir}
	if _, err := second.Screenshot(app.ctx); err != nil { // front, so its boot can paint
		t.Fatal(err)
	}
	b.waitReady()
	b.eval(`(async()=>{await loadSessions();await resumeSession(`+jsString(sid)+`,sessionByID[`+jsString(sid)+`]||null);return true;})()`, nil)
	b.waitJS("second view shows history", `document.querySelector('#stream').textContent.includes('REPLY to first')`, 10*time.Second)
	b.eval(`(()=>{const L=m=>window.__obs&&window.__obs.log.push(m+' '+performance.now().toFixed(0));
 const h=syncHandleFrame;syncHandleFrame=function(f){if(f.type!=='run_event'||(f.event&&f.event.type==='session_activity'))L('frame '+f.type+(f.event?':'+f.event.type:''));return h.apply(this,arguments);};
 const r=renderLoadedTranscript;renderLoadedTranscript=function(){L('render');return r.apply(this,arguments);};
 const d=dispatchQueuedTask;dispatchQueuedTask=function(){L('dispatch');return d.apply(this,arguments);};
 const rp=renderPromptQueue;renderPromptQueue=function(){L('queue '+promptQueue.length);return rp.apply(this,arguments);};
 return true;})()`, nil)
	for _, v := range []*audit130App{app, b} {
		v.eval(fix1037Observe, nil)
		v.eval(fix1037SlowHistory, nil)
		v.eval(`window.__queuedText='QUEUED-TWO-VIEWS';true`, nil)
	}
	if _, err := app.page.Screenshot(app.ctx); err != nil {
		t.Fatal(err)
	}
	// Both views are on screen (two windows): the background tab must not
	// suspend its sync connection as a hidden page would.
	b.eval(`(()=>{Object.defineProperty(document,'hidden',{configurable:true,get:()=>false});syncResume();return true;})()`, nil)
	app.send("HOLD second turn")
	app.waitJS("second turn running", `streaming===true`, 10*time.Second)
	app.send("QUEUED-TWO-VIEWS message")
	for i, v := range []*audit130App{app, b} {
		var dbg string
		v.eval(`JSON.stringify({q:promptQueue.map(x=>x.text+'@'+x.session_id),sid:activeSessionID,streaming:streaming})`, &dbg)
		t.Logf("view %d before wait: %s", i, dbg)
		v.waitJS("queue row", `[...document.querySelectorAll('#prompt-queue .queue-text')].some(n=>n.textContent.includes('QUEUED-TWO-VIEWS'))`, 10*time.Second)
	}
	p.hold <- struct{}{}
	for _, v := range []*audit130App{app, b} {
		v.waitJS("queued reply", `document.querySelector('#stream').textContent.includes('REPLY to QUEUED-TWO-VIEWS')`, 20*time.Second)
	}
	time.Sleep(2500 * time.Millisecond)
	for i, v := range []*audit130App{app, b} {
		var obs struct {
			Gaps    []float64 `json:"gaps"`
			Bubbles int       `json:"bubbles"`
			Log     []string  `json:"log"`
		}
		v.eval(`(()=>({gaps:window.__obs.gaps,log:window.__obs.log,bubbles:[...document.querySelectorAll('#stream .msg-user')].filter(n=>n.textContent.includes('QUEUED-TWO-VIEWS')).length}))()`, &obs)
		t.Logf("view %d: log=%v gaps=%d bubbles=%d", i, obs.Log, len(obs.Gaps), obs.Bubbles)
		if len(obs.Gaps) > 0 {
			t.Errorf("view %d: queued message disappeared for %d observation(s) (%.0f..%.0f ms)", i, len(obs.Gaps), obs.Gaps[0], obs.Gaps[len(obs.Gaps)-1])
		}
		if obs.Bubbles != 1 {
			t.Errorf("view %d: queued message bubbles = %d, want 1", i, obs.Bubbles)
		}
	}
	if p.parentCalls.Load() != 3 {
		t.Errorf("parent calls = %d (the queued message must run exactly once)", p.parentCalls.Load())
	}
}

type fix1037WorkerView struct {
	Badge       string   `json:"badge"`
	BadgeHidden bool     `json:"badgeHidden"`
	BadgeIdle   bool     `json:"badgeIdle"`
	SideBg      string   `json:"sideBg"`
	Composer    bool     `json:"composer"`
	Card        string   `json:"card"`
	Items       []string `json:"items"`
	Liveness    string   `json:"liveness"`
	Stalled     bool     `json:"stalled"`
	StallBtns   int      `json:"stallBtns"`
	PanelHidden bool     `json:"panelHidden"`
	PanelItems  int      `json:"panelItems"`
	PanelLive   string   `json:"panelLive"`
	Status      string   `json:"status"`
}

const fix1037WorkerState = `(()=>{const b=document.querySelector('#background-agents');const side=document.querySelector('.side-item.active .side-bg')||document.querySelector('.side-item .side-bg');
const card=[...document.querySelectorAll('#stream .task-row')].pop();const live=card&&card.querySelector('.task-liveness');
const p=document.querySelector('#worker-overview');const pl=document.querySelector('#worker-overview .worker-overview-live');
return {badge:b.textContent,badgeHidden:b.hidden,badgeIdle:b.classList.contains('idle'),sideBg:side?side.textContent:'',composer:document.querySelector('#composer').classList.contains('bg-working'),
card:card?card.textContent:'',items:card?[...card.querySelectorAll('.task-activity-item')].map(n=>n.textContent):[],
liveness:live&&!live.hidden?live.textContent:'',stalled:!!(card&&card.classList.contains('stalled')),stallBtns:card?card.querySelectorAll('.worker-stall-actions:not([hidden]) button').length:0,
panelHidden:p.hidden,panelItems:document.querySelectorAll('#worker-overview-list .worker-overview-item').length,panelLive:pl&&!pl.hidden?pl.textContent:'',
status:document.querySelector('#run-status').textContent};})()`

// Items 1, 2 and 4 end to end: background work is visible on the session and
// composer; a card rebuilt from history keeps receiving live progress; a
// refresh rebuilds the run's tool calls; liveness and the stall warning show;
// the worker panel never blinks when the worker finishes.
func TestFix1037BackgroundWorkerLivenessEndToEnd(t *testing.T) {
	p := newFix1037Provider(3)
	app := fix1037StartApp(t, p)
	app.send("SPAWN one worker")
	app.waitJS("spawn turn done", `!streaming&&document.querySelector('#stream').textContent.includes('Spawned a background')`, 15*time.Second)
	// Item 1: the coordinator is idle, the worker runs.
	app.waitJS("background state", `!document.querySelector('#background-agents').hidden&&document.querySelector('.side-item .side-bg')`, 10*time.Second)
	var v fix1037WorkerView
	app.eval(fix1037WorkerState, &v)
	t.Logf("idle with worker: %+v", v)
	if !strings.Contains(v.Badge, "Pracuje w tle (1)") || !v.BadgeIdle || !v.Composer || !strings.Contains(v.SideBg, "1 w tle") {
		t.Fatalf("background work not shown clearly: %+v", v)
	}
	app.cleanShot("1037_background_idle", true)

	// Item 4 root cause: progress after the turn ended reaches the rebuilt card.
	p.step <- struct{}{}
	app.waitJS("live tool call on rebuilt card", `[...document.querySelectorAll('#stream .task-row')].pop().querySelectorAll('.task-activity-item').length>=1`, 10*time.Second)
	app.eval(`(()=>{const c=[...document.querySelectorAll('#stream .task-row')].pop();c.open=true;c._body.hidden=false;return true;})()`, nil)
	app.waitJS("liveness line", `(document.querySelector('#stream .task-row .task-liveness:not([hidden])')||{}).textContent.includes('ostatnia aktywność')`, 10*time.Second)
	app.eval(fix1037WorkerState, &v)
	t.Logf("after first tool: %+v", v)
	if strings.Contains(v.Card, "sprzed odświeżenia") {
		t.Fatalf("card still shows the refresh placeholder: %q", v.Card)
	}
	app.cleanShot("1037_worker_live", false)

	// Refresh: the run's earlier calls are rebuilt from the live journal.
	p.step <- struct{}{}
	app.waitJS("second call", `[...document.querySelectorAll('#stream .task-row')].pop().querySelectorAll('.task-activity-item').length>=2`, 10*time.Second)
	var sid string
	app.eval(`activeSessionID`, &sid)
	app.reload()
	// saveUI persists the language after a debounce; a reload can come first.
	app.setLanguage("pl")
	app.eval(`(async()=>{await loadSessions();await resumeSession(`+jsString(sid)+`,sessionByID[`+jsString(sid)+`]||null);return true;})()`, nil)
	app.waitJS("journal rebuilt after refresh", `(()=>{const c=[...document.querySelectorAll('#stream .task-row')].pop();return c&&c.querySelectorAll('.task-activity-item').length>=2;})()`, 15*time.Second)
	app.eval(`(()=>{const c=[...document.querySelectorAll('#stream .task-row')].pop();c.open=true;c._body.hidden=false;return true;})()`, nil)
	app.eval(fix1037WorkerState, &v)
	t.Logf("after refresh: %+v", v)
	if strings.Contains(v.Card, "sprzed odświeżenia") || len(v.Items) < 2 {
		t.Fatalf("refresh did not rebuild the run's tool calls: %+v", v)
	}
	app.waitJS("panel liveness after refresh", `(document.querySelector('#worker-overview .worker-overview-live:not([hidden])')||{}).textContent.includes('ostatnia aktywność')`, 5*time.Second)
	app.cleanShot("1037_worker_after_refresh", false)

	// Stall warning with Stop/Restart (threshold shortened for the test).
	app.eval(`WORKER_STALL_MS=1500;true`, nil)
	app.waitJS("stall warning", `[...document.querySelectorAll('#stream .task-row')].pop().classList.contains('stalled')`, 10*time.Second)
	app.eval(fix1037WorkerState, &v)
	t.Logf("stalled: %+v", v)
	if !strings.Contains(v.Liveness, "brak postępu") || v.StallBtns != 2 {
		t.Fatalf("stall warning/actions missing: %+v", v)
	}
	app.cleanShot("1037_worker_stalled", false)
	app.eval(`WORKER_STALL_MS=5*60*1000;true`, nil)

	// Item 2: the panel stays while the worker finishes and the wake turn runs.
	app.eval(fix1037Observe, nil)
	app.waitJS("panel shown", `!document.querySelector('#worker-overview').hidden`, 10*time.Second)
	app.eval(`window.__watchPanel=true;true`, nil)
	p.step <- struct{}{} // third tool call
	p.step <- struct{}{} // final report -> wake turn
	app.waitJS("wake reply", `document.querySelector('#stream').textContent.includes('Wake acknowledged')`, 20*time.Second)
	time.Sleep(1500 * time.Millisecond)
	app.eval(fix1037WorkerState, &v)
	var gaps []float64
	app.eval(`window.__obs.panelGaps`, &gaps)
	t.Logf("after finish: %+v gaps=%d", v, len(gaps))
	app.cleanShot("1037_worker_done_panel", false)
	if len(gaps) > 0 {
		t.Fatalf("worker panel disappeared %d time(s) when the worker finished", len(gaps))
	}
	if v.PanelHidden || v.PanelItems != 1 {
		t.Fatalf("finished worker left the panel: %+v", v)
	}
	if !v.BadgeHidden || v.Composer || v.SideBg != "" {
		t.Fatalf("background state not cleared after the worker finished: %+v", v)
	}
}

// Debug: record which code path removes a user bubble containing the
// queued text (removeChild/remove/replaceChildren/innerHTML on #stream).
const fix1037TraceRemovals = `(()=>{window.__rm=[];const has=n=>n&&n.nodeType===1&&window.__queuedText&&n.textContent.includes(window.__queuedText)&&(n.matches('.msg-user')||n.querySelector('.msg-user'));
const note=(how)=>window.__rm.push(how+' @'+performance.now().toFixed(0)+' '+(new Error().stack||'').split('\n').slice(2,9).map(s=>s.trim()).join(' | '));
const rc=Node.prototype.removeChild;Node.prototype.removeChild=function(c){if(has(c))note('removeChild');return rc.call(this,c);};
const rm=Element.prototype.remove;Element.prototype.remove=function(){if(has(this))note('remove');return rm.call(this);};
const st=document.querySelector('#stream');const d=Object.getOwnPropertyDescriptor(Element.prototype,'innerHTML');
Object.defineProperty(st,'innerHTML',{configurable:true,get(){return d.get.call(this);},set(v){if(has(this))note('innerHTML');d.set.call(this,v);}});
const rcs=Element.prototype.replaceChildren;Element.prototype.replaceChildren=function(){if(this===st&&has(this))note('replaceChildren');return rcs.apply(this,arguments);};
return true;})()`

// Item 3, single view, orchestrator conditions: a background worker finishes
// around the end of the coordinator's turn while a message is queued, so the
// automatic wake-up turn races the queued message's turn.
func TestFix1037QueuedMessageSingleViewRacesWakeTurn(t *testing.T) {
	for _, offset := range []time.Duration{-150 * time.Millisecond, 0, 120 * time.Millisecond, 600 * time.Millisecond} {
		t.Run(offset.String(), func(t *testing.T) { fix1037SingleViewRace(t, offset, 0, 4*time.Second) })
	}
	// The wake-up turn claims the conversation first (the queued turn's
	// request is slower to leave the browser).
	t.Run("wake-first-long", func(t *testing.T) { fix1037SingleViewRace(t, 0, 400*time.Millisecond, 4*time.Second) })
	t.Run("wake-first-short", func(t *testing.T) { fix1037SingleViewRace(t, 0, 400*time.Millisecond, 1500*time.Millisecond) })
	// The window is hidden and shown while the queued turn waits for the
	// automatic turn (the app follows its own turn through sync then).
	t.Run("wake-first-refocus", func(t *testing.T) { fix1037SingleViewRace(t, 0, 400*time.Millisecond, -1500*time.Millisecond) })
}

func fix1037SingleViewRace(t *testing.T, offset, chatDelay, wakeHold time.Duration) {
	p := newFix1037Provider(0)
	p.slowQueued.Store(int64(3 * time.Second))
	app := fix1037StartApp(t, p)
	app.eval(fix1037Observe, nil)
	app.eval(fix1037SlowHistory, nil)
	app.eval(`(()=>{const L=m=>window.__obs.log.push(m+' '+performance.now().toFixed(0));
 const d=dispatchQueuedTask;dispatchQueuedTask=function(){L('dispatch');return d.apply(this,arguments);};
 const sp=sendPrompt;sendPrompt=function(t){L('send '+String(t).slice(0,12));return sp.apply(this,arguments);};
 const h=syncHandleFrame;syncHandleFrame=function(f){if(f.type!=='run_event'||(f.event&&/session_activity|done/.test(f.event.type)))L('frame '+f.type+(f.event?':'+f.event.type:'')+(f.auto?' auto':''));return h.apply(this,arguments);};
 return true;})()`, nil)
	app.send("SPAWN one worker")
	app.waitJS("spawn turn done", `!streaming&&document.querySelector('#stream').textContent.includes('Spawned a background')`, 15*time.Second)
	p.holdWake.Store(true)
	app.send("HOLD user turn")
	app.waitJS("user turn running", `streaming===true&&document.querySelector('#stream').textContent.includes('HOLD user turn')`, 10*time.Second)
	if chatDelay > 0 {
		app.eval(`(()=>{const f=window.fetch;window.fetch=function(u,o){if(String(u)==='/api/chat'&&o&&o.method==='POST'&&String(o.body).includes('QUEUED'))return new Promise(r=>setTimeout(r,`+fmt.Sprint(chatDelay.Milliseconds())+`)).then(()=>f.call(this,u,o));return f.call(this,u,o);};return true;})()`, nil)
	}
	app.eval(`window.__queuedText='QUEUED-SINGLE-VIEW';true`, nil)
	app.eval(fix1037TraceRemovals, nil)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		var dump string
		_ = app.page.Eval(context.Background(), `JSON.stringify({log:window.__obs.log,rm:window.__rm,gaps:window.__obs.gaps.length,q:promptQueue.map(x=>x.text),pause:pauseQueue,ev:[...document.querySelectorAll('#stream .event-line')].map(n=>n.textContent)})`, &dump)
		t.Logf("dump: %s", dump)
	})
	app.send("QUEUED-SINGLE-VIEW message")
	app.waitJS("queue row", `[...document.querySelectorAll('#prompt-queue .queue-text')].some(n=>n.textContent.includes('QUEUED-SINGLE-VIEW'))`, 10*time.Second)
	if offset < 0 {
		p.step <- struct{}{}
		time.Sleep(-offset)
		p.hold <- struct{}{}
	} else {
		p.hold <- struct{}{}
		time.Sleep(offset)
		p.step <- struct{}{}
	}
	if wakeHold < 0 {
		wakeHold = -wakeHold
		time.Sleep(700 * time.Millisecond)
		app.eval(fix1037HideShow, nil)
	}
	time.Sleep(wakeHold)
	p.hold <- struct{}{} // release a held wake turn, if one started
	p.hold <- struct{}{}
	app.waitJS("queued reply", `document.querySelector('#stream').textContent.includes('REPLY to QUEUED-SINGLE-VIEW')`, 30*time.Second)
	time.Sleep(2500 * time.Millisecond)
	var obs struct {
		Gaps    []float64 `json:"gaps"`
		Bubbles int       `json:"bubbles"`
		Log     []string  `json:"log"`
		Rm      []string  `json:"rm"`
	}
	app.eval(`(()=>({gaps:window.__obs.gaps,log:window.__obs.log,rm:window.__rm,bubbles:[...document.querySelectorAll('#stream .msg-user')].filter(n=>n.textContent.includes('QUEUED-SINGLE-VIEW')).length}))()`, &obs)
	p.mu.Lock()
	t.Logf("provider: %v", p.trace)
	p.mu.Unlock()
	t.Logf("log=%v", obs.Log)
	for _, r := range obs.Rm {
		t.Logf("removed: %s", r)
	}
	if len(obs.Gaps) > 0 {
		t.Fatalf("queued message disappeared for %d observation(s) (%.0f..%.0f ms)", len(obs.Gaps), obs.Gaps[0], obs.Gaps[len(obs.Gaps)-1])
	}
	if obs.Bubbles != 1 {
		t.Fatalf("queued message bubbles = %d, want 1", obs.Bubbles)
	}
	// Settled tail blocks (no longer "live") must not be virtualized: a culled
	// tail block is a message that vanishes for seconds in WebView2.
	var cv string
	app.eval(`(()=>{const b=[...document.querySelectorAll('#stream .msg-user')].filter(n=>n.textContent.includes('QUEUED-SINGLE-VIEW')).pop();return b.classList.contains('transcript-live')+' '+getComputedStyle(b).contentVisibility;})()`, &cv)
	if !strings.HasSuffix(cv, " visible") {
		t.Fatalf("queued bubble at the tail is virtualized: %s", cv)
	}
}

// 1.0.37: the settings show "Max. parallel agents" with a warning when it is
// unlimited or high (rate limits, cost, local LM Studio serializes requests).
func TestFix1037ParallelAgentsSettingWarns(t *testing.T) {
	app := audit130StartApp(t, 1280, 900)
	app.setLanguage("pl")
	state := func() (label, warn string) {
		var out struct {
			Label string `json:"label"`
			Warn  string `json:"warn"`
		}
		app.eval(`(async()=>{openPanel('modes');for(let i=0;i<60;i++){const r=[...document.querySelectorAll('.knob-row')].find(r=>r.querySelector('.k-name')&&r.querySelector('.k-name').title.endsWith('max_parallel_agents'));if(r){const w=r.querySelector('.k-warn');return {label:r.querySelector('.k-name').firstChild.textContent,warn:w?w.textContent:''};}await new Promise(r=>setTimeout(r,50));}return {label:'',warn:''};})()`, &out)
		return out.Label, out.Warn
	}
	label, warn := state()
	if label != "Maks. równoległych agentów" || warn != "" {
		t.Fatalf("default row: label=%q warn=%q", label, warn)
	}
	app.eval(`jpost('/api/config',{key:'max_parallel_agents',value:'0'}).then(()=>true)`, nil)
	app.eval(`(async()=>{await sections.modes();return true;})()`, nil)
	_, warn = state()
	if !strings.Contains(warn, "429") || !strings.Contains(warn, "LM Studio") {
		t.Fatalf("unlimited without warning: %q", warn)
	}
	app.cleanShot("1037_parallel_agents_setting", false)
}
