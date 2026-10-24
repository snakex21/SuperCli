package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/testbrowser"
)

// Copy is always present under AI replies; Edit/Restore follow the
// assistant_edit switch and need a persisted message identity.
func TestAssistantActionsNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "assistant actions browser")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []llm.Message{
		{Role: llm.RoleUser, Content: "question"},
		{Role: llm.RoleAssistant, Content: "<thinking>secret</thinking>\nVisible answer"},
		{Role: llm.RoleUser, Content: "again"},
		{Role: llm.RoleAssistant, Content: "second answer"},
	} {
		if err := session.NewWriter(store, sess.ID).AppendMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	page0, err := srv.eng.transcriptPage(ctx, sess.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := strconv.ParseInt(page0.Messages[3].MessageID, 10, 64)
	if _, err := store.EditAssistantMessage(ctx, sess.ID, session.MessageReceipt{Seq: 4, ID: id}, "edited second", false); err != nil {
		t.Fatal(err)
	}
	messages, err := srv.eng.transcript(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	en, err := assetsFS.ReadFile("assets/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<!doctype html><html class="assistant-edit-off"><head><meta charset="utf-8"><link rel="stylesheet" href="/app.css"></head><body><script>var fixtureCatalog=%s;</script><script src="/prelude.js"></script><script src="/03-markdown.js"></script><script src="/04-transcript.js"></script><script src="/08-sessions.js"></script><script src="/ready.js"></script></body></html>`, en)
		case "/app.css":
			w.Header().Set("Content-Type", "text/css")
			b, _ := assetsFS.ReadFile("assets/app.css")
			_, _ = w.Write(b)
		case "/prelude.js":
			fmt.Fprint(w, queueEditBrowserPrelude+`
var superCliUI={fileMutationTools:{patch_file:true},mutationKind:function(){return '';}};var preferences={};
function registerLanguageRefresh(){} function toolDisclosureOpen(){return false;}
function thinkingDisclosureOpen(){return false;}function renderText(s){return s;}
function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}
function renderSentAttachments(){}function sentAttachmentsFor(){return [];}
function userMessageDisplayText(text){return text;}function updateWorkerOverview(){}
function escHtml(s){return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}
var escAttr=escHtml;var toasts=[];function toast(s){toasts.push(s);}
`)
		case "/ready.js":
			fmt.Fprint(w, `toolDisclosureOpen=function(){return false;};smartScroll=function(){};activeSessionID=transcriptSessionID='fixture';window.fixtureReady=true;`)
		default:
			b, err := assetsFS.ReadFile("assets/js" + r.URL.Path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
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
	var failure string
	script := `(()=>{try{
function check(ok,why){if(!ok)throw Error(why);}
function shown(n){return !!n&&getComputedStyle(n).display!=='none';}
check(window.fixtureReady,'scripts not ready');
var copied=[];Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:function(s){copied.push(s);return Promise.resolve();}}});
var messages=` + string(data) + `;
var host=document.createElement('div');host.appendChild(buildHistoryFragment(messages,null,false));document.body.appendChild(host);
var bars=host.querySelectorAll('.msg-actions');
check(bars.length===2,'every AI reply needs one action row, got '+bars.length);
bars.forEach(function(bar){check(shown(bar.querySelector('.msg-copy')),'copy hidden');check(!shown(bar.querySelector('.msg-edit')),'edit visible while assistant_edit is off');});
bars[0].querySelector('.msg-copy').click();
check(copied[0]==='Visible answer','copy must use the visible answer, got '+JSON.stringify(copied[0]));
check(host.querySelectorAll('.msg-assistant')[1].textContent.indexOf('edited second')>=0,'history does not render the edited text');
document.documentElement.classList.remove('assistant-edit-off');
check(shown(bars[0].querySelector('.msg-edit'))&&!shown(bars[0].querySelector('.msg-restore')),'unedited reply: edit hidden or restore shown');
check(shown(bars[1].querySelector('.msg-restore'))&&shown(bars[1].querySelector('.msg-edited-mark')),'edited reply lacks restore/mark');
var legacy=buildHistoryFragment([{role:'assistant',seq:9,content:'legacy without id'}],null,false);
check(legacy.querySelector('.msg-copy')&&!legacy.querySelector('.msg-edit'),'reply without identity must offer copy only');
transcriptLiveAppend=true;var live=addAssistantMsg();transcriptLiveAppend=false;live._raw='live reply';sealAssistantSegment(live);
check(!live._actions,'actions must not split a live segment from its tool rows');
addLiveAssistantActions();
check(live._actions&&live._actions.querySelector('.msg-copy')&&!live._actions.querySelector('.msg-edit'),'finished live reply must show copy only');
return '';
}catch(e){return e.stack||String(e);}})()`
	if err := page.Eval(ctx, script, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
}
