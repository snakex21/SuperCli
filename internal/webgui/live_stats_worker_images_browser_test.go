package webgui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// Production transcript/chat scripts in Chromium, no Engine or model:
//   - a mid-turn tool call refreshes the visible stats pane once (throttled),
//     and never while the stats tab or sidebar is hidden;
//   - a worker's tool image shows as a thumbnail in its delegation activity,
//     live and when restored from saved history.
func TestLiveStatsAndWorkerImagesNativeBrowser(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	index, err := assetsFS.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	en, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			shell := string(index)
			shell = shell[:strings.Index(shell, "  <script src=")]
			fmt.Fprint(w, shell, `<script>window.fixtureErrors=[];window.addEventListener('error',e=>fixtureErrors.push(e.message));</script><script src="/runtime.js"></script><script src="/js/00-helpers.js"></script><script src="/catalog.js"></script><script src="/js/01-i18n.js"></script><script src="/js/02-ui-settings.js"></script><script src="/js/03-markdown.js"></script><script src="/js/03b-highlight.js"></script><script src="/js/04-transcript.js"></script><script src="/js/04c-tool-views.js"></script><script src="/js/05-chat.js"></script><script src="/js/08-sessions.js"></script><script src="/fixture.js"></script></body></html>`)
		case "/runtime.js":
			w.Header().Set("Content-Type", "text/javascript")
			b, _ := sharedUIFS.ReadFile("shared-ui/runtime.js")
			w.Write(b)
		case "/catalog.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprintf(w, "var I18N={en:%s};var UI_LANGUAGES=[{code:'en',name:'English'}];", en)
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, `var activeWorkspacePath='';window.statsRenders=0;
function renderStats(){window.statsRenders++;} function renderAttachments(){} function updateProjectLabel(){}
window.fixtureReady=true;`)
		default:
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{}`)
				return
			}
			b, e := assetsFS.ReadFile("assets" + r.URL.Path)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			if strings.HasSuffix(r.URL.Path, ".js") {
				w.Header().Set("Content-Type", "text/javascript")
			} else if strings.HasSuffix(r.URL.Path, ".css") {
				w.Header().Set("Content-Type", "text/css")
			}
			w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1200, Height: 900})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var failure string
	script := `(async()=>{try{
function check(ok,why){if(!ok)throw Error(why);}
function wait(ms){return new Promise(function(r){setTimeout(r,ms);});}
check(window.fixtureReady&&!fixtureErrors.length,'scripts failed: '+fixtureErrors.join(';'));
var tab=document.querySelector('#side-tabs button[data-tab="stats"]');check(tab,'stats tab missing');
tab.id='side-tab-stats';
function clocks(){Object.values(toolRows).forEach(function(r){clearInterval(r._clock);});}
function call(id){handleEvent({type:'tool_call',name:'read_lines',args:'{"file":"a.go"}',id:id},null);}

// 1. Visible stats tab: a burst of tool calls refreshes once after ~1s.
ui.sidebarHidden=false;tab.classList.add('active');statsRenders=0;
call('a');call('b');call('c');
check(statsRenders===0,'stats refreshed synchronously on every tool call');
await wait(1300);
check(statsRenders===1,'mid-turn refresh count '+statsRenders+', want 1');
call('d');await wait(1300);
check(statsRenders===2,'later tool call did not refresh again: '+statsRenders);
// 2. Another tab or a hidden sidebar does no work.
tab.classList.remove('active');statsRenders=0;call('e');await wait(1300);
check(statsRenders===0,'hidden stats tab was refreshed');
tab.classList.add('active');ui.sidebarHidden=true;call('f');await wait(1300);
check(statsRenders===0,'hidden sidebar was refreshed');
ui.sidebarHidden=false;tab.classList.remove('active');clocks();

// 3. Live worker image: thumbnail appears only when the call is opened.
activeSessionID=transcriptSessionID='fixture';
var hex='a'.repeat(64),token='session:fixture/'+hex+'.png';
handleEvent({type:'tool_call',name:'task',args:JSON.stringify({agent:'explore',prompt:'Look at the picture'}),id:'spawn'},null);
handleEvent({type:'worker_progress',id:'worker-1',name:'explore',kind:'started',run:1,parent_call_id:'spawn'},null);
handleEvent({type:'worker_progress',id:'worker-1',name:'explore',kind:'tool_call',run:1,parent_call_id:'spawn',call_id:'img',tool:'read_image',args:'{"path":"pic.png"}'},null);
handleEvent({type:'worker_progress',id:'worker-1',name:'explore',kind:'tool_result',run:1,parent_call_id:'spawn',call_id:'img',output:'image read',images:[token,'https://evil.test/x.png']},null);
var row=toolRows.spawn;check(row,'delegation row missing');
row.open=true;row.dispatchEvent(new Event('toggle'));
var item=row.querySelector('.task-activity-item');check(item,'worker call missing');
var detail=item.querySelector('.activity-result');check(detail,'worker call has no expandable detail');
check(!detail.querySelector('.activity-images'),'thumbnail rendered while folded');
detail.open=true;detail.dispatchEvent(new Event('toggle'));
var live=detail.querySelector('.activity-images');check(live,'live worker thumbnail missing');
check(live.querySelectorAll('img').length===1,'live thumbnails: '+live.querySelectorAll('img').length);
check(!live.innerHTML.includes('evil.test'),'remote URL accepted as worker image');
clocks();

// 4. Saved history keeps bare file names; they resolve to this session.
var box=document.createElement('div');
fillRecordedWorkerActivity(box,{calls:[{id:'h',tool:'read_image',args:'{"path":"pic.png"}',output:'image read',completed:true,images:[hex+'.png','../escape.png']}],omitted:0},null);
document.body.appendChild(box);
var saved=box.querySelector('.activity-result');check(saved,'history call has no detail');
saved.open=true;saved.dispatchEvent(new Event('toggle'));
var restored=saved.querySelector('.activity-images');check(restored,'history worker thumbnail missing');
check(restored.querySelectorAll('img').length===1,'history thumbnails: '+restored.querySelectorAll('img').length);
check(!restored.innerHTML.includes('escape'),'unsafe saved name accepted');
check(!fixtureErrors.length,'browser errors: '+fixtureErrors.join(';'));
return '';
}catch(e){return e.stack||String(e);}})()`
	if err = page.Eval(ctx, script, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
}
