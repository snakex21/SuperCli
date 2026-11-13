package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// The open Goal panel follows data_changed frames: debounced into one fetch,
// redrawn only for changed data, keeping typed input, focus and scroll.
func TestLiveGoalPanelRefreshNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	index, err := assetsFS.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	en, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	taskStatus := "pending"
	var goalFetches atomic.Int32
	goalJSON := func() string {
		mu.Lock()
		defer mu.Unlock()
		tasks := []map[string]any{{"seq": 1, "title": "First step", "status": taskStatus}}
		for i := 2; i <= 40; i++ {
			tasks = append(tasks, map[string]any{"seq": i, "title": fmt.Sprintf("Step %d", i), "status": "pending"})
		}
		b, _ := json.Marshal(map[string]any{"id": "g-1", "title": "Live goal", "status": "active", "scope": "project", "tasks": tasks})
		return string(b)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			shell := string(index)
			shell = shell[:strings.Index(shell, "  <script src=")]
			fmt.Fprint(w, shell, `<script>window.fixtureErrors=[];window.addEventListener('error',e=>fixtureErrors.push(e.message));window.addEventListener('unhandledrejection',e=>fixtureErrors.push(String(e.reason)));Object.defineProperty(document,'hidden',{configurable:true,get:()=>false});var activeSessionID='',toasts=[];function toast(s){toasts.push(s);}</script><script src="/runtime.js"></script><script src="/js/00-helpers.js"></script><script src="/catalog.js"></script><script src="/js/01-i18n.js"></script><script src="/js/02-ui-settings.js"></script><script src="/js/11a-panel-core.js"></script><script src="/js/11f-panel-data-memory.js"></script><script src="/js/11g-panel-goal-usage.js"></script></body></html>`)
		case "/runtime.js":
			w.Header().Set("Content-Type", "text/javascript")
			b, _ := sharedUIFS.ReadFile("shared-ui/runtime.js")
			w.Write(b)
		case "/catalog.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprintf(w, "var I18N={en:%s};var UI_LANGUAGES=[{code:'en',name:'English'}];", en)
		case "/api/goal":
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("catalog") == "1" {
				fmt.Fprint(w, `{"unassigned":[],"paused":[]}`)
				return
			}
			goalFetches.Add(1)
			fmt.Fprint(w, goalJSON())
		case "/api/fixture/complete":
			mu.Lock()
			taskStatus = "done"
			mu.Unlock()
		default:
			b, e := assetsFS.ReadFile("assets" + r.URL.Path)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			if strings.HasSuffix(r.URL.Path, ".js") {
				w.Header().Set("Content-Type", "text/javascript")
			}
			if strings.HasSuffix(r.URL.Path, ".css") {
				w.Header().Set("Content-Type", "text/css")
			}
			w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1000, Height: 600})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var failure string
	if err := page.Eval(ctx, liveGoalPanelScript, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
	// Open + 5 debounced frames (1) + identical-data frame (1).
	if n := goalFetches.Load(); n != 3 {
		t.Fatalf("goal fetches = %d, want 3 (debounce)", n)
	}
}

const liveGoalPanelScript = `(async()=>{try{
 function check(ok,why){if(!ok)throw Error(why);}
 const wait=ms=>new Promise(r=>setTimeout(r,ms));
 check(fixtureErrors.length===0,'setup: '+fixtureErrors.join(';'));
 openPanel('goal');
 for(let i=0;i<100&&!panelContent.querySelector('.goal-task-group');i++)await wait(20);
 check(panelContent.querySelector('.goal-task-group'),'goal panel not rendered');
 check(panelContent.querySelector('.goal-task.status-pending'),'first task not pending');
 const note=panelContent.querySelector('.goal-notes input');
 note.value='half typed note';note.focus();note.setSelectionRange(4,4);
 panelContent.scrollTop=200;const scroll=panelContent.scrollTop;check(scroll>0,'panel not scrollable');
 // Identical data: no redraw at all (no flicker).
 const before=panelContent.querySelector('.goal-overview');
 panelLiveRefresh('goal');await wait(500);
 check(panelContent.querySelector('.goal-overview')===before,'identical data was redrawn');
 // Agent completes a task elsewhere; a burst of frames is debounced.
 await fetch('/api/fixture/complete');
 for(let i=0;i<5;i++)panelLiveRefresh('goal');
 for(let i=0;i<50&&!panelContent.querySelector('.goal-task.status-done');i++)await wait(20);
 check(panelContent.querySelector('.goal-task.status-done'),'panel did not refresh after goal change; hidden='+document.hidden+' overlay='+overlay.hidden+' sec='+currentSection+' create='+goalCreateOpen+' pending='+goalMutationsPending);
 const after=panelContent.querySelector('.goal-notes input');
 check(after.value==='half typed note','typed note lost: '+after.value);
 check(document.activeElement===after&&after.selectionStart===4,'focus/caret lost');
 check(Math.abs(panelContent.scrollTop-scroll)<=1,'scroll reset: '+panelContent.scrollTop+' vs '+scroll);
 // Closed panel: no fetch.
 closePanel();panelLiveRefresh('goal');await wait(400);
 check(fixtureErrors.length===0,'errors: '+fixtureErrors.join(';'));
 return '';
}catch(e){return String(e&&e.stack||e);}})()`
