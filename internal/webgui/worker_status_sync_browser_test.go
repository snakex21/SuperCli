package webgui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

func TestWorkerStatusReconcileNativeBrowser(t *testing.T) {
	for _, locale := range []string{"en", "pl"} {
		t.Run(locale, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			catalog, err := assetsFS.ReadFile("assets/locales/" + locale + ".json")
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
				switch r.URL.Path {
				case "/":
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					fmt.Fprintf(w, `<!doctype html><html><body><script>var fixtureCatalog=%s;</script><script src="/prelude.js"></script><script src="/04-transcript.js"></script><script src="/05-chat.js"></script><script src="/05b-sync.js"></script><script src="/08-sessions.js"></script><script src="/ready.js"></script></body></html>`, catalog)
				case "/prelude.js":
					fmt.Fprint(w, queueEditBrowserPrelude+`
var superCliUI={fileMutationTools:{}};var preferences={};
function registerLanguageRefresh(){}function toolDisclosureOpen(){return false;}
function thinkingDisclosureOpen(){return false;}function renderText(s){return s;}
function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}
function updateRates(){}function renderCost(){}function renderQueue(){}
`)
				case "/ready.js":
					fmt.Fprint(w, `smartScroll=function(){};loadSessions=function(){};renderStats=function(){};addTurnMeta=function(){};syncConnect=function(){};activeSessionID=transcriptSessionID='fixture';window.fixtureReady=true;`)
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
			if err := page.Eval(ctx, workerStatusReconcileBrowserScript, &failure); err != nil {
				t.Fatal(err)
			}
			if failure != "" {
				t.Fatal(failure)
			}
		})
	}
}

