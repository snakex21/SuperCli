package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/storage/session"
	"supercli/internal/testbrowser"
)

func TestFullToolDetailsNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "full human tool details")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	workerHandle := "worker_ui_" + strings.Repeat("a", 32)
	mainHandle := "out_" + strings.Repeat("b", 32)
	taskHandle := "out_" + strings.Repeat("c", 32)
	taskPreview := `<task-notification><task-id>worker-3</task-id><agent>review</agent><status>done</status><result>Krótki podgląd</result></task-notification>`
	full := "FIRST\n" + strings.Repeat("observation\n", 100) + "MIDDLE_CANARY <script>window.injected=true</script>\n" + strings.Repeat("tail\n", 100) + "LAST"
	args := `{"file":"src/żółć.go","content":"pierwsza linia\ndruga linia"}`
	detail, _ := json.Marshal(agent.WorkerToolDetail{Args: args, Output: full, Error: "FULL_ERROR_CANARY"})
	for handle, value := range map[string]string{workerHandle: string(detail), mainHandle: full, taskHandle: strings.Replace(taskPreview, "Krótki podgląd", full, 1)} {
		if err = writer.SaveToolOutput(ctx, handle, value); err != nil {
			t.Fatal(err)
		}
	}
	pl, _ := assetsFS.ReadFile("assets/locales/pl.json")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><head><link rel="stylesheet" href="/app.css"></head><body><script>var fixtureCatalog=%s;</script><script src="/runtime.js"></script><script src="/00-helpers.js"></script><script src="/prelude.js"></script><script src="/04-transcript.js"></script><script src="/08-sessions.js"></script><script src="/ready.js"></script></body></html>`, pl)
		case "/runtime.js":
			b, _ := sharedUIFS.ReadFile("shared-ui/runtime.js")
			w.Write(b)
		case "/prelude.js":
			fmt.Fprint(w, queueEditBrowserPrelude+`
