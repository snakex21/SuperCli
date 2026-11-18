package webgui

import (
	"strings"
	"testing"
)

const fontsOpenAppearance = `(async()=>{
 const sleep=ms=>new Promise(r=>setTimeout(r,ms));
 if(document.querySelector('#panel-overlay').hidden)document.querySelector('#open-panel').click();
 document.querySelector('#panel-nav button[data-sec="appearance"]').click();
 for(let i=0;i<100;i++){if(document.querySelector('#panel-content select.font-select'))return true;await sleep(50);}
 return false;})()`

// Every embedded family loads in the browser, the font choice is persisted
// and survives a reload, the preview follows the choice, and a "Custom…"
// font that is not installed falls back to the system stack (and cannot
// break out of the quoted family name).
func TestFontSettingsEmbeddedPersistAndCustomFallback(t *testing.T) {
	app := audit130StartApp(t, 1280, 900)
	app.setLanguage("pl")
	var loaded map[string]any
	app.eval(`(async()=>{
const fams=new Set();
for (const v of Object.values(UI_FONTS).concat(Object.values(CODE_FONTS))) { const m=v.match(/^"(SuperCli [^"]+)"/); if (m) fams.add(m[1]); }
const out={};
for (const f of fams) { const faces=await document.fonts.load('16px "'+f+'"', 'Aąř Жж 0'); out[f]=faces.length; }
return out;})()`, &loaded)
	if len(loaded) < 10 {
		t.Fatalf("expected at least 10 embedded families, got %v", loaded)
	}
	for family, n := range loaded {
		if n.(float64) < 1 {
			t.Errorf("embedded family %q did not load", family)
		}
	}

	var ok bool
	app.eval(fontsOpenAppearance, &ok)
	if !ok {
		t.Fatal("appearance section did not render the font selects")
	}
	// Choose Inter and Fira Code (ligatures) through the selects.
	app.eval(`(async()=>{
for (const [key,val] of [["uiFont","inter"],["codeFont","firalig"]]) {
  const s=document.querySelector('select.font-select[data-font-key="'+key+'"]'); s.value=val; s.dispatchEvent(new Event('change',{bubbles:true}));
}
await new Promise(r=>setTimeout(r,800)); return true;})()`, nil)
	app.shot("font-settings-inter-firalig")
	var state map[string]string
	app.eval(`(()=>({sans:getComputedStyle(document.documentElement).getPropertyValue('--sans'),
 preview:document.querySelector('.font-preview-text').style.fontFamily,
 code:document.querySelector('.font-preview-code').style.fontFamily}))()`, &state)
	var visibleCustom int
	app.eval(`[...document.querySelectorAll('.font-custom-row,.font-custom-hint')].filter(e=>getComputedStyle(e).display!=='none').length`, &visibleCustom)
	if visibleCustom != 0 {
		t.Fatalf("%d custom-font rows visible although no font is set to Custom…", visibleCustom)
	}
	if !strings.Contains(state["sans"], "SuperCli Inter") || !strings.Contains(state["preview"], "SuperCli Inter") || !strings.Contains(state["code"], "SuperCli Fira Code Ligatures") {
		t.Fatalf("choice not applied to the page and preview: %v", state)
	}
	// Persisted: a fresh page load keeps both choices.
	app.reload()
	var after map[string]string
	app.eval(`(()=>({ui:ui.uiFont, code:ui.codeFont, mono:getComputedStyle(document.documentElement).getPropertyValue('--mono')}))()`, &after)
	if after["ui"] != "inter" || after["code"] != "firalig" || !strings.Contains(after["mono"], "SuperCli Fira Code Ligatures") {
		t.Fatalf("font choice not persisted across reload: %v", after)
	}

	// Custom font: a missing name keeps the system stack as fallback, and
	// quotes/semicolons cannot escape the family name.
	app.eval(fontsOpenAppearance, &ok)
	app.eval(`(async()=>{
const s=document.querySelector('select.font-select[data-font-key="uiFont"]'); s.value='custom'; s.dispatchEvent(new Event('change',{bubbles:true}));
const i=document.querySelector('input.font-custom-input[data-font-key="uiFontCustom"]');
i.value='NoSuchFont 123"; color: red; }'; i.dispatchEvent(new Event('input',{bubbles:true}));
await new Promise(r=>setTimeout(r,800)); return true;})()`, nil)
	var custom map[string]any
	app.eval(`(()=>({sans:getComputedStyle(document.documentElement).getPropertyValue('--sans').trim(),
 body:getComputedStyle(document.body).fontFamily, hidden:document.querySelector('.font-custom-row').hidden,
 installed:document.fonts.check('16px "NoSuchFont 123"')}))()`, &custom)
	sans := custom["sans"].(string)
	// Punctuation is dropped, so the typed text stays one quoted family name
	// followed by the system stack.
	if !strings.HasPrefix(sans, `"NoSuchFont 123`) || !strings.Contains(sans, `", -apple-system, "Segoe UI"`) || strings.Count(sans, `"`) != 4 || strings.ContainsAny(sans, ";{}") {
		t.Fatalf("custom font family not sanitized with fallback: %q", sans)
	}
	if custom["hidden"] == true {
		t.Fatal("custom name field hidden while Custom… is selected")
	}
	if !strings.Contains(custom["body"].(string), "Segoe UI") {
		t.Fatalf("body font has no system fallback: %v", custom["body"])
	}
	app.shot("font-settings-custom-missing")
}
