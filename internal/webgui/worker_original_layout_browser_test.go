package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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

// Optional original source: SUPERCLI_WORKER_UI_SOURCE_DIR/internal/webgui.
// Compare complete cards in the real shell, including custom fonts and scale.
// Synthetic content only: no Engine, real settings, provider, or database.
func TestWorkerOriginalFullCardNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	reference := os.Getenv("SUPERCLI_WORKER_UI_SOURCE_DIR")
	pl, _ := assetsFS.ReadFile("assets/locales/pl.json")
	read := func(old bool, name string) ([]byte, error) {
		if old && reference != "" {
			return os.ReadFile(filepath.Join(reference, filepath.FromSlash(name)))
		}
		if strings.HasPrefix(name, "shared-ui/") {
			return sharedUIFS.ReadFile(name)
		}
		return assetsFS.ReadFile(name)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		old := strings.HasPrefix(r.URL.Path, "/reference/")
		name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/current/"), "/reference/")
		switch name {
		case "":
			index, err := read(old, "assets/index.html")
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
			fmt.Fprint(w, shell[:at], `<script>window.fixtureErrors=[];window.addEventListener('error',e=>fixtureErrors.push(e.message));</script><script src="runtime.js"></script><script src="js/00-helpers.js"></script><script src="catalog.js"></script><script src="js/01-i18n.js"></script><script src="js/02-ui-settings.js"></script><script src="js/03-markdown.js"></script><script src="js/03b-highlight.js"></script><script src="js/04-transcript.js"></script><script src="js/05-chat.js"></script><script src="js/08-sessions.js"></script><script src="fixture.js"></script></body></html>`)
		case "catalog.js":
			w.Header().Set("Content-Type", "text/javascript")
			// Equal translated strings isolate appearance from wording changes.
			fmt.Fprintf(w, "var I18N={pl:%s,en:%s};var UI_LANGUAGES=[{code:'pl',name:'Polski'}];", pl, pl)
		case "fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, workerOriginalLayoutFixture)
		default:
			if strings.HasPrefix(name, "api/") {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{}`)
				return
			}
			asset := "assets/" + name
			if name == "runtime.js" {
				asset = "shared-ui/runtime.js"
			}
			b, err := read(old, asset)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(name)))
			_, _ = w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1280, Height: 950})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	type metric struct {
		Box   []float64 `json:"box"`
		Style []string  `json:"style"`
	}
	for _, profile := range []string{"system", "georgia-courier-large"} {
		for _, theme := range []string{"dark", "light", "midnight"} {
			t.Run(profile+"/"+theme, func(t *testing.T) {
				measure := func(old bool) map[string]metric {
					t.Helper()
					prefix := "/current/"
					if old {
						prefix = "/reference/"
					}
					page, err := browser.NewPage(ctx, server.URL+prefix+"?profile="+profile+"&theme="+theme)
					if err != nil {
						t.Fatal(err)
					}
					// Activate this owned headless target before awaiting layout frames.
					if _, err = page.Screenshot(ctx); err != nil {
						t.Fatal(err)
					}
					var result struct {
						Failure string            `json:"failure"`
						Layout  map[string]metric `json:"layout"`
					}
					if err = page.Eval(ctx, fmt.Sprintf(`measureWorkerOriginalLayout(%t)`, old), &result); err != nil {
						t.Fatal(err)
					}
					if result.Failure != "" {
						t.Fatal(result.Failure)
					}
					if stage := os.Getenv("SUPERCLI_WORKER_UI_STAGE"); stage != "" {
						shot, err := page.Screenshot(ctx)
						if err != nil {
							t.Fatal(err)
						}
						label := "current"
						if old {
							label = "original"
						}
						if err = os.WriteFile(filepath.Join(stage, "full-card-"+profile+"-"+theme+"-"+label+".png"), shot, 0600); err != nil {
							t.Fatal(err)
						}
					}
					return result.Layout
				}
				current := measure(false)
				if reference == "" {
					return // Fixture also checks fonts, compactness and expansion.
				}
				old := measure(true)
				for name, now := range current {
					before, ok := old[name]
					if !ok {
						t.Errorf("original is missing %s", name)
						continue
					}
					for i, value := range now.Box {
						if math.Abs(value-before.Box[i]) > 1 {
							t.Errorf("%s geometry %d: current %.2f, original %.2f", name, i, value, before.Box[i])
						}
					}
					nowStyle, _ := json.Marshal(now.Style)
					oldStyle, _ := json.Marshal(before.Style)
					if string(nowStyle) != string(oldStyle) {
						t.Errorf("%s style: current %s, original %s", name, nowStyle, oldStyle)
					}
				}
				t.Logf("compared %d full-shell card elements with original source", len(current))
			})
		}
	}
}

const workerOriginalLayoutFixture = `
ui.lang='pl';ui.sidebarHidden=true;ui.toolsExpanded=true;ui.theme=new URLSearchParams(location.search).get('theme')||'dark';
var custom=new URLSearchParams(location.search).get('profile')==='georgia-courier-large';
ui.uiFont=custom?'georgia':'system';ui.codeFont=custom?'courier':'system';ui.uiScale=custom?'large':'normal';
var activeSessionID='fixture';transcriptSessionID='fixture';
function pauseWorkerStatusSync(){}function resumeWorkerStatusSync(){}
applyUI();hideWelcome();smartScroll=function(){};
addUserMsg('Sprawdź kod i podaj krótkie podsumowanie.');
addToolCall('read_lines','{"file":"internal/agent/loop.go","from":10,"to":12}','read');
addToolResult('read','10 | func run() {\n11 |     return\n12 | }');
addToolCall('task',JSON.stringify({agent:'review',prompt:'Sprawdź projekt.\nPotem podaj wynik.'}),'delegation');
addWorkerProgress({id:'worker-1',name:'review',kind:'started',parent_call_id:'delegation',run:1,model:'fixture-model',reasoning_effort:'high'});
['read_lines','ctx_execute','search_code'].forEach(function(tool,index){
  var args=tool==='ctx_execute'?{command:['go','test','./...']}:{file:'internal/agent/loop.go',query:'worker'};
  addWorkerProgress({id:'worker-1',name:'review',kind:'tool_call',parent_call_id:'delegation',run:1,call_id:'call-'+index,tool:tool,args:JSON.stringify(args)});
  addWorkerProgress({id:'worker-1',name:'review',kind:'tool_result',parent_call_id:'delegation',run:1,call_id:'call-'+index,tool:tool,output:'RESULT '+index});
});
addToolResult('delegation','<task-notification><task-id>worker-1</task-id><agent>review</agent><status>done</status><summary>review done · 4 steps · 200 in/40 out tok</summary><tools>read_lines, ctx_execute, search_code</tools><result>Sprawdzenie zakończone.</result></task-notification>');
Object.values(toolRows).forEach(function(row){row.open=true;row.dispatchEvent(new Event('toggle'));row._body.hidden=false;clearInterval(row._clock);});
async function measureWorkerOriginalLayout(old){try{
await new Promise(requestAnimationFrame);
function check(ok,why){if(!ok)throw Error(why);}
check(!fixtureErrors.length,'fixture errors: '+fixtureErrors.join('; '));
var row=toolRows.delegation,read=toolRows.read,items=Array.from(row.querySelectorAll('.task-activity-item'));
check(items.length===3,'missing tool rows');
var layout={};
function measure(name,node){check(node,'missing '+name);var b=node.getBoundingClientRect(),s=getComputedStyle(node);layout[name]={box:[b.x,b.y,b.width,b.height],style:[s.fontFamily,s.fontSize,s.fontWeight,s.lineHeight,s.padding,s.margin,s.borderTopWidth,s.borderRadius,s.columnGap,s.rowGap,s.color,s.backgroundColor]};}
measure('tool',read);measure('tool-header',read.querySelector('summary'));measure('tool-body',read._body);measure('tool-title',read._tname);measure('tool-code',read.querySelector('.file-line-code'));
measure('card',row);measure('header',row.querySelector('summary'));measure('body',row._body);measure('title',row._tname);measure('brief',row.querySelector('.task-brief'));measure('activity',row._activity);measure('report',row.querySelector('.task-report'));
items.forEach(function(item,i){measure('activity-'+i,item);check(item.getBoundingClientRect().height<27,'activity no longer compact');['activity-dot','activity-name','activity-hint','activity-status'].forEach(function(c){measure('activity-'+i+'-'+c,item.querySelector('.'+c));});});
check(getComputedStyle(row.querySelector('.task-brief')).fontFamily.includes(custom?'Georgia':'Segoe UI'),'UI font preference lost');
check(getComputedStyle(items[0]).fontFamily.includes(custom?'Courier New':'Consolas'),'code font preference lost');
if(!old){
  var detail=items[0].querySelector('.activity-result');check(detail&&!detail.open,'result not initially compact');
  detail.querySelector('summary').click();check(detail.open&&detail.textContent.includes('RESULT 0'),'row does not reveal result');
  detail.querySelector('summary').click();check(!detail.open,'result cannot collapse');
  check(Math.abs(items[0].getBoundingClientRect().height-layout['activity-0'].box[3])<1,'collapse left an extra row');
}
row.open=false;row._body.hidden=true;await new Promise(requestAnimationFrame);measure('collapsed-card',row);row.open=true;row._body.hidden=false;
read.open=false;read._body.hidden=true;await new Promise(requestAnimationFrame);measure('collapsed-tool',read);read.open=true;read._body.hidden=false;
// Colour parity is state-dependent. Checking one completed read misses run,
// write, web, failures and the names-only history fallback.
function colors(name,node){check(node,'missing colour target '+name);var s=getComputedStyle(node);layout['colors-'+name]={box:[],style:[s.color,s.backgroundColor,s.borderLeftColor,s.borderTopColor,s.opacity,s.filter]};}
['read_lines','create_file','ctx_execute','web_fetch'].forEach(function(tool,i){
  var key='palette-'+i,args=JSON.stringify({file:'file.go',path:'file.go',command:['go','test'],url:'https://example.invalid'});
  addToolCall(tool,args,key);var ordinary=toolRows[key];clearInterval(ordinary._clock);
  colors(tool+'-running-title',ordinary._tname);colors(tool+'-running-status',ordinary._stat);colors(tool+'-running-card',ordinary);
  // The main sample is finished. Use a new live row for lifecycle colours.
  if(!toolRows['palette-worker']){addToolCall('task','{"agent":"review","prompt":"Palette"}','palette-worker');addWorkerProgress({id:'worker-2',name:'review',kind:'started',parent_call_id:'palette-worker',run:1});}
  var live=toolRows['palette-worker'];clearInterval(live._clock);
  addWorkerProgress({id:'worker-2',name:'review',kind:'tool_call',parent_call_id:'palette-worker',run:1,call_id:key,tool:tool,args:args});
  var item=live._workerCalls[key];
  colors(tool+'-activity-running-name',item.querySelector('.activity-name'));colors(tool+'-activity-running-dot',item.querySelector('.activity-dot'));colors(tool+'-activity-running-status',item.querySelector('.activity-status'));
  addWorkerProgress({id:'worker-2',name:'review',kind:'tool_result',parent_call_id:'palette-worker',run:1,call_id:key,tool:tool,output:'ok'});
  colors(tool+'-activity-done-name',item.querySelector('.activity-name'));colors(tool+'-activity-done-dot',item.querySelector('.activity-dot'));colors(tool+'-activity-done-status',item.querySelector('.activity-status'));
  addToolResult(key,'ok');colors(tool+'-done-card',ordinary);colors(tool+'-done-title',ordinary._tname);
  var failed=key+'-failed';addToolCall(tool,args,failed);addToolResult(failed,'','failed');var failure=toolRows[failed];
  colors(tool+'-failed-card',failure);colors(tool+'-failed-title',failure._tname);colors(tool+'-failed-status',failure._stat);
  addWorkerProgress({id:'worker-2',name:'review',kind:'tool_call',parent_call_id:'palette-worker',run:1,call_id:failed,tool:tool,args:args});
  addWorkerProgress({id:'worker-2',name:'review',kind:'tool_result',parent_call_id:'palette-worker',run:1,call_id:failed,tool:tool,err:'failed'});
  var failedItem=live._workerCalls[failed];colors(tool+'-activity-failed-name',failedItem.querySelector('.activity-name'));colors(tool+'-activity-failed-dot',failedItem.querySelector('.activity-dot'));colors(tool+'-activity-failed-status',failedItem.querySelector('.activity-status'));
});
var live=toolRows['palette-worker'];colors('worker-running',live);colors('worker-running-status',live._stat);
['done','failed','stopped'].forEach(function(state){
  var note={id:'worker-2',agent:'review',status:state,summary:'review '+state,tools:'read_lines, create_file, ctx_execute, web_fetch',result:'Report'};
  var saved=addHistoryTask(note);saved.open=true;saved._body.hidden=false;saved.dispatchEvent(new Event('toggle'));
  colors('history-'+state,saved);colors('history-'+state+'-title',saved._tname);colors('history-'+state+'-status',saved._stat);
  var names=Array.from(saved.querySelectorAll('.activity-name'));check(names.length===4,'legacy history lost tool names');
  names.forEach(function(name,i){colors('history-'+state+'-tool-'+i,name);});
  if(!old){
    var status=saved.querySelector('.activity-status');check(status.textContent==='—','names-only history claims a verified tool outcome');
    check(getComputedStyle(status).color===getComputedStyle(document.documentElement).getPropertyValue('--faint').trim()||getComputedStyle(status).color===getComputedStyle(saved.querySelector('.activity-hint')).color,'unknown status should remain muted');
  }
});
Object.values(toolRows).forEach(function(r){clearInterval(r._clock);});
return {layout:layout};
}catch(e){return {failure:e.stack||String(e)};}}
`
