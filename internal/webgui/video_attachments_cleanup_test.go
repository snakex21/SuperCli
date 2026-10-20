package webgui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// Negative scheduling only: real DOM video/canvas objects, a native JPEG encoder
// whose callback is held, and tracked native timer/listener/Blob URL APIs. No
// production hooks. TestVideoAttachmentsNativeBrowser remains the independent
// real VP8/WebM decoder proof; these cases isolate cleanup at each await point.
func TestVideoAttachmentTimeoutCleanupNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	html, _ := assetsFS.ReadFile("assets/index.html")
	shell := strings.Split(string(html), `<script src=`)[0]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprintf(w, `%s<script>var fixtureCatalog={},activeWorkspacePath='cleanup-fixture';</script><script src="/fixture.js"></script><script src="/runtime.js"></script><script>var superCliUI=window.SuperCliUI;</script><script src="/js/05-chat.js"></script><script src="/js/05c-video-attachments.js"></script></body></html>`, shell)
		case "/fixture.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, queueEditBrowserPrelude, `var fixtureWarnings=[];function toast(message){fixtureWarnings.push(message)}`)
		case "/runtime.js":
			b, _ := sharedUIFS.ReadFile("shared-ui/runtime.js")
			w.Header().Set("Content-Type", "text/javascript")
			w.Write(b)
		default:
			b, err := assetsFS.ReadFile("assets" + r.URL.Path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/javascript")
			w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 800, Height: 600})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	for _, mode := range []string{"metadata-timeout", "seek-timeout", "jpeg-timeout", "jpeg-cancel", "jpeg-scope-change"} {
		t.Run(mode, func(t *testing.T) {
			page, err := browser.NewPage(ctx, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			script := fmt.Sprintf(`(async()=>{const mode=%q;`, mode) + videoCleanupFixtureJS + `})()`
			if err := page.Eval(ctx, script, &got); err != nil {
				t.Fatal(err)
			}
			if got != "ok" {
				t.Fatal(got)
			}
		})
	}
}

