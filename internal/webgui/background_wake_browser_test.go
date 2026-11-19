package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/testbrowser"
)

// Production 04/05/05b scripts in a native browser: background sync frames
// update the running-agents badge and an idle task card, and an automatic
// (auto) remote run is shown with the wake marker instead of a user bubble.
func TestBackgroundSyncFramesNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	wire := func(e agent.Event) string {
		w, ok := toWireEvent(e)
		if !ok {
			t.Fatalf("dropped %T", e)
		}
		b, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	frame := func(f syncFrame) string {
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	started := wire(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "explore", Kind: "started", ParentCallID: "bg", Run: 1})
	finished, _ := toWireEvent(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "explore", Kind: "finished", ParentCallID: "bg", Run: 1, Status: "done"})
	en, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<!doctype html><html><body><span id="background-agents" hidden></span><script>var fixtureCatalog=%s;</script><script src="/prelude.js"></script><script src="/04-transcript.js"></script><script src="/05-chat.js"></script><script src="/05b-sync.js"></script><script src="/ready.js"></script></body></html>`, en)
		case "/prelude.js":
			fmt.Fprint(w, queueEditBrowserPrelude+`
var superCliUI={fileMutationTools:{},mutationKind:function(){return '';},decodeSSEFrame:function(){return null;}};var preferences={};
function registerLanguageRefresh(){} function toolDisclosureOpen(){return true;}
function thinkingDisclosureOpen(){return false;} function renderText(s){return s;}
function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}
`)
		case "/ready.js":
			fmt.Fprint(w, queueEditBrowserReady+`smartScroll=function(){};appendStream=function(n){stream.appendChild(n);};window.fixtureReady=true;`)
		default:
			b, e := assetsFS.ReadFile("assets/js" + r.URL.Path)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1000, Height: 800})
	if err != nil {
		t.Skipf("native browser unavailable: %v", err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var failure string
	script := `(()=>{try{
if(!window.fixtureReady)throw Error('scripts not ready');
function check(ok,msg){if(!ok)throw Error(msg);}
activeSessionID='s1';window.resumeSession=function(){return Promise.resolve();};
syncHandleFrame(` + frame(syncFrame{Type: "background_agents", SessionID: "s1", Count: 2}) + `);
var badge=document.getElementById('background-agents');
check(!badge.hidden&&badge.textContent.indexOf('2')>=0,'badge not shown: '+badge.textContent);
syncHandleFrame(` + frame(syncFrame{Type: "background_agents", SessionID: "other", Count: 5}) + `);
check(badge.textContent.indexOf('5')<0,'badge shows another conversation');
// A card from an earlier turn keeps updating with no live run.
handleEvent({type:'tool_call',name:'task',args:'{"agent":"explore","prompt":"review","async":true}',id:'bg'},null);
syncHandleFrame({type:'background_event',session_id:'s1',event:` + started + `});
check(workerRows['worker-1']===toolRows.bg,'background start did not bind the card');
syncHandleFrame({type:'background_event',session_id:'s1',event:` + mustJSON(t, finished) + `});
check(toolRows.bg.classList.contains('done'),'background finish did not reach the card');
syncHandleFrame(` + frame(syncFrame{Type: "background_agents", SessionID: "s1", Count: 0}) + `);
check(badge.hidden,'badge stayed after agents finished');
// Snapshot replays counts.
syncHandleFrame({type:'snapshot',runs:[],background:{s1:1}});
check(!badge.hidden&&badge.textContent.indexOf('1')>=0,'snapshot count lost');
// Automatic run: marker, not a user bubble.
setRunState=function(){};syncStartRunTimer=function(){};var userBubbles=0;addUserMsg=function(){userBubbles++;return el('div');};
syncHandleFrame({type:'run_started',session_id:'s1',turn_id:'server-9',auto:true,prompt:'[automatic wake-up]'});
var run=syncRuns.s1;check(run&&run.auto,'auto flag lost');
run.reattach();
check(userBubbles===0,'automatic turn rendered as a user message');
check(stream.querySelector('.auto-wake'),'automatic turn marker missing');
// Orphaned cards get a definite state with a Resume action, never "unconfirmed" forever.
handleEvent({type:'tool_call',name:'task',args:'{"agent":"code","prompt":"orphan"}',id:'orph'},null);
handleEvent({type:'worker_progress',id:'worker-7',name:'code',kind:'started',parent_call_id:'orph',run:1},null);
var orphan=toolRows.orph;check(workerRows['worker-7']===orphan,'orphan not bound');
applyWorkerStatusSnapshot([{id:'worker-7',agent:'code',status:'interrupted',run:1,resumable:true,last_error:'runner ended'}],[{id:'worker-7',row:orphan,run:1,activity:orphan._taskActivitySerial||0}]);
check(orphan._taskStatus==='interrupted','interrupted state not applied');
check(orphan._stat.textContent===t('task.interrupted'),'interrupted label: '+orphan._stat.textContent);
check(orphan.querySelector('.worker-resume'),'resume action missing');
var sent=[];sendPrompt=function(text){sent.push(text);};streaming=false;
orphan.querySelector('.worker-resume').click();
check(sent.length===1&&sent[0].indexOf('worker-7')>=0,'resume did not ask the coordinator: '+sent.join('|'));
handleEvent({type:'tool_call',name:'task',args:'{"agent":"code","prompt":"lost"}',id:'lost'},null);
handleEvent({type:'worker_progress',id:'worker-8',name:'code',kind:'started',parent_call_id:'lost',run:1},null);
var lost=toolRows.lost,binding={id:'worker-8',row:lost,run:1,activity:lost._taskActivitySerial||0};
applyWorkerStatusSnapshot([],[binding]);applyWorkerStatusSnapshot([],[binding]);
check(lost._taskStatus==='interrupted','missing runner stayed unconfirmed');
Object.values(toolRows).forEach(r=>clearInterval(r._clock));clearInterval(runTimer);
return "";}catch(e){return e.stack||String(e)}})()`
	if err := page.Eval(ctx, script, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
