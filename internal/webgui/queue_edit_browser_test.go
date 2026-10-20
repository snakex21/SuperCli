package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// The actual complete chat script, native dialog and HTTP SSE run only against
// an isolated in-memory queue. No Engine, user data, model or OS action exists.
func TestQueueEditNativeBrowserAutoSend(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		width, height  int
		save, autoStop bool
	}{{"desktop-save", 1280, 900, true, false}, {"phone-cancel", 390, 844, false, false}, {"auto-merge-stop", 390, 844, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
			defer cancel()
			type task struct {
				ID          string   `json:"id"`
				Prompt      string   `json:"prompt"`
				Session     string   `json:"session_id"`
				Attachments []string `json:"attachments"`
			}
			type request struct {
				Prompt      string   `json:"prompt"`
				QueueID     string   `json:"queue_task_id"`
				Attachments []string `json:"attachments"`
			}
			tasks := []task{{"a", "original A", "s1", []string{"/stage/original.txt"}}, {"b", "original B", "s1", []string{}}}
			var mu sync.Mutex
			var requests []request
			attempts := 0
			claimed := map[string]bool{}
			starts := make(chan int, 4)
			finishes := []chan struct{}{make(chan struct{}), make(chan struct{})}
			patchStarted, patchPermit := make(chan struct{}, 1), make(chan struct{})
			reloadStarted, reloadPermit := make(chan struct{}, 1), make(chan struct{})
			patched := false
			mergeStarted, mergePermit := make(chan struct{}, 1), make(chan struct{})
			queueMode := "off"
			if tc.autoStop {
				queueMode = "auto"
			}
			en, err := assetsFS.ReadFile("assets/locales/en.json")
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					fmt.Fprintf(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/app.css"></head><body><script>var fixtureCatalog=%s;</script><script src="/fixture.js"></script><script src="/runtime.js"></script><script>var superCliUI=window.SuperCliUI;superCliUI.createComposerDraftStore=function(){return {scope:function(){return 'session:s1';},clear:function(){},restore:function(){}};};</script><script src="/js/05-chat.js"></script><script src="/js/05a-lightbox.js"></script><script src="/fixture-ready.js"></script></body></html>`, en)
				case "/fixture.js":
					w.Header().Set("Content-Type", "text/javascript")
					fmt.Fprint(w, queueEditBrowserPrelude)
				case "/fixture-ready.js":
					w.Header().Set("Content-Type", "text/javascript")
					fmt.Fprint(w, queueEditBrowserReady)
				case "/api/tasks":
					if r.Method == http.MethodPost && r.URL.Query().Get("merge") == "1" {
						mergeStarted <- struct{}{}
						select {
						case <-mergePermit:
						case <-r.Context().Done():
							return
						}
						mu.Lock()
						tasks = []task{{ID: "merged", Prompt: "original A\noriginal B", Session: "s1", Attachments: []string{"/stage/original.txt"}}}
						mu.Unlock()
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "merged", "prompt": "original A\noriginal B", "session_id": "s1", "attachments": []string{"/stage/original.txt"}, "merged": true}})
						return
					}
					if r.Method == http.MethodPatch {
						var body task
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							http.Error(w, err.Error(), 400)
							return
						}
						patchStarted <- struct{}{}
						select {
						case <-patchPermit:
						case <-r.Context().Done():
							return
						}
						mu.Lock()
						defer mu.Unlock()
						if claimed[body.ID] {
							http.Error(w, "409 task claimed", 409)
							return
						}
						for i := range tasks {
							if tasks[i].ID == body.ID {
								tasks[i].Prompt = body.Prompt
								tasks[i].Attachments = body.Attachments
							}
						}
						patched = true
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{}`)
						return
					}
					mu.Lock()
					if r.Method == http.MethodDelete {
						id := r.URL.Query().Get("id")
						for i := range tasks {
							if tasks[i].ID == id {
								tasks = append(tasks[:i], tasks[i+1:]...)
								break
							}
						}
						mu.Unlock()
						fmt.Fprint(w, `{}`)
						return
					}
					needsGate := patched
					mu.Unlock()
					if needsGate {
						select {
						case reloadStarted <- struct{}{}:
						default:
						}
						select {
						case <-reloadPermit:
						case <-r.Context().Done():
							return
						}
					}
					mu.Lock()
					defer mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"tasks": tasks, "queue_merge": queueMode, "running": true})
				case "/api/chat":
					var body request
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					mu.Lock()
					attempts++
					index := len(requests)
					if index >= len(finishes) || (body.QueueID != "" && claimed[body.QueueID]) {
						mu.Unlock()
						http.Error(w, "duplicate claim", 409)
						return
					}
					if body.QueueID != "" {
						claimed[body.QueueID] = true
					}
					requests = append(requests, body)
					mu.Unlock()
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-store")
					fmt.Fprint(w, "data: {\"type\":\"session\",\"session_id\":\"s1\"}\n\ndata: {\"type\":\"session_activity\",\"session_id\":\"s1\",\"user_seq\":3}\n\n")
					w.(http.Flusher).Flush()
					starts <- index
					select {
					case <-finishes[index]:
					case <-r.Context().Done():
						return
					}
					fmt.Fprint(w, "data: {\"type\":\"done\"}\n\n")
					w.(http.Flusher).Flush()
				case "/runtime.js":
					content, err := sharedUIFS.ReadFile("shared-ui/runtime.js")
					if err != nil {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", "text/javascript")
					_, _ = w.Write(content)
				default:
					content, err := assetsFS.ReadFile("assets" + r.URL.Path)
					if err != nil {
						http.NotFound(w, r)
						return
					}
					if strings.HasSuffix(r.URL.Path, ".js") {
						w.Header().Set("Content-Type", "text/javascript")
					} else {
						w.Header().Set("Content-Type", "text/css")
					}
					_, _ = w.Write(content)
				}
			}))
			defer server.Close()
			browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: tc.width, Height: tc.height})
			if err != nil {
				t.Fatal(err)
			}
			defer browser.Close()
			page, err := browser.NewPage(ctx, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			evaluate := func(script string, out any) {
				t.Helper()
				if err := page.Eval(ctx, script, out); err != nil {
					t.Fatal(err)
				}
			}
			wait := func(ch <-chan struct{}) {
				t.Helper()
				select {
				case <-ch:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			waitStart := func(want int) {
				t.Helper()
				select {
				case got := <-starts:
					if got != want {
						t.Fatalf("run=%d want %d", got, want)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if _, err := page.Screenshot(ctx); err != nil {
				t.Fatal(err)
			}
			evaluate(`window.fixturePrimary=sendPrompt('primary SSE');true`, nil)
			waitStart(0)
			if tc.autoStop {
				evaluate(`loadPromptQueue()`, nil)
				close(finishes[0])
				evaluate(`fixturePrimary`, nil)
				wait(mergeStarted)
				var safe bool
				evaluate(`(()=>{editQueuedTask(promptQueue[1]);return queueEdit===null&&queueMerging;})()`, &safe)
				if !safe {
					t.Fatal("submitted HTTP merge allowed a new editor")
				}
				evaluate(`document.getElementById('stop-run-btn').click();true`, nil)
				close(mergePermit)
				evaluate(`fixtureUntil(()=>!queueMerging&&!queueDispatching)`, nil)
				evaluate(`pauseQueue&&runStopRevision===1&&!streaming`, &safe)
				mu.Lock()
				posts := attempts
				mu.Unlock()
				if !safe || posts != 1 {
					t.Fatalf("merge reply revived stopped handoff: paused=%v chat attempts=%d", safe, posts)
				}
				return
			}
			evaluate(`(async()=>{await loadPromptQueue();document.querySelector('[data-queue-edit-id="a"]').click();queueEdit.input.value='edited A\n'+('long/path/'.repeat(30));queueEdit.input.dispatchEvent(new Event('input',{bubbles:true}));queueEdit.attachments=['/stage/revised.txt'];return true;})()`, nil)
			close(finishes[0])
			evaluate(`fixturePrimary`, nil)
			var blocked bool
			evaluate(`queueEdit&&queueEdit.dialog.open&&queueEdit.input.value.startsWith('edited A')&&!streaming&&!!queueAutoDrain`, &blocked)
			if !blocked {
				t.Fatal("primary EOF discarded editor or failed to defer")
			}
			mu.Lock()
			count := len(requests)
			mu.Unlock()
			if count != 1 {
				t.Fatalf("stale head auto-sent: %d chat POSTs", count)
			}
			var fits bool
			evaluate(`(()=>{var r=queueEdit.dialog.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth+1&&r.top>=0&&r.bottom<=innerHeight+1;})()`, &fits)
			if !fits {
				t.Fatal("queue editor exceeds viewport")
			}
			if dir := os.Getenv("SUPERCLI_QUEUE_EDIT_ARTIFACT_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				image, err := page.Screenshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				name := filepath.Join(dir, "queue-edit-"+tc.name+".png")
				if err := os.WriteFile(name, image, 0644); err != nil {
					t.Fatal(err)
				}
				t.Logf("artifact %s", name)
			}
			if tc.save {
				evaluate(`Array.from(queueEdit.dialog.querySelectorAll('button')).find(n=>n.textContent===t('common.save')).click();true`, nil)
				wait(patchStarted)
				mu.Lock()
				count = len(requests)
				mu.Unlock()
				if count != 1 {
					t.Fatal("dispatched before PATCH completion")
				}
				close(patchPermit)
				wait(reloadStarted)
				var waiting bool
				evaluate(`queueEdit&&queueEdit.saving&&queueEdit.dialog.open`, &waiting)
				if !waiting {
					t.Fatal("editor closed before durable reload")
				}
				mu.Lock()
				count = len(requests)
				mu.Unlock()
				if count != 1 {
					t.Fatal("dispatched before durable reload")
				}
				close(reloadPermit)
			} else {
				evaluate(`Array.from(queueEdit.dialog.querySelectorAll('button')).find(n=>n.textContent===t('common.cancel')).click();true`, nil)
			}
			waitStart(1)
			evaluate(`fixtureUntil(()=>fixtureSends.length===2)`, nil)
			mu.Lock()
			sent := requests[1]
			count = len(requests)
			mu.Unlock()
			wantPrompt, wantAttachment := "original A", "/stage/original.txt"
			if tc.save {
				wantPrompt = "edited A\n" + strings.Repeat("long/path/", 30)
				wantAttachment = "/stage/revised.txt"
			}
			if count != 2 || sent.QueueID != "a" || sent.Prompt != wantPrompt || len(sent.Attachments) != 1 || sent.Attachments[0] != wantAttachment {
				t.Fatalf("wrong dispatch: count=%d sent=%+v", count, sent)
			}
			evaluate(`pauseQueue=true;true`, nil)
			close(finishes[1])
			evaluate(`Promise.all(fixtureSends)`, nil)
			mu.Lock()
			count = len(requests)
			posts := attempts
			mu.Unlock()
			if count != 2 || posts != 2 {
				t.Fatalf("duplicate dispatch: accepted=%d attempts=%d", count, posts)
			}
		})
	}
}

const queueEditBrowserPrelude = `
function $(selector){var n=document.querySelector(selector);if(!n){n=document.createElement(selector==='#prompt'?'textarea':selector==='#composer'?'form':'div');n.id=selector.slice(1);document.body.appendChild(n);}return n;}
function $$(selector){return Array.from(document.querySelectorAll(selector));}
function el(tag,cls,text){var n=document.createElement(tag);n.className=cls||'';if(text!=null)n.textContent=text;return n;}
function t(key){return fixtureCatalog[key]||key;}
function i18nEl(tag,cls,key){return el(tag,cls,t(key));}
async function j(url,options){var r=await fetch(url,options);if(!r.ok)throw new Error(await r.text());return r.json();}
function jpost(url,body){return j(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});}
var activeAppDialog=null,ui={appBadge:false},overlay={hidden:true},currentSection='',sections={},sessionByID={s1:{}},transcriptSessionID='s1',transcriptAbortCtl=null,loadedTranscriptMessages=[],transcriptHasMore=false,transcriptBeforeSeq=0,transcriptLiveAppend=false;
var stream=el('div'),toolRows={},workerRows={},openToolOrder=[];document.body.appendChild(stream);
function fmtDuration(){return '1s';}function clip(s){return s;}
`
const queueEditBrowserReady = `
activeSessionID='s1';
['updateAppBadge','wireQueuedTaskDrag','sealAssistantSegment','closeQuestionOverlay','settleOpenTools','releaseLiveTranscriptBlocks','renderStats','addFileChanges','addTurnMeta','loadSideGoal','closeAssistantReasoning','flushAssistantRender','releaseAssistantParagraphCaches','pruneWorkerOverview','bindLiveMessageReceipt','notifyDone','rememberSentAttachments','toast'].forEach(function(name){window[name]=function(){};});
addUserMsg=function(){return el('div');};addLatestMessageRewind=async function(){return 0;};loadSessions=function(){return new Promise(function(){});};
var fixtureSends=[],fixtureSend=sendPrompt;sendPrompt=function(){var p=fixtureSend.apply(null,arguments);fixtureSends.push(p);return p;};
async function fixtureUntil(check){for(var i=0;i<100;i++){if(check())return true;await new Promise(r=>setTimeout(r,10));}throw new Error('fixture condition not reached');}
`
