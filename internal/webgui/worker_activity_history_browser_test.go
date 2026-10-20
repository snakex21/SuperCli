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
	"supercli/internal/storage/session"
	"supercli/internal/testbrowser"
)

func TestWorkerUIHistoryNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "worker activity history")
	if err != nil {
		t.Fatal(err)
	}
	note := strings.ReplaceAll(legacyWorkerNotice, "worker-5", "worker-1")
	activity := json.RawMessage(`{"version":1,"id":"worker-1","run":1,"omitted":2,"calls":[{"id":"read","tool":"read_lines","args":"{\"file\":\"src/żółć.go\"}","output":"VISIBLE_OUTPUT <script>window.workerUIInjected=true</script>","completed":true},{"id":"missing","tool":"list_dir","args":"{\"path\":\"missing\"}","error":"folder missing","completed":true},{"id":"interrupted","tool":"read_output","completed":false}]}`)
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Content: "Human request"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "delegate", Name: "task", Arguments: `{"agent":"explore","prompt":"Read fixture"}`}}},
		{Role: llm.RoleTool, Name: "task", ToolCallID: "delegate", Content: note, Origin: &llm.ContextOrigin{Source: llm.SourceTaskResult, WorkerActivity: activity}},
	} {
		if err = session.NewWriter(store, sess.ID).AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := srv.eng.transcript(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || len(messages[2].WorkerActivity) == 0 {
		t.Fatal("transcript dropped the persisted UI trail")
	}
	data, _ := json.Marshal(messages)
	en, _ := assetsFS.ReadFile("assets/locales/en.json")
	pl, _ := assetsFS.ReadFile("assets/locales/pl.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><head><link rel="stylesheet" href="/app.css"></head><body><script>var fixtureCatalog=%s;var I18N={en:%s,pl:%s};var UI_LANGUAGES=[{code:'en'},{code:'pl'}];</script><script src="/00-helpers.js"></script><script src="/prelude.js"></script><script src="/01-i18n.js"></script><script src="/04-transcript.js"></script><script src="/08-sessions.js"></script><script src="/ready.js"></script></body></html>`, pl, en, pl)
		case "/prelude.js":
			fmt.Fprint(w, queueEditBrowserPrelude+`
var superCliUI={fileMutationTools:{},mutationKind:function(){return '';}},preferences={};
function toolDisclosureOpen(){return false;}function thinkingDisclosureOpen(){return false;}
function renderText(s){var n=document.createElement('div');n.textContent=s;return n.innerHTML;}
function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}
function renderSentAttachments(){}function sentAttachmentsFor(){return [];}
function userMessageDisplayText(s){return s;}function updateWorkerOverview(){}
`)
		case "/ready.js":
			fmt.Fprint(w, `smartScroll=function(){};ui.lang='pl';activeSessionID=transcriptSessionID='fixture';window.fixtureReady=true;`)
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
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1050, Height: 950})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	script := `(()=>{try{
function check(ok,why){if(!ok)throw Error(why);}
check(window.fixtureReady,'scripts missing');
var messages=` + string(data) + `;var before=JSON.stringify(messages);
stream.appendChild(buildHistoryFragment(messages,null,false));
var row=stream.querySelector('.task-row');check(row,'worker card missing');
check(!row.querySelector('.task-activity-item'),'saved activity rendered eagerly');
row.open=true;row.dispatchEvent(new Event('toggle'));
var calls=Array.from(row.querySelectorAll('.task-activity-item'));
check(calls.length===3,'individual calls not restored');
check(calls[0].querySelector('.activity-name').textContent===t('tool.read_lines'),'existing tool translation ignored');
check(calls[0].querySelector('.activity-args').textContent.includes('src/żółć.go'),'arguments lost');
check(calls[0].querySelector('.activity-output').textContent.includes('<script>'),'result changed');
check(!row.querySelector('.task-activity script')&&!window.workerUIInjected,'tool output executed as HTML');
check(calls[0].classList.contains('done')&&calls[1].classList.contains('failed'),'outcomes lost');
check(calls[2].querySelector('.activity-status').textContent==='—'&&calls[2].querySelector('.activity-status').title===t('task.statusUnconfirmed'),'interrupted call falsely completed');
check(row.querySelector('.task-activity-note').textContent===t('task.activityPartial'),'omission not disclosed');
check(!row.textContent.includes(t('task.activitySummaryOnly')),'new log fell back to names only');
var detail=calls[0].querySelector('details');detail.open=true;
ui.lang='en';applyI18n();
check(row._tname.textContent.includes('Worker '),'worker title lost original Worker label');
check(calls[0].querySelector('.activity-name').textContent===I18N.en['tool.read_lines'],'language switch left stale tool names');
check(detail.open&&calls[0].querySelector('.activity-output').textContent.includes('VISIBLE_OUTPUT'),'translation discarded expanded tool data');
ui.lang='pl';applyI18n();
var old=addHistoryTask({id:'worker-2',agent:'explore',status:'done',summary:'15 steps · 100 in/20 out tok',tools:'list_dir×4, read_many×4',result:'Old report'});
old.open=true;old.dispatchEvent(new Event('toggle'));
check(old.querySelector('.activity-name').textContent===t('tool.list_dir')+'×4','legacy counted names untranslated');
check(old.textContent.includes(t('task.activitySummaryOnly')),'legacy missing details were fabricated');
var wrong=addHistoryTask({id:'worker-3',agent:'explore',status:'done',summary:'1 step',tools:'read_lines',result:'Another report'},'',messages[2].worker_activity);
wrong.open=true;wrong.dispatchEvent(new Event('toggle'));
check(!wrong.querySelector('.activity-output'),'another worker inherited this tool trail');
check(JSON.stringify(messages)===before,'renderer modified canonical data');
check(stream.querySelectorAll('.msg-user').length===1,'worker tools became user messages');
return '';
}catch(e){return e.stack||String(e);}})()`
	var failure string
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
		if err = os.WriteFile(filepath.Join(stage, "worker-activity.png"), shot, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkerUITranscriptRejectsUnownedMetadata(t *testing.T) {
	data := json.RawMessage(`{"version":1,"id":"worker-1","run":1,"calls":[]}`)
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Origin: &llm.ContextOrigin{Source: llm.SourceHuman, WorkerActivity: data}},
		{Role: llm.RoleTool, Name: "read_lines", Origin: &llm.ContextOrigin{Source: llm.SourceTaskResult, WorkerActivity: data}},
		{Role: llm.RoleTool, Name: "task", Origin: &llm.ContextOrigin{Source: llm.SourceHuman, WorkerActivity: data}},
	} {
		if len(transcriptWorkerActivity(m)) != 0 {
			t.Fatal("unowned metadata became a worker tool log")
		}
	}
}
