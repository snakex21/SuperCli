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

// The "Jump to latest" control appears only after the reader scrolled away
// from the tail, adds no scroll height (no layout jump), and returns to the
// tail with tail-following and composer focus restored.
func TestJumpToLatestNativeBrowser(t *testing.T) {
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
			fmt.Fprint(w, shell, `<script>window.fixtureErrors=[];window.addEventListener('error',e=>fixtureErrors.push(e.message));window.addEventListener('unhandledrejection',e=>fixtureErrors.push(String(e.reason)));</script><script src="/runtime.js"></script><script src="/js/00-helpers.js"></script><script src="/catalog.js"></script><script src="/js/01-i18n.js"></script><script src="/js/02-ui-settings.js"></script><script src="/js/03-markdown.js"></script><script src="/js/03b-highlight.js"></script><script src="/js/04-transcript.js"></script><script src="/js/05-chat.js"></script><script src="/js/08-sessions.js"></script><script src="/fixture.js"></script></body></html>`)
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
		case "/api/settings":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		case "/api/sessions":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"id":"long","first_user_msg":"Synthetic long"}]`)
		case "/api/transcript":
			messages := make([]map[string]any, 0, 60)
			for seq := 11461; seq <= 11520; seq++ {
				messages = append(messages, longHistoryMessage(seq))
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"messages": messages, "has_more": false})
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
	if err := page.Eval(ctx, jumpLatestIntegration, &result); err != nil {
		t.Fatal(err)
	}
	t.Logf("jump to latest: %s", result)
}

const jumpLatestIntegration = `(async()=>{
 fixtureCheck(fixtureErrors.length===0,'script setup: '+fixtureErrors.join(';'));
 fixtureCheck(await resumeSession('long'),'resume failed');
 await fixtureSettle();
 fixtureCheck(jumpLatestBtn.hidden,'button visible at the tail');
 stage.dispatchEvent(new WheelEvent('wheel'));
 stage.scrollTop=0;
 await fixtureSettle();
 fixtureCheck(!transcriptFollowTail,'scrolling up kept tail following');
 fixtureCheck(!jumpLatestBtn.hidden,'button hidden after scrolling away');
 fixtureCheck(jumpLatestBtn.textContent.indexOf('Jump to latest')>=0,'label not translated: '+jumpLatestBtn.textContent);
 fixtureCheck(jumpLatestDock.getBoundingClientRect().height===0,'button dock adds layout height');
 var rect=jumpLatestBtn.getBoundingClientRect(),view=stage.getBoundingClientRect();
 fixtureCheck(rect.bottom<=view.bottom&&rect.top>=view.top,'button is outside the visible transcript');
 jumpLatestBtn.click();
 await fixtureSettle();
 var gap=stage.scrollHeight-stage.clientHeight-stage.scrollTop;
 fixtureCheck(Math.abs(gap)<=1,'did not reach the tail: gap='+gap);
 fixtureCheck(transcriptFollowTail,'tail following not restored');
 fixtureCheck(jumpLatestBtn.hidden,'button still visible at the tail');
 fixtureCheck(document.activeElement===promptEl,'composer focus not restored');
 fixtureCheck(fixtureErrors.length===0,'browser errors: '+fixtureErrors.join(';'));
 return {gap};
})()`
