package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// Production scripts versus the frozen pre-guard overview functions below.
// Fake wire events, isolated browser/profile, no Engine or model.
func TestWorkerOverviewUnchangedCostNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	en, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><head><link rel="stylesheet" href="/app.css"></head><body><details id="worker-overview" open><summary id="worker-overview-summary"></summary><div id="worker-overview-list"></div></details><script>var fixtureCatalog=%s;</script><script src="/prelude.js"></script><script src="/04-transcript.js"></script><script src="/05-chat.js"></script><script src="/ready.js"></script></body></html>`, en)
		case "/prelude.js":
			fmt.Fprint(w, queueEditBrowserPrelude+`
var superCliUI={fileMutationTools:{},mutationKind:function(){return '';}};var preferences={};
function registerLanguageRefresh(){} function toolDisclosureOpen(){return true;}
function thinkingDisclosureOpen(){return false;} function renderText(s){return s;}
function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}
`)
		case "/ready.js":
			fmt.Fprint(w, `toolDisclosureOpen=function(){return true;};smartScroll=function(){};transcriptScrollTop=function(){return 0;};appendStream=function(n){stream.appendChild(n);};clip=function(s,n){s=String(s||'');return s.length>n?s.slice(0,n-1)+'…':s;};window.fixtureReady=true;`)
		case "/app.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			b, _ := assetsFS.ReadFile("assets/app.css")
			w.Write(b)
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
	var result struct {
		Error   string          `json:"error"`
		Samples json.RawMessage `json:"samples"`
		Checks  string          `json:"checks"`
	}
	err = page.Eval(ctx, workerOverviewCostFixture, &result)
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	t.Logf("synchronous dispatch only; 8 workers x 100 tool-call/result pairs; smartScroll stubbed identically; no frame/presentation claim; samples=%s; checks=%s", result.Samples, result.Checks)
}

// Frozen from 04-transcript.js before the production unchanged-state guard.
// Only names/binding flag differ; neither function calls the guarded renderer.
const workerOverviewLegacyFixture = `
function legacyUpdate(id, agentName, status, activity, row) {
  if (!id) return;
  var item = workerOverview[id];
  if (row && workerRows[id] && workerRows[id] !== row) return;
  if (item && item.run && (!row || !row._taskRun)) return;
  if (!item) {
    item = workerOverview[id] = { id: id };
    item.button = el("button", "worker-overview-item");
    item.button.type = "button";
    item.button.addEventListener("click", function () {
      if (!item.row || !item.row.isConnected) return;
      item.row.open = true;
      item.row._body.hidden = false;
      item.row.scrollIntoView({ block: "center", behavior: "smooth" });
    });
    item.button.appendChild(el("span", "worker-overview-dot"));
    item.label = el("span", "worker-overview-name");
    item.detail = el("span", "worker-overview-activity");
    item.state = el("span", "worker-overview-status");
    item.button.append(item.label, item.detail, item.state);
    $("#worker-overview-list").appendChild(item.button);
  }
  item.run = row && row._taskRun || item.run || 0;
  item.agent = agentName || item.agent || "";
  item.status = status || item.status || "running";
  if (activity != null) item.activity = activity;
  if (row) item.row = row;
  item.button.dataset.state = item.status;
  item.label.textContent = workerLabel(id) + (item.agent ? " · " + item.agent : "");
  item.detail.textContent = clip(String(item.activity || "").split("\n")[0], 140);
  item.state.textContent = taskStatusLabel(item.status);
  item.button.title = clip([id, item.row && item.row._workerModelInfo, item.activity].filter(Boolean).join(" · "), 400);
  renderWorkerOverview();
}
function legacyRender() {
  var panel = $("#worker-overview");
  if (!panel) return;
  if (!legacyOverviewBound) {
    legacyOverviewBound = true;
    panel.addEventListener("toggle", function () {
      if (panel.open !== workerOverviewAutoOpen) workerOverviewUserToggled = true;
    });
  }
  var items = Object.values(workerOverview).sort(function (a, b) {
    return workerOverviewOrder(a) - workerOverviewOrder(b) ||
      a.id.localeCompare(b.id, undefined, { numeric: true });
  });
  var list = $("#worker-overview-list");
  items.forEach(function (w, index) {
    var next = list.children[index] || null;
    if (next !== w.button) list.insertBefore(w.button, next);
  });
  var counts = {};
  items.forEach(function (w) { counts[w.status] = (counts[w.status] || 0) + 1; });
  var parts = [t("task.workers")];
  [["running", "task.active"], ["done", "task.done"], ["failed", "task.failed"], ["stopped", "task.stopped"]].forEach(function (p) {
    if (counts[p[0]]) parts.push(t(p[1]) + ": " + counts[p[0]]);
  });
  $("#worker-overview-summary").textContent = parts.join(" · ");
  panel.hidden = !items.length;
  if (items.length && !workerOverviewUserToggled) {
    workerOverviewAutoOpen = !!counts.running;
    panel.open = workerOverviewAutoOpen;
  }
}
`

const workerOverviewCostFixture = `(()=>{try {
if(!window.fixtureReady)throw Error('scripts not ready');
function check(ok,msg){if(!ok)throw Error(msg);}
const productionUpdate=updateWorkerOverview, productionRender=renderWorkerOverview;
let renders=0, legacyOverviewBound=false;
` + workerOverviewLegacyFixture + `
function countedProductionRender(){renders++;return productionRender();}
function countedLegacyRender(){renders++;return legacyRender();}
function emit(e){handleEvent(e,null);}
function reset(candidate){
  Object.values(toolRows).forEach(r=>clearInterval(r._clock));
  stream.replaceChildren();toolRows={};workerRows={};openToolOrder=[];resetWorkerOverview();
  updateWorkerOverview=candidate?productionUpdate:legacyUpdate;
  renderWorkerOverview=candidate?countedProductionRender:countedLegacyRender;
  for(let i=1;i<=8;i++){
    emit({type:'tool_call',name:'task',args:JSON.stringify({agent:'code',prompt:'fixture task '+i}),id:'parent'+i});
    emit({type:'worker_progress',id:'worker-'+i,name:'code',kind:'started',parent_call_id:'parent'+i,run:1,model:'fixture-model',reasoning_effort:'high'});
    clearInterval(toolRows['parent'+i]._clock);
  }
}
const events=[];
for(let n=0;n<100;n++)for(let i=1;i<=8;i++){
  const base={type:'worker_progress',id:'worker-'+i,name:'code',parent_call_id:'parent'+i,run:1,call_id:'read-'+n,tool:'read_lines'};
  events.push(Object.assign({},base,{kind:'tool_call',args:'{"file":"fixture.txt"}'}));
  events.push(Object.assign({},base,{kind:'tool_result',output:'fixture output'}));
}
function sample(candidate){
  reset(candidate);
  const panel=$('#worker-overview'), button=workerOverview['worker-1'].button;
  button.focus();
  const observer=new MutationObserver(()=>{});
  observer.observe(panel,{subtree:true,childList:true,attributes:true,characterData:true});
  renders=0;
  const start=performance.now();events.forEach(emit);const ms=performance.now()-start;
  const records=observer.takeRecords();observer.disconnect();
  check(document.activeElement===button,'focus lost');
  check(panel.open&&!panel.hidden,'active panel not immediately open');
  check(Object.keys(workerOverview).length===8,'membership changed');
  for(let i=1;i<=8;i++){
    const row=toolRows['parent'+i],item=workerOverview['worker-'+i];
    check(item.row===row&&workerRows[item.id]===row,'ownership changed');
    check(item.status==='running'&&item.activity.includes('fixture.txt'),'overview mismatch');
    check(Object.keys(row._workerCalls).length===100,'activity history lost');
    check(Object.values(row._workerCalls).every(c=>c.classList.contains('done')),'tool result lost');
  }
  return {mode:candidate?'production':'legacy',ms,renders,skipped:candidate?events.length-renders:0,mutations:records.length,
    childList:records.filter(r=>r.type==='childList').length,attributes:records.filter(r=>r.type==='attributes').length,
    snapshot:JSON.stringify([panel.outerHTML,stream.textContent])};
}
// Warm both paths before recording. Alternate order to reduce warmup bias.
sample(false);const probe=sample(true);check(probe.skipped===1592&&probe.renders===8,'production failed to suppress unchanged renders');
const samples=[];
for(let n=0;n<5;n++){
  const a=sample(n%2===1), b=sample(n%2!==1);
  check(a.snapshot===b.snapshot,'legacy/production DOM differs');
  delete a.snapshot;delete b.snapshot;samples.push(a,b);
}
// Changed state still goes through production synchronously, with no rAF wait.
reset(true);
let row=toolRows.parent1, item=workerOverview['worker-1'];
function refresh(){updateWorkerOverview(item.id,'code','running',null,row);}
function overviewMutations(action){
  const observer=new MutationObserver(()=>{});
  observer.observe($('#worker-overview'),{subtree:true,childList:true,attributes:true,characterData:true});
  action();const records=observer.takeRecords();observer.disconnect();return records;
}
check(overviewMutations(()=>{for(let n=0;n<100;n++)refresh();}).length===0,'unchanged update mutated DOM');
const expected={label:item.label.textContent,detail:item.detail.textContent,state:item.state.textContent,title:item.button.title};
item.button.focus();
for(const [alter,verify] of [
  [()=>item.label.textContent='tampered',()=>item.label.textContent===expected.label],
  [()=>item.detail.textContent='tampered',()=>item.detail.textContent===expected.detail],
  [()=>item.state.textContent='tampered',()=>item.state.textContent===expected.state],
  [()=>item.button.title='tampered',()=>item.button.title===expected.title],
  [()=>item.button.dataset.state='failed',()=>item.button.dataset.state==='running'],
  [()=>$('#worker-overview-summary').textContent='tampered',()=>$('#worker-overview-summary').textContent===workerOverviewSummary(workerOverviewView.counts)],
  [()=>$('#worker-overview').hidden=true,()=>!$('#worker-overview').hidden],
  [()=>$('#worker-overview').open=false,()=>$('#worker-overview').open],
  [()=>$('#worker-overview-list').appendChild(item.button),()=>$('#worker-overview-list').firstChild===item.button],
  [()=>item.button.remove(),()=>$('#worker-overview-list').firstChild===item.button],
  [()=>item.label.remove(),()=>item.button.children[1]===item.label],
  [()=>item.button.appendChild(item.label),()=>item.button.children[1]===item.label],
  [()=>$('#worker-overview-list').appendChild(document.createElement('button')),()=>$('#worker-overview-list').children.length===8]
]){alter();refresh();check(verify(),'same-state DOM repair failed');}
item.button.focus();check(overviewMutations(refresh).length===0&&document.activeElement===item.button,'unchanged focus/no-mutation regression');
row.open=false;row._body.hidden=true;row.remove();const detachedRenders=renders;
refresh();check(renders===detachedRenders+1,'detached row skipped repair');
item.button.click();check(!row.open&&row._body.hidden,'detached row click had side effects');
stream.appendChild(row);item.button.click();check(row.open&&!row._body.hidden,'connected row click failed');
const longActivity='x'.repeat(600);
updateWorkerOverview(item.id,'code','running',longActivity+'old tail',row);
const oldPreview=item.detail.textContent, oldTitle=item.button.title, beforeTail=renders;
updateWorkerOverview(item.id,'code','running',longActivity+'new tail',row);
check(item.activity===longActivity+'new tail'&&renders===beforeTail+1,'raw clipped activity was skipped');
check(item.detail.textContent===oldPreview&&item.button.title===oldTitle,'tail test not beyond clipping');
const oldPanel=$('#worker-overview'), newPanel=document.createElement('details');
newPanel.id='worker-overview';newPanel.innerHTML='<summary id="worker-overview-summary"></summary><div id="worker-overview-list"></div>';
oldPanel.replaceWith(newPanel);refresh();
check(item.button.isConnected&&item.button.parentNode===$('#worker-overview-list')&&newPanel.open,'new root not repaired');
check(workerOverviewBound===newPanel,'new root toggle listener not bound');
newPanel.open=false;newPanel.dispatchEvent(new Event('toggle'));
check(workerOverviewUserToggled,'user toggle not observed on new root');
refresh();check(!newPanel.open,'unchanged update overrode user toggle');
updateWorkerOverview(item.id,'code','running','changed activity',row);
check(!newPanel.open,'changed update overrode user toggle');
newPanel.open=true;newPanel.dispatchEvent(new Event('toggle'));
for(let i=1;i<=8;i++)emit({type:'worker_progress',id:'worker-'+i,name:'code',kind:'finished',parent_call_id:'parent'+i,run:1,status:'done'});
check(newPanel.open,'finish overrode user toggle');
reset(true);row=toolRows.parent1;item=workerOverview['worker-1'];
row._workerModelInfo='changed model / effort';
updateWorkerOverview(item.id,'code','running',null,row);
check(item.button.title.includes('changed model / effort'),'model tooltip stale');
const key='tool.running', previous=fixtureCatalog[key];fixtureCatalog[key]='LOCALIZED ACTIVE';
updateWorkerOverview(item.id,'code','running',null,row);
check(item.state.textContent===taskStatusLabel('running'),'localized status stale');
fixtureCatalog[key]=previous;
const summaryKey='task.active', previousSummary=fixtureCatalog[summaryKey];
fixtureCatalog[summaryKey]='LOCALIZED SUMMARY';refresh();
check($('#worker-overview-summary').textContent.includes('LOCALIZED SUMMARY'),'summary-only localization stale');
fixtureCatalog[summaryKey]=previousSummary;refresh();
// A pending steer/continuation must not claim ownership; an accepted start can.
for(const mode of ['steer','continue']){
  emit({type:'tool_call',name:'send_message',args:JSON.stringify({to:'worker-1',mode,message:'instruction'}),id:'pending-'+mode});
  check(workerRows[item.id]===row&&item.row===row,'dispatch stole ownership');
  emit({type:'tool_result',id:'pending-'+mode,output:'',err:'fixture rejection'});
  check(workerRows[item.id]===row&&item.row===row,'rejection stole ownership');
}
emit({type:'tool_call',name:'send_message',args:'{"to":"worker-1","mode":"continue","message":"new"}',id:'next'});
emit({type:'worker_progress',id:'worker-1',name:'code',kind:'started',parent_call_id:'next',run:2,model:'new-model',reasoning_effort:'low'});
const live=toolRows.next;
check(workerRows[item.id]===live&&item.row===live&&item.run===2,'accepted start not immediate');
check(live._workerModel==='new-model'&&live._workerEffort==='low','effort metadata lost');
emit({type:'worker_progress',id:'worker-1',name:'code',kind:'finished',parent_call_id:'parent1',run:1,status:'done'});
check(item.row===live&&item.status==='running','stale finish clobbered current');
for(let i=1;i<=8;i++)emit({type:'worker_progress',id:'worker-'+i,name:'code',kind:'finished',parent_call_id:i===1?'next':'parent'+i,run:i===1?2:1,status:'done'});
check(!$('#worker-overview').open,'completion collapse not immediate');
pruneWorkerOverview();check(!Object.keys(workerOverview).length&&$('#worker-overview').hidden,'prune failed');
resetWorkerOverview();check(!$('#worker-overview-list').children.length,'reset failed');
Object.values(toolRows).forEach(r=>clearInterval(r._clock));
return {samples,checks:'identical legacy/production paired DOM/history; focus; immediate lifecycle; ownership/ordinal; metadata/localization; prune/reset; unchanged zero mutations; DOM repair/new root; raw clipped activity; user toggle'};
}catch(e){return {error:e.stack||String(e)}}})()`