var preferences={},runToolCount=0;function registerLanguageRefresh(){}function toolDisclosureOpen(){return false;}
function thinkingDisclosureOpen(){return false;}function renderText(s){return escHtml(s);}
function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}
function renderSentAttachments(){}function sentAttachmentsFor(){return [];}
function userMessageDisplayText(s){return s;}function pauseWorkerStatusSync(){}
`)
		case "/ready.js":
			fmt.Fprintf(w, `activeSessionID=transcriptSessionID=%q;smartScroll=function(){};window.fixtureReady=true;`, sess.ID)
		case "/api/tool-output":
			requests.Add(1)
			srv.handleSavedToolOutput(w, r)
		case "/app.css":
			w.Header().Set("Content-Type", "text/css")
			b, _ := assetsFS.ReadFile("assets/app.css")
			w.Write(b)
		default:
			b, err := assetsFS.ReadFile("assets/js" + r.URL.Path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1050, Height: 950})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	eval := func(script string) {
		t.Helper()
		var failure string
		if err := page.Eval(ctx, script, &failure); err != nil {
			t.Fatal(err)
		}
		if failure != "" {
			t.Fatal(failure)
		}
	}
	eval(fmt.Sprintf(`(()=>{try{
function check(ok,why){if(!ok)throw Error(why);}check(window.fixtureReady,'fixture scripts');
var log={version:1,id:'worker-1',run:1,calls:[{id:'read',tool:'read_lines',args:'{"file":"preview"}',output:'FIRST … LAST',completed:true,partial:true,detail_handle:%q}]};
window.workerCard=addHistoryTask({id:'worker-1',agent:'review',status:'done',result:'Raport.'},'Pierwszy akapit.\n\nDrugi akapit.',log);
workerCard.open=true;workerCard.dispatchEvent(new Event('toggle'));
window.workerDetail=workerCard.querySelector('.activity-result');check(workerDetail&&!workerDetail.open,'activity initially compact');
check(!workerCard.textContent.includes('MIDDLE_CANARY'),'large detail rendered eagerly');
check(getComputedStyle(workerCard.querySelector('.task-brief')).whiteSpace==='normal','task brief lost original compact layout');
window.messages=[{seq:1,role:'tool',name:'read_lines',tool_call_id:'main-read',content:'preview',tool_output_handle:%q}];
stream.appendChild(buildHistoryFragment(messages,null,false));window.mainRow=stream.lastElementChild;
check(!mainRow.open&&!mainRow.textContent.includes('MIDDLE_CANARY'),'history initially compact');return '';
}catch(e){return e.stack||String(e);}})()`, workerHandle, mainHandle))
	eval(fmt.Sprintf(`(()=>{try{
stream.appendChild(buildHistoryFragment([{seq:2,role:'tool',name:'task',tool_call_id:'task-report',content:%q,tool_output_handle:%q}],null,false));
window.fullTaskRow=stream.lastElementChild;
if(fullTaskRow.open)throw Error('full task report opened eagerly');return '';
}catch(e){return e.stack||String(e);}})()`, taskPreview, taskHandle))
	if requests.Load() != 0 {
		t.Fatal("folded details requested full payloads")
	}
	eval(`(async()=>{try{
workerDetail.open=true;workerDetail.dispatchEvent(new Event('toggle'));await workerDetail._workerDetailRequest;
if(!workerDetail.querySelector('.activity-output').textContent.includes('MIDDLE_CANARY'))throw Error('worker output is still clipped');
if(!workerDetail.querySelector('.activity-args').textContent.includes('druga linia'))throw Error('full arguments missing');
if(!workerDetail.textContent.includes('FULL_ERROR_CANARY'))throw Error('error body lost alongside tool output');
if(workerDetail.querySelector('.task-activity-note'))throw Error('complete output still marked partial');
mainRow.open=true;mainRow.dispatchEvent(new Event('toggle'));await mainRow._fullOutputRequest;
if(!mainRow.textContent.includes('MIDDLE_CANARY'))throw Error('history output is still clipped');
fullTaskRow.open=true;fullTaskRow.dispatchEvent(new Event('toggle'));await fullTaskRow._fullOutputRequest;
if(!fullTaskRow.querySelector('.task-report').textContent.includes('MIDDLE_CANARY'))throw Error('task report is still clipped');
if(window.injected||stream.querySelector('script'))throw Error('output executed as HTML');
workerDetail.open=false;workerDetail.dispatchEvent(new Event('toggle'));workerDetail.open=true;workerDetail.dispatchEvent(new Event('toggle'));
return '';}catch(e){return e.stack||String(e);}})()`)
	if requests.Load() != 3 {
		t.Fatalf("full details should be read once each, got %d", requests.Load())
	}
	// Live events use the same complete-detail reader as restored history.
	eval(fmt.Sprintf(`(async()=>{try{
addToolCall('task','{"agent":"review","prompt":"Sprawdź projekt"}','live');
addWorkerProgress({id:'worker-2',name:'review',kind:'started',parent_call_id:'live',run:1});
addWorkerProgress({id:'worker-2',name:'review',kind:'tool_call',parent_call_id:'live',run:1,call_id:'read',tool:'read_lines',args:'{}'});
addWorkerProgress({id:'worker-2',name:'review',kind:'tool_result',parent_call_id:'live',run:1,call_id:'read',tool:'read_lines',output:'preview',detail_handle:%q,partial:true});
var row=toolRows.live;row.open=true;row.dispatchEvent(new Event('toggle'));var detail=row.querySelector('.activity-result');detail.open=true;detail.dispatchEvent(new Event('toggle'));await detail._workerDetailRequest;
if(!detail.textContent.includes('MIDDLE_CANARY'))throw Error('live detail clipped');clearInterval(row._clock);return '';
}catch(e){return e.stack||String(e);}})()`, workerHandle))
}
