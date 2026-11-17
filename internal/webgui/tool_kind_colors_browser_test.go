package webgui

import (
	"strings"
	"testing"
)

// Tool rows carry a colour per kind of tool (read, write, run, web) on the
// glyph and the title, as before 1.0.26; status still wins: a failed or
// denied row is red/denied and a row waiting for approval uses the pending
// colour, whatever its kind.
func TestToolRowKindColours(t *testing.T) {
	app := audit130StartApp(t, 1280, 900)
	app.setLanguage("pl")
	var got map[string]any
	app.eval(`(async()=>{
hideWelcome();
addUserMsg('Sprawdź projekt.');
addToolCall('read_lines','{"file":"main.go","from":1,"to":5}','r1');
addToolResult('r1','1 | package main');
addToolCall('patch_file','{"path":"main.go","old":"a","new":"b"}','w1');
addToolResult('w1','ok');
addToolCall('ctx_execute','{"command":["go","test","./..."]}','x1');
addToolResult('x1','ok');
addToolCall('web_fetch','{"url":"https://example.org"}','n1');
addToolResult('n1','page');
addToolCall('search_code','{"query":"main"}','f1');
addToolResult('f1','', 'boom');
await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
const css=getComputedStyle(document.documentElement);
const out={text:getComputedStyle(document.body).color, error:css.getPropertyValue('--status-error').trim()};
for (const id of ['r1','w1','x1','n1','f1']) {
  const row=toolRows[id]; const name=row.querySelector('.tname');
  out[id]={cls:row.className, name:getComputedStyle(name).color, glyph:getComputedStyle(name,'::before').color, glyphText:getComputedStyle(name,'::before').content};
}
return out;})()`, &got)
	app.shot("tool-kind-colours")
	row := func(id string) map[string]any { return got[id].(map[string]any) }
	text := got["text"].(string)
	colours := map[string]string{}
	for id, kind := range map[string]string{"r1": "tk-read", "w1": "tk-write", "x1": "tk-run", "n1": "tk-web"} {
		r := row(id)
		if !strings.Contains(r["cls"].(string), kind) {
			t.Fatalf("%s row lacks %s: %v", id, kind, r["cls"])
		}
		name, glyph := r["name"].(string), r["glyph"].(string)
		if name == text {
			t.Errorf("%s (%s) title has the plain text colour %s; want a kind colour", id, kind, name)
		}
		if glyph != name {
			t.Errorf("%s (%s) glyph colour %s differs from its title %s", id, kind, glyph, name)
		}
		if r["glyphText"] == "none" || r["glyphText"] == "" {
			t.Errorf("%s (%s) has no kind glyph", id, kind)
		}
		colours[name] = kind
	}
	if len(colours) != 4 {
		t.Errorf("the four tool kinds share colours: %v", colours)
	}
	failed := row("f1")
	if !strings.Contains(failed["cls"].(string), "failed") {
		t.Fatalf("failed row not marked failed: %v", failed["cls"])
	}
	if _, isKindColour := colours[failed["name"].(string)]; isKindColour {
		t.Errorf("failed read row keeps its kind colour %s; status must win", failed["name"])
	}
}
