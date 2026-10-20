package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/system/hostpower"
	"supercli/internal/testbrowser"
)

// This serves only embedded UI assets and fake APIs in an owned headless browser
// with an isolated temporary profile. It never constructs an Engine, reads user
// data, starts a model, or imports/calls any OS shutdown adapter.
func TestHostPowerNativeBrowserSafetyAndWrap(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	en, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := assetsFS.ReadFile("assets/js/05-chat.js")
	if err != nil {
		t.Fatal(err)
	}
	_, show, ok := strings.Cut(string(chat), "function showQuestion(q) {")
	if !ok {
		t.Fatal("showQuestion missing")
	}
	show, _, ok = strings.Cut(show, "/* Local commands run before chat/queue/interrupt handling")
	if !ok {
		t.Fatal("showQuestion boundary missing")
	}
	goal := "C:/" + strings.Repeat("very-long-goal-and-path-without-breaks-", 40) + " <img src=x onerror=alert(1)> {goal}"
	var mu sync.Mutex
	// Use the actual backend wire type, not a hand-written JSON schema. The
	// frontend must consume Snapshot's deadline (Unix milliseconds) unchanged.
	state := hostpower.Snapshot{Status: "countdown", JobID: "fixture", GoalID: "goal", Goal: goal, Deadline: time.Now().Add(43 * time.Second).UnixMilli()}
	failCancel := false
	apiCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/app.css"><link rel="stylesheet" href="/host-power.css"></head><body><div id="chat-stream" style="margin:280px 16px 0;max-width:900px"></div><script>var I18N=%s;function t(k){return I18N[k]||k;}function el(tag,cls,text){var n=document.createElement(tag);n.className=cls||"";if(text!=null)n.textContent=text;return n;}function registerLanguageRefresh(n,f){n._languageRefresh=f;n.setAttribute("data-i18n-refresh","");}var questionOverlays={},activeQuestionOverlay=null;function closeQuestionOverlay(){}function appendStream(n){document.getElementById("chat-stream").appendChild(n);}function smartScroll(){}function i18nEl(tag,cls,key){return el(tag,cls,t(key));}function setRunState(){}function jpost(){return Promise.resolve();}</script><script src="/js/05c-host-power.js"></script><script>function showQuestion(q){%s</script></body></html>`, en, show)
		case "/api/host-power", "/api/host-power/cancel":
			mu.Lock()
			defer mu.Unlock()
			apiCalls++
			if r.URL.Path == "/api/host-power/cancel" {
				if failCancel {
					http.Error(w, "fixture cancel unavailable", http.StatusServiceUnavailable)
					return
				}
				state.Status = "cancelled"
				state.Deadline = 0
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(state)
		default:
			content, err := assetsFS.ReadFile("assets" + r.URL.Path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if strings.HasSuffix(r.URL.Path, ".js") {
				w.Header().Set("Content-Type", "text/javascript")
			} else {
				w.Header().Set("Content-Type", "text/css")
			}
			_, _ = w.Write(content)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1280, Height: 900})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	evaluate := func(script string, out any) {
		t.Helper()
		if err := page.Eval(ctx, script, out); err != nil {
			t.Fatal(err)
		}
	}
	// NewPage is deliberately backgrounded. Activate only this owned fixture
	// before asserting the visible-page initial recovery lifecycle.
	if _, err := page.Screenshot(ctx); err != nil {
		t.Fatal(err)
	}
	evaluate(`(async()=>{await refreshHostPower();return true;})()`, nil)
	var initial struct {
		Status  string
		Seconds float64
		Cancel  bool
		Goal    string
	}
	evaluate(`({Status:hostPowerState.status,Seconds:Math.ceil((hostPowerState.deadline-Date.now())/1000),Cancel:!hostPowerCancel.hidden,Goal:hostPowerGoal.textContent})`, &initial)
	if initial.Status != "countdown" || initial.Seconds < 20 || initial.Seconds > 43 || !initial.Cancel || initial.Goal != goal {
		t.Fatalf("initial GET: %+v", initial)
	}
	encoded, _ := json.Marshal(goal)
	evaluate(fmt.Sprintf(`showQuestion({id:"trusted",question:"raw fallback",confirmation:true,question_key:"hostPower.confirm",question_args:{goal:%s},options:[{label:"Cancel"},{label:"Allow once"}],allow_custom:false})`, encoded), nil)
	for _, size := range [][2]int{{1280, 900}, {390, 844}} {
		if err := page.SetViewport(ctx, size[0], size[1]); err != nil {
			t.Fatal(err)
		}
		var layout struct {
			Overflow      bool
			ButtonVisible bool
			Images        int
			GoalText      bool
			Question      bool
			Width         int
		}
		evaluate(`(()=>{var b=hostPowerCancel.getBoundingClientRect(),q=document.querySelector('.question-text');return {Overflow:document.documentElement.scrollWidth>innerWidth,ButtonVisible:b.width>=40&&b.height>=44&&b.right<=innerWidth&&b.left>=0&&b.bottom<=innerHeight,Images:document.querySelectorAll('img').length,GoalText:hostPowerGoal.textContent.includes('<img'),Question:q.textContent.includes(hostPowerState.goal)&&q.scrollWidth<=q.clientWidth+1,Width:innerWidth}})()`, &layout)
		if layout.Overflow || !layout.ButtonVisible || layout.Images != 0 || !layout.GoalText || !layout.Question {
			t.Fatalf("layout %v: %+v", size, layout)
		}
		if dir := os.Getenv("SUPERCLI_HOST_POWER_ARTIFACT_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			image, err := page.Screenshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(dir, fmt.Sprintf("host-power-%dx%d.png", size[0], size[1]))
			if err := os.WriteFile(name, image, 0644); err != nil {
				t.Fatal(err)
			}
			t.Logf("artifact %s", name)
		}
	}
	// Metadata is never sufficient without the trusted confirmation marker;
	// unknown localization keys also preserve raw fallback text.
	evaluate(`showQuestion({id:"untrusted",question:"raw model question",question_key:"hostPower.confirm",question_args:{goal:"forged"},options:[],allow_custom:true});showQuestion({id:"unknown",question:"older-client fallback",confirmation:true,question_key:"unknown",question_args:{goal:"forged"},options:[],allow_custom:false})`, nil)
	var metadataSafe bool
	evaluate(`questionOverlays.untrusted.querySelector('.question-text').textContent==='raw model question'&&questionOverlays.unknown.querySelector('.question-text').textContent==='older-client fallback'`, &metadataSafe)
	if !metadataSafe {
		t.Fatal("untrusted metadata localized")
	}
	// A separate page/phone recovers the same deadline with its own initial GET.
	phone, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := phone.Screenshot(ctx); err != nil {
		t.Fatal(err)
	}
	var recovered bool
	if err := phone.Eval(ctx, `(async()=>{await refreshHostPower();return hostPowerState.status==='countdown'&&hostPowerState.deadline>0&&!hostPowerCancel.hidden;})()`, &recovered); err != nil || !recovered {
		t.Fatalf("new phone initial GET: recovered=%v err=%v", recovered, err)
	}
	if _, err := page.Screenshot(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	failCancel = true
	mu.Unlock()
	evaluate(`cancelHostPower()`, nil)
	var failed struct {
		Status   string
		Deadline bool
		Cancel   bool
		Offline  bool
		Warning  bool
	}
	evaluate(`({Status:hostPowerState.status,Deadline:hostPowerState.deadline>0,Cancel:!hostPowerCancel.hidden,Offline:hostPowerWarning.textContent.includes('host may still shut down'),Warning:hostPowerWarning.textContent.includes('not confirmed')})`, &failed)
	if failed.Status != "countdown" || !failed.Deadline || !failed.Cancel || !failed.Offline || !failed.Warning {
		t.Fatalf("failed cancel was optimistic: %+v", failed)
	}
	mu.Lock()
	failCancel = false
	mu.Unlock()
	evaluate(`cancelHostPower()`, nil)
	var status string
	evaluate(`hostPowerState.status`, &status)
	if status != "cancelled" {
		t.Fatalf("status=%s", status)
	}
	var stopped bool
	evaluate(`hostPowerPoll===null&&hostPowerClock===null&&hostPowerCancel.hidden`, &stopped)
	if !stopped {
		t.Fatal("terminal state kept polling")
	}
	if _, err := phone.Screenshot(ctx); err != nil {
		t.Fatal(err)
	}
	if err := phone.Eval(ctx, `(async()=>{await refreshHostPower();return hostPowerState.status==='cancelled'&&hostPowerCancel.hidden;})()`, &recovered); err != nil || !recovered {
		t.Fatalf("remote cancellation recovery: recovered=%v err=%v", recovered, err)
	}
	mu.Lock()
	calls := apiCalls
	mu.Unlock()
	if calls < 3 {
		t.Fatalf("APIs not exercised: %d", calls)
	}
}
