package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
	// Optional visual check: SUPERCLI_JUMP_SHOT_DIR=<dir> saves the visible
	// button in every theme (jump-latest-<theme>.png).
	if dir := os.Getenv("SUPERCLI_JUMP_SHOT_DIR"); dir != "" {
		var ok bool
		if err := page.Eval(ctx, `(async()=>{await resumeSession('long');await fixtureSettle();stage.dispatchEvent(new WheelEvent('wheel'));stage.scrollTop=0;await fixtureSettle();return !jumpLatestBtn.hidden;})()`, &ok); err != nil || !ok {
			t.Fatalf("screenshot setup: %v visible=%v", err, ok)
		}
		for _, theme := range []string{"dark", "light", "midnight"} {
			var done bool
			if err := page.Eval(ctx, `(async()=>{var th=`+strconvQuote(theme)+`;if(th==='dark')delete document.documentElement.dataset.theme;else document.documentElement.dataset.theme=th;await new Promise(r=>setTimeout(r,250));return true;})()`, &done); err != nil {
				t.Fatal(err)
			}
			shot, err := page.Screenshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "jump-latest-"+theme+".png"), shot, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		var reset bool
		_ = page.Eval(ctx, `(()=>{delete document.documentElement.dataset.theme;return true})()`, &reset)
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
 // Moderate, clearly separated pill: body-text size, 28-34px tall, focusable.
 // 1.0.35 made it a huge pill that dominated the transcript.
 var bodySize=parseFloat(getComputedStyle(document.body).fontSize),btnStyle=getComputedStyle(jumpLatestBtn);
 fixtureCheck(rect.height>=28&&rect.height<=34,'button height not moderate: '+rect.height);
 fixtureCheck(Math.abs(parseFloat(btnStyle.fontSize)-bodySize)<=0.5,'button text not body size: '+btnStyle.fontSize+' vs '+bodySize);
 fixtureCheck(rect.width<=320,'button too wide: '+rect.width);
 fixtureCheck(btnStyle.fontFamily===getComputedStyle(document.body).fontFamily,'button ignores the chosen font');
 fixtureCheck(btnStyle.boxShadow!=='none'&&btnStyle.backgroundColor!=='rgba(0, 0, 0, 0)','button has no solid backdrop');
 jumpLatestBtn.focus();fixtureCheck(document.activeElement===jumpLatestBtn,'button is not focusable');
 // WCAG AA text contrast of the filled pill in every theme.
 function lum(c){var m=c.match(/[\d.]+/g).slice(0,3).map(Number).map(function(v){v/=255;return v<=0.03928?v/12.92:Math.pow((v+0.055)/1.055,2.4);});return 0.2126*m[0]+0.7152*m[1]+0.0722*m[2];}
 var themes=['','light','midnight'],ratios=[];
 themes.forEach(function(theme){
   if(theme)document.documentElement.dataset.theme=theme;else delete document.documentElement.dataset.theme;
   var st=getComputedStyle(jumpLatestBtn),a=lum(st.color),b=lum(st.backgroundColor);
   var ratio=(Math.max(a,b)+0.05)/(Math.min(a,b)+0.05);ratios.push((theme||'dark')+'='+ratio.toFixed(2));
   fixtureCheck(ratio>=4.5,'contrast below AA in '+(theme||'dark')+': '+ratio.toFixed(2)+' '+st.color+' on '+st.backgroundColor);
 });
 delete document.documentElement.dataset.theme;
 console.log('jump contrast '+ratios.join(' '));
 // New output while away shows a count.
 var extra=document.createElement('div');extra.className='msg';extra.textContent='later';stream.appendChild(extra);
 updateJumpLatest();
 fixtureCheck(jumpLatestCount.textContent.indexOf('1')>=0,'new-message count missing: '+jumpLatestCount.textContent);
 jumpLatestBtn.click();
 await fixtureSettle();
 var gap=stage.scrollHeight-stage.clientHeight-stage.scrollTop;
 fixtureCheck(Math.abs(gap)<=1,'did not reach the tail: gap='+gap);
 fixtureCheck(transcriptFollowTail,'tail following not restored');
 fixtureCheck(jumpLatestBtn.hidden,'button still visible at the tail');
 fixtureCheck(document.activeElement===promptEl,'composer focus not restored');
 fixtureCheck(fixtureErrors.length===0,'browser errors: '+fixtureErrors.join(';'));
 return {gap,contrast:ratios.join(" ")};
})()`

func strconvQuote(s string) string { return strconv.Quote(s) }
