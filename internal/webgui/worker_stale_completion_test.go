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

// Runs the production scripts and wire conversion in an isolated native browser,
// without an Engine, model, user configuration, or live GUI.
func TestWorkerStaleCompletionNativeBrowser(t *testing.T) {
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
	first := wire(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "code", Kind: "started", ParentCallID: "first", Run: 1, Model: "old-model", ReasoningEffort: "low"})
	second := wire(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "code", Kind: "started", ParentCallID: "second", Run: 2, Model: "new-model", ReasoningEffort: "high"})
	call := wire(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "code", Kind: "tool_call", ParentCallID: "second", Run: 2, CallID: "read2", Tool: "read_lines", Args: `{"file":"current.txt"}`})
	stale := wire(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "code", Kind: "finished", ParentCallID: "first", Run: 1, Status: "done", Model: "old-model", ReasoningEffort: "low"})
	note := `<task-notification><task-id>worker-1</task-id><agent>code</agent><status>done</status><worker-model>old-model</worker-model><reasoning-effort>low</reasoning-effort><result>HISTORICAL_REPORT</result></task-notification>`
	notification := wire(agent.WorkerNotificationEvent{TaskID: "worker-1", Agent: "code", Status: "done", Text: note, Summary: "HISTORICAL_REPORT", Run: 1})
	legacyNotification := wire(agent.WorkerNotificationEvent{TaskID: "worker-1", Agent: "code", Status: "done", Text: note, Summary: "HISTORICAL_REPORT"})
	result := wire(agent.ToolResultEvent{ID: "first", Output: note})
	finish := wire(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "code", Kind: "finished", ParentCallID: "second", Run: 2, Status: "done"})
	en, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<!doctype html><html><body><script>var fixtureCatalog=%s;</script><script src="/prelude.js"></script><script src="/04-transcript.js"></script><script src="/05-chat.js"></script><script src="/ready.js"></script></body></html>`, en)
		case "/prelude.js":
			fmt.Fprint(w, queueEditBrowserPrelude+`
var superCliUI={fileMutationTools:{},mutationKind:function(){return '';}};var preferences={};
function registerLanguageRefresh(){} function toolDisclosureOpen(){return true;}
function thinkingDisclosureOpen(){return false;} function renderText(s){return s;}
function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}
`)
		case "/ready.js":
			fmt.Fprint(w, `toolDisclosureOpen=function(){return true;};smartScroll=function(){}; transcriptScrollTop=function(){return 0;};appendStream=function(n){stream.appendChild(n);};window.fixtureReady=true;`)
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
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	eval := func(s string) {
		t.Helper()
		var failure string
		if err := page.Eval(ctx, `(()=>{try{`+s+`;return ""}catch(e){return e.stack||String(e)}})()`, &failure); err != nil {
			t.Fatal(err)
		}
		if failure != "" {
			t.Fatal(failure)
		}
	}
	eval(`if(!window.fixtureReady)throw Error('scripts not ready');
