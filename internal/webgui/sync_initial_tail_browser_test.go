package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// Separate from the profiling workload: production syncRefreshView ->
// resumeSession -> finally(syncSettleTranscriptScroll), with real rendering and
// native frames. Only the idle SSE transport is replaced; no sync caller,
// transcript renderer, scroll function or animation frame is stubbed.
func TestSyncRefreshInitialTailNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
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
			fmt.Fprint(w, shell, `<script>window.fixtureErrors=[];window.addEventListener('error',e=>fixtureErrors.push(e.message));window.addEventListener('unhandledrejection',e=>fixtureErrors.push(String(e.reason)));window.EventSource=class{close(){}};</script><script src="/runtime.js"></script><script src="/js/00-helpers.js"></script><script src="/catalog.js"></script><script src="/js/01-i18n.js"></script><script src="/js/02-ui-settings.js"></script><script src="/js/03-markdown.js"></script><script src="/js/03b-highlight.js"></script><script src="/js/04-transcript.js"></script><script src="/js/05-chat.js"></script><script src="/js/08-sessions.js"></script><script src="/fixture.js"></script><script src="/js/05b-sync.js"></script></body></html>`)
		case "/runtime.js":
			w.Header().Set("Content-Type", "text/javascript")
			b, _ := sharedUIFS.ReadFile("shared-ui/runtime.js")
			w.Write(b)
		case "/catalog.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprintf(w, "var I18N={en:%s};var UI_LANGUAGES=[{code:'en',name:'English'}];", en)
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, longHistoryBrowserFixture)
		case "/api/sessions":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"id":"long","first_user_msg":"Synthetic long"}]`)
		case "/api/transcript":
			if r.URL.Query().Get("id") != "long" || r.URL.Query().Get("limit") != "60" {
				http.Error(w, "unexpected page", 400)
				return
			}
			messages := make([]map[string]any, 0, 60)
			for seq := 11461; seq <= 11520; seq++ {
				messages = append(messages, longHistoryMessage(seq))
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"messages": messages, "has_more": true, "before_seq": 11461})
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
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1000, Height: 800})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := page.Screenshot(ctx); err != nil {
		t.Fatal(err)
	}
	var result json.RawMessage
	if err := page.Eval(ctx, syncInitialTailIntegration, &result); err != nil {
		t.Fatal(err)
	}
	t.Logf("production sync integration: %s", result)
}

const syncInitialTailIntegration = `(async()=>{
 fixtureCheck(fixtureErrors.length===0,'script setup: '+fixtureErrors.join(';'));
 // Model an already-connected idle sync transport; keep production watch,
 // refresh and finally callbacks. No SSE payloads/remote runs are needed here.
 syncDisconnect();syncSession=activeSessionID='long';
 syncConnection=new EventSource('/unused-synthetic-transport');syncReady=Promise.resolve();
 transcriptResumeScroll=null;
 var observedOwner=null,monitor=null;
 function observe(){if(initialTranscriptTail)observedOwner=initialTranscriptTail;if(syncRefreshPending||initialTranscriptTail)monitor=requestAnimationFrame(observe);}
 syncRefreshView();monitor=requestAnimationFrame(observe);
 while(syncRefreshPending)await new Promise(resolve=>setTimeout(resolve,1));
 await fixtureSettle();cancelAnimationFrame(monitor);
 var gap=stage.scrollHeight-stage.clientHeight-stage.scrollTop;
 fixtureCheck(Math.abs(gap)<=1,'syncRefreshView with no saved scroll lost latest tail: gap='+gap);
 fixtureCheck(observedOwner&&observedOwner.frames>0,'production refresh did not run initial settlement');
 fixtureCheck(!initialTranscriptTail&&transcriptResumeScroll===null,'initial owner/restoration state did not settle');
 fixtureCheck(transcriptBeforeSeq===11461&&stream.querySelectorAll(':scope > :not(.msg-actions)').length===61,'long latest page was not bounded');
 fixtureCheck(getComputedStyle(stream.querySelector(':scope > .msg-assistant')).contentVisibility==='auto','virtualization disabled');
 fixtureCheck(document.activeElement===promptEl,'refresh lost composer focus');
 var initialFrames=observedOwner.frames;
 // A genuine eligible saved manual anchor must still supersede an initial
 // owner. Capture through production, then stage the competing owner before
 // delivering that saved anchor to the production sync restoration caller.
 stage.scrollTop=150;transcriptFollowTail=false;captureTranscriptScroll();resumeTranscriptScroll();
 var saved=transcriptResumeScroll;transcriptResumeScroll=null;
 settleInitialTranscriptTail();fixtureCheck(!!initialTranscriptTail,'manual restore lacked competing initial owner');
 transcriptResumeScroll=saved;syncSettleTranscriptScroll();
 fixtureCheck(!initialTranscriptTail,'eligible sync manual restore failed to cancel initial owner');
 await fixtureSettle();
 fixtureCheck(!transcriptResumeScroll&&Math.abs(stage.scrollTop-saved.top)<=1,'eligible sync manual restore lost position: '+stage.scrollTop+' vs '+saved.top);
 fixtureCheck(!transcriptFollowTail,'manual restoration forced tail following');
 fixtureCheck(fixtureErrors.length===0,'browser errors: '+fixtureErrors.join(';'));
 syncDisconnect();
 return {tailGap:gap,initialFrames,manualTop:stage.scrollTop,expectedManualTop:saved.top,productionSyncLoaded:true};
})()`
