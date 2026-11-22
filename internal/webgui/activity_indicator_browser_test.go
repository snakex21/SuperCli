package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// The activity line maps events to one short translated state, never flashes
// steps shorter than a second, warns about silence only past the step's limit
// (a command: its own timeout; the user's turn: never) and disappears when
// the turn ends. Every check drives the pure model with an explicit clock.
func TestActivityIndicatorStatesSilenceAndEndNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	pl, err := assetsFS.ReadFile("assets/locales/pl.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		switch name {
		case "":
			index, err := assetsFS.ReadFile("assets/index.html")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			shell := string(index)
			at := strings.Index(shell, "  <script src=")
			if at < 0 {
				http.Error(w, "missing scripts", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, shell[:at], `<script>window.fixtureErrors=[];window.addEventListener('error',e=>fixtureErrors.push(e.message));</script><script src="runtime.js"></script><script src="js/00-helpers.js"></script><script src="catalog.js"></script><script src="js/01-i18n.js"></script><script src="js/02-ui-settings.js"></script><script src="js/03-markdown.js"></script><script src="js/03b-highlight.js"></script><script src="js/04-transcript.js"></script><script src="js/05-chat.js"></script><script src="js/05f-activity.js"></script><script src="js/08-sessions.js"></script><script src="fixture.js"></script></body></html>`)
		case "catalog.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprintf(w, "var I18N={pl:%s,en:%s};var UI_LANGUAGES=[{code:'pl',name:'Polski'}];", pl, pl)
		case "fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, activityIndicatorFixture)
		default:
			if strings.HasPrefix(name, "api/") {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{}`)
				return
			}
			var b []byte
			var err error
			if name == "runtime.js" {
				b, err = sharedUIFS.ReadFile("shared-ui/runtime.js")
			} else {
				b, err = assetsFS.ReadFile("assets/" + name)
			}
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(name)))
			_, _ = w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1280, Height: 800})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []string{"checkActivityStates()", "checkActivityDebounce()", "checkActivitySilence()", "checkActivityLine()", "checkWorkerActivityLine()", "checkMessageStatsAndReasoning()"} {
		var failure string
		if err := page.Eval(ctx, check, &failure); err != nil {
			t.Fatalf("%s: %v", check, err)
		}
		if failure != "" {
			t.Fatalf("%s: %s", check, failure)
		}
	}
	if dir := os.Getenv("SUPERCLI_ACTIVITY_SHOTS"); dir != "" {
		png, err := page.Screenshot(ctx)
		if err == nil {
			_ = os.WriteFile(filepath.Join(dir, "unit-fixture.png"), png, 0o644)
		}
	}
}

func TestRuntimeReportsCommandTimeout(t *testing.T) {
	srv := newTestServer(t, false)
	rec := httptest.NewRecorder()
	srv.handleRuntime(rec, httptest.NewRequest(http.MethodGet, "/api/runtime", nil))
	var payload struct {
		CommandTimeoutMS *int `json:"command_timeout_ms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CommandTimeoutMS == nil || *payload.CommandTimeoutMS <= 0 {
		t.Fatalf("runtime must report the default command timeout: %s", rec.Body.String())
	}
}

const activityIndicatorFixture = `
ui.lang='pl';ui.sidebarHidden=true;ui.theme='dark';applyUI();hideWelcome();smartScroll=function(){};
var activeSessionID='fixture';transcriptSessionID='fixture';
function check(ok,why){if(!ok)throw Error(why);}
function args(v){return JSON.stringify(v);}
function label(m,now,user){var v=runActivityView(m,now,!!user);return v?v.label:null;}
function run(fn){try{fn();return '';}catch(e){return e.message;}}
function checkActivityStates(){return run(function(){
  check(!fixtureErrors.length,'fixture errors: '+fixtureErrors.join('; '));
  var T=1000000,m=runActivityNew(T);
  check(label(m,T)==='Myśli…','start: '+label(m,T));
  runActivityNote(m,{type:'message',text:'Hej'},T+10);
  check(label(m,T+2000)==='Pisze odpowiedź…','writing: '+label(m,T+2000));
  runActivityNote(m,{type:'tool_call',id:'c1',name:'ctx_execute',args:args({command:['go','test','./...']})},T+3000);
  var v=runActivityView(m,T+5000);
  check(v.label==='Uruchamia testy'&&v.step==='0:02','tests: '+JSON.stringify(v));
  check(v.detail==='go test ./...','full command only in details: '+v.detail);
  check(v.turn==='0:05','turn clock: '+v.turn);
  runActivityNote(m,{type:'tool_result',id:'c1',output:'ok'},T+6000);
  runActivityNote(m,{type:'tool_call',id:'c2',name:'create_file',args:args({path:'internal/agent/main.go',content:'x'})},T+6100);
  check(label(m,T+8000)==='Edytuje main.go','edit: '+label(m,T+8000));
  check(runActivityView(m,T+8000).detail==='internal/agent/main.go','path only in details');
  runActivityNote(m,{type:'tool_result',id:'c2'},T+8100);
  runActivityNote(m,{type:'tool_call',id:'c3',name:'ctx_execute',args:args({command:'npm run lint'})},T+8200);
  check(label(m,T+10000)==='Uruchamia polecenie','command: '+label(m,T+10000));
  runActivityNote(m,{type:'tool_result',id:'c3'},T+10100);
  runActivityNote(m,{type:'tool_call',id:'c4',name:'task',args:args({agent:'code',prompt:'Napraw testy'})},T+10200);
  runActivityNote(m,{type:'worker_progress',kind:'started',id:'worker-3',parent_call_id:'c4',run:1},T+10300);
  check(label(m,T+12000)==='Czeka na: Worker 3','worker: '+label(m,T+12000));
  runActivityNote(m,{type:'question',question:{id:'q1',question:'Który?'}},T+12500);
  check(label(m,T+12600)==='Czeka na Twoją odpowiedź','user wait shown at once: '+label(m,T+12600));
  runActivityNote(m,{type:'question_closed',id:'q1'},T+13000);
  runActivityNote(m,{type:'tool_result',id:'c4'},T+14000);
  runActivityNote(m,{type:'notice',text:'connection to the model dropped (EOF) — retrying in 2s (retry 2/5)'},T+15000);
  check(label(m,T+16100)==='Ponawia po błędzie połączenia (2/5)','retry: '+label(m,T+16100));
  runActivityNote(m,{type:'notice',text:'fake: rate limited (HTTP 429) — retrying in 1m0s (retry 1/3)'},T+17000);
  check(label(m,T+18100)==='Limit zapytań, ponawia (1/3)','rate limit: '+label(m,T+18100));
  runActivityNote(m,{type:'tool_call',id:'c5',name:'send_message',args:args({to:'worker-2',message:'dalej'})},T+19000);
  check(label(m,T+20100)==='Czeka na: Worker 2','send_message: '+label(m,T+20100));
  runActivityNote(m,{type:'tool_call',id:'c6',name:'task',args:args({prompt:'tło',async:true})},T+20200);
  runActivityNote(m,{type:'tool_result',id:'c6'},T+20300);
  check(label(m,T+21000)==='Czeka na: Worker 2','async delegation is not a wait: '+label(m,T+21000));
  runActivityNote(m,{type:'done'},T+22000);
  check(runActivityView(m,T+22000)===null,'done must clear the line');
});}
function checkActivityDebounce(){return run(function(){
  var T=2000000,m=runActivityNew(T);
  check(label(m,T+5000)==='Myśli…','thinking');
  // A 400 ms read never flashes; the thinking clock keeps running.
  runActivityNote(m,{type:'tool_call',id:'r1',name:'read_lines',args:args({path:'a.go'})},T+5000);
  var mid=runActivityView(m,T+5300);
  check(mid.label==='Myśli…'&&mid.step==='0:05','short step flashed: '+JSON.stringify(mid));
  runActivityNote(m,{type:'tool_result',id:'r1'},T+5400);
  runActivityNote(m,{type:'reasoning',text:'…'},T+5500);
  var after=runActivityView(m,T+6000);
  check(after.label==='Myśli…'&&after.step==='0:06','thinking clock reset by a hidden step: '+JSON.stringify(after));
  // A step lasting over a second is shown, at most once per second.
  runActivityNote(m,{type:'tool_call',id:'r2',name:'ctx_execute',args:args({command:['go','build','./...']})},T+7000);
  check(label(m,T+7500)==='Myśli…','changed before 1 s');
  check(label(m,T+8100)==='Buduje','long step not shown: '+label(m,T+8100));
  runActivityNote(m,{type:'tool_result',id:'r2'},T+8200);
  runActivityNote(m,{type:'tool_call',id:'r3',name:'web_search',args:args({query:'x'})},T+8300);
  check(label(m,T+9000)==='Buduje','label changed twice within a second: '+label(m,T+9000));
  check(label(m,T+9400)==='Szuka w sieci','next step: '+label(m,T+9400));
  // The history keeps steps (details on expand), capped.
  check(m.history.length>=3,'history: '+JSON.stringify(m.history));
});}
function checkActivitySilence(){return run(function(){
  var T=3000000,m=runActivityNew(T);
  check(!runActivityView(m,T+59000).silent,'silent before 60 s');
  var v=runActivityView(m,T+60000);
  check(v.silent&&v.label==='Brak aktywności od 1 min','quiet: '+JSON.stringify(v));
  runActivityNote(m,{type:'reasoning',text:'.'},T+61000);
  check(!runActivityView(m,T+62000).silent,'an event ends the silence');
  // A command may stay quiet until its own timeout.
  runActivityNote(m,{type:'tool_call',id:'k',name:'ctx_execute',args:args({command:'go test ./...',timeout_ms:180000})},T+70000);
  check(!runActivityView(m,T+70000+170000).silent,'command flagged before its timeout');
  var c=runActivityView(m,T+70000+181000);
  check(c.silent&&c.label==='Brak aktywności od 3 min','command timeout: '+JSON.stringify(c));
  runActivityNote(m,{type:'tool_result',id:'k'},T+300000);
  // Configured default timeout (no timeout_ms), and 0 = no limit.
  var saved=activityCommandTimeoutMS;
  activityCommandTimeoutMS=0;
  runActivityNote(m,{type:'tool_call',id:'n',name:'ctx_execute',args:args({command:'zig build'})},T+301000);
  check(!runActivityView(m,T+301000+3600000).silent,'unlimited command flagged');
  activityCommandTimeoutMS=saved;
  runActivityNote(m,{type:'tool_result',id:'n'},T+4000000);
  // A synchronous worker running a command inherits the command's timeout.
  runActivityNote(m,{type:'tool_call',id:'w',name:'task',args:args({prompt:'x'})},T+4000100);
  runActivityNote(m,{type:'worker_progress',kind:'tool_call',id:'worker-1',parent_call_id:'w',tool:'ctx_execute',args:args({command:'go test',timeout_ms:600000})},T+4000200);
  check(!runActivityView(m,T+4000200+300000).silent,'worker command flagged early');
  check(runActivityView(m,T+4000200+600000).silent,'worker command never flagged');
  // The user's turn is never "no activity".
  var u=runActivityNew(T);
  runActivityNote(u,{type:'question',question:{id:'q'}},T+1000);
  var uv=runActivityView(u,T+3600000,true);
  check(!uv.silent&&uv.label==='Czeka na Twoją odpowiedź','user wait flagged: '+JSON.stringify(uv));
  // A question shown outside this stream (approval) still has a running clock.
  var w=runActivityNew(T);runActivityView(w,T+1000,true);
  check(runActivityView(w,T+5000,true).step==='0:04','user-wait clock stuck: '+runActivityView(w,T+5000,true).step);
  check(activityQuietText(45000)==='45 s'&&activityQuietText(125000)==='2 min','quiet text');
  check(activityClock(72000)==='1:12'&&activityClock(3723000)==='1:02:03','clock');
  check(activityGoDuration('1m30s')===90000&&activityGoDuration('500ms')===500,'go durations');
});}
function checkActivityLine(){return run(function(){
  var now=5000000;runActivityNow=function(){return now;};
  var box=document.getElementById('run-activity'),status=document.getElementById('run-status');
  streaming=true;
  runActivityStart(now);
  check(!box.hidden&&getComputedStyle(status).display==='none','line not shown or #run-status duplicated');
  check(box.querySelector('.run-activity-label').textContent==='Myśli…','label');
  now+=1500;renderRunActivity();
  runActivityEvent({type:'tool_call',id:'x',name:'ctx_execute',args:args({command:['go','test','./...'],timeout_ms:90000})});
  now+=2000;renderRunActivity();
  check(box.querySelector('.run-activity-label').textContent==='Uruchamia testy','dom label: '+box.textContent);
  check(box.querySelector('.run-activity-time').textContent==='0:02','dom time: '+box.textContent);
  check(box.querySelector('.run-activity-line').title.indexOf('go test ./...')>=0,'details on hover');
  check(box.querySelector('.run-activity-continue').hidden,'continue before silence');
  now+=95000;renderRunActivity();
  check(box.classList.contains('silent')&&!box.querySelector('.run-activity-continue').hidden,'silence actions missing');
  check(box.querySelector('.run-activity-label').textContent==='Brak aktywności od 1 min','silent text: '+box.textContent);
  var probe=document.createElement('span');probe.style.color='var(--danger)';document.body.appendChild(probe);
  var danger=getComputedStyle(probe).color;probe.remove();
  check(getComputedStyle(box.querySelector('.run-activity-label')).color!==danger,'silence must not be red');
  // Expand shows the steps of the turn.
  box.querySelector('.run-activity-line').click();
  var steps=document.getElementById('run-activity-steps');
  check(!steps.hidden&&steps.querySelectorAll('.run-activity-step').length>=2,'steps on expand: '+steps.textContent);
  // Turn end: the line and its panel disappear, #run-status comes back.
  finishRunPresentation();
  check(box.hidden&&steps.hidden&&getComputedStyle(status).display!=='none','line left after the turn');
  streaming=false;runFinishing=false;
});}
function checkMessageStatsAndReasoning(){return run(function(){
  // The message's own numbers in human words; only fields with a value.
  var parts=turnMetaParts({tok_in:83140,tok_cached:75399,tok_out:2391},72000,1);
  check(parts.join(' | ')===['1:12','wejście '+fmtCompactNumber(7741),'z cache '+fmtCompactNumber(75399),'wyjście '+fmtCompactNumber(2391),'1 narzędzie'].join(' | '),'stats: '+parts.join(' | '));
  check(turnMetaParts({tok_out:12},4200,0).join(' | ')==='4.2s | wyjście 12','empty fields shown');
  // One reasoning chip per turn: later segments are marked.
  stream.replaceChildren();
  addUserMsg('pytanie');
  var a=addAssistantMsg(),b=addAssistantMsg();
  addUserMsg('drugie');
  var c=addAssistantMsg();
  check(!a.classList.contains('turn-cont')&&b.classList.contains('turn-cont')&&!c.classList.contains('turn-cont'),'turn segments not marked');
});}
function checkWorkerActivityLine(){return run(function(){
  var now=Date.now();workerNow=function(){return now;};
  addToolCall('task',args({agent:'code',prompt:'Dodaj pająka'}),'call-a',now);
  addWorkerProgress({id:'worker-4',name:'code',kind:'started',parent_call_id:'call-a',run:1,at:now});
  addToolResult('call-a','<task-notification><task-id>worker-4</task-id><agent>code</agent><status>running</status><summary>started</summary></task-notification>');
  var item=workerOverview['worker-4'];
  check(item&&item.button.classList.contains('live'),'overview item is not live');
  check(item.live.textContent==='Myśli… · 0:00','thinking line: '+item.live.textContent);
  addWorkerProgress({id:'worker-4',name:'code',kind:'tool_call',call_id:'t1',tool:'ctx_execute',args:args({command:['zig','build','test']}),parent_call_id:'call-a',run:1,at:now});
  now+=1500;renderWorkerLiveness(toolRows['call-a']);
  check(item.live.textContent==='Uruchamia testy · 0:01','worker line: '+item.live.textContent);
  check(item.live.title.indexOf('zig build test')>=0,'command only on hover: '+item.live.title);
  // A status snapshot has no arguments: the running call on the card has them.
  now+=1500;toolRows['call-a']._liveness.args='';renderWorkerLiveness(toolRows['call-a']);
  check(item.live.textContent==='Uruchamia testy · 0:03','snapshot lost the command: '+item.live.textContent);
  check(getComputedStyle(item.detail).display==='none'&&getComputedStyle(item.state).display==='none','duplicate text in the worker line');
  check(item.live.textContent.indexOf('ostatnia')<0,'constant last-activity counter');
  addWorkerProgress({id:'worker-4',name:'code',kind:'finished',status:'done',parent_call_id:'call-a',run:1,at:now});
  check(!item.button.classList.contains('live')&&item.live.hidden,'finished worker still live');
});}
`
