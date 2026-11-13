package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

func TestClaudeLoginNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	catalog, _ := assetsFS.ReadFile("assets/locales/pl.json")
	js, _ := assetsFS.ReadFile("assets/js/11da-panel-claude.js")
	quotas, _ := assetsFS.ReadFile("assets/js/07a-subscription-usage.js")
	fixture, _ := json.Marshal(map[string]any{
		"logged_in": true, "provider": "claude", "models": []string{"claude-sonnet-4-6"},
		"usage": map[string]any{"captured_at": time.Now(), "windows": []map[string]any{
			{"id": "five_hour", "window_seconds": 18000, "used_percent": 28, "remaining_percent": 72, "resets_at": time.Now().Add(2 * time.Hour)},
			{"id": "seven_day", "window_seconds": 604800, "used_percent": 15, "remaining_percent": 85, "resets_at": time.Now().Add(4 * 24 * time.Hour)},
		}},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app.css" {
			b, _ := assetsFS.ReadFile("assets/app.css")
			w.Header().Set("Content-Type", "text/css")
			w.Write(b)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/app.css"><style>body{display:block;margin:0;background:var(--bg)}#panel{max-width:660px;margin:30px auto;padding:0 16px}</style><main id="panel"></main><textarea id="prompt" hidden></textarea><script>
var catalog=%s,fixture=%s,panelContent=document.getElementById('panel'),ui={lang:'pl'};
function t(key){return catalog[key]||key;}function el(tag,cls,text){var n=document.createElement(tag);n.className=cls||'';if(text)n.textContent=text;return n;}
function statsFormatter(){return new Intl.NumberFormat('pl-PL',{maximumFractionDigits:1});}function fmtInteger(n){return String(n);}function fmtDateTime(value){return new Date(value).toLocaleString('pl-PL');}
var requests=[],finishLogin,modelsLoaded=0,chatClosed=false,modelRequest,notices=[];
var finishStats;var loginFinished=new Promise(function(resolve){finishStats=resolve;});
var finishChat;var chatted=new Promise(function(resolve){finishChat=resolve;});
function j(url,opts){requests.push(url);if(url==='/api/claude/account')return Promise.resolve({logged_in:false});return new Promise(function(resolve){finishLogin=resolve;});}
function jpost(url,body){requests.push(url);if(url==='/api/model')modelRequest=body;return Promise.resolve({ok:true});}
function toast(message){notices.push(message);}function loadModels(){modelsLoaded++;return Promise.resolve();}
function renderStats(){finishStats();return Promise.resolve();}function renderProvidersList(){panelContent.innerHTML='';}
function closePanel(){chatClosed=true;finishChat();}function openPanel(){}
%s
%s
</script>`, catalog, fixture, quotas, js)
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1000, Height: 950})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var failure string
	err = page.Eval(ctx, `(async()=>{try{
 function check(ok,why){if(!ok)throw Error(why);}
 await renderClaudeLogin([]);
 var login=panelContent.querySelector('.claude-auth-primary');
 check(login&&!login.disabled&&!panelContent.querySelector('.claude-auth-secondary'),'initial auth actions');
 login.click();check(panelContent.querySelector('.claude-auth-primary').disabled,'duplicate login allowed');
 check(requests.join(',')==='/api/claude/account,/api/claude/login?lang=pl','login polls account state');
 finishLogin(fixture);await loginFinished;
 check(panelContent.querySelector('h2').textContent==='Konto Claude połączone','missing success confirmation');
 check(modelsLoaded===1&&notices.length===1,'completion did not refresh models or confirm success');
 check(panelContent.textContent.includes('72%')&&panelContent.textContent.includes('85%'),'remaining subscription not shown');
 check(panelContent.querySelectorAll('[role="meter"]').length===2,'quota meters missing');
 check(!panelContent.textContent.includes('USD'),'subscription shown as API price');
 var summary=subscriptionCostPrimary({subscriptionUsage:[{provider:'ChatGPT',windows:fixture.usage.windows},{provider:'Claude',windows:fixture.usage.windows}]});
 check(summary.includes('ChatGPT')&&summary.includes('Claude')&&summary.includes('72%'),'both subscription summaries missing');
 var future=new Date(Date.now()+3600e3).toISOString(),past=new Date(Date.now()-60e3).toISOString();
 var old=subscriptionCostPrimary({subscriptionUsage:[{provider:'Claude',windows:[{id:'five_hour',window_seconds:18000,remaining_percent:64,stale:true,resets_at:future}]}]});
 check(old.includes('64%')&&old.includes(t('acct.usage.stale')),'saved unexpired snapshot hidden after restart: '+old);
 var gone=subscriptionCostPrimary({subscriptionUsage:[{provider:'Claude',windows:[{id:'five_hour',window_seconds:18000,remaining_percent:64,stale:true,resets_at:past}]}]});
 check(!gone.includes('64%')&&gone.includes('—'),'expired window shown as current: '+gone);
 return '';
 }catch(e){return e.stack||String(e);}})()`, &failure)
	if err != nil || failure != "" {
		t.Fatalf("%v / %s", err, failure)
	}
	if out := os.Getenv("SUPERCLI_AUTH_PREVIEW_DIR"); out != "" {
		if err := os.MkdirAll(out, 0700); err != nil {
			t.Fatal(err)
		}
		png, err := page.Screenshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, "claude-connected.png"), png, 0600); err != nil {
			t.Fatal(err)
		}
	}
	err = page.Eval(ctx, `(async()=>{try{
 panelContent.querySelector('.claude-auth-primary').click();await chatted;
 if(!chatClosed||modelRequest.provider!=='claude'||modelRequest.model!=='claude-sonnet-4-6')throw Error('chat action did not select Claude');
 panelContent.querySelector('.claude-auth-secondary').click();await Promise.resolve();await Promise.resolve();
 if(panelContent.querySelector('.claude-auth-secondary')||panelContent.querySelector('.claude-auth-primary').disabled)throw Error('logout did not reset actions');
 if(requests.length!==4||requests[3]!=='/api/claude/logout')throw Error('unexpected polling');
 return '';
 }catch(e){return e.stack||String(e);}})()`, &failure)
	if err != nil || failure != "" {
		t.Fatalf("%v / %s", err, failure)
	}
}