function check(ok,msg){if(!ok)throw Error(msg);}
function emit(e){handleEvent(e,null);}
// Neither steering nor a rejected continuation creates a new invocation.
var ownershipFailures=[];
['steer','continue'].forEach(function(mode){
  toolRows={};workerRows={};openToolOrder=[];resetWorkerOverview();
  emit({type:'tool_call',name:'task',args:'{"agent":"code","prompt":"active"}',id:'first'});emit(` + first + `);
  var active=toolRows.first;
  function owns(label){if(workerRows['worker-1']!==active||workerOverview['worker-1'].row!==active)ownershipFailures.push(mode+': '+label+' stole active ownership');}
  emit({type:'tool_call',name:'send_message',args:JSON.stringify({to:'worker-1',mode:mode,message:'correction'}),id:'instruction'});
  owns('dispatch');
  emit({type:'tool_result',id:'instruction',output:mode==='steer'?'correction queued':'',err:mode==='continue'?'worker is already running':''});
  owns('result');
  emit(Object.assign(` + call + `,{run:1,parent_call_id:'first',call_id:'active-read'}));
  if(!workerOverview['worker-1'].activity.includes('current.txt'))ownershipFailures.push(mode+': original progress ignored');
  emit(` + stale + `);
  if(workerOverview['worker-1'].status!=='done')ownershipFailures.push(mode+': original finish ignored');
  emit(` + notification + `);owns('notification');
  if(!active.textContent.includes('HISTORICAL_REPORT'))ownershipFailures.push(mode+': original report lost');
  Object.values(toolRows).forEach(r=>clearInterval(r._clock));
});
check(!ownershipFailures.length,ownershipFailures.join('; '));
toolRows={};workerRows={};openToolOrder=[];resetWorkerOverview();
emit({type:'tool_call',name:'task',args:'{"agent":"code","prompt":"old"}',id:'first'});emit(` + first + `);
emit({type:'tool_call',name:'send_message',args:'{"to":"worker-1","mode":"continue","message":"new"}',id:'second'});
check(workerRows['worker-1']===toolRows.first,'pending continuation stole ownership');
emit(` + second + `);check(toolRows.second._previousTaskRow===toolRows.first,'continuation backlink lost');emit(` + call + `);
var live=toolRows.second;var old=toolRows.first;
function liveIntact(){check(workerRows['worker-1']===live,'latest row regressed');check(workerOverview['worker-1'].status==='running','overview stopped');check(workerOverview['worker-1'].row===live,'overview row regressed');check(live._workerModel==='new-model'&&live._workerEffort==='high','model/effort regressed');check(live._workerCalls.read2.classList.contains('running'),'current tool stopped');check(workerOverview['worker-1'].activity.includes('current.txt'),'current activity replaced');}
liveIntact();emit(` + stale + `);liveIntact();emit(Object.assign(` + stale + `,{parent_call_id:''}));liveIntact();emit(Object.assign(` + stale + `,{kind:'started'}));liveIntact();
emit({type:'worker_progress',id:'worker-1',kind:'finished',status:'done'});liveIntact();
emit(` + notification + `);liveIntact();emit(` + legacyNotification + `);liveIntact();emit(` + result + `);liveIntact();
check(old.textContent.includes('HISTORICAL_REPORT'),'historical report lost from old row');check(!live.textContent.includes('HISTORICAL_REPORT'),'old report painted on current row');
emit(` + finish + `);check(workerOverview['worker-1'].status==='done','current finish ignored');
emit(Object.assign(` + notification + `,{run:2,text:` + notification + `.text.replace('old-model','new-model').replace('low','high')}));check(workerOverview['worker-1'].status==='done','current report ignored');
// A new root can reuse worker-1/run 1 without a transcript reset.
emit({type:'tool_call',name:'task',args:'{"agent":"code","prompt":"fresh root"}',id:'fresh-root'});
emit(Object.assign(` + first + `,{parent_call_id:'fresh-root'}));check(workerRows['worker-1']===toolRows['fresh-root']&&workerOverview['worker-1'].status==='running','new root run 1 rejected');
// Legacy initial starts and notes still work when there is no known ordinal.
emit({type:'tool_call',name:'task',args:'{"agent":"code","prompt":"legacy"}',id:'legacy'});
emit({type:'worker_progress',id:'worker-3',name:'code',kind:'started',parent_call_id:'legacy'});
emit(Object.assign(` + legacyNotification + `,{id:'worker-3',text:` + legacyNotification + `.text.replace('worker-1','worker-3')}));check(workerOverview['worker-3'].status==='done','legacy completion rejected');
// Reset boundaries permit fresh worker-1/run 1 and legacy initial notes.
toolRows={};workerRows={};openToolOrder=[];resetWorkerOverview();
emit(` + legacyNotification + `);check(stream.textContent.includes('HISTORICAL_REPORT'),'legacy initial report lost');
emit({type:'tool_call',name:'task',args:'{"agent":"code","prompt":"fresh"}',id:'fresh'});
emit(Object.assign(` + first + `,{parent_call_id:'fresh'}));check(workerOverview['worker-1'].status==='running','fresh reset rejected');
Object.values(toolRows).forEach(r=>clearInterval(r._clock));`)
}

func TestWorkerNotificationInvocationWire(t *testing.T) {
	for _, run := range []int{0, 1, 2} {
		w, ok := toWireEvent(agent.WorkerNotificationEvent{TaskID: "worker-1", Status: "done", Run: run})
		if !ok || w.Run != run {
			t.Fatalf("notification ordinal lost: %+v", w)
		}
		b, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(b, &decoded); err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			if _, exists := decoded["run"]; exists {
				t.Fatal("legacy zero must be omitted")
			}
		} else if decoded["run"] != float64(run) {
			t.Fatalf("wire ordinal lost: %s", b)
		}
	}
}