const workerStatusReconcileBrowserScript = `(async()=>{try{
function check(ok,why){if(!ok)throw Error(why);}
check(window.fixtureReady,'scripts not ready');
var requests=0,nextWorkers=[],hiddenForTest=false;
Object.defineProperty(document,'hidden',{configurable:true,get:function(){return hiddenForTest;}});
function emit(ev){handleEvent(ev,null);}
function clockOff(){Object.values(toolRows).forEach(function(r){clearInterval(r._clock);});}
function reset(){
  pauseWorkerStatusSync();clockOff();stream.innerHTML='';toolRows={};workerRows={};openToolOrder=[];resetWorkerOverview();
  streaming=false;runFinishing=false;activeSessionID=transcriptSessionID='fixture';projectEpoch=1;requests=0;nextWorkers=[];
  j=function(url){check(url==='/api/workers','status sync called unrelated endpoint '+url);requests++;return Promise.resolve({workers:nextWorkers});};
}
function start(id,run,call){
  emit({type:'tool_call',name:'task',args:JSON.stringify({agent:'code',prompt:'Fixture job '+id}),id:call});
  emit({type:'worker_progress',id:id,name:'code',kind:'started',run:run,parent_call_id:call,model:'fixture-model',reasoning_effort:'high'});
  return toolRows[call];
}
function progress(id,run,call,toolCall,path){
  emit({type:'worker_progress',id:id,name:'code',kind:'tool_call',run:run,parent_call_id:call,call_id:toolCall,tool:'read_lines',args:JSON.stringify({file:path})});
}
function snapshot(id,run,status,path){return {id:id,agent:'code',run:run,status:status,steps:3,tokens_in:20,tokens_out:10,updated_at:Date.now(),activity:{kind:'tool_call',tool:'read_lines',args:JSON.stringify({file:path||'background.go'}),call_id:'background-read',at:Date.now()}};}
function note(id,status,result){return '<task-notification>\n<task-id>'+id+'</task-id>\n<agent>code</agent>\n<status>'+status+'</status>\n<summary>Fixture status</summary>\n<result>'+result+'</result>\n</task-notification>';}

// Terminal progress is sticky, including the row and the complete lazy report.
reset();var row=start('worker-1',1,'first');progress('worker-1',1,'first','read-1','current.go');
check(requests===0,'normal live events started snapshot polling');
check(workerOverview['worker-1'].activity.includes('current.go'),'actual activity not shown');
check(!workerOverview['worker-1'].activity.includes(t('task.lastActivity')),'activity gained a prefix absent from the original layout');
emit({type:'worker_progress',id:'worker-1',name:'code',kind:'finished',run:1,parent_call_id:'first',status:'done'});
emit({type:'worker',id:'worker-1',name:'code',run:1,status:'done',text:note('worker-1','done','FULL_RESULT_RETAINED')});
emit({type:'worker_progress',id:'worker-1',name:'code',kind:'started',run:1,parent_call_id:'first'});
progress('worker-1',1,'first','late-read','stale.go');
emit({type:'worker_progress',id:'worker-1',name:'code',kind:'tool_result',run:1,parent_call_id:'first',call_id:'read-1',tool:'read_lines',output:'read complete'});
emit({type:'tool_result',id:'first',output:note('worker-1','running','stale acknowledgement')});
check(workerOverview['worker-1'].status==='done'&&row._taskStatus==='done','late callbacks revived finished worker');
check(!row.classList.contains('running')&&row._clock===null,'terminal row still looks busy');
check(workerOverview['worker-1'].activity.includes('current.go'),'completion replaced actual activity with original prompt');
row.open=true;row.dispatchEvent(new Event('toggle'));check(row.textContent.includes('FULL_RESULT_RETAINED'),'late ack removed complete report');

// A new accepted invocation may run, but the old run cannot stop it.
emit({type:'tool_call',name:'send_message',args:'{"to":"worker-1","message":"continue","mode":"continue"}',id:'second'});
emit({type:'worker_progress',id:'worker-1',name:'code',kind:'started',run:2,parent_call_id:'second'});
progress('worker-1',2,'second','read-2','new.go');
emit({type:'worker_progress',id:'worker-1',name:'code',kind:'finished',run:1,parent_call_id:'first',status:'failed'});
check(workerRows['worker-1']===toolRows.second&&workerOverview['worker-1'].status==='running','old finish changed new invocation');
settleOpenTools();check(workerOverview['worker-1'].status==='running','parent stream completion falsely stopped background worker');

// Completion can be absent from SSE: authoritative snapshot repairs it.
nextWorkers=[snapshot('worker-1',2,'done','final.go'),snapshot('worker-99',8,'running','foreign.go')];
await resumeWorkerStatusSync();
check(workerOverview['worker-1'].status==='done','missing terminal event was not reconciled');
check(workerStatusTimer===null,'terminal workers kept polling');
check(!workerOverview['worker-99']&&Object.keys(workerOverview).length===1,'foreign conversation worker was imported');
check(toolRows.second._stat.textContent===t('task.done'),'row still shows running after snapshot');

// Snapshot preserves real background activity, and the next poll can finish.
reset();row=start('worker-2',1,'background');nextWorkers=[snapshot('worker-2',1,'running','background-file.go')];
await resumeWorkerStatusSync();check(requests===1&&workerStatusTimer!==null,'active detached worker not monitored');
check(workerOverview['worker-2'].activity.includes('background-file.go'),'snapshot did not show real background tool');
check(!workerOverview['worker-2'].activity.includes(t('task.lastActivity')),'snapshot activity gained a prefix absent from the original layout');
nextWorkers=[snapshot('worker-2',1,'failed','background-file.go')];await refreshWorkerStatusSnapshot();
check(workerOverview['worker-2'].status==='failed'&&workerStatusTimer===null,'failed background worker stayed running');

// Pending requests are single-flight and cannot overwrite newer live activity.
reset();row=start('worker-3',1,'single');progress('worker-3',1,'single','old','old.go');var resolveSnapshot;
j=function(url){check(url==='/api/workers','wrong endpoint');requests++;return new Promise(function(resolve){resolveSnapshot=resolve;});};
var pending=resumeWorkerStatusSync();var same=refreshWorkerStatusSnapshot();check(pending===same,'snapshot request duplicated');await Promise.resolve();
progress('worker-3',1,'single','new','fresh.go');resolveSnapshot({workers:[snapshot('worker-3',1,'running','old.go')]});await pending;
check(requests===1&&workerOverview['worker-3'].activity.includes('fresh.go'),'late snapshot regressed live activity');

// The manual workers sidebar must capture the same request-time guards.
reset();row=start('worker-30',1,'sidebar');progress('worker-30',1,'sidebar','old','sidebar-old.go');
if(!$('#worker-list')){var host=document.createElement('div');host.id='worker-list';document.body.appendChild(host);}
if(!$('#worker-heading')){var heading=document.createElement('div');heading.id='worker-heading';document.body.appendChild(heading);}
var resolveSidebar;j=function(){return new Promise(function(resolve){resolveSidebar=resolve;});};
var sidebar=loadWorkers();await Promise.resolve();progress('worker-30',1,'sidebar','new','sidebar-fresh.go');
resolveSidebar({workers:[snapshot('worker-30',1,'running','sidebar-old.go')]});await sidebar;
check(workerOverview['worker-30'].activity.includes('sidebar-fresh.go'),'sidebar snapshot regressed newer live activity');
sidebar=loadWorkers();await Promise.resolve();
emit({type:'tool_call',name:'send_message',args:'{"to":"worker-30","message":"new","mode":"continue"}',id:'sidebar-next'});
emit({type:'worker_progress',id:'worker-30',name:'code',kind:'started',run:2,parent_call_id:'sidebar-next'});
resolveSidebar({workers:[snapshot('worker-30',1,'done','sidebar-old.go')]});await sidebar;
check(workerRows['worker-30']===toolRows['sidebar-next']&&workerOverview['worker-30'].status==='running','sidebar snapshot changed newer invocation');

// A response requested before switching session/root cannot mutate the new view.
reset();start('worker-4',1,'old-root');var resolveOld;
j=function(){requests++;return new Promise(function(resolve){resolveOld=resolve;});};pending=resumeWorkerStatusSync();await Promise.resolve();
activeSessionID='other';projectEpoch++;toolRows={};workerRows={};openToolOrder=[];resetWorkerOverview();start('worker-4',1,'new-root');
resolveOld({workers:[snapshot('worker-4',1,'done','old.go')]});await pending;
check(workerOverview['worker-4'].status==='running'&&workerRows['worker-4']===toolRows['new-root'],'old response changed another session');

// Network uncertainty is not displayed as verified running, with bounded retries.
reset();row=start('worker-5',1,'failure');j=function(){requests++;return Promise.reject(Error('offline'));};
await resumeWorkerStatusSync();await refreshWorkerStatusSnapshot();await refreshWorkerStatusSnapshot();
check(requests===3&&workerStatusFailures===3&&workerStatusTimer===null,'failed source polled without bound');
check(workerOverview['worker-5'].status==='unknown'&&workerOverview['worker-5'].state.textContent===t('task.statusUnconfirmed'),'network error falsely claims worker is active');
check(!row.classList.contains('running')&&row._stat.textContent===t('task.statusUnconfirmed'),'unknown row still looks actively running');
check(!$('#worker-overview').hidden&&$('#worker-overview').open,'uncertainty was hidden as if no work remained');

// A different invocation does not complete/rebind the displayed task.
reset();row=start('worker-6',2,'different');nextWorkers=[snapshot('worker-6',1,'done','old.go')];await resumeWorkerStatusSync();
check(workerOverview['worker-6'].status==='unknown'&&row._taskRun===2&&workerStatusTimer===null,'mismatched run incorrectly settled current task');
nextWorkers=[snapshot('worker-6',2,'stopped','new.go')];await resumeWorkerStatusSync();
check(workerOverview['worker-6'].status==='stopped','explicit refresh did not recover correct invocation state');

// A terminal live event during an outstanding fetch beats its stale snapshot.
reset();row=start('worker-7',1,'race');var resolveRace;
j=function(){return new Promise(function(resolve){resolveRace=resolve;});};pending=resumeWorkerStatusSync();await Promise.resolve();
emit({type:'worker_progress',id:'worker-7',name:'code',kind:'finished',run:1,parent_call_id:'race',status:'failed'});
resolveRace({workers:[snapshot('worker-7',1,'done','stale.go')]});await pending;
check(workerOverview['worker-7'].status==='failed','snapshot overwrote a newer terminal event');

// An unversioned saved acknowledgement cannot adopt an unrelated invocation.
reset();var historyRow=addHistoryTask(parseTaskNotification(note('worker-8','running','Started in background')));
toolRows['saved-call']=historyRow;
nextWorkers=[snapshot('worker-8',9,'done','other-invocation.go'),snapshot('worker-100',1,'running','other-session.go')];await resumeWorkerStatusSync();
check(workerOverview['worker-8'].status==='unknown'&&!(historyRow._taskRun>0),'unversioned history adopted unrelated terminal snapshot');
check(!workerOverview['worker-100'],'restoring history imported unrelated worker');
// Accepted started replay establishes the exact invocation before snapshots apply.
emit({type:'worker_progress',id:'worker-8',name:'code',kind:'started',run:1,parent_call_id:'saved-call'});
check(historyRow.classList.contains('running')&&historyRow._stat.textContent===t('tool.running'),'verified live start did not clear unknown row status');
nextWorkers=[snapshot('worker-8',1,'running','history-live.go')];await resumeWorkerStatusSync();
check(workerOverview['worker-8'].status==='running'&&workerOverview['worker-8'].activity.includes('history-live.go'),'versioned restored worker has no activity overview');

// Visibility pauses requests/timers; returning checks the latest authoritative state.
hiddenForTest=true;document.dispatchEvent(new Event('visibilitychange'));check(workerStatusTimer===null&&!workerStatusArmed,'hidden page kept polling');
var before=requests;await refreshWorkerStatusSnapshot();check(requests===before,'hidden page made snapshot request');
nextWorkers=[snapshot('worker-8',1,'done','history-live.go')];hiddenForTest=false;document.dispatchEvent(new Event('visibilitychange'));
if(workerStatusRequest)await workerStatusRequest;
check(workerOverview['worker-8'].status==='done'&&workerStatusTimer===null,'visible page did not reconcile completed worker');
check(stream.querySelectorAll('.msg-user').length===0,'status/activity was injected as a human chat message');
// Activity explains what was recorded, without treating model steps as tool calls.
reset();var activityRow=start('worker-50',1,'activity-call');
check(activityRow.querySelector('.task-activity-empty').textContent===t('task.activityWaiting'),'new worker has an unexplained empty activity section');
emit({type:'worker_progress',id:'worker-50',name:'explore',kind:'tool_call',run:1,parent_call_id:'activity-call',call_id:'find-browser',tool:'tool_search',args:JSON.stringify({query:'browser'})});
check(!activityRow.querySelector('.task-activity-empty'),'real activity retained the waiting placeholder');
var detailText='No matching capability. <script>window.workerActivityInjected=true</script>';
emit({type:'worker_progress',id:'worker-50',name:'explore',kind:'tool_result',run:1,parent_call_id:'activity-call',call_id:'find-browser',tool:'tool_search',output:detailText});
check(activityRow.querySelector('.activity-result pre').textContent===detailText,'worker tool result is only in a desktop tooltip or was modified');
check(!activityRow.querySelector('.activity-result script')&&!window.workerActivityInjected,'worker tool result was interpreted as HTML');
emit({type:'tool_result',id:'activity-call',output:note('worker-50','done','Check blocked by unavailable capability')});
check(activityRow.querySelector('.activity-result pre').textContent===detailText,'final report discarded real tool activity');
emit({type:'tool_call',name:'send_message',args:JSON.stringify({to:'worker-50',message:'Stop, do not repeat the check.'}),id:'empty-continuation'});
emit({type:'worker_progress',id:'worker-50',name:'explore',kind:'started',run:2,parent_call_id:'empty-continuation'});
emit({type:'tool_result',id:'empty-continuation',output:note('worker-50','done','Confirmed; no further actions')});
var emptyRun=toolRows['empty-continuation'];
check(emptyRun.querySelector('.task-activity-empty').textContent===t('task.activityUnavailable'),'empty continuation hid the absence of recorded tools');
check(!emptyRun.querySelector('.task-activity-item'),'empty continuation copied earlier tools');
check(activityRow.querySelector('.activity-result pre').textContent===detailText,'continuation erased the earlier tool result');
check(emptyRun.querySelector('.task-brief').textContent==='Stop, do not repeat the check.','continuation lost its own instruction');
check(emptyRun.querySelector('.task-backlink'),'continuation lost its previous-delegation link');
pauseWorkerStatusSync();clockOff();return '';
}catch(e){pauseWorkerStatusSync();Object.values(toolRows).forEach(function(r){clearInterval(r._clock);});return e.stack||String(e);}})()`
