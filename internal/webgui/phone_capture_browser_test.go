package webgui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// Real embedded composer, chat upload implementation and capture module; only
// surrounding app services are fakes. No camera, model, Engine or user data.
func TestPhoneCaptureAssetContract(t *testing.T) {
	html, err := assetsFS.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	chat := strings.Index(string(html), `src="js/05-chat.js"`)
	capture := strings.Index(string(html), `src="js/05d-phone-capture.js"`)
	if chat < 0 || capture <= chat || strings.Count(string(html), `src="js/05d-phone-capture.js"`) != 1 {
		t.Fatal("capture module must load once after chat")
	}
	if _, err := assetsFS.ReadFile("assets/js/05d-phone-capture.js"); err != nil {
		t.Fatal(err)
	}
}

func TestPhoneCaptureNativeBrowser(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		width    int
		hostname bool
	}{
		{"320", 320, false},
		{"390", 390, false},
		{"1280", 1280, false},
		{"1280-hostname", 1280, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			width := tc.width
			ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
			defer cancel()
			html, _ := assetsFS.ReadFile("assets/index.html")
			shell := strings.Split(string(html), `<script src=`)[0]
			catalog, _ := assetsFS.ReadFile("assets/locales/en.json")
			var uploads, pickers atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					fmt.Fprintf(w, `%s<script>var fixtureCatalog=%s;</script><script src="/fixture.js"></script><script src="/runtime.js"></script><script>var superCliUI=window.SuperCliUI;</script><script src="/js/05-chat.js"></script><script src="/js/05d-phone-capture.js"></script></body></html>`, shell, catalog)
				case "/fixture.js":
					w.Header().Set("Content-Type", "text/javascript")
					fmt.Fprint(w, queueEditBrowserPrelude, `
var activeWorkspacePath='fixture';var fixtureErrors=[];window.addEventListener('error',e=>fixtureErrors.push(e.message));window.addEventListener('unhandledrejection',e=>fixtureErrors.push(String(e.reason)));var fixtureToasts=[];function toast(message){fixtureToasts.push(message);};
`)
				case "/api/attachment/upload":
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					defer r.MultipartForm.RemoveAll()
					if len(r.MultipartForm.File["files"]) != 1 {
						http.Error(w, "expected one file", 400)
						return
					}
					n := uploads.Add(1)
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"paths":["/stage/capture-%d.jpg"]}`, n)
				case "/api/file-picker":
					pickers.Add(1)
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"paths":[]}`)
				default:
					var b []byte
					var err error
					if r.URL.Path == "/runtime.js" {
						b, err = sharedUIFS.ReadFile("shared-ui/runtime.js")
					} else {
						b, err = assetsFS.ReadFile("assets" + r.URL.Path)
					}
					if err != nil {
						http.NotFound(w, r)
						return
					}
					if strings.HasSuffix(r.URL.Path, ".js") {
						w.Header().Set("Content-Type", "text/javascript")
					} else if strings.HasSuffix(r.URL.Path, ".css") {
						w.Header().Set("Content-Type", "text/css")
					}
					w.Write(b)
				}
			}))
			defer server.Close()
			browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: width, Height: 844})
			if err != nil {
				t.Fatal(err)
			}
			defer browser.Close()
			pageURL := server.URL
			if tc.hostname {
				// Chromium resolves special-use *.localhost to loopback. Keep the
				// listener private while exercising a non-literal-loopback hostname.
				pageURL = strings.Replace(server.URL, "127.0.0.1", "phone-capture.localhost", 1)
			}
			page, err := browser.NewPage(ctx, pageURL)
			if err != nil {
				t.Fatal(err)
			}
			check := func(js string) {
				t.Helper()
				var result string
				if err := page.Eval(ctx, js, &result); err != nil {
					t.Fatal(err)
				}
				if result != "ok" {
					t.Fatal(result)
				}
			}
			if tc.hostname {
				check(`location.hostname==='phone-capture.localhost'?'ok':'hostname fixture did not navigate'`)
			}
			check(`(()=>{var m=document.getElementById('phone-attachment-menu');if(!m)return 'capture menu missing';if(!m.hidden)return 'menu auto-opened';for(var [id,accept] of [['phone-photo-input','image/*'],['phone-video-input','video/*']]){var i=document.getElementById(id);if(!i||i.type!=='file'||i.accept!==accept||i.getAttribute('capture')!=='environment'||i.multiple)return 'capture contract '+id;}return 'ok';})()`)
			if width == 1280 {
				check(`(async()=>{document.getElementById('attach-btn').click();await new Promise(r=>setTimeout(r,100));return document.getElementById('phone-attachment-menu').hidden?'ok':'desktop intercepted';})()`)
				if pickers.Load() != 1 || uploads.Load() != 0 {
					t.Fatalf("desktop picker=%d uploads=%d", pickers.Load(), uploads.Load())
				}
				return
			}
			check(`(()=>{var b=document.getElementById('attach-btn'),m=document.getElementById('phone-attachment-menu');b.click();if(m.hidden||b.getAttribute('aria-expanded')!=='true'||!m.contains(document.activeElement))return 'menu focus/open';var r=m.getBoundingClientRect();if(r.left<0||r.right>innerWidth||document.documentElement.scrollWidth>innerWidth)return 'phone overflow';m.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));if(!m.hidden||document.activeElement!==b)return 'escape focus';b.click();document.getElementById('prompt').click();if(!m.hidden)return 'outside click';b.click();document.getElementById('phone-attachment-close').click();return m.hidden&&document.activeElement===b?'ok':'close';})()`)
			// Observe native input clicks without opening a real chooser/camera. The
			// production action listeners and change/upload path are not replaced.
			check(`(()=>{window.inputClicks=[];document.addEventListener('click',e=>{if(e.target.matches('input[type=file]')){inputClicks.push(e.target.id);e.preventDefault();}},true);for(var action of ['photo','video','files']){document.getElementById('attach-btn').click();document.getElementById('phone-attachment-'+action).click();if(!document.getElementById('phone-attachment-menu').hidden)return 'action left menu open';}return JSON.stringify(inputClicks)===JSON.stringify(['phone-photo-input','phone-video-input','phone-files-input'])?'ok':'input activation '+inputClicks;})()`)
			check(`(async()=>{var i=document.getElementById('phone-photo-input');i.dispatchEvent(new Event('cancel'));i.dispatchEvent(new Event('change'));await new Promise(r=>setTimeout(r,30));return pendingAttachments.length===0?'ok':'cancel uploaded';})()`)
			check(`(async()=>{window.chooseCapture=function(id,name,type){var i=document.getElementById(id),d=new DataTransfer();d.items.add(new File(['fixture'],name,{type:type}));i.files=d.files;i.dispatchEvent(new Event('change'));return i;};for(var id of ['phone-photo-input','phone-photo-input','phone-video-input']){document.getElementById('attach-btn').click();document.getElementById(id==='phone-video-input'?'phone-attachment-video':'phone-attachment-photo').click();var before=pendingAttachments.length,i=chooseCapture(id,id==='phone-video-input'?'clip.mp4':'same.jpg',id==='phone-video-input'?'video/mp4':'image/jpeg');if(i.value)return 'input not reset';for(var n=0;n<100&&document.getElementById('attach-btn').disabled;n++)await new Promise(r=>setTimeout(r,10));if(pendingAttachments.length!==before+1)return 'upload missing/duplicate';}return 'ok';})()`)
			if uploads.Load() != 3 || pickers.Load() != 0 {
				t.Fatalf("phone uploads=%d desktop pickers=%d", uploads.Load(), pickers.Load())
			}
			check(`(()=>{var b=document.getElementById('attach-btn');b.click();document.getElementById('phone-attachment-photo').click();activeSessionID='other';chooseCapture('phone-photo-input','stale.jpg','image/jpeg');activeSessionID='';b.click();document.getElementById('phone-attachment-photo').click();projectEpoch++;chooseCapture('phone-photo-input','stale.jpg','image/jpeg');b.click();document.getElementById('phone-attachment-photo').click();queueEdit={};chooseCapture('phone-photo-input','stale.jpg','image/jpeg');b.click();if(!document.getElementById('phone-attachment-menu').hidden)return 'menu during queue edit';queueEdit=null;return 'ok';})()`)
			if uploads.Load() != 3 {
				t.Fatalf("stale scope uploaded: %d", uploads.Load())
			}
			check(`(async()=>{var b=document.getElementById('attach-btn'),stop=document.getElementById('stop-run-btn'),send=document.getElementById('send-btn');var oldFetch=window.fetch;window.fetch=async function(url,options){if(url==='/api/attachment/upload')await new Promise(r=>window.releaseCaptureUpload=r);return oldFetch(url,options);};stop.hidden=false;b.click();document.getElementById('phone-attachment-photo').click();chooseCapture('phone-photo-input','busy.jpg','image/jpeg');if(!b.disabled||b.getAttribute('aria-busy')!=='true'||stop.disabled||send.disabled)return 'busy/Stop/send state';chooseCapture('phone-photo-input','duplicate.jpg','image/jpeg');b.click();if(!document.getElementById('phone-attachment-menu').hidden)return 'busy menu';releaseCaptureUpload();for(var n=0;n<100&&b.disabled;n++)await new Promise(r=>setTimeout(r,10));window.fetch=oldFetch;if(b.disabled||b.hasAttribute('aria-busy')||pendingAttachments.length!==4)return 'busy not released';return 'ok';})()`)
			if uploads.Load() != 4 {
				t.Fatalf("busy duplicate upload: %d", uploads.Load())
			}
			check(`(async()=>{var oldFetch=window.fetch;window.fetch=async function(url,options){if(url==='/api/attachment/upload')return new Response('fixture failure',{status:500});return oldFetch(url,options);};var b=document.getElementById('attach-btn');b.click();document.getElementById('phone-attachment-photo').click();var i=chooseCapture('phone-photo-input','error.jpg','image/jpeg');for(var n=0;n<100&&b.disabled;n++)await new Promise(r=>setTimeout(r,10));window.fetch=oldFetch;return !b.disabled&&!i.value&&pendingAttachments.length===4&&fixtureToasts.some(s=>s.includes('fixture failure'))?'ok':'failure recovery';})()`)
			check(`(()=>{var b=document.getElementById('attach-btn');b.click();var m=document.getElementById('phone-attachment-menu');document.getElementById('phone-attachment-close').focus();m.dispatchEvent(new KeyboardEvent('keydown',{key:'Tab',bubbles:true}));if(!m.hidden||document.activeElement!==b)return 'Tab exit';pendingAttachments=Array(8).fill('/full');document.getElementById('attach-btn').click();if(!document.getElementById('phone-attachment-menu').hidden)return 'menu at attachment limit';return fixtureErrors.length?fixtureErrors.join('\n'):'ok';})()`)
		})
	}
}
