package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// An optional byte-exact release fixture enables an actual old/new visual
// comparison without requiring Git tags or external commands in ordinary CI.
func TestWorkerReleaseCompactLayoutNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	catalog, _ := assetsFS.ReadFile("assets/locales/pl.json")
	reference := os.Getenv("SUPERCLI_WORKER_UI_REFERENCE_DIR")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		switch r.URL.Path {
		case "/":
			prefix := "/current/"
			if r.URL.Query().Get("reference") == "1" && reference != "" {
				prefix = "/reference/"
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="%sapp.css"></head><body><script>var fixtureCatalog=%s;</script><script src="/runtime.js"></script><script src="/00-helpers.js"></script><script src="/prelude.js"></script><script src="%s04-transcript.js"></script></body></html>`, prefix, catalog, prefix)
		case "/runtime.js":
			b, _ := sharedUIFS.ReadFile("shared-ui/runtime.js")
			_, _ = w.Write(b)
		case "/prelude.js":
			fmt.Fprint(w, queueEditBrowserPrelude+`
var preferences={},runToolCount=0;function registerLanguageRefresh(){}function toolDisclosureOpen(){return true;}
function thinkingDisclosureOpen(){return false;}function renderText(s){return s;}
function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}
function pauseWorkerStatusSync(){}function smartScroll(){}
`)
		default:
			name := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/current/"), "/reference/"), "/")
			var b []byte
			var err error
			if strings.HasPrefix(r.URL.Path, "/reference/") && reference != "" && (name == "app.css" || name == "04-transcript.js") {
				b, err = os.ReadFile(filepath.Join(reference, name))
			} else if name == "app.css" {
				b, err = assetsFS.ReadFile("assets/app.css")
			} else {
				b, err = assetsFS.ReadFile("assets/js/" + name)
			}
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if name == "app.css" {
				w.Header().Set("Content-Type", "text/css; charset=utf-8")
			}
			_, _ = w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1250, Height: 950})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	type layout struct {
		Height float64     `json:"height"`
		Rows   [][]float64 `json:"rows"`
	}
	measure := func(old bool) layout {
		t.Helper()
		url := server.URL
		if old {
			url += "?reference=1"
		}
		page, err := browser.NewPage(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Failure string `json:"failure"`
			Layout  layout `json:"layout"`
		}
		script := `(()=>{try{
function check(ok,why){if(!ok)throw Error(why);}
smartScroll=function(){};toolDisclosureOpen=function(){return true;};
addToolCall('task',JSON.stringify({agent:'review',prompt:'Sprawdź projekt.\nPotem podaj wynik.'}),'delegation');
addWorkerProgress({id:'worker-1',name:'review',kind:'started',parent_call_id:'delegation',run:1,model:'fixture-model',reasoning_effort:'high'});
['read_lines','ctx_execute','search_code'].forEach(function(tool,index){
  var args=tool==='ctx_execute'?{command:['go','test','./...']}:{file:'internal/agent/loop.go',query:'worker'};
  addWorkerProgress({id:'worker-1',name:'review',kind:'tool_call',parent_call_id:'delegation',run:1,call_id:'call-'+index,tool:tool,args:JSON.stringify(args)});
  addWorkerProgress({id:'worker-1',name:'review',kind:'tool_result',parent_call_id:'delegation',run:1,call_id:'call-'+index,tool:tool,output:'RESULT '+index});
});
addToolResult('delegation','<task-notification><task-id>worker-1</task-id><agent>review</agent><status>done</status><summary>review done · 4 steps · 200 in/40 out tok</summary><tools>read_lines, ctx_execute, search_code</tools><result>Sprawdzenie zakończone.</result></task-notification>');
var row=toolRows.delegation;row.open=true;row.dispatchEvent(new Event('toggle'));row._body.hidden=false;
var items=Array.from(row.querySelectorAll('.task-activity-item'));check(items.length===3,'tool rows missing');
var base=row.getBoundingClientRect();
var measured={height:base.height,rows:items.map(function(item){var b=item.getBoundingClientRect();return [b.height,...['.activity-dot','.activity-name','.activity-hint','.activity-status'].map(function(s){var r=item.querySelector(s).getBoundingClientRect();return r.left-base.left;})];})};
if(!` + fmt.Sprint(old) + `){
  var detail=items[0].querySelector('.activity-result');check(detail,'compact row lost result access');
  check(!detail.open,'result expanded by default');
  detail.querySelector('summary').click();check(detail.open&&detail.textContent.includes('RESULT 0'),'row click does not reveal result');
  detail.querySelector('summary').click();check(!detail.open,'row cannot collapse result');
  check(Math.abs(items[0].getBoundingClientRect().height-measured.rows[0][0])<1,'collapse left an extra result row');
}
clearInterval(row._clock);return {layout:measured};
}catch(e){Object.values(toolRows).forEach(function(r){clearInterval(r._clock);});return {failure:e.stack||String(e)};}})()`
		if err = page.Eval(ctx, script, &result); err != nil {
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
			name := "worker-layout-current.png"
			if old {
				name = "worker-layout-v1.0.14.png"
			}
			if err = os.WriteFile(filepath.Join(stage, name), shot, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return result.Layout
	}
	current := measure(false)
	if reference == "" {
		for _, row := range current.Rows {
			if row[0] > 24 {
				t.Fatalf("collapsed activity is no longer one compact line: %+v", current)
			}
		}
		return
	}
	old := measure(true)
	currentJSON, _ := json.Marshal(current)
	oldJSON, _ := json.Marshal(old)
	t.Logf("current=%s released=%s", currentJSON, oldJSON)
	if math.Abs(current.Height-old.Height) > 2 {
		t.Errorf("card height changed: current %.2f, release %.2f", current.Height, old.Height)
	}
	for i, row := range current.Rows {
		for j, value := range row {
			if math.Abs(value-old.Rows[i][j]) > 1 {
				t.Errorf("activity row %d metric %d differs: current %.2f, release %.2f", i, j, value, old.Rows[i][j])
			}
		}
	}
}
