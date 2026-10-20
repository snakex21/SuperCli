package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/stats"
	"supercli/internal/testbrowser"
)

func TestWorkerContextStatsContextOnly(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	appendStatsMessage(t, store, sess.ID, llm.Message{Role: llm.RoleAssistant, Content: "not telemetry content"})
	first := stats.WorkerContext{AddedMessages: 2, AddedBytes: 31}
	second := stats.WorkerContext{Requests: 3, MessageExposures: 6, ContextBytes: 93}
	rows := []session.TurnSummary{{ToolDiag: session.TurnToolDiag{WorkerContext: &first}}, {ToolDiag: session.TurnToolDiag{WorkerContext: &second}}, {}}
	view := summarizeTelemetry(rows, statsTokensView{})
	want := first
	want.Add(second)
	if view.WorkerContext == nil || *view.WorkerContext != want || view.Samples != 0 || view.DurationMS != 0 || view.Bottleneck != "" {
		t.Fatalf("context-only summary: %+v", view)
	}
	view.WorkerContext.AddedBytes = 999
	if first.AddedBytes != 31 {
		t.Fatal("summary aliases row")
	}
	for _, scope := range []string{"legacy", "zero"} {
		var diag session.TurnToolDiag
		if scope == "zero" {
			diag.WorkerContext = &stats.WorkerContext{}
		}
		out := summarizeTelemetry([]session.TurnSummary{{ToolDiag: diag}}, statsTokensView{})
		data, err := json.Marshal(out)
		if err != nil || strings.Contains(string(data), "worker_context") {
			t.Fatalf("%s optional field: %s %v", scope, data, err)
		}
	}
	if err := store.AppendTurnSummary(context.Background(), session.TurnSummary{SessionID: sess.ID, ToolDiag: session.TurnToolDiag{WorkerContext: &want}}); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	NewServer(eng, false).handleStats(recorder, httptest.NewRequest(http.MethodGet, "/api/stats?telemetry=1&session="+sess.ID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("API: %d %s", recorder.Code, recorder.Body.String())
	}
	var response statsView
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Telemetry.WorkerContext == nil || *response.Telemetry.WorkerContext != want || response.Telemetry.Samples != 0 {
		t.Fatalf("API context-only: %+v", response.Telemetry)
	}
	if strings.Contains(recorder.Body.String(), "not telemetry content") {
		t.Fatal("API leaked body")
	}
}

func TestWorkerContextStatsBrowserENPL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	catalogs := map[string]json.RawMessage{}
	for _, lang := range []string{"en", "pl"} {
		data, err := assetsFS.ReadFile("assets/locales/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		catalogs[lang] = data
	}
	catalogJSON, err := json.Marshal(catalogs)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><body><script>var catalogs=%s;var lang='en',sections={},activeModelID='';function registerLanguageRefresh(){};function t(k){return catalogs[lang][k]||k;}function el(tag,cls,text){var n=document.createElement(tag);n.className=cls||'';if(text!==undefined)n.textContent=text;return n;}function i18nEl(tag,cls,key){return el(tag,cls,t(key));}function fmtInteger(n){return String(n);}</script><script src="/07-stats.js"></script><script src="/11g-panel-goal-usage.js"></script></body>`, catalogJSON)
		default:
			data, err := assetsFS.ReadFile("assets/js" + r.URL.Path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
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
	var failure string
	script := `(()=>{try{
 function check(ok,msg){if(!ok)throw Error(msg);}
 [undefined,null,{}, {added_messages:0,added_bytes:0,requests:0,message_exposures:0,context_bytes:0}, {added_messages:-5,requests:'bad'}].forEach(function(w){
  var normalized=normalizeStats({telemetry:{worker_context:w}}).telemetry;
  check(normalized.workerContext===null,'absent/zero normalization');
  check(!renderPerformanceTelemetry(normalized).querySelector('.telemetry-worker-context'),'absent/zero shown');
 });
 ['en','pl'].forEach(function(code){lang=code;
  var raw={added_messages:2,added_bytes:31,requests:3,message_exposures:6,context_bytes:93,body:'SECRET WORKER BODY',content:'SECRET WORKER CONTENT'};
  var normalized=normalizeStats({telemetry:{worker_context:raw}}).telemetry;
  check(Object.keys(normalized.workerContext).length===5,'unexpected nonnumeric fields');
  var section=renderPerformanceTelemetry(normalized);document.body.appendChild(section);
  var worker=section.querySelector('.telemetry-worker-context');check(worker,'context-only hidden');
  check(worker.querySelector('h4').textContent===catalogs[code]['telemetry.workerContext.title'],'localized title');
  var values=Array.from(worker.querySelectorAll('dd')).map(function(n){return n.textContent;});
  check(values.join('|')==='2|31|3|6|93','numeric facts: '+values.join('|'));
  check(!section.textContent.includes('SECRET'),'body exposed');
  check(worker.textContent.includes('UTF-8'),'missing byte explanation');
  check(!section.querySelector('.telemetry-split'),'invented phase timings');
  check(code==='pl'?worker.textContent.includes('ponowne przesłania'):worker.textContent.includes('retransmissions'),'missing retry explanation ('+code+'): '+worker.textContent);
 });return '';
 }catch(e){return e.stack||String(e);}})()`
	if err := page.Eval(ctx, script, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
}
