package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/testbrowser"
)

// Exercise the actual transcript DTO and production renderer in a native
// browser, not just a role-string assertion or a JavaScript DOM substitute.
func TestWorkerNotificationHistoryNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "worker history browser")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []llm.Message{
		{Role: llm.RoleUser, Content: "Human request"},
		{Role: llm.RoleUser, Content: legacyWorkerNotice},
		{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: legacyWorkerNotice},
	} {
		if err := session.NewWriter(store, sess.ID).AppendMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := srv.eng.transcript(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	en, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"></head><body><script>var fixtureCatalog=%s;</script><script src="/prelude.js"></script><script src="/04-transcript.js"></script><script src="/08-sessions.js"></script><script src="/ready.js"></script></body></html>`, en)
		case "/prelude.js":
			fmt.Fprint(w, queueEditBrowserPrelude+`
var superCliUI={fileMutationTools:{patch_file:true},mutationKind:function(){return '';}};var preferences={};
function registerLanguageRefresh(){} function toolDisclosureOpen(){return false;}
function thinkingDisclosureOpen(){return false;}function renderText(s){return s;}
function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}
function renderSentAttachments(){}function sentAttachmentsFor(){return [];}
function userMessageDisplayText(text){return text;}function updateWorkerOverview(){}
`)
		case "/ready.js":
			fmt.Fprint(w, `toolDisclosureOpen=function(){return false;};smartScroll=function(){};activeSessionID=transcriptSessionID='fixture';window.fixtureReady=true;`)
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
function check(ok,why){if(!ok)throw Error(why);}
check(window.fixtureReady,'scripts not ready');
var messages=` + string(data) + `;
var original=JSON.stringify(messages);
var fragment=buildHistoryFragment(messages,null,false);
var host=document.createElement('div');host.appendChild(fragment);document.body.appendChild(host);
check(host.querySelectorAll('.msg-user').length===1,'worker report rendered as human bubble');
check(host.querySelectorAll('.msg-rewind').length===1,'worker report became a rewind target');
var rows=Array.from(host.querySelectorAll('.task-row'));
check(rows.length===2,'legacy or newly marked worker report disappeared');
rows.forEach(function(row){
  check(!row.open,'worker report is not folded by default');
  check(!row.querySelector('.msg-rewind'),'worker card contains human rewind button');
  var report=row.querySelector('.task-report');
  check(report&&report.innerHTML==='','large report eagerly rendered while closed');
  row.open=true;row.dispatchEvent(new Event('toggle'));
  check(report.textContent.includes('Full report ą日本語'),'complete worker report unavailable after opening');
});
check(JSON.stringify(messages)===original,'rendering modified canonical DTO');

var valid=messages.find(function(m){return m.role==='tool'&&m.name==='task';}).content;
var caseID=0;
function historyCase(name,output,delegated,callName,resultName,args){
  var id='notification-case-'+(++caseID);
  var sample=[
    {role:'assistant',content:'',tool_calls:[{id:id,name:callName||name,arguments:args||'{}'}]},
    {role:'tool',name:resultName===undefined?name:resultName,tool_call_id:id,content:output}
  ];
  var before=JSON.stringify(sample);
  var fragment=buildHistoryFragment(sample,null,false);
  var tools=fragment.querySelectorAll('.tool-row');
  check(tools.length===1,name+' lost or duplicated its result');
  check(fragment.querySelectorAll('.task-row').length===(delegated?1:0),name+' misclassified a result: '+output.slice(0,80));
  if(!delegated)check(tools[0]._historyName===(callName||name),name+' lost the originating tool name');
  check(JSON.stringify(sample)===before,name+' changed the canonical result');
  return tools[0];
}
historyCase('read_many','SOURCE_START\nif (src.indexOf("<task-notification>") < 0) return null;\nSOURCE_END',false);
historyCase('search_code',valid,false);
historyCase('read_many','const quoted = '+JSON.stringify(valid)+';',false);
historyCase('ctx_execute',valid,false);
historyCase('read_many',valid,false,'read_many','task');
historyCase('task',valid,true,'task','');
historyCase('send_message',valid,true);

var invalid=[
  '<task-notification>',
  '<task-notification></task-notification>',
  valid.replace('</task-notification>',''),
  valid.replace(/<task-id>[\s\S]*?<\/task-id>/,''),
  valid.replace(/<task-id>[\s\S]*?<\/task-id>/,'<task-id>not-a-worker</task-id>'),
  valid.replace(/<agent>[\s\S]*?<\/agent>/,''),
  valid.replace(/<status>[\s\S]*?<\/status>/,''),
  valid.replace(/<status>[\s\S]*?<\/status>/,'<status>mystery</status>'),
  valid.replace('</result>',''),
  'Quoted report:\n'+valid,
  valid+'\nAdditional non-report text'
];
invalid.forEach(function(output,index){
  check(parseTaskNotification(output)===null,'malformed report '+index+' fabricated worker metadata');
  historyCase('task',output,false);
  historyCase('send_message',output,false);
});
var result='RAW_HTML <details>日本語</details>\n'+valid+'\n<status>failed</status>';
var nested=valid.slice(0,valid.indexOf('<result>'))+'<result>'+result+'</result></task-notification>';
var parsed=parseTaskNotification(nested);
check(parsed&&parsed.status==='done'&&parsed.result===result,'nested report text changed metadata or report content');
check(parseTaskNotification(valid+'\n[Attached: worker tool observations (historical snapshots).]\nread_many')!==null,'historical observations suffix rejected');
check(taskStatusLabel('')===t('task.statusUnconfirmed'),'missing status became done');
check(taskStatusLabel('mystery')===t('task.statusUnconfirmed'),'unknown status became done');
check(taskStatusLabel('created')===t('tool.running'),'created worker became done');

var absent=historyCase('task',valid,true);
check(absent.querySelector('.task-activity-empty').textContent===t('task.activityUnavailable'),'missing history activity was silently omitted');
var withTools=valid.replace('<summary>','<tools>tool_search×3, read_lines</tools><summary>');
var brief='Inspect the UI without changing data.';
var summarized=historyCase('task',withTools,true,'task','task',JSON.stringify({agent:'explore',prompt:brief}));
check(summarized.querySelector('.task-brief').textContent===brief,'history lost the originating task brief');
check(summarized.querySelectorAll('.task-activity-item').length===2,'recorded tool names were lost');
check(summarized.querySelector('.activity-name').textContent===toolDisplayName('tool_search')+'×3','translated tool label or recorded count was lost: '+summarized.querySelector('.activity-name').textContent+' expected '+toolDisplayName('tool_search')+'×3');
check(summarized.querySelectorAll('.task-activity-item')[1].classList.contains('tk-read'),'names-only history lost tool-type colours');
check(!summarized.querySelector('.task-activity-item.done'),'tool-name summary fabricated successful outcomes');
check(summarized.querySelector('.task-activity-note').textContent===t('task.activitySummaryOnly'),'summary-only limitations were hidden');
renderTaskResult(summarized,parseTaskNotification(withTools),null,brief,false,true);
check(summarized.querySelectorAll('.task-activity-item').length===2,'repeated history rendering duplicated activity');
var originalT=t;
t=function(key,vars){return {'tool.list_dir':'Zawartość folderu','tool.read_many':'Odczyt plików'}[key]||originalT(key,vars);};
var palette=historyCase('task',valid.replace('<summary>','<tools>list_dir × 3, read_many, patch_file, ctx_execute, web_lookup, custom_tool</tools><summary>'),true);
var paletteItems=palette.querySelectorAll('.task-activity-item');
check(paletteItems[0].querySelector('.activity-name').textContent==='Zawartość folderu×3','counted list_dir bypassed the active language');
check(paletteItems[1].querySelector('.activity-name').textContent==='Odczyt plików','read_many bypassed the active language');
check(paletteItems[0].classList.contains('tk-read')&&paletteItems[1].classList.contains('tk-read')&&paletteItems[2].classList.contains('tk-write')&&paletteItems[3].classList.contains('tk-run')&&paletteItems[4].classList.contains('tk-web')&&!/tk-/.test(paletteItems[5].className),'names-only history tool-type colours wrong');
check(paletteItems[5].querySelector('.activity-name').textContent==='custom_tool','unknown tool label changed');
check(!palette.querySelector('.task-activity-item.done,.task-activity-item.failed,.task-activity-item.running'),'summary invented per-call status');
check(paletteItems[0].querySelector('.activity-status').textContent==='—'&&paletteItems[0].querySelector('.activity-status').title===t('task.statusUnconfirmed'),'missing result was presented as a known outcome');
t=originalT;
var continuation='Do not retry; stop the blocked browser check.';
var continued=historyCase('send_message',valid,true,'send_message','send_message',JSON.stringify({to:'worker-5',message:continuation}));
check(continued.querySelector('.task-brief').textContent===continuation,'continuation displayed the original task brief');
check(continued.querySelector('.task-activity-empty')&&!continued.querySelector('.task-activity-item'),'continuation inherited the previous run activity');
check(summarized.querySelector('.task-brief').textContent===brief&&summarized.querySelectorAll('.task-activity-item').length===2,'continuation changed the previous run');

var pager={_pendingHistoryCalls:Object.create(null),_pendingHistoryImages:[]};
var boundary=buildHistoryFragment([{role:'tool',seq:20,name:'send_message',tool_call_id:'boundary-call',content:withTools}],pager,false).querySelector('.task-row');
check(boundary&&!boundary.querySelector('.task-brief'),'boundary result guessed an unavailable brief');
buildHistoryFragment([{role:'assistant',seq:19,content:'',tool_calls:[{id:'boundary-call',name:'send_message',arguments:JSON.stringify({to:'worker-5',message:continuation})}]}],pager,true);
check(boundary.querySelector('.task-brief').textContent===continuation,'loading the preceding page lost the continuation brief');
check(boundary.querySelectorAll('.task-activity-item').length===2,'loading the preceding call lost the activity summary');
var falseBoundary=buildHistoryFragment([{role:'tool',seq:30,name:'task',tool_call_id:'ordinary-boundary',content:valid}],pager,false).querySelector('.task-row');
buildHistoryFragment([{role:'assistant',seq:29,content:'',tool_calls:[{id:'ordinary-boundary',name:'read_many',arguments:'{}'}]}],pager,true);
check(!falseBoundary.classList.contains('task-row')&&falseBoundary._historyName==='read_many','preceding ordinary call did not demote a misleading result name');
return '';
}catch(e){return e.stack||String(e);}})()`
	if err := page.Eval(ctx, script, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
}
