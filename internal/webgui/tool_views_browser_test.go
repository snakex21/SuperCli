package webgui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// Readable tool views parse each tool's text format and fall back to raw
// output (false) for anything they do not recognise.
func TestToolViewsNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	pl, _ := assetsFS.ReadFile("assets/locales/pl.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><head><link rel="stylesheet" href="/app.css"></head><body><script>var fixtureCatalog=%s;</script><script src="/runtime.js"></script><script src="/js/00-helpers.js"></script><script src="/prelude.js"></script><script src="/js/03b-highlight.js"></script><script src="/js/04-transcript.js"></script><script src="/js/04c-tool-views.js"></script><script>window.fixtureReady=true;</script></body></html>`, pl)
		case "/runtime.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			b, _ := sharedUIFS.ReadFile("shared-ui/runtime.js")
			w.Write(b)
		case "/prelude.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			fmt.Fprint(w, queueEditBrowserPrelude+`
var preferences={},runToolCount=0;function registerLanguageRefresh(){}function toolDisclosureOpen(){return false;}
function thinkingDisclosureOpen(){return false;}function renderText(s){return escHtml(s);}
function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}
function renderSentAttachments(){}function sentAttachmentsFor(){return [];}
function userMessageDisplayText(s){return s;}function pauseWorkerStatusSync(){}
`)
		case "/app.css":
			w.Header().Set("Content-Type", "text/css")
			b, _ := assetsFS.ReadFile("assets/app.css")
			w.Write(b)
		default:
			b, err := assetsFS.ReadFile("assets" + r.URL.Path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 900, Height: 900})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var failure string
	if err := page.Eval(ctx, `(()=>{try{
function check(ok,why){if(!ok)throw Error(why);}
check(window.fixtureReady,'fixture scripts');
function render(name,raw){var b=document.createElement('div');document.body.appendChild(b);var ok=renderRichToolPayload(b,'Output',raw,name,'{}');return ok?b:null;}

var tree=render('list_dir','C:\\proj contains 3 item(s) (within depth 2; deeper folders marked):\ngo/\ngo/a.go (2048 bytes)\nREADME.md (10 bytes)\n... (entry limit 3 reached; listing incomplete; narrow path)');
check(tree,'list_dir not recognised');
var rows=tree.querySelectorAll('.tv-tree-row');check(rows.length===3,'tree rows '+rows.length);
check(rows[0].classList.contains('dir')&&rows[1].querySelector('.tv-name').textContent==='a.go','tree names');
check(parseInt(rows[1].style.paddingLeft)>parseInt(rows[0].style.paddingLeft),'nested entry not indented');
check(rows[1].querySelector('.tv-size').textContent==='2.0 KB','size label');
check(tree.querySelector('.tv-foot')&&tree.querySelector('.tv-count').textContent==='3','tree footer/count');

var search=render('search_code','a/b.go:12:func x() {\na/b.go:20:return y\nc.js:3:z()\n[search limit reached: 3 matches]');
check(search,'search_code locations not recognised');
check(search.querySelectorAll('.file-code-section').length===2&&search.querySelectorAll('.file-code-line.hit').length===3,'search groups');
check(!render('search_code','SuperCli/a.go\nSuperCli/b.go'),'file list must stay raw');

var web=render('web_lookup','Results for "go" (engine: duckduckgo):\n\n1. Go <img src=x onerror=window.xss=1>\n   https://go.dev/\n   The Go language.\n2. Bad\n   javascript:alert(1)\n\nUse web_download for a direct file URL.');
check(web,'web results not recognised');
var cards=web.querySelectorAll('.tv-card');check(cards.length===2,'cards '+cards.length);
check(cards[0].querySelector('a').href==='https://go.dev/'&&cards[0].querySelector('.tv-card-snippet').textContent==='The Go language.','card link/snippet');
check(!cards[1].querySelector('a'),'non-http URL became a link');
check(!window.xss&&!web.querySelector('img'),'title rendered as HTML');
check(web.querySelector('.tv-foot'),'usage note kept');

var fetched=render('web_fetch','URL: https://example.com/\nTitle: Example\n\nBody text');
check(fetched&&fetched.querySelector('.tv-card-title').textContent==='Example'&&fetched.textContent.includes('Body text'),'web_fetch header');

var goal=render('goal','3 task(s):\n  [x] 1. design\n  [>] 2. build\n  [~] 3. skip it');
check(goal,'goal tasks not recognised');
var tasks=goal.querySelectorAll('.tv-task');
check(tasks.length===3&&tasks[0].classList.contains('done')&&tasks[1].classList.contains('active')&&tasks[2].classList.contains('skipped'),'task states');
var added=render('goal','added 2 tasks:\n  1. first\n  2. second');
check(added&&added.querySelectorAll('.tv-task.pending').length===2,'added tasks');
check(!render('goal','task 1 -> done'),'status line must stay raw');
check(!render('list_dir','unexpected text'),'unknown format must stay raw');

// Tool input: argument rows instead of JSON braces and quotes.
var input=toolInputPre('{"path":"C:\\\\proj","depth":2,"tools":["a","b"],"opts":{"deep":true}}');
check(input.className==='tool-input-view','input still JSON: '+input.outerHTML);
var keys=[].map.call(input.querySelectorAll('.tool-input-key'),function(k){return k.textContent;}).join(',');
check(keys==='path,depth,tools,opts.deep','input keys '+keys);
check(!/[{}"]/.test(input.textContent)&&input.textContent.includes('a, b'),'input text '+input.textContent);
check(toolInputPre('{}').tagName==='PRE'&&toolInputPre('not json').tagName==='PRE','empty/invalid input must stay raw');

// load_session_image: translated name and a real thumbnail from the path line.
var media=toolMediaDescriptor('Session image img_1 (a.png) will be attached to the next model request\npath: C:\\data\\a.png','load_session_image',false);
check(media&&media.path==='C:\\data\\a.png'&&media.kind==='image','load_session_image preview '+JSON.stringify(media));
check(!toolMediaDescriptor('Session image img_1 (a.png) will be attached to the next model request','load_session_image',false),'missing path must not preview');
check(!toolMediaDescriptor('x\npath: relative/a.png','load_session_image',false),'relative path must not preview');
// Reloaded images stay folded inside their row until it is opened.
window.renderSentAttachments=function(node,paths){var i=document.createElement('img');i.dataset.path=paths[0];node.appendChild(i);};
var holder=document.createElement('div');document.body.appendChild(holder);
var row=document.createElement('details');row._body=document.createElement('div');row.appendChild(document.createElement('summary'));row.appendChild(row._body);holder.appendChild(row);
appendToolMediaPreview(row,'Session image img_1 (a.png) will be attached to the next model request\npath: C:\\data\\a.png','load_session_image',false);
check(holder.children.length===1&&!row._body.querySelector('img'),'reloaded image shown before opening');
row.open=true;row.dispatchEvent(new Event('toggle'));
check(row._body.querySelector('img')&&row._body.querySelector('img').dataset.path==='C:\\data\\a.png'&&holder.children.length===1,'reloaded image missing inside row');
check(toolDisplayName('load_session_image')==='Podgląd obrazu','load_session_image label '+toolDisplayName('load_session_image'));
return '';
}catch(e){return e.stack||String(e);}})()`, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
}
