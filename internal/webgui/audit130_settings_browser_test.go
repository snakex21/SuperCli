package webgui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// audit-130: GUI settings (control panel, the TUI /settings parity surface)
// driven with DOM events in the complete app: open, change a config knob,
// cancel an edit with Escape, persistence across a reload, appearance.

const audit130OpenMaxRetries = `(async()=>{
 const sleep=ms=>new Promise(r=>setTimeout(r,ms));
 if(document.querySelector('#panel-overlay').hidden)document.querySelector('#open-panel').click();
 document.querySelector('#panel-nav button[data-sec="settings"]').click();
 for(let i=0;i<100;i++){const n=[...document.querySelectorAll('#panel-content .k-name')].find(e=>e.title==='config.toml · max_retries');if(n)return true;await sleep(50);}
 return false;})()`

const audit130MaxRetriesInput = `[...document.querySelectorAll('#panel-content .k-name')].find(e=>e.title==='config.toml · max_retries').parentElement.querySelector('input.k-edit')`

func audit130ConfigKnob(app *audit130App, key string) string {
	app.t.Helper()
	var raw string
	app.eval(`(async()=>{const r=await fetch('/api/config');const j=await r.json();const k=(j.knobs||[]).find(k=>k.key===`+jsString(key)+`);return k?String(k.raw||''):'<missing>';})()`, &raw)
	return raw
}

func TestAudit130SettingsChangePersistAndCancel(t *testing.T) {
	app := audit130StartApp(t, 1280, 900)
	app.setLanguage("pl")
	var ok bool
	app.eval(audit130OpenMaxRetries, &ok)
	if !ok {
		t.Fatal("settings section did not render the max_retries knob")
	}
	before := audit130ConfigKnob(app, "max_retries")
	// Change: type 7 and press Enter.
	app.eval(`(async()=>{const i=`+audit130MaxRetriesInput+`;i.focus();i.value='7';i.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true}));await new Promise(r=>setTimeout(r,400));return true;})()`, nil)
	if got := audit130ConfigKnob(app, "max_retries"); got != "7" {
		t.Fatalf("max_retries after Enter = %q (before %q), want 7", got, before)
	}
	var persisted string
	_ = filepath.WalkDir(app.dataDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".toml") {
			if b, e := os.ReadFile(path); e == nil {
				for _, line := range strings.Split(string(b), "\n") {
					if strings.HasPrefix(strings.TrimSpace(line), "max_retries") {
						persisted = path + ": " + strings.TrimSpace(line)
					}
				}
			}
		}
		return nil
	})
	t.Logf("persisted: %s", persisted)
	if !strings.Contains(persisted, "max_retries = 7") {
		t.Fatalf("max_retries = 7 not written to a config file under the isolated data dir: %q", persisted)
	}
	// Cancel: type 9 and press Escape inside the field: the edit is reverted
	// and nothing is saved.
	var reverted string
	app.eval(`(async()=>{await `+audit130OpenMaxRetries+`;const i=`+audit130MaxRetriesInput+`;i.focus();i.value='9';i.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));await new Promise(r=>setTimeout(r,400));return i.value;})()`, &reverted)
	if reverted != "7" {
		t.Errorf("Escape did not revert the field: %q", reverted)
	}
	if got := audit130ConfigKnob(app, "max_retries"); got != "7" {
		t.Fatalf("Escape saved the cancelled edit: max_retries = %q", got)
	}
	// Reload: the saved value is shown again.
	app.reload()
	app.eval(audit130OpenMaxRetries, &ok)
	var shown string
	app.eval(`(`+audit130MaxRetriesInput+`).value`, &shown)
	if shown != "7" {
		t.Fatalf("after reload the settings field shows %q, want 7", shown)
	}
	// Escape outside a field closes the panel.
	var hidden bool
	app.eval(`(async()=>{document.querySelector('#panel-close').focus();document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));await new Promise(r=>setTimeout(r,100));return document.querySelector('#panel-overlay').hidden;})()`, &hidden)
	if !hidden {
		t.Fatal("Escape did not close the control panel")
	}
	// Appearance persists across a reload (portable /api/settings).
	app.eval(`(async()=>{document.querySelector('#open-panel').click();document.querySelector('#panel-nav button[data-sec="appearance"]').click();await new Promise(r=>setTimeout(r,200));
 const b=[...document.querySelectorAll('#panel-content .seg button')].find(b=>b.textContent===t('app.light'));b.click();await new Promise(r=>setTimeout(r,400));return true;})()`, nil)
	app.reload()
	var theme string
	app.eval(`ui.theme`, &theme)
	if theme != "light" {
		t.Fatalf("theme after reload = %q, want light", theme)
	}
}

