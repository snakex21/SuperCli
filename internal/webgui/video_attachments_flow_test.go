package webgui

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/testbrowser"
)

type videoFixtureProvider struct {
	mu       sync.Mutex
	vision   atomic.Bool
	requests [][]llm.Message
}

func (p *videoFixtureProvider) Name() string         { return "echo-test" }
func (p *videoFixtureProvider) SupportsVision() bool { return p.vision.Load() }
func (p *videoFixtureProvider) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	p.requests = append(p.requests, append([]llm.Message(nil), msgs...))
	p.mu.Unlock()
	out := make(chan llm.Delta, 1)
	out <- llm.Delta{Content: "Fixture only.", FinishReason: "stop"}
	close(out)
	return out, nil
}

// Real shared uploader, native decoder, streaming disk upload, durable queue and
// /api/chat loop with a fake provider. All files/config/session data are temporary.
func TestVideoAttachmentsUploadScopeAndWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	app := newTestServer(t, false)
	provider := &videoFixtureProvider{}
	provider.vision.Store(true)
	app.eng.prov = provider
	html, _ := assetsFS.ReadFile("assets/index.html")
	shell := strings.Split(string(html), `<script src=`)[0]
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprintf(w, `%s<script>var fixtureCatalog={};var activeWorkspacePath=%q;</script><script src="/fixture.js"></script><script src="/runtime.js"></script><script>var superCliUI=window.SuperCliUI;</script><script src="/js/05-chat.js"></script><script src="/js/05c-video-attachments.js"></script></body></html>`, shell, app.eng.Home())
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, queueEditBrowserPrelude, `var fixtureToasts=[];function toast(s){fixtureToasts.push(s)}`)
		case "/runtime.js":
			b, _ := sharedUIFS.ReadFile("shared-ui/runtime.js")
			w.Header().Set("Content-Type", "text/javascript")
			w.Write(b)
		case "/api/attachment/upload":
			posts.Add(1)
			app.handleAttachmentUpload(w, r)
		case "/api/tasks":
			app.handleTasks(w, r)
		case "/api/chat":
			app.handleChat(w, r)
		default:
			b, err := assetsFS.ReadFile("assets" + r.URL.Path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if strings.HasSuffix(r.URL.Path, ".js") {
				w.Header().Set("Content-Type", "text/javascript")
			}
			w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 800, Height: 700})
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
	check := func(js string) {
		t.Helper()
		var got string
		if err := page.Eval(ctx, js, &got); err != nil {
			t.Fatal(err)
		}
		if got != "ok" {
			t.Fatal(got)
		}
	}
	check(`(async()=>{` + nativeVideoFixtureJS + `
 window.fixtureVideo=await makeVideoFixture();window.fixtureCheck=(ok,msg)=>{if(!ok)throw Error(msg)};
 await uploadAttachmentFiles([fixtureVideo,new File(['notes'],'notes.txt')],'composer.attach');
 fixtureCheck(pendingAttachments.length===8,'original+other+six frames');window.fixturePaths=pendingAttachments.slice();
 const task=await jpost('/api/tasks',{prompt:'inspect fixture',attachments:fixturePaths});
 const rows=await j('/api/tasks');fixtureCheck(rows.length===1&&JSON.stringify(rows[0].attachments)===JSON.stringify(fixturePaths),'durable queue rehydrate');
 window.fixtureTask=task;return 'ok';})()`)
	if posts.Load() != 1 {
		t.Fatalf("multipart posts=%d", posts.Load())
	}
	rows, err := app.eng.queuedTasks(ctx)
	if err != nil || len(rows) != 1 || len(rows[0].Attachments) != 8 {
		t.Fatalf("queue=%+v err=%v", rows, err)
	}
	paths := rows[0].Attachments
	before, err := buildDirectAttachmentImages(app.eng.Home(), paths[:1])
	if err != nil || len(before) != 0 {
		t.Fatalf("original-only wire images=%d err=%v", len(before), err)
	}
	original, err := os.ReadFile(paths[0])
	if err != nil || len(original) < 100 {
		t.Fatalf("original not preserved: %v", err)
	}
	for _, path := range paths[2:] {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || format != "jpeg" || cfg.Width != 96 || cfg.Height != 64 || len(raw) > 512<<10 {
			t.Fatalf("frame=%s config=%+v format=%s err=%v", path, cfg, format, err)
		}
	}
	// Baseline actual provider request with the original only: no video part or
	// invented image pixels. Then compare the same fixture plus native snapshots.
	check(`(async()=>{const r=await fetch('/api/chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({prompt:'original-only fixture baseline',attachments:fixturePaths.slice(0,1)})});const body=await r.text();fixtureCheck(r.ok&&!body.includes('"type":"error"'),'baseline chat '+body);return 'ok'})()`)
	provider.mu.Lock()
	baselineImages := 0
	for _, messages := range provider.requests {
		for _, message := range messages {
			for _, part := range message.Parts {
				if part.Image != nil {
					baselineImages++
				}
			}
		}
	}
	baselineCalls := len(provider.requests)
	provider.mu.Unlock()
	if baselineCalls == 0 || baselineImages != 0 {
		t.Fatalf("original-only provider calls=%d image parts=%d", baselineCalls, baselineImages)
	}
	// Real chat request: avoid frontend presentation fakes affecting the proof.
	check(`(async()=>{const r=await fetch('/api/chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({prompt:'describe sparse fixture',attachments:fixturePaths})});const body=await r.text();fixtureCheck(r.ok&&!body.includes('"type":"error"'),'chat '+body);return 'ok'})()`)
	assertWire := func(want int, textOnly bool) {
		t.Helper()
		provider.mu.Lock()
		defer provider.mu.Unlock()
		if len(provider.requests) == 0 {
			t.Fatal("provider not called")
		}
		msgs := provider.requests[len(provider.requests)-1]
		images := 0
		var text strings.Builder
		for _, m := range msgs {
			text.WriteString(m.Content)
			for _, part := range m.Parts {
				text.WriteString(part.Text)
				if part.Image != nil {
					images++
					if part.Image.MediaType != "image/jpeg" {
						t.Fatalf("wire MIME=%s", part.Image.MediaType)
					}
					data, err := base64.StdEncoding.DecodeString(part.Image.Data)
					if err != nil {
						t.Fatal(err)
					}
					if len(data) == 0 && part.Image.Path != "" {
						data, err = os.ReadFile(part.Image.Path)
					}
					if err != nil || len(data) == 0 {
						t.Fatalf("wire pixels missing: %v", err)
					}
				}
			}
		}
		if images != want || !strings.Contains(text.String(), "sparse visual samples only") || !strings.Contains(text.String(), "No audio was extracted") {
			t.Fatalf("wire images=%d want=%d sparse/audio note=%v", images, want, strings.Contains(text.String(), "sparse visual samples only"))
		}
		if textOnly && !strings.Contains(text.String(), "selected provider is text-only") {
			t.Fatal("missing text-only note")
		}
	}
	assertWire(6, false)
	provider.vision.Store(false)
	check(`(async()=>{const r=await fetch('/api/chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({prompt:'describe sparse fixture',attachments:fixturePaths})});const body=await r.text();fixtureCheck(r.ok&&!body.includes('"type":"error"'),'text chat '+body);return 'ok'})()`)
	assertWire(0, true)
	t.Logf("actual fixture wire: original %s (%d bytes) -> before 0 image parts; after 6 JPEG parts; confirmed text-only 0 parts", filepath.Base(paths[0]), len(original))
	// Paused preparation is test-only scheduling; successful extraction above is
	// native. Validate every scope fence without racing decoder speed.
	check(`(async()=>{
 const check=fixtureCheck;pendingAttachments=[];promptEl.value='preserve draft';
 const real=SuperCliVideoAttachments, realFetch=window.fetch;let release,started;
 function hold(){started=new Promise(r=>{release=r});window.SuperCliVideoAttachments={prepare:async(files,opts)=>{await started;if(opts.signal.aborted)throw new DOMException('cancel','AbortError');return {files:Array.from(files),warnings:[]}}}}
 hold();let first=uploadAttachmentFiles([fixtureVideo],'composer.attach').then(()=>null,e=>e.name);
 check(attachmentUploadBusy(),'busy flag');let concurrent=false;try{await uploadAttachmentFiles([fixtureVideo],'composer.attach')}catch(e){concurrent=true}check(concurrent,'concurrent rejection');
 document.getElementById('composer').dispatchEvent(new Event('submit',{cancelable:true}));document.getElementById('interrupt-btn').click();check(promptEl.value==='preserve draft','busy send lost draft');
 projectEpoch++;pendingAttachments=['/unrelated'];release();check(await first==='AbortError','midprepare project switch');check(pendingAttachments[0]==='/unrelated'&&promptEl.value==='preserve draft','scope preserved');
 hold();pendingAttachments=[];first=uploadAttachmentFiles([fixtureVideo],'composer.attach').then(()=>null,e=>e.name);activeSessionID='new-session';release();check(await first==='AbortError','session fence');activeSessionID='';
 hold();pendingAttachments=[];first=uploadAttachmentFiles([fixtureVideo],'composer.attach').then(()=>null,e=>e.name);pendingAttachments=[];release();check(await first==='AbortError','pending array fence');
 hold();fixtureTask.text=fixtureTask.prompt;promptQueue=[fixtureTask];editQueuedTask(fixtureTask,document.getElementById('attach-btn'));const edit=queueEdit;check(!!edit,'real queue editor');edit.attachments=[fixturePaths[0]];edit.text='preserve edited queue draft';edit.input.value=edit.text;
 first=uploadAttachmentFiles([fixtureVideo],'composer.attach',edit).then(()=>null,e=>e.name);check(edit.uploading&&edit.dialog.querySelector('button.primary').disabled,'edit busy/save disabled');await saveQueuedTaskEdit();check(!edit.saving&&edit.input.value==='preserve edited queue draft','save while preparing changed draft');closeQueueEditor(edit,false);release();check(await first==='AbortError'&&edit.attachments.length===1,'closed edit fence');
 // An intentionally non-abortable late response proves the post-response fence,
 // not just AbortController's transport behavior. There is no retry.
 window.SuperCliVideoAttachments=real;pendingAttachments=[];
 let reply;const late=new Promise(r=>reply=r);window.fetch=async()=>{await late;return {ok:true,json:async()=>({paths:['/late.txt']})}};
 first=uploadAttachmentFiles([new File(['late'],'late.txt')],'composer.attach').then(()=>null,e=>e.name);
 await new Promise(r=>setTimeout(r,0));projectEpoch++;pendingAttachments=['/new-draft'];reply();check(await first==='AbortError'&&pendingAttachments[0]==='/new-draft','late response fence');
 window.fetch=realFetch;window.SuperCliVideoAttachments=real;return 'ok';})()`)
	if posts.Load() != 1 {
		t.Fatalf("stale preparation posted: %d", posts.Load())
	}
}
