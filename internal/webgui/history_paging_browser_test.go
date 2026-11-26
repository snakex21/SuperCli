package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/testbrowser"
)

// A long, busy session (the stress fixture: hundreds of turns, big tool
// outputs, worker reports) in the real GUI against the real server:
//   - scrolling back reaches the first message with no gaps or duplicates,
//     also while new messages arrive and the view is rebuilt by live sync
//     refreshes, and while a run of the conversation is shown;
//   - a worker in the panel above the chat whose card is far back is loaded,
//     opened, shown and highlighted, and stays open through live updates;
//   - a worker whose card is loaded opens without the view jumping.
func TestLongBusyHistoryPagingAndWorkerJumpNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	const turns = 160
	sid := seedBusyHistory(t, store, srv.eng.Home(), turns)
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
		_ = page.Eval(ctx, `typeof resumeSession==='function'&&typeof loadOlderTranscript==='function'&&!!document.getElementById('stream')`, &ready)
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		t.Fatal("GUI did not start")
	}
	if _, err := page.Screenshot(ctx); err != nil { // activate the target for rAF
		t.Fatal(err)
	}
	run := func(name, body string) {
		t.Helper()
		var failure string
		script := `(async()=>{try{
var SID=` + fmt.Sprintf("%q", sid) + `, TURNS=` + fmt.Sprint(turns) + `;
function check(ok,why){if(!ok)throw Error(why);}
var sleep=ms=>new Promise(r=>setTimeout(r,ms));
// Every seeded turn exactly once, in order: user "turn N:" rows.
function turnsSeen(){
  var seen=[];document.querySelectorAll('#stream .msg-user').forEach(function(n){var m=/^turn (\d+):/.exec(n.textContent.trim());if(m)seen.push(+m[1]);});
  return seen;
}
function checkComplete(what){
  var seen=turnsSeen();
  check(seen.length===TURNS,what+': '+seen.length+' turns shown, want '+TURNS);
  for(var i=0;i<seen.length;i++)check(seen[i]===i,what+': turn '+i+' is '+seen[i]+' (gap or duplicate)');
}
async function loadAll(between){
  for(var n=0;n<200&&document.querySelector('#stream .history-older');n++){
    stage.scrollTop=0;
    await loadOlderTranscript();
    if(between)await between(n);
  }
  check(!document.querySelector('#stream .history-older'),'history pager never ended');
}
` + body + `
return '';
}catch(e){return e.message;}})()`
		if err := page.Eval(ctx, script, &failure); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if failure != "" {
			t.Fatalf("%s: %s", name, failure)
		}
	}

	// 1. Plain scroll back to the first message.
	run("scroll back", `
ui.toolsExpanded=false;
await resumeSession(SID);
check(document.querySelector('#stream .history-older'),'no pager on a long session');
await loadAll();
checkComplete('scroll back');
`)

	// 2. The same while the agent keeps writing and live sync refreshes rebuild
	// the view between pages; loaded older pages and the reading position stay.
	var live atomic.Bool
	live.Store(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		writer := session.NewWriter(store, sid)
		for i := 0; live.Load() && i < 400; i++ {
			_ = writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("live update %d", i)})
			time.Sleep(15 * time.Millisecond)
		}
	}()
	run("scroll back with live refreshes", `
await resumeSession(SID);
// What the reader sees at the top of the view: that row's text and offset.
// The row the reader has at the top of the view (by message and position
// within it), its text and offset; then where that row is after the rebuild.
function viewTop(){
  var top=stage.getBoundingClientRect().top;
  var rows=Array.from(stream.children);
  for(var i=0;i<rows.length;i++){var c=rows[i],b=c.getBoundingClientRect();if(b.bottom>top&&c._historySeq!=null){
    var nth=0;for(var k=i-1;k>=0&&rows[k]._historySeq===c._historySeq;k--)nth++;
    return {seq:c._historySeq,nth:nth,text:c.textContent.slice(0,80),offset:Math.round(b.top-top)};}}
  return null;
}
function sameRow(at){
  var top=stage.getBoundingClientRect().top,nth=at.nth;
  for(var c of stream.children){if(c._historySeq===at.seq){if(nth--===0)return {seq:at.seq,nth:at.nth,text:c.textContent.slice(0,80),offset:Math.round(c.getBoundingClientRect().top-top)};}}
  return null;
}
await loadAll(async function(n){
  stage.scrollTop=Math.min(stage.scrollTop+400,stage.scrollHeight/3);
  transcriptFollowTail=false;
  // Let lazily rendered rows reach their painted size first.
  for(var f=0;f<3;f++)await new Promise(r=>requestAnimationFrame(r));
  var before=viewTop();
  await resumeSession(SID,null,false,true); // a live sync refresh rebuilds the view
  var after=before&&sameRow(before);
  check(before&&after&&before.text===after.text&&Math.abs(before.offset-after.offset)<=2,'refresh moved the reader: '+JSON.stringify([before,after]));
});
checkComplete('scroll back with live refreshes');
// After everything is loaded a refresh keeps all of it.
await resumeSession(SID,null,false,true);
checkComplete('refresh after full load');
var lives={};document.querySelectorAll('#stream .msg-assistant').forEach(function(n){var m=/live update (\d+)/.exec(n.textContent);if(m){check(!lives[m[1]],'live update '+m[1]+' shown twice');lives[m[1]]=1;}});
`)
	live.Store(false)
	<-done

	// 3. A run of the conversation shown in the view keeps the way back.
	run("pager during a run", `
await resumeSession(SID);
var sent=sendPrompt('ping');
await sleep(30);
check(document.querySelector('#stream .history-older'),'a starting run removed the history pager');
await sent;
for(var w=0;w<200&&streaming;w++)await sleep(25);
check(document.querySelector('#stream .history-older'),'the history pager is gone after the run');
await loadAll();
checkComplete('after a run');
`)

	// 3b. With the whole history loaded, live refreshes fetch only the newest
	// turn and what follows it: one small request each, no gaps, no
	// duplicates (they re-fetched up to 40 pages of 500 rows before).
	run("load all for live refreshes", `
await resumeSession(SID);
await loadAll();
checkComplete('before live refreshes');
`)
	writer := session.NewWriter(store, sid)
	for i := 0; i < 3; i++ {
		_ = writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("live turn %d", i)})
		_ = writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("live answer %d", i)})
		var got struct {
			Requests []string `json:"requests"`
			Bytes    int      `json:"bytes"`
			Failure  string   `json:"failure"`
		}
		script := `(async()=>{var reqs=[],bytes=0,orig=window.fetch;
window.fetch=async function(u,o){var r=await orig(u,o);if(String(u).indexOf('/api/transcript')>=0){reqs.push(String(u).replace(/^.*\?/,''));var c=r.clone();bytes+=(await c.text()).length;}return r;};
var failure='';try{await resumeSession(` + fmt.Sprintf("%q", sid) + `,null,false,true);}catch(e){failure=e.message;}
window.fetch=orig;
var seen=[];document.querySelectorAll('#stream .msg-user').forEach(function(n){var m=/^turn (\d+):/.exec(n.textContent.trim());if(m)seen.push(+m[1]);});
for(var k=0;k<seen.length;k++)if(seen[k]!==k){failure=failure||('gap or duplicate at turn '+k);break;}
if(seen.length!==` + fmt.Sprint(turns) + `)failure=failure||('turns shown: '+seen.length);
var lives=Array.from(document.querySelectorAll('#stream .msg-user')).filter(function(n){return /^live turn /.test(n.textContent.trim());}).map(function(n){return n.textContent.trim();});
if(lives.length!==` + fmt.Sprint(i+1) + `||new Set(lives).size!==lives.length)failure=failure||('live turns shown: '+JSON.stringify(lives));
if(document.querySelector('#stream .history-older'))failure=failure||'older pages were dropped';
return {requests:reqs,bytes:bytes,failure:failure};})()`
		if err := page.Eval(ctx, script, &got); err != nil {
			t.Fatal(err)
		}
		t.Logf("live refresh %d: requests %v, %d bytes", i+1, got.Requests, got.Bytes)
		if got.Failure != "" {
			t.Fatalf("live refresh %d: %s", i+1, got.Failure)
		}
		if len(got.Requests) != 1 || !strings.Contains(got.Requests[0], "after=") || got.Bytes > 64<<10 {
			t.Fatalf("live refresh %d fetched %v (%d bytes), want one small after= request", i+1, got.Requests, got.Bytes)
		}
	}

	// 4. Worker panel: a card far back (not loaded) is loaded, opened, shown,
	// highlighted, and stays open through three live updates.
	run("panel jump far back", `
await resumeSession(SID);
await loadAll();
var target='worker-107';
check(workerCardFor(target),'fixture has no card for '+target);
updateWorkerOverview(target,'code','running','zombie spawn rules',workerCardFor(target));
// Reopen: only the latest page is loaded; the panel keeps the worker.
await resumeSession(SID);
check(!workerCardFor(target),'the card should not be loaded yet');
var button=workerOverview[target]&&workerOverview[target].button;
check(button,'panel has no button for '+target);
var states=[];var watch=setInterval(function(){states.push(button.classList.contains('loading'));},5);
button.click();
for(var i=0;i<400&&!(workerCardFor(target)&&workerCardFor(target).open);i++)await sleep(10);
await sleep(900);
clearInterval(watch);
check(states.indexOf(true)>=0,'no loading state while older history loaded');
check(!button.classList.contains('loading'),'loading state stuck');
var card=workerCardFor(target);
check(card&&card.open&&!card._body.hidden,'card not opened');
check(card.classList.contains('worker-card-flash'),'card not highlighted');
function visible(row){var v=stage.getBoundingClientRect(),r=row.querySelector('summary').getBoundingClientRect();return r.top>=v.top-1&&r.bottom<=v.bottom+1;}
check(visible(card),'card not scrolled into view: '+JSON.stringify([stage.getBoundingClientRect(),card.querySelector('summary').getBoundingClientRect(),stage.scrollTop,stage.scrollHeight]));
for(var u=0;u<3;u++){
  await resumeSession(SID,null,false,true);
  if(typeof renderWorkerLiveness==='function')renderWorkerLiveness(workerCardFor(target));
  var c=workerCardFor(target);
  check(c&&c.open&&!c._body.hidden&&getComputedStyle(c._body).display!=='none','card closed after live update '+(u+1));
  check(visible(c),'card left the view after live update '+(u+1));
}
`)

	// 5. Worker panel: a loaded, visible card opens without moving the view.
	run("panel jump loaded", `
await resumeSession(SID);
await loadOlderTranscriptUntil(function(){return document.querySelector('#stream details.task-row');});
await sleep(300);
var cards=Array.from(document.querySelectorAll('#stream details.task-row'));
check(cards.length,'no worker card on the latest page');
var card=cards[cards.length-1], id=card._taskID;
check(id,'card without a worker id');
if(card.open)toggleTaskRow(card);
card.querySelector('summary').scrollIntoView({block:'center'});
await sleep(100);
updateWorkerOverview(id,'code','running','step',card);
var before=card.querySelector('summary').getBoundingClientRect().top, tops=[];
var watch=setInterval(function(){tops.push(Math.round(card.querySelector('summary').getBoundingClientRect().top));},5);
workerOverview[id].button.click();
await sleep(600);
clearInterval(watch);
check(card.open&&!card._body.hidden,'loaded card not opened');
var moved=tops.filter(function(v){return Math.abs(v-before)>1;});
check(!moved.length,'view jumped: '+JSON.stringify(tops.slice(0,20)));
`)
}