// Escape in a settings text field only cancels that edit (as the TUI does:
// the first Esc cancels the edit, a second Esc closes /settings). Focus stays
// inside the dialog, so the next Escape closes the panel.
func TestAudit130SettingsEscapeInFieldKeepsPanelOpen(t *testing.T) {
	app := audit130StartApp(t, 1280, 900)
	app.setLanguage("pl")
	var ok bool
	app.eval(audit130OpenMaxRetries, &ok)
	if !ok {
		t.Fatal("settings did not render")
	}
	original := audit130ConfigKnob(app, "max_retries")
	var got struct {
		Value        string `json:"value"`
		Hidden       bool   `json:"hidden"`
		FocusInside  bool   `json:"focusInside"`
		HiddenSecond bool   `json:"hiddenSecond"`
	}
	app.eval(`(async()=>{const sleep=ms=>new Promise(r=>setTimeout(r,ms));const ov=document.querySelector('#panel-overlay');const i=`+audit130MaxRetriesInput+`;i.focus();i.value='9';i.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));await sleep(200);
 const r={value:i.value,hidden:ov.hidden,focusInside:ov.contains(document.activeElement)};
 document.activeElement.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));await sleep(100);
 r.hiddenSecond=ov.hidden;return r;})()`, &got)
	t.Logf("escape in field: %+v (stored %q)", got, original)
	if got.Hidden {
		t.Fatalf("one Escape in the field closed the control panel (field value reverted to %q)", got.Value)
	}
	if got.Value != original {
		t.Errorf("Escape did not cancel the edit: field shows %q, stored value %q", got.Value, original)
	}
	if after := audit130ConfigKnob(app, "max_retries"); after != original {
		t.Errorf("cancelled edit was saved: max_retries %q -> %q", original, after)
	}
	if !got.FocusInside {
		t.Error("focus left the control panel after Escape in a field")
	}
	if !got.HiddenSecond {
		t.Error("a second Escape outside the field did not close the control panel")
	}
}

// The control panel overlay covers the app like a modal and is exposed as
// one: role=dialog aria-modal labelled by its title, focus moves into it on
// open, Tab wraps inside it, and closing returns focus to the opener.
func TestAudit130SettingsPanelFocusOnOpenAndClose(t *testing.T) {
	app := audit130StartApp(t, 1280, 900)
	app.setLanguage("pl")
	var got struct {
		Role        string `json:"role"`
		Modal       string `json:"modal"`
		FocusOpen   string `json:"focusOpen"`
		FocusClosed string `json:"focusClosed"`
		Label       string `json:"label"`
		TabWrap     string `json:"tabWrap"`
		ShiftWrap   string `json:"shiftWrap"`
	}
	app.eval(`(async()=>{const sleep=ms=>new Promise(r=>setTimeout(r,ms));const f=()=>{const a=document.activeElement;return a?a.tagName+'#'+a.id:'null';};
 const opener=document.querySelector('#open-panel');opener.focus();opener.click();await sleep(400);
 const ov=document.querySelector('#panel-overlay'),panel=ov.querySelector('.panel');
 const r={role:(panel.getAttribute('role')||ov.getAttribute('role')||''),modal:(panel.getAttribute('aria-modal')||ov.getAttribute('aria-modal')||''),focusOpen:f()+(ov.contains(document.activeElement)?' (inside)':' (outside)')};
 const lb=document.getElementById(panel.getAttribute('aria-labelledby')||'');r.label=lb&&panel.contains(lb)?lb.textContent.trim():'';
 const items=[...panel.querySelectorAll('button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),a[href]')].filter(e=>e.getClientRects().length);
 const first=items[0],last=items[items.length-1];
 last.focus();last.dispatchEvent(new KeyboardEvent('keydown',{key:'Tab',bubbles:true,cancelable:true}));r.tabWrap=document.activeElement===first?'first':f();
 first.dispatchEvent(new KeyboardEvent('keydown',{key:'Tab',shiftKey:true,bubbles:true,cancelable:true}));r.shiftWrap=document.activeElement===last?'last':f();
 document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));await sleep(100);
 r.focusClosed=f();return r;})()`, &got)
	t.Logf("panel a11y: %+v", got)
	var failures []string
	if !strings.Contains(got.FocusOpen, "(inside)") {
		failures = append(failures, "focus stays outside the opened panel: "+got.FocusOpen)
	}
	if got.Role != "dialog" || got.Modal != "true" {
		failures = append(failures, "panel lacks role=dialog aria-modal=true: role="+got.Role+" aria-modal="+got.Modal)
	}
	if got.Label == "" {
		failures = append(failures, "panel has no aria-labelledby title inside it")
	}
	if got.TabWrap != "first" || got.ShiftWrap != "last" {
		failures = append(failures, "Tab does not wrap inside the panel: Tab from last -> "+got.TabWrap+", Shift+Tab from first -> "+got.ShiftWrap)
	}
	if got.FocusClosed != "BUTTON#open-panel" {
		failures = append(failures, "focus went to "+got.FocusClosed+" after Escape closed the panel, want the opener BUTTON#open-panel")
	}
	if len(failures) > 0 {
		t.Fatalf("%s", audit130Join(failures))
	}
}