const videoCleanupFixtureJS = `
 function check(ok,message){if(!ok)throw Error(mode+': '+message)}
 const native={create:document.createElement,add:EventTarget.prototype.addEventListener,remove:EventTarget.prototype.removeEventListener,
  setTimeout:window.setTimeout.bind(window),clearTimeout:window.clearTimeout.bind(window),setInterval:window.setInterval.bind(window),clearInterval:window.clearInterval.bind(window),
  createURL:URL.createObjectURL,revokeURL:URL.revokeObjectURL,File:window.File,fetch:window.fetch};
 const videos=[],canvases=[],listeners=[],timers=new Map(),intervals=new Set(),urls=new Set(),delays=[];
 const late=[],posted=[];let encodes=0,frameFiles=0,revocations=0;
 // Test polling uses private native timers, never the tracked production timers.
 async function until(condition,message){for(let i=0;i<100;i++){if(condition())return;await new Promise(r=>native.setTimeout(r,5))}throw Error(mode+': timed out waiting for '+message)}
 window.setTimeout=function(fn,delay,...args){let id=native.setTimeout(()=>{timers.delete(id);fn(...args)},delay);timers.set(id,{fn:()=>fn(...args),delay});delays.push(delay);return id};
 window.clearTimeout=function(id){timers.delete(id);return native.clearTimeout(id)};
 window.setInterval=function(fn,delay,...args){const id=native.setInterval(fn,delay,...args);intervals.add(id);return id};
 window.clearInterval=function(id){intervals.delete(id);return native.clearInterval(id)};
 function fireTimeout(delay){const entry=Array.from(timers).find(([id,item])=>item.delay===delay);check(!!entry,'missing '+delay+'ms timer');native.clearTimeout(entry[0]);timers.delete(entry[0]);entry[1].fn()}
 EventTarget.prototype.addEventListener=function(type,fn,options){if(videos.includes(this)||this instanceof AbortSignal)listeners.push({target:this,type,fn});return native.add.call(this,type,fn,options)};
 EventTarget.prototype.removeEventListener=function(type,fn,options){const i=listeners.findIndex(v=>v.target===this&&v.type===type&&v.fn===fn);if(i>=0)listeners.splice(i,1);return native.remove.call(this,type,fn,options)};
 URL.createObjectURL=function(blob){const url=native.createURL.call(URL,blob);urls.add(url);return url};
 URL.revokeObjectURL=function(url){check(urls.delete(url),'URL revoked twice or unowned');revocations++;return native.revokeURL.call(URL,url)};
 window.File=new Proxy(native.File,{construct(target,args){if(String(args[1]).includes('-frame-'))frameFiles++;return Reflect.construct(target,args)}});
 document.createElement=function(tag,...args){
  const element=native.create.call(document,tag,...args);
  if(tag==='video'){
   videos.push(element);let source='',current=0;
   const removeAttribute=element.removeAttribute,remove=element.remove;
   element.fixture={pauses:0,loads:0,removes:0};
   Object.defineProperties(element,{
    src:{configurable:true,get:()=>source,set:value=>{source=value}},duration:{configurable:true,get:()=>10},
    videoWidth:{configurable:true,get:()=>640},videoHeight:{configurable:true,get:()=>360},
    readyState:{configurable:true,get:()=>2},seekable:{configurable:true,get:()=>({length:1})},
    currentTime:{configurable:true,get:()=>current,set:value=>{current=value;if(mode!=='seek-timeout')queueMicrotask(()=>element.dispatchEvent(new Event('seeked')))}}
   });
   element.load=function(){element.fixture.loads++;if(source&&mode!=='metadata-timeout')queueMicrotask(()=>element.dispatchEvent(new Event('loadedmetadata')))};
   element.pause=function(){element.fixture.pauses++};
   element.removeAttribute=function(name){if(name==='src')source='';return removeAttribute.call(this,name)};
   element.remove=function(){element.fixture.removes++;return remove.call(this)};
  }
  if(tag==='canvas'){
   canvases.push(element);element.fixture={removes:0};const remove=element.remove;
   element.remove=function(){element.fixture.removes++;return remove.call(this)};
   // drawImage still exercises native canvas pixels, without relying on a fake
   // video's decoder state in a negative scheduling test.
   const getContext=element.getContext,toBlob=element.toBlob;
   const source=native.create.call(document,'canvas');source.width=source.height=2;source.getContext('2d').fillRect(0,0,2,2);
   element.getContext=function(...args){const ctx=getContext.apply(this,args);if(ctx&&!ctx.fixtureDraw){const draw=ctx.drawImage;ctx.fixtureDraw=true;ctx.drawImage=function(video,...sizes){return draw.call(this,source,...sizes)}}return ctx};
   element.toBlob=function(callback,type,quality){encodes++;return toBlob.call(this,blob=>{check(blob&&blob.type==='image/jpeg','native JPEG encoding');late.push(()=>callback(blob))},type,quality)};
  }
  return element;
 };
 window.fetch=async function(url,options){check(url==='/api/attachment/upload','unexpected network '+url);const files=options.body.getAll('files');posted.push(files);return {ok:true,json:async()=>({paths:files.map((file,i)=>'/stage/cleanup-'+posted.length+'-'+i+'-'+file.name),workspace:activeWorkspacePath})}};
 try{
  // A recognized local container header permits entry to the controlled native
  // metadata/seek boundary. The separate happy test uses a real decodable file.
  const original=new native.File([new Uint8Array([0x1a,0x45,0xdf,0xa3,0,0,0,0])],'cleanup-source.webm',{type:'video/webm'});
  pendingAttachments=[];fixtureWarnings.length=0;promptEl.value='keep draft';
  let failure=null,settled=false;
  const upload=uploadAttachmentFiles([original],'composer.attach').catch(error=>{failure=error}).finally(()=>{settled=true});
  check(attachmentUploadBusy(),'uploader never became busy');
  if(mode==='metadata-timeout'){
   await until(()=>timers.size===1&&delays.includes(5000),'metadata timer');fireTimeout(5000);
  }else if(mode==='seek-timeout'){
   await until(()=>Array.from(timers.values()).some(v=>v.delay===3000),'seek timer');fireTimeout(3000);
  }else{
   await until(()=>late.length===1,'held native encoding callback');
   check(encodes===1&&delays.includes(2000),'JPEG timeout budget/encoder attempts');
   if(mode==='jpeg-timeout')fireTimeout(2000);
   else if(mode==='jpeg-cancel')cancelAttachmentUpload();
   else {projectEpoch++;pendingAttachments=['/unrelated/draft.txt'];}
  }
  await until(()=>settled,'uploader settlement');await upload;
  const cancelled=mode==='jpeg-cancel'||mode==='jpeg-scope-change';
  if(cancelled){check(failure&&failure.name==='AbortError','cancellation did not reject honestly');check(posted.length===0,'cancelled upload posted');check(pendingAttachments.length===(mode==='jpeg-scope-change'?1:0),'cancelled attachment mutation')}
  else{
   check(!failure,'timeout failed whole upload: '+failure);check(posted.length===1&&posted[0].length===1,'timeout uploaded extra files');
   const file=posted[0][0];check(file.name===original.name&&file.type===original.type&&file.size===original.size,'original lost/replaced');check(await file.text()===await original.text(),'original bytes changed');
   check(pendingAttachments.length===1&&pendingAttachments[0].endsWith(original.name),'original not attached');
   check(fixtureWarnings.some(v=>v.includes('original kept')&&v.includes('timed out')),'no visible original-preserved timeout warning');
  }
  function released(){
   check(videos.length===1&&canvases.length===1,'unexpected media allocations');
   const video=videos[0],canvas=canvases[0];
   check(video.src===''&&!video.hasAttribute('src')&&!video.isConnected&&video.fixture.pauses===1&&video.fixture.loads===2&&video.fixture.removes===1,'video not released');
   check(canvas.width===0&&canvas.height===0&&!canvas.isConnected&&canvas.fixture.removes===1,'canvas not released');
   check(urls.size===0&&revocations===1,'Blob URL leaked');check(listeners.length===0,'media/signal listeners leaked: '+listeners.map(v=>v.type));
   check(timers.size===0&&intervals.size===0,'timeout/scope-watch timers leaked');check(frameFiles===0,'late or unexpected frame File created');
   check(!attachmentUploadBusy()&&attachmentUpload===null,'upload busy/scope guard retained');check(promptEl.value==='keep draft','unrelated draft altered');
   if(mode==='jpeg-scope-change')check(pendingAttachments[0]==='/unrelated/draft.txt','new scope draft overwritten');
  }
  released();
  const snapshot=JSON.stringify({paths:pendingAttachments,warnings:fixtureWarnings,posts:posted.length,encodes});
  // A native encoder cannot be cancelled. Deliver its real JPEG after timeout
  // or cancellation; it must not resume the frame loop, upload, or mutate scope.
  for(const callback of late)callback();
  await new Promise(r=>native.setTimeout(r,20));
  check(JSON.stringify({paths:pendingAttachments,warnings:fixtureWarnings,posts:posted.length,encodes})===snapshot,'late encoding callback changed settled state');released();
  // Also discharge events that would have completed a timed-out metadata/seek
  // await; no listener may resurrect the preparation after resource teardown.
  videos[0].dispatchEvent(new Event('loadedmetadata'));videos[0].dispatchEvent(new Event('seeked'));videos[0].dispatchEvent(new Event('error'));
  await Promise.resolve();released();
  const followup=new native.File(['followup'],'followup.txt');const before=posted.length;
  await uploadAttachmentFiles([followup],'composer.attach');check(posted.length===before+1&&posted.at(-1).length===1,'new scope upload blocked after cleanup');
  check(!attachmentUploadBusy()&&attachmentUpload===null&&timers.size===0&&intervals.size===0&&listeners.length===0,'followup retained guard/timer/listener');
  return 'ok';
 }finally{
  document.createElement=native.create;EventTarget.prototype.addEventListener=native.add;EventTarget.prototype.removeEventListener=native.remove;
  for(const id of timers.keys())native.clearTimeout(id);for(const id of intervals)native.clearInterval(id);
  for(const url of urls)native.revokeURL.call(URL,url);
  window.setTimeout=native.setTimeout;window.clearTimeout=native.clearTimeout;window.setInterval=native.setInterval;window.clearInterval=native.clearInterval;
  URL.createObjectURL=native.createURL;URL.revokeObjectURL=native.revokeURL;window.File=native.File;window.fetch=native.fetch;
 }
`
