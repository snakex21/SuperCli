package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/testbrowser"
)

// A worker card in the chat opens with a click on its line, also when the
// card was rebuilt from saved history, and stays open while the view keeps
// being refreshed (a run of the conversation continuing elsewhere rebuilds
// the transcript on its events; busy workers make that frequent).
func TestWorkerCardOpensAndStaysOpenAcrossRefreshesNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "worker card open")
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"agent": "code", "prompt": "Cel: wrogie moby w zwykłym świecie"})
	running := `<task-notification><task-id>worker-313</task-id><agent>code</agent><status>running</status><summary>code running in background</summary></task-notification>`
	writer := session.NewWriter(store, sess.ID)
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Content: "Rozdziel pracę na workerów"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-313", Name: "task", Arguments: string(args)}}},
		{Role: llm.RoleTool, ToolCallID: "call-313", Name: "task", Content: running},
		{Role: llm.RoleAssistant, Content: "Worker 313 pracuje."},
	} {
		if err := writer.AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	api := httptest.NewServer(srv.Handler())
	defer api.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1200, Height: 900})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, api.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	var ready bool
	for i := 0; i < 200 && !ready; i++ {
		_ = page.Eval(ctx, `typeof resumeSession==='function'&&typeof ui!=='undefined'&&!!document.getElementById('stream')`, &ready)
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		t.Fatal("GUI did not start")
	}
	var failure string
	script := fmt.Sprintf(`(async()=>{try{
function check(ok,why){if(!ok)throw Error(why);}
ui.toolsExpanded=false;
await resumeSession(%q);
var card=()=>document.querySelector('#stream .task-row');
check(card()&&!card().open,'saved worker card missing or open');
// A click on the card's line (not on Stop) opens it.
card().querySelector('summary .thint').click();
await new Promise(r=>setTimeout(r,50));
check(card().open&&!card()._body.hidden,'click did not open the card');
// Three refreshes of the same conversation (sync rebuild + status paint).
for(var i=0;i<3;i++){
  await resumeSession(%q,null,false,true);
  var row=card();
  if(typeof renderWorkerLiveness==='function')renderWorkerLiveness(row);
  check(row&&row.open&&!row._body.hidden&&getComputedStyle(row._body).display!=='none','card closed after refresh '+(i+1));
}
// A real press whose line is repainted before the release (no click event
// then reaches the card) still toggles it: closed, then open again.
function press(row, repaint){
  var at=row.querySelector('summary .thint');
  at.dispatchEvent(new PointerEvent('pointerdown',{bubbles:true,button:0}));
  if(repaint)repaint();
  row.querySelector('summary .thint').dispatchEvent(new PointerEvent('pointerup',{bubbles:true,button:0}));
}
press(card(),function(){setTaskHint(card(),'Cel: wrogie moby — nowy krok');});
await new Promise(r=>setTimeout(r,20));
check(!card().open,'press with a repaint did not close the card');
press(card(),function(){card().querySelector('summary .thint').replaceChildren(document.createTextNode('nowy tekst'));});
await new Promise(r=>setTimeout(r,20));
check(card().open,'press with a repaint did not open the card');
// The click that follows a press does not toggle a second time.
press(card());card().querySelector('summary .thint').click();
await new Promise(r=>setTimeout(r,20));
check(!card().open,'press + click toggled twice');
card().querySelector('summary .thint').click();
await new Promise(r=>setTimeout(r,20));
check(card().open,'keyboard/plain click did not open the card');
// Closing is kept too.
card().querySelector('summary .thint').click();
await new Promise(r=>setTimeout(r,50));
await resumeSession(%q,null,false,true);
check(!card().open,'a closed card reopened after refresh');
// A card of a run still going on (replayed from its events on every
// refresh, so the card element is new each time) keeps its open state too.
var A=function(o){return JSON.stringify(o);}, t0=Date.now();
var replay=[{type:'tool_call',name:'task',id:'call-315',args:A({agent:'code',prompt:'Cel: lochy w zwykłym świecie'})},
  {type:'worker_progress',id:'worker-315',name:'code',kind:'started',parent_call_id:'call-315',run:1,at:t0},
  {type:'tool_result',id:'call-315',output:'<task-notification><task-id>worker-315</task-id><agent>code</agent><status>running</status><summary>started</summary></task-notification>'}];
function liveRun(){return {detached:true,terminal:false,userSeq:1e9,events:replay,reattach:function(){var c=null;replay.forEach(function(ev){c=handleEvent(ev,c);});}};}
var liveCard=function(){return Array.from(document.querySelectorAll('#stream .task-row')).find(function(r){return r._taskID==='worker-315';});};
detachedRuns[%q]=liveRun();
await resumeSession(%q,null,false,true);
check(liveCard()&&!liveCard().open,'replayed card missing or open');
liveCard().querySelector('summary .thint').click();
await new Promise(r=>setTimeout(r,50));
check(liveCard().open,'click did not open the replayed card');
for(var j=0;j<3;j++){
  var before=liveCard();
  detachedRuns[%q]=liveRun();
  await resumeSession(%q,null,false,true);
  check(liveCard()!==before,'fixture did not rebuild the replayed card');
  renderWorkerLiveness(liveCard());
  check(liveCard().open&&!liveCard()._body.hidden,'replayed card closed after refresh '+(j+1));
}
return '';
}catch(e){return e.message;}})()`, sess.ID, sess.ID, sess.ID, sess.ID, sess.ID, sess.ID, sess.ID)
	if err := page.Eval(ctx, script, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
}
