package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/testbrowser"
)

// This provider drives the real task tool, background worker and filesystem
// tools. It does not contact a model or ask one to describe its tool activity.
type workerAsyncUIProvider struct{ file string }

func (*workerAsyncUIProvider) Name() string { return "worker-ui-fixture" }
func (p *workerAsyncUIProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	child, reads, delegated := false, 0, false
	for _, m := range messages {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "WORKER_UI_ACTIVITY_FIXTURE") {
			child = true
		}
		if m.Role == llm.RoleTool && m.Name == "read_lines" {
			reads++
		}
		for _, call := range m.ToolCalls {
			delegated = delegated || call.Name == "task"
		}
	}
	out := make(chan llm.Delta, 2)
	var call *llm.ToolCall
	if child && reads < 3 {
		args, _ := json.Marshal(map[string]any{"file": p.file, "from": reads + 1, "to": reads + 1})
		call = &llm.ToolCall{ID: fmt.Sprintf("fixture-read-%d", reads+1), Name: "read_lines", Arguments: string(args)}
	} else if !child && !delegated {
		call = &llm.ToolCall{ID: "fixture-delegate", Name: "task", Arguments: `{"agent":"explore","async":true,"prompt":"WORKER_UI_ACTIVITY_FIXTURE: read three fixture lines, then finish."}`}
	}
	if call != nil {
		out <- llm.Delta{ToolCall: call, FinishReason: "tool_calls"}
	} else {
		out <- llm.Delta{Content: "Read-only fixture complete.", FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

func TestWorkerAsyncUIActualRunAndHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	file := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(file, []byte("package fixture\n// second line\n// third line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	eng.prov = &workerAsyncUIProvider{file: file}
	var events []wireEvent
	var sid string
	if err = eng.runStream(ctx, "implement a short delegated fixture inspection", "", "", func(ev wireEvent) {
		if ev.Type == "session" {
			sid = ev.SessionID
		}
		switch ev.Type {
		case "tool_call", "tool_result", "worker_progress", "worker":
			events = append(events, ev)
		}
	}); err != nil {
		t.Fatal(err)
	}
	var calls, results int
	for _, ev := range events {
		if ev.Type != "worker_progress" {
			continue
		}
		if ev.ParentCallID != "fixture-delegate" {
			t.Errorf("background %s lost its originating task call: %+v", ev.Kind, ev)
		}
		if ev.Kind == "tool_call" {
			calls++
		}
		if ev.Kind == "tool_result" {
			results++
		}
	}
	if calls != 3 || results != 3 {
		t.Fatalf("real run has calls=%d results=%d; events=%+v", calls, results, events)
	}
	messages, err := eng.transcript(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	liveJSON, _ := json.Marshal(events)
	historyJSON, _ := json.Marshal(messages)
	catalog, _ := assetsFS.ReadFile("assets/locales/pl.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/app.css"></head><body><script>var fixtureCatalog=%s;</script><script src="/runtime.js"></script><script src="/00-helpers.js"></script><script src="/prelude.js"></script><script src="/04-transcript.js"></script><script src="/05-chat.js"></script><script src="/08-sessions.js"></script><script src="/ready.js"></script></body></html>`, catalog)
		case "/runtime.js":
			b, _ := sharedUIFS.ReadFile("shared-ui/runtime.js")
			_, _ = w.Write(b)
		case "/prelude.js":
			fmt.Fprint(w, queueEditBrowserPrelude+`
var preferences={};
function registerLanguageRefresh(){}function toolDisclosureOpen(){return true;}
function thinkingDisclosureOpen(){return false;}function renderText(s){return s;}
function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}
function renderSentAttachments(){}function sentAttachmentsFor(){return [];}
function userMessageDisplayText(s){return s;}
`)
		case "/ready.js":
			fmt.Fprint(w, `toolDisclosureOpen=function(){return true;};smartScroll=function(){};loadSessions=function(){};activeSessionID=transcriptSessionID='fixture';window.fixtureReady=true;`)
		case "/app.css":
			b, _ := assetsFS.ReadFile("assets/app.css")
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write(b)
		default:
			b, err := assetsFS.ReadFile("assets/js" + r.URL.Path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1100, Height: 950})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var failure string
	script := `(()=>{try{
function check(ok,why){if(!ok)throw Error(why);}
function reset(){Object.values(toolRows).forEach(function(r){clearInterval(r._clock);});stream.innerHTML='';toolRows={};workerRows={};openToolOrder=[];resetWorkerOverview();}
var events=` + string(liveJSON) + `,messages=` + string(historyJSON) + `;
function verify(label){var rows=stream.querySelectorAll('.task-row');check(rows.length===1,label+': duplicate delegation cards');var row=rows[0];row.open=true;row.dispatchEvent(new Event('toggle'));check(row.querySelectorAll('.task-activity-item').length===3,label+': missing individual calls '+JSON.stringify({count:row.querySelectorAll('.task-activity-item').length,saved:row._savedWorkerActivity,body:row.textContent.slice(0,450)}));check(row.querySelectorAll('.activity-result').length===3,label+': missing expandable results');check(row.textContent.includes('Read-only fixture complete.'),label+': final report missing');check(!row.textContent.includes('<task-notification>')&&!row.textContent.includes('Results arrive automatically'),label+': model protocol leaked into UI');check(!row.textContent.includes(t('task.activitySummaryOnly')),label+': names-only fallback');return row;}
reset();events.forEach(function(ev){handleEvent(ev,null);});verify('actual live order');
reset();var ack=events.find(function(ev){return ev.type==='tool_result'&&ev.id==='fixture-delegate';});check(ack&&ack.output.includes('<continuation>'),'fixture did not use actual background acknowledgement');
events.filter(function(ev){return ev.type==='tool_call';}).forEach(function(ev){handleEvent(ev,null);});handleEvent(ack,null);
var running=toolRows['fixture-delegate'];check(running._taskID==='worker-1'&&running._taskStatus==='running','acknowledgement did not bind the original running card');
check(running.querySelector('.task-activity-empty').textContent===t('task.activityWaiting'),'running acknowledgement claims activity is unavailable');
check(running._clock!==null&&running._stat.textContent.indexOf(t('tool.running')+' · ')===0,'background acknowledgement froze the worker clock');
events.filter(function(ev){return ev.type==='worker_progress'||ev.type==='worker';}).forEach(function(ev){handleEvent(ev,null);});check(verify('ack before progress')===running,'completion replaced original card');
check(running._clock===null,'finished worker kept ticking');
reset();events.filter(function(ev){return ev.type==='tool_call'||ev.type==='tool_result';}).forEach(function(ev){handleEvent(ev,null);});
addToolCall('task',JSON.stringify({agent:'explore',prompt:'Unrelated parallel task'}),'unrelated');
events.filter(function(ev){return ev.type==='worker_progress'||ev.type==='worker';}).forEach(function(ev){handleEvent(ev,null);});
check(toolRows['fixture-delegate'].querySelectorAll('.task-activity-item').length===3&&!toolRows.unrelated.querySelector('.task-activity-item'),'parallel task captured another worker activity');
clearInterval(toolRows.unrelated._clock);toolRows.unrelated.remove();verify('parallel same-role delegation');
reset();var opened=false;events.filter(function(ev){return ev.call_id!=='fixture-read-2';}).forEach(function(ev){handleEvent(ev,null);if(ev.type==='worker_progress'&&ev.kind==='tool_result'&&ev.call_id==='fixture-read-1'){var d=toolRows['fixture-delegate'].querySelector('.activity-result');d.open=true;opened=true;}});
var repaired=verify('partial progress recovery');check(opened&&repaired.querySelector('.activity-result').open,'repair collapsed an expanded existing result');
reset();events.filter(function(ev){return ev.type==='tool_call'||ev.type==='tool_result'||ev.type==='worker';}).forEach(function(ev){handleEvent(ev,null);});verify('missed progress recovery');
reset();stream.appendChild(buildHistoryFragment(messages,null,false));verify('canonical history');
reset();var finalIndex=messages.findIndex(function(m){return m.worker_activity&&m.worker_activity.calls.length===3;});check(finalIndex>0,'fixture did not persist final activity');
var pager=historyPager();stream.appendChild(buildHistoryFragment(messages.slice(finalIndex),pager,false));var restored=verify('newer history page');
stream.insertBefore(buildHistoryFragment(messages.slice(0,finalIndex),pager,true),stream.firstChild);check(verify('older acknowledgement page')===restored,'older page replaced the existing report card');
reset();var conflict=JSON.parse(JSON.stringify(messages));conflict[finalIndex].worker_activity.parent_call_id='different-task-call';stream.appendChild(buildHistoryFragment(conflict,null,false));check(stream.querySelectorAll('.task-row').length===2,'known foreign invocation was merged by worker ID alone');
reset();handleEvent(events.find(function(ev){return ev.type==='tool_call';}),null);handleEvent({type:'worker_progress',kind:'started',id:'worker-1',name:'explore',run:2,parent_call_id:'fixture-delegate'},null);var current=toolRows['fixture-delegate'];handleEvent(events.find(function(ev){return ev.type==='worker';}),null);check(workerRows['worker-1']===current&&current._taskStatus==='running'&&!current._savedWorkerActivity,'old activity receipt overwrote a newer invocation');
reset();stream.appendChild(buildHistoryFragment(messages,null,false));verify('final display');
return '';
}catch(e){return e.stack||String(e);}finally{Object.values(toolRows).forEach(function(r){clearInterval(r._clock);});}})()`
	if err = page.Eval(ctx, script, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
	if stage := os.Getenv("SUPERCLI_WORKER_UI_STAGE"); stage != "" {
		shot, err := page.Screenshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(stage, "worker-async-activity.png"), shot, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
