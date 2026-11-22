package webgui

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// A running worker card must not flicker: clock ticks, status polls, replayed
// starts and the running acknowledgement may only touch the elapsed span. Long
// task text uses the shared sidebar marquee (static ellipsis under reduced motion).
func TestWorkerTaskSummaryStableMarqueeNativeBrowser(t *testing.T) {
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
			fmt.Fprint(w, shell[:at], `<script>window.fixtureErrors=[];window.addEventListener('error',e=>fixtureErrors.push(e.message));</script><script src="runtime.js"></script><script src="js/00-helpers.js"></script><script src="catalog.js"></script><script src="js/01-i18n.js"></script><script src="js/02-ui-settings.js"></script><script src="js/03-markdown.js"></script><script src="js/03b-highlight.js"></script><script src="js/04-transcript.js"></script><script src="js/05-chat.js"></script><script src="js/08-sessions.js"></script><script src="fixture.js"></script></body></html>`)
		case "catalog.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprintf(w, "var I18N={pl:%s,en:%s};var UI_LANGUAGES=[{code:'pl',name:'Polski'}];", pl, pl)
		case "fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, workerTaskMarqueeFixture)
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
	// Activate this headless target so animation frames run.
	if _, err = page.Screenshot(ctx); err != nil {
		t.Fatal(err)
	}
	var failure string
	if err = page.Eval(ctx, `checkWorkerTaskSummaryStable()`, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
	if err = page.EmulateMediaFeature(ctx, "prefers-reduced-motion", "reduce"); err != nil {
		t.Fatal(err)
	}
	if err = page.Eval(ctx, `checkWorkerTaskReducedMotion()`, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
}

const workerTaskMarqueeFixture = `
ui.lang='pl';ui.sidebarHidden=true;ui.theme='dark';applyUI();hideWelcome();smartScroll=function(){};
var activeSessionID='fixture';transcriptSessionID='fixture';
var LONG='Zbadaj dokładnie moduł internal/agent i sprawdź, dlaczego orkiestrator nie budzi się po zakończeniu pracy workerów; przygotuj poprawkę i testy regresyjne dla wszystkich ścieżek.';
function check(ok,why){if(!ok)throw Error(why);}
function frame(){return new Promise(requestAnimationFrame);}
function wait(ms){return new Promise(function(r){setTimeout(r,ms);});}
var ELAPSED=/\d+m\d\ds$/;
function startWorker(call,id,prompt,ago){
  addToolCall('task',JSON.stringify({agent:'code',prompt:prompt}),call,Date.now()-ago);
  addWorkerProgress({id:id,name:'code',kind:'started',parent_call_id:call,run:1,model:'gpt-5.5-codex',reasoning_effort:'xhigh'});
  addToolResult(call,'<task-notification><task-id>'+id+'</task-id><agent>code</agent><status>running</status><summary>started</summary></task-notification>');
  return toolRows[call];
}
async function checkWorkerTaskSummaryStable(){try{
  check(!fixtureErrors.length,'fixture errors: '+fixtureErrors.join('; '));
  var row=startWorker('long','worker-7',LONG,287000),short=startWorker('short','worker-8','Krótkie zadanie.',65000);
  await frame();await frame();
  var hint=row._thint,inner=hint.firstChild,name=row._tname,title=name.firstChild,label=row._modelLabel,time=row.querySelector('.tstat-time');
  check(inner&&inner.classList.contains('marquee-text')&&hint.childNodes.length===1,'task text lost its marquee span');
  check(inner.textContent===LONG&&hint.title===LONG,'running acknowledgement replaced the task text: '+inner.textContent);
  check(label&&label.parentNode===name&&label.textContent.indexOf('xhigh')>=0,'model label missing');
  check(time&&ELAPSED.test(time.textContent),'elapsed span missing: '+row._stat.textContent);
  check(getComputedStyle(time).fontVariantNumeric.indexOf('tabular-nums')>=0,'elapsed digits are proportional');
  // 1.0.39: a delegation card cuts its goal on the right with an ellipsis
  // (a scrolling marquee showed it cut on the left in a still view).
  check(!hint.classList.contains('marquee-overflow')&&getComputedStyle(inner).animationName==='none','task text must not scroll');
  check(getComputedStyle(inner).textOverflow==='ellipsis'&&inner.scrollWidth>inner.clientWidth,'overflowing task text is not cut with an ellipsis on the right');
  check(!short._thint.classList.contains('marquee-overflow')&&getComputedStyle(short._thint.firstChild).animationName==='none','short task text must not scroll');
  var stat0=row._stat.textContent.replace(ELAPSED,'');
  var seen=[],mo=new MutationObserver(function(list){seen.push.apply(seen,list);});
  mo.observe(row.querySelector('summary'),{subtree:true,childList:true,characterData:true,attributes:true,attributeFilter:['class','style']});
  var times={},syncs=0,realSync=syncMarquee;
  var syncLog=[];syncMarquee=function(v){syncs++;syncLog.push(v.clientWidth+'px '+v.textContent.slice(0,12));return realSync.apply(this,arguments);};
  for(var i=0;i<5;i++){
    await wait(270);
    // Every source that used to rewrite the summary: status poll, replayed
    // start, worker tool activity and the clock itself.
    applyWorkerStatusSnapshot([{id:'worker-7',run:1,status:'running'},{id:'worker-8',run:1,status:'running'}],workerStatusBindings());
    check(row._stat.textContent.replace(ELAPSED,'')===stat0&&ELAPSED.test(row._stat.textContent),'status poll dropped the elapsed time: '+row._stat.textContent);
    addWorkerProgress({id:'worker-7',name:'code',kind:'started',parent_call_id:'long',run:1,model:'gpt-5.5-codex',reasoning_effort:'xhigh'});
    addWorkerProgress({id:'worker-7',name:'code',kind:'tool_call',parent_call_id:'long',run:1,call_id:'c'+i,tool:'read_lines',args:'{"file":"a.go"}'});
    addWorkerProgress({id:'worker-7',name:'code',kind:'tool_result',parent_call_id:'long',run:1,call_id:'c'+i,tool:'read_lines',output:'ok'});
    times[time.textContent]=true;
    await frame();
  }
  seen=seen.concat(mo.takeRecords());mo.disconnect();syncMarquee=realSync;
  check(syncs===0,'marquee re-measured '+syncs+' times without a resize or text change: '+syncLog.join(" | "));
  check(Object.keys(times).length>1,'clock did not tick');
  var stray=seen.filter(function(m){return m.target!==time&&m.target.parentNode!==time;});
  check(!stray.length,'summary rewritten outside the elapsed span: '+stray.map(function(m){return m.type+' '+(m.target.className||m.target.nodeName)+' '+(m.attributeName||'');}).join(', '));
  check(hint.firstChild===inner&&inner.textContent===LONG&&name.firstChild===title&&row._modelLabel===label&&label.parentNode===name&&row.querySelector('.tstat-time')===time,'summary nodes were replaced');
  // Narrowing the card cuts the text with an ellipsis; it never scrolls.
  var medium=startWorker('medium','worker-10','Sprawdź testy pakietu webgui.',1000);
  await wait(60);await frame();
  check(!medium._thint.classList.contains('marquee-overflow'),'fitting task text scrolls');
  medium.style.width='420px';await wait(60);await frame();
  check(!medium._thint.classList.contains('marquee-overflow')&&getComputedStyle(medium._thint.firstChild).textOverflow==='ellipsis','narrowed card scrolls instead of an ellipsis');
  medium.style.width='';await wait(60);await frame();
  check(!medium._thint.classList.contains('marquee-overflow'),'widened card scrolls');
  addWorkerProgress({id:'worker-7',name:'code',kind:'finished',parent_call_id:'long',run:1,status:'done'});
  check(!row._clock&&ELAPSED.test(row._stat.textContent),'finished status lost its elapsed time: '+row._stat.textContent);
  check(!fixtureErrors.length,'errors: '+fixtureErrors.join('; '));
  return '';
}catch(e){return e.stack||String(e);}}
async function checkWorkerTaskReducedMotion(){try{
  check(matchMedia('(prefers-reduced-motion: reduce)').matches,'reduced motion not emulated');
  var row=startWorker('calm','worker-9',LONG,1000);
  await frame();await frame();
  var hint=row._thint,inner=hint.firstChild,s=getComputedStyle(inner);
  check(s.animationName==='none','reduced motion still scrolls: '+s.animationName);
  check(s.textOverflow==='ellipsis','reduced motion lost the static ellipsis');
  check(hint.title===LONG&&inner.textContent===LONG,'full task text not kept');
  check(!fixtureErrors.length,'errors: '+fixtureErrors.join('; '));
  Object.values(toolRows).forEach(function(r){clearInterval(r._clock);});
  return '';
}catch(e){return e.stack||String(e);}}
`
