package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// UI-only baseline: production shell, renderer, scrolling and session navigation;
// synthetic pages generated on demand, no Engine, database, provider or real data.
// Timing logs are operation -> settled animation frames, NOT presented pixels.
func TestLongHistoryBaselineNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	index, err := assetsFS.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	en, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests []string
	var unexpected []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			shell := string(index)
			shell = shell[:strings.Index(shell, "  <script src=")]
			fmt.Fprint(w, shell, `<script>window.fixtureErrors=[];window.addEventListener('error',e=>fixtureErrors.push(e.message));window.addEventListener('unhandledrejection',e=>fixtureErrors.push(String(e.reason)));</script><script src="/runtime.js"></script><script src="/js/00-helpers.js"></script><script src="/catalog.js"></script><script src="/js/01-i18n.js"></script><script src="/js/02-ui-settings.js"></script><script src="/js/03-markdown.js"></script><script src="/js/03b-highlight.js"></script><script src="/js/04-transcript.js"></script><script src="/js/05-chat.js"></script><script src="/js/08-sessions.js"></script><script src="/fixture.js"></script></body></html>`)
		case "/runtime.js":
			w.Header().Set("Content-Type", "text/javascript")
			b, _ := sharedUIFS.ReadFile("shared-ui/runtime.js")
			w.Write(b)
		case "/catalog.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprintf(w, "var I18N={en:%s};var UI_LANGUAGES=[{code:'en',name:'English'}];", en)
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, longHistoryBrowserFixture)
		case "/api/settings":
			// Disclosure toggles save UI preferences in production. Discard the
			// synthetic write here: no config file or application server exists.
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		case "/api/sessions":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"id":"long","first_user_msg":"Synthetic long","updated_at":"2026-10-18T12:00:00Z"},{"id":"short","first_user_msg":"Synthetic short","updated_at":"2026-10-18T12:00:00Z"}]`)
		case "/api/transcript":
			id := r.URL.Query().Get("id")
			total := 11520
			if id == "short" {
				total = 180
			} else if id != "long" {
				http.Error(w, "unknown synthetic session", 400)
				return
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			before, _ := strconv.Atoi(r.URL.Query().Get("before"))
			if limit != 60 {
				http.Error(w, "unbounded page", 400)
				return
			}
			end := total
			if before > 0 {
				end = before - 1
			}
			start := end - limit + 1
			if start < 1 {
				start = 1
			}
			messages := make([]map[string]any, 0, limit)
			for seq := start; seq <= end; seq++ {
				messages = append(messages, longHistoryMessage(seq))
			}
			mu.Lock()
			requests = append(requests, fmt.Sprintf("%s:%d-%d", id, start, end))
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"messages": messages, "has_more": start > 1, "before_seq": start})
		default:
			if strings.HasPrefix(r.URL.Path, "/api/") {
				mu.Lock()
				unexpected = append(unexpected, r.Method+" "+r.URL.Path)
				mu.Unlock()
				http.Error(w, "fixture forbids other API operations", 400)
				return
			}
			b, e := assetsFS.ReadFile("assets" + r.URL.Path)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			if strings.HasSuffix(r.URL.Path, ".js") {
				w.Header().Set("Content-Type", "text/javascript")
			}
			if strings.HasSuffix(r.URL.Path, ".css") {
				w.Header().Set("Content-Type", "text/css")
			}
			w.Write(b)
		}
	})
	server := httptest.NewServer(handler)
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
	// Screenshot activates ONLY this owned headless target before any rAF timing.
	if _, err := page.Screenshot(ctx); err != nil {
		t.Fatal(err)
	}
	var ready struct {
		Errors []string `json:"errors"`
		Ready  bool     `json:"ready"`
	}
	if err := page.Eval(ctx, `({errors:fixtureErrors,ready:typeof fixtureCycle==='function'})`, &ready); err != nil {
		t.Fatal(err)
	}
	if !ready.Ready || len(ready.Errors) > 0 {
		t.Fatalf("fixture setup: %+v", ready)
	}
	sample := func(label string) testbrowser.MemoryMetrics {
		t.Helper()
		pre, err := page.MemoryMetrics(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := page.CollectGarbage(ctx); err != nil {
			t.Fatal(err)
		}
		post, err := page.MemoryMetrics(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var dom json.RawMessage
		if err := page.Eval(ctx, `fixtureState()`, &dom); err != nil {
			t.Fatal(err)
		}
		t.Logf("memory %s beforeGC=%+v afterGC=%+v state=%s", label, pre, post, dom)
		return post
	}
	sample("empty")
	t.Log("dataset long=11520 records/192 pages/26219520 body bytes (25.005 MiB); short=180 records/3 pages/409680 body bytes; page=60, initial last=60, loaded=180 after 2 older pages; cold=1 warmups=2 measured=6 PER dataset; same body distribution")
	for _, id := range []string{"short", "long"} {
		if err := page.Navigate(ctx, server.URL); err != nil {
			t.Fatal(err)
		}
		if _, err := page.Screenshot(ctx); err != nil {
			t.Fatal(err)
		}
		sample(id + "/fresh-document")
		var measuredHeap []float64
		var measuredNodes, measuredDocuments, measuredListeners []int
		var measuredTimings []map[string]float64
		var measuredTailReady []map[string]any
		for cycle := -3; cycle < 6; cycle++ {
			phase := "measured"
			if cycle == -3 {
				phase = "cold"
			} else if cycle < 0 {
				phase = "warmup"
			}
			var result json.RawMessage
			if err := page.Eval(ctx, fmt.Sprintf(`fixtureCycle(%q)`, id), &result); err != nil {
				t.Fatalf("%s %s %d: %v", id, phase, cycle, err)
			}
			t.Logf("cycle %s %s %d %s", id, phase, cycle, result)
			m := sample(fmt.Sprintf("%s/%s/%d", id, phase, cycle))
			if cycle >= 0 {
				var decoded struct {
					Timings   map[string]float64 `json:"timings"`
					TailReady map[string]any     `json:"tailReady"`
				}
				if err := json.Unmarshal(result, &decoded); err != nil {
					t.Fatal(err)
				}
				measuredHeap = append(measuredHeap, m.UsedSize)
				measuredNodes = append(measuredNodes, m.Nodes)
				measuredDocuments = append(measuredDocuments, m.Documents)
				measuredListeners = append(measuredListeners, m.JSEventListeners)
				measuredTimings = append(measuredTimings, decoded.Timings)
				measuredTailReady = append(measuredTailReady, decoded.TailReady)
			}
		}
		summary, _ := json.Marshal(map[string]any{"heapAfterGC": measuredHeap, "cdpNodes": measuredNodes, "documents": measuredDocuments, "listeners": measuredListeners, "timings": measuredTimings, "tailReady": measuredTailReady})
		t.Logf("MEASURED SUMMARY %s %s", id, summary)
		var issues json.RawMessage
		if err := page.Eval(ctx, `fixtureIssues`, &issues); err != nil {
			t.Fatal(err)
		}
		t.Logf("always-on scroll correctness %s: %s", id, issues)
		if string(issues) != "[]" {
			t.Errorf("scroll correctness RED %s: %s", id, issues)
		}
	}
	var control json.RawMessage
	if err := page.Eval(ctx, `(async()=>{
	 async function capture(){await resumeSession('long');await fixtureSettle();return {top:stage.scrollTop,height:stage.scrollHeight,client:stage.clientHeight,gap:stage.scrollHeight-stage.scrollTop-stage.clientHeight};}
	 var baseline=await capture();var style=document.createElement('style');style.textContent='.stream > * {content-visibility:visible !important;}';document.head.appendChild(style);
	 var withoutCV=await capture();style.remove();return {baseline,withoutCV};
	})()`, &control); err != nil {
		t.Fatal(err)
	}
	t.Logf("tail isolation (test-only CSS counterfactual, NOT proposed blanket devirtualization): %s", control)
	if err := page.Eval(ctx, `(async()=>{newSession();await fixtureSettle();fixtureCheck(!activeSessionID&&!stream.children.length,'teardown');})()`, nil); err != nil {
		t.Fatal(err)
	}
	sample("teardown")
	var regressions json.RawMessage
	if err := page.Eval(ctx, longHistoryScrollRegressions, &regressions); err != nil {
		t.Fatal(err)
	}
	t.Logf("initial-tail native JS regressions: %s", regressions)
	mu.Lock()
	defer mu.Unlock()
	if len(unexpected) > 0 {
		t.Fatalf("unexpected API calls: %v", unexpected)
	}
	// Every complete cycle has 3 primary pages, 1 switch page and 1 back page.
	if len(requests) != 18*5+2 {
		t.Fatalf("page requests=%d, want 92: %v", len(requests), requests)
	}
	t.Logf("page requests=%d (5/cycle + 2 diagnostic; max 60/request; long initial=11461-11520, oldest loaded=11341; short oldest loaded=1); requests=%v", len(requests), requests)
}

func longHistoryMessage(seq int) map[string]any {
	m := map[string]any{"seq": seq, "message_id": strconv.Itoa(seq)}
	role, body := "assistant", ""
	callID := fmt.Sprintf("call-%d", seq-seq%8)
	switch seq % 8 {
	case 1:
		role = "user"
		body = fmt.Sprintf("Synthetic request %d. Review the bounded history fixture.\n", seq)
		m["attachments"] = []string{"synthetic/notes.txt"}
	case 2:
		body = "<think>Compare invariants and preserve anchors.</think>\n\n## Review\n\n**Synthetic** markdown with `inline code` and a [loopback link](/).\n\n- alpha\n- beta\n\n```go\nfunc example() int { return 42 }\n```\n\n"
	case 3:
		body = "<think>Inspect file output before concluding.</think>\n\nWorking on the synthetic file.\n"
		m["tool_calls"] = []map[string]string{{"id": callID, "name": "read_file", "arguments": "{\"path\":\"synthetic/notes.txt\"}"}}
	case 4:
		role = "tool"
		m["name"] = "read_file"
		m["tool_call_id"] = callID
		body = "synthetic/notes.txt\n1: alpha\n2: beta\n"
	case 5:
		role = "tool"
		m["name"] = "task"
		prefix := fmt.Sprintf("<task-notification><task-id>worker-%d</task-id><agent>code</agent><status>completed</status><summary>3 steps · 1 tool calls</summary><tools>read_file</tools><result>## Worker report\n\n", seq)
		suffix := "</result></task-notification>"
		// Fill inside the result; arbitrary trailing text is not a valid host
		// notification. Keep the same exact body size as the other records.
		payloadLen := 2276 - len(prefix) - len(suffix)
		body = prefix + strings.Repeat("report detail. ", (payloadLen+14)/15)[:payloadLen] + suffix
	case 6:
		body = "| Check | Result |\n| --- | --- |\n| Paging | bounded |\n| Anchor | retained |\n\n> Synthetic quotation.\n\n"
	case 7:
		role = "tool"
		m["name"] = "ctx_execute"
		m["tool_call_id"] = callID + "-exec"
		body = "{\"stdout\":\"synthetic check passed\",\"exit_code\":0,\"stderr\":\"\"}\n"
	default:
		body = "### Conclusion\n\nThe isolated fixture has no provider, database or user data.\n\n"
	}
	if len(body) < 2276 {
		padding := strings.Repeat("Synthetic bounded history detail with varied readable prose and measured layout.\n", 40)
		body += padding[:2276-len(body)]
	}
	m["role"], m["content"] = role, body
	return m
}

const longHistoryBrowserFixture = `
// Only peripheral panels/network startup are omitted. Rendering, composer,
// session navigation, lazy payloads and scrolling below are production code.
var activeWorkspacePath='';
ui.rememberSessionRuntime=false;
function renderStats(){} function renderAttachments(){} function updateProjectLabel(){}
var fixtureIssues=[];
function fixtureCheck(ok,message){if(!ok)throw new Error(message);}
function fixtureScrollCheck(ok,message){if(!ok)fixtureIssues.push(message);}
function fixtureState(){return {connectedNodes:(()=>{var w=document.createTreeWalker(document,NodeFilter.SHOW_ALL),n=0;while(w.nextNode())n++;return n;})(),elements:document.querySelectorAll('*').length,streamChildren:stream.children.length,rawWrappers:loadedTranscriptMessages.length,workers:Object.keys(workerRows).length,tools:Object.keys(toolRows).length,formatterCache:statsFormatterKeys.length,transcriptBeforeSeq:transcriptBeforeSeq,hasMore:transcriptHasMore,session:activeSessionID,errors:fixtureErrors};}
async function fixtureSettle(onFrame){
 var prev='',stable=0;
 for(var i=0;i<40;i++){await new Promise(requestAnimationFrame);var now=[stage.scrollTop,stage.scrollHeight,stream.children.length].join(':');stable=now===prev?stable+1:0;prev=now;if(onFrame)onFrame();if(i>=4&&stable>=3)return;}
 throw new Error('layout did not settle');
}
async function fixtureCycle(id){
 fixtureCheck(!document.hidden,'headless target not active');
 var timings={},tailReady={},other=id==='long'?'short':'long';
 async function timed(name,fn){
  var start=performance.now();await fn();var owner=initialTranscriptTail;
  await fixtureSettle(function(){
   if(['open','switch','back'].indexOf(name)>=0&&!tailReady[name]&&!initialTranscriptTail&&Math.abs(stage.scrollHeight-stage.clientHeight-stage.scrollTop)<=1)
    tailReady[name]={ms:+(performance.now()-start).toFixed(2),frames:owner?owner.frames:0};
  });
  timings[name]=+(performance.now()-start).toFixed(2);
 }
 await timed('open',async()=>fixtureCheck(await resumeSession(id),'resume failed'));
 fixtureCheck(activeSessionID===id&&document.activeElement===promptEl,'open focus/session');
 fixtureCheck(loadedTranscriptMessages.length===0,'raw page wrappers retained');
 fixtureCheck(!initialTranscriptTail,'initial tail settlement did not finish');
 fixtureCheck(getComputedStyle(stream.querySelector(':scope > .msg-assistant')).contentVisibility==='auto','virtualization disabled');
 var tail={top:stage.scrollTop,height:stage.scrollHeight,client:stage.clientHeight,gap:stage.scrollHeight-stage.scrollTop-stage.clientHeight,follow:transcriptFollowTail};
 fixtureScrollCheck(Math.abs(tail.gap)<=1,'initial '+id+' tail '+JSON.stringify(tail));
 var lastPage=fixtureState();
 fixtureCheck(transcriptBeforeSeq===(id==='long'?11461:121),'initial page bound');
 await timed('scroll',async()=>{stage.scrollTop=stage.scrollHeight/2;});
 var tool=stream.querySelector('.tool-row:not(.task-row)'),worker=stream.querySelector('.task-row');
 fixtureCheck(tool&&worker,'mixed workload missing');
 fixtureCheck(!tool._historyPayloadRendered&&!worker.querySelector('.task-report').textContent,'folded payload eagerly rendered');
 await timed('expand',async()=>{tool.open=true;worker.open=true;});
 fixtureCheck(tool._historyPayloadRendered&&worker.querySelector('.task-report').textContent.length>1000,'lazy payload did not render');
 var anchor=stream.querySelector('.msg-user');
 anchor.scrollIntoView({block:'start'});await fixtureSettle();
 var before=anchor.getBoundingClientRect().top;
 await timed('older1',loadOlderTranscript);
 fixtureCheck(!initialTranscriptTail,'older prepend started tail settlement');
 var drift=+(anchor.getBoundingClientRect().top-before).toFixed(2);
 fixtureCheck(anchor.isConnected,'prepend rebuilt old rows');
 fixtureScrollCheck(Math.abs(drift)<=4,'prepend '+id+' anchor drift '+drift);
 before=anchor.getBoundingClientRect().top;
 await timed('older2',loadOlderTranscript);
 fixtureCheck(!initialTranscriptTail,'second prepend started tail settlement');
 var drift2=+(anchor.getBoundingClientRect().top-before).toFixed(2);
 fixtureScrollCheck(Math.abs(drift2)<=4,'second prepend '+id+' anchor drift '+drift2);
 fixtureCheck(tool.isConnected&&tool.open&&worker.open,'prepend lost disclosures');
 fixtureCheck(transcriptBeforeSeq===(id==='long'?11341:1),'loaded page bound');
 fixtureCheck(transcriptHasMore===(id==='long'),'pager exhaustion');
 var loaded=fixtureState();
 await timed('switch',async()=>fixtureCheck(await resumeSession(other),'switch failed'));
 fixtureCheck(!anchor.isConnected&&document.activeElement===promptEl,'switch retained DOM/focus');
 fixtureCheck(Object.values(workerRows).every(n=>n.isConnected),'stale worker registry');
 await timed('back',async()=>fixtureCheck(await resumeSession(id),'back failed'));
 fixtureCheck(document.activeElement===promptEl,'back focus');
 fixtureScrollCheck(Math.abs(stage.scrollHeight-stage.scrollTop-stage.clientHeight)<=1,'back '+id+' tail gap '+(stage.scrollHeight-stage.scrollTop-stage.clientHeight));
 fixtureCheck(stream.children.length===lastPage.streamChildren,'switch/back accumulated rows');
 fixtureCheck(Object.values(workerRows).every(n=>n.isConnected),'stale worker registry after back');
 fixtureCheck(fixtureErrors.length===0,'browser errors: '+fixtureErrors.join(';'));
 return {timings,tailReady,tail,anchorDrift:[drift,drift2],lastPage,loaded,back:fixtureState()};
}
`

// Real production scrolling/rendering in Chromium. Only the adversarial guard
// cases use a local callback queue to invoke even cancelled rAFs deliberately;
// the performance workload and user-away/layout race use native animation frames.
const longHistoryScrollRegressions = `(async()=>{
 var passed=[], pending=[], nativeRAF=requestAnimationFrame, nativeCancel=cancelAnimationFrame;
 function check(ok,name){fixtureCheck(ok,name);passed.push(name);}
 function seed(){
  cancelInitialTranscriptTail();transcriptResumeScroll=null;
  activeSessionID=transcriptSessionID='scroll-regression';++sessionResumeSeq;
  transcriptHasMore=false;transcriptBeforeSeq=1;
  loadedTranscriptMessages=[{role:'user',content:'Synthetic anchor'},
   {role:'assistant',content:'## Synthetic layout\n\n'+('Readable bounded history text.\n'.repeat(100))},
   {role:'assistant',content:'### Tail\n\n'+('Tail prose.\n'.repeat(60))}];
  pending=[];renderLoadedTranscript(false);
 }
 function stillTop(callback,name){stage.scrollTop=0;callback(0);check(stage.scrollTop===0,name);}
 requestAnimationFrame=function(callback){pending.push(callback);return pending.length;};
 cancelAnimationFrame=function(){}; // Run cancelled callbacks too: fencing must win.
 try {
  for(var kind of ['wheel','touchstart','pointerdown','keydown']){
   seed();var stale=pending[0];
   var event=kind==='keydown'?new KeyboardEvent('keydown',{key:'PageUp',bubbles:true}):new Event(kind,{bubbles:true});
   stage.dispatchEvent(event);
   check(!initialTranscriptTail,kind+' cancels pending settlement');stillTop(stale,kind+' stale callback cannot pull tail');
  }
  seed();var stale=pending[0];document.body.dispatchEvent(new KeyboardEvent('keydown',{key:'Home',bubbles:true}));
  check(!initialTranscriptTail,'document scroll key cancels');stillTop(stale,'document key stale callback fenced');
  seed();promptEl.dispatchEvent(new KeyboardEvent('keydown',{key:'Home',bubbles:true}));
  check(!!initialTranscriptTail,'composer editing key is not document scroll intent');cancelInitialTranscriptTail();
  seed();var first=pending.shift();first(0);var stale=pending.shift();
  stage.dispatchEvent(new Event('wheel'));check(!initialTranscriptTail,'wheel after first reveal cancels');stillTop(stale,'wheel during pending layout keeps manual top');
  for(var scope of ['session','project','resume']){
   seed();var stale=pending[0];
   if(scope==='session')activeSessionID='another-session';
   if(scope==='project')++projectEpoch;
   if(scope==='resume')++sessionResumeSeq;
   stillTop(stale,scope+' stale scope cannot scroll');check(!initialTranscriptTail,scope+' releases pending state');
  }
  seed();var stale=pending[0];newSession();stale(0);
  check(!initialTranscriptTail&&!stream.children.length,'new session invalidates removed tail');
  seed();var stale=pending[0];seed();var current=initialTranscriptTail;
  stillTop(stale,'superseded rAF cannot scroll');check(initialTranscriptTail===current,'superseded rAF cannot cancel newer owner');
  seed();var stale=pending[0];loadOlderTranscript();
  check(!initialTranscriptTail,'older load cancels before request/guard');stillTop(stale,'older load stale rAF cannot scroll');
  seed();var stale=pending[0];stage.scrollTop=150;transcriptFollowTail=false;captureTranscriptScroll();var saved=transcriptResumeScroll;
  check(!initialTranscriptTail&&!saved.followTail,'scroll capture cancels initial owner');
  stage.scrollTop=saved.top;stale(0);check(stage.scrollTop===saved.top,'capture position wins over stale rAF');
  loadedTranscriptMessages=[{role:'assistant',content:'Restored manual history.\n'.repeat(150)}];
  renderLoadedTranscript(false);check(!initialTranscriptTail,'saved manual anchor suppresses initial tail');
  pending=[];resumeTranscriptScroll();settleTranscriptScroll(function(){return true;});
  for(var i=0;pending.length&&i<4;i++)pending.shift()(0);
  check(!transcriptResumeScroll&&Math.abs(stage.scrollTop-saved.top)<=1,'sync two-frame manual restore retains anchor');
  seed();var stale=pending[0];loadedTranscriptMessages=[{role:'assistant',content:'Preserved history.\n'.repeat(100)}];
  renderLoadedTranscript(true);var preserved=stage.scrollTop;stale(0);
  check(!initialTranscriptTail&&stage.scrollTop===preserved,'preserve-scroll render cancels initial tail');
  seed();var count=0;
  while(pending.length&&count<20){initialTranscriptTail.tail.style.minHeight=(1000+count*200)+'px';pending.shift()(0);count++;}
  check(count===16&&!initialTranscriptTail&&!pending.length,'changing layout has hard 16-frame bound');
 } finally {
  cancelInitialTranscriptTail();requestAnimationFrame=nativeRAF;cancelAnimationFrame=nativeCancel;
 }
 // Native frame race: the initial jump has happened, but revealed blocks have
 // not finished settling when the user wheels back to the top.
 seed();await new Promise(requestAnimationFrame);
 check(!!initialTranscriptTail,'native layout race actually has pending reveal');
 stage.dispatchEvent(new WheelEvent('wheel',{deltaY:-300,bubbles:true}));stage.scrollTop=0;
 await fixtureSettle();check(!initialTranscriptTail&&stage.scrollTop===0,'native user-away survives later layout frames');
 newSession();await fixtureSettle();
 check(!initialTranscriptTail&&!stream.children.length,'native teardown has no pending owner');
 check(fixtureErrors.length===0,'no native regression script errors');
 return {checks:passed,hardFrameCap:count};
})()`