// seedBusyHistory writes a busy agent session: every turn runs a build (big
// output now and then); every 15th turn the coordinator starts a worker (a
// "task" card) and later gets its report as a notification.
func seedBusyHistory(t *testing.T, store *session.Store, home string, turns int) string {
	t.Helper()
	ctx := context.Background()
	sess, err := store.Create(home, "fixture", "busy Minecraft session")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	add := func(m llm.Message) {
		if err := writer.AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	big := strings.Repeat("BUILD line: task :compileJava UP-TO-DATE, zombie spawn rule checked\n", 600)
	for i := 0; i < turns; i++ {
		add(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("turn %d: dodaj kolejną regułę spawnu zombie %s", i, strings.Repeat("szczegóły ", 30))})
		call := fmt.Sprintf("build-%d", i)
		add(llm.Message{Role: llm.RoleAssistant, Content: "Sprawdzam build.", ToolCalls: []llm.ToolCall{{ID: call, Name: "ctx_execute", Arguments: `{"command":["gradlew","build"]}`}}})
		out := strings.Repeat("BUILD SUCCESSFUL in 12s\n", 40)
		if i%40 == 0 {
			out = big
		}
		add(llm.Message{Role: llm.RoleTool, Name: "ctx_execute", ToolCallID: call, Content: out})
		if i%15 == 7 {
			id, task := fmt.Sprintf("worker-%d", 100+i), fmt.Sprintf("task-%d", i)
			args, _ := json.Marshal(map[string]string{"agent": "code", "prompt": fmt.Sprintf("Cel: reguła spawnu %d", i)})
			add(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: task, Name: "task", Arguments: string(args)}}})
			add(llm.Message{Role: llm.RoleTool, ToolCallID: task, Name: "task", Content: fmt.Sprintf(`<task-notification><task-id>%s</task-id><agent>code</agent><status>running</status><summary>code running in background</summary></task-notification>`, id)})
			add(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("<task-notification>\n<task-id>%s</task-id>\n<status>completed</status>\n<result>%s</result>\n</task-notification>", id, strings.Repeat("worker report line ", 60))})
		}
		add(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("Reguła %d dodana, build przechodzi.", i)})
	}
	return sess.ID
}
