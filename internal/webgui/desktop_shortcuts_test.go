package webgui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

type shortcutTestView struct {
	script      string
	nativeCalls int
}

func (v *shortcutTestView) Init(js string)                         { v.script = js }
func (v *shortcutTestView) EnableSelectiveBrowserShortcuts() error { v.nativeCalls++; return nil }

func TestDesktopShortcutBootstrap(t *testing.T) {
	v := &shortcutTestView{}
	if err := installDesktopShortcutProtection(v, "http://127.0.0.1:1234/"); err != nil {
		t.Fatal(err)
	}
	if v.nativeCalls != 1 || !strings.Contains(v.script, "preventDefault") {
		t.Fatal("native extension and DOM guard not both installed")
	}
	if desktopBrowserShortcutScript(":invalid") != "" {
		t.Fatal("invalid owned URL accepted")
	}
}

// Isolated headless browser and loopback fixture only: no Engine, app config,
// live user URL, LLM, or visible/native window. The guard is the first script,
// before a queue editor's capturing listener, just as with native Init.
func TestDesktopShortcutBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	keyboard, err := assetsFS.ReadFile("assets/js/12-keyboard-init.js")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the actual app keyboard listener, without running app initialization.
	appKeyboard := strings.Split(string(keyboard), "/* ═══ init")[0]
	var address string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		guard := ""
		if r.URL.Query().Get("native") == "1" {
			guard = desktopBrowserShortcutScript(address)
		}
		platform := r.URL.Query().Get("platform")
		fmt.Fprintf(w, `<!doctype html><html><head><script>Object.defineProperty(navigator,'platform',{value:%q});</script><script>%s</script></head><body><textarea id="queue-editor">queued draft</textarea><script>
  window.ui={keybinds:{panel:'Ctrl+R',sidebar:'Ctrl+S'}};
  window.overlay={hidden:true};window.panelToggles=0;window.sidebarToggles=0;
  window.openPanel=function(){overlay.hidden=false;panelToggles++};window.closePanel=function(){overlay.hidden=true;panelToggles++};
  window.toggleSidebar=function(){sidebarToggles++};window.$=function(){return {hidden:true}};
  window.installAppKeyboard=function(){%s};
  window.appKeys=[];
  document.addEventListener('keydown',function(e){window.appKeys.push(e.key)},true);
  document.getElementById('queue-editor').addEventListener('keydown',function(e){window.targetKeys=(window.targetKeys||0)+1});
  </script></body></html>`, platform, guard, appKeyboard)
	}))
	defer server.Close()
	address = server.URL
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 800, Height: 600})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	for _, tc := range []struct {
		platform string
		native   bool
	}{{"Win32", false}, {"Win32", true}, {"MacIntel", false}, {"MacIntel", true}, {"Linux x86_64", true}} {
		native := tc.native
		t.Run(fmt.Sprintf("%s/native=%v", tc.platform, native), func(t *testing.T) {
			suffix := "?platform=" + url.QueryEscape(tc.platform)
			if native {
				suffix += "&native=1"
			}
			page, err := browser.NewPage(ctx, server.URL+suffix)
			if err != nil {
				t.Fatal(err)
			}
			var failures []string
			script := fmt.Sprintf(`(() => {
  const native=%t, failures=[];
  const mac=/Mac|iPhone|iPad|iPod/i.test(navigator.platform);
  const mod=mac?{metaKey:true}:{ctrlKey:true};
  const cases=[
   ['reload','r',mod,true],['hard reload','R',{...mod,shiftKey:true},true],
   ['F5','F5',{},true],['CtrlF5','F5',{ctrlKey:true},true],['ShiftF5','F5',{shiftKey:true},true],
   ['print','p',mod,true],['save','s',mod,true],['source','u',mod,true],
   ['F12','F12',{},true],...['i','j','c'].map(k=>['devtools '+k,k,{...mod,shiftKey:true},true]),
   ...['c','v','x','a','z','y','+','-','0','w'].map(k=>['keep '+k,k,mod,false]),
   ['redo','z',{...mod,shiftKey:true},false],['AltF4','F4',{altKey:true},false],
   ['AltGr','r',{ctrlKey:true,altKey:true},false],['IME','r',{...mod,isComposing:true},false],
   ['IME229','r',{...mod,keyCode:229},false],['normal','r',{},false],
   ['other-mod','r',mac?{ctrlKey:true}:{metaKey:true},false],
   ['shift-save','s',{...mod,shiftKey:true},false],['AltR','r',{altKey:true},false],
   ['AltShiftR','r',{ctrlKey:true,altKey:true,shiftKey:true},false],['CmdF5','F5',{metaKey:true},false]
  ];
  if(mac) for(const key of ['i','j','c','u'])cases.push(['mac option '+key,key,{metaKey:true,altKey:true},true]);
  const editor=document.querySelector('textarea');editor.focus();
  for(const [name,key,mods,blocked] of cases){
   const before=window.appKeys.length,targetBefore=window.targetKeys||0;
   const event=new KeyboardEvent('keydown',{key,...mods,bubbles:true,cancelable:true});
   editor.dispatchEvent(event);
   if(event.defaultPrevented!==(native&&blocked))failures.push(name+' prevention='+event.defaultPrevented);
   if(window.appKeys.length!==before+1||(window.targetKeys||0)!==targetBefore+1)failures.push(name+' app handler lost');
  }
  window.installAppKeyboard();
  // The real app listener must still execute when a configured binding shares
  // a protected browser shortcut. Test both bubbling and queue-editor capture.
  for(const [key,counter] of [['r','panelToggles'],['s','sidebarToggles']]){
   const before=window[counter];editor.dispatchEvent(new KeyboardEvent('keydown',{key,ctrlKey:true,bubbles:true,cancelable:true}));
   if(window[counter]!==before+1)failures.push('real app binding lost: '+key);
  }
  // Queue editor uses a propagation barrier: the window capture guard must
  // already have run, while the editor itself still receives each key.
  editor.addEventListener('keydown',e=>e.stopPropagation());
  // Keep document capture alive, as used by the actual queue modal.
  let captured=0;document.addEventListener('keydown',()=>captured++,true);
  const queued=new KeyboardEvent('keydown',{key:'r',...mod,bubbles:true,cancelable:true});
  const targetBefore=window.targetKeys;editor.dispatchEvent(queued);
  if(queued.defaultPrevented!==native||captured!==1||window.targetKeys!==targetBefore+1)failures.push('queue capture barrier');
  const wheel=new WheelEvent('wheel',{ctrlKey:true,bubbles:true,cancelable:true});editor.dispatchEvent(wheel);
  if(wheel.defaultPrevented)failures.push('zoom wheel prevented');
  const context=new MouseEvent('contextmenu',{bubbles:true,cancelable:true});editor.dispatchEvent(context);
  if(context.defaultPrevented)failures.push('context menu prevented');
  if(editor.value!=='queued draft')failures.push('queue draft changed');
  return failures;
 })()`, native)
			if err := page.Eval(ctx, script, &failures); err != nil {
				t.Fatal(err)
			}
			if len(failures) > 0 {
				t.Fatal(failures)
			}
			// Re-running the guard in an unrelated frame must not cancel frame events.
			if native {
				var prevented bool
				script := `(async()=>{const f=document.createElement('iframe');f.src='/?native=1';document.body.append(f);await new Promise(r=>f.onload=r);const e=new f.contentWindow.KeyboardEvent('keydown',{key:'r',ctrlKey:true,bubbles:true,cancelable:true});f.contentDocument.body.dispatchEvent(e);return e.defaultPrevented})()`
				if err := page.Eval(ctx, script, &prevented); err != nil {
					t.Fatal(err)
				}
				if prevented {
					t.Fatal("iframe shortcut affected")
				}
			}
		})
	}
	// Even if native Init follows navigation to another page, the guard must
	// not act on an unrelated path in the same origin.
	unowned, err := browser.NewPage(ctx, server.URL+"/unowned?native=1")
	if err != nil {
		t.Fatal(err)
	}
	var prevented bool
	if err := unowned.Eval(ctx, `(()=>{const e=new KeyboardEvent('keydown',{key:'r',ctrlKey:true,bubbles:true,cancelable:true});document.body.dispatchEvent(e);return e.defaultPrevented})()`, &prevented); err != nil {
		t.Fatal(err)
	}
	if prevented {
		t.Fatal("unowned document affected by desktop guard")
	}
}
