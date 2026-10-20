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

// Offline, real native WebM encoding AND decoding: canvas-generated color video,
// no camera/microphone, fixture download, encoder dependency, user data or LLM.
func TestVideoAttachmentsNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<script src="/js/05c-video-attachments.js"></script>`)
			return
		}
		b, err := assetsFS.ReadFile("assets" + r.URL.Path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript")
		w.Write(b)
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 800, Height: 600})
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
	var result string
	err = page.Eval(ctx, `(async()=>{
 function check(ok,msg){if(!ok)throw Error(msg)}
 `+nativeVideoFixtureJS+`
 const original=await makeVideoFixture();
 const prepared=await SuperCliVideoAttachments.prepare([original],{slots:8});
 check(prepared.files[0]===original,'original identity');
 check(prepared.files.length===7,'six frames: '+prepared.files.length+' '+prepared.warnings);
 const times=[];
 for(const f of prepared.files.slice(1)){
  check(f.type==='image/jpeg'&&f.size<=512*1024,'JPEG MIME/bytes');
  const img=await createImageBitmap(f);check(img.width===96&&img.height===64,'native dimensions');img.close();
  const m=f.name.match(/frame-(\d+)-at-([\d.]+)s-of-([\d.]+)s/);check(m,'caption '+f.name);times.push(+m[2]);
 }
 check(times[0]<.15&&times[5]>1&&times.every((v,i)=>!i||v>times[i-1]),'whole duration coverage '+times);
 const crowded=await SuperCliVideoAttachments.prepare([original],{slots:2});check(crowded.files.length===2&&crowded.warnings.length,'slot warning');
 const full=await SuperCliVideoAttachments.prepare([original],{slots:1});check(full.files.length===1&&full.warnings.length,'no room preserves original');
 const two=await SuperCliVideoAttachments.prepare([original,new File([original],'other.mov',{type:'video/quicktime'})],{slots:8});check(two.files.length===8,'shared budget '+two.warnings);
 const bad=new File(['not a video'],'broken.mp4',{type:'video/mp4'});
 const failed=await SuperCliVideoAttachments.prepare([bad],{slots:8});check(failed.files[0]===bad&&failed.files.length===1&&failed.warnings.length,'malformed preserved');
 const unsupportedBytes=new Uint8Array(await original.arrayBuffer());for(let i=0;i<unsupportedBytes.length-5;i++){if(String.fromCharCode(...unsupportedBytes.slice(i,i+5))==='V_VP8'){unsupportedBytes.set(new TextEncoder().encode('V_BAD'),i);break}}
 const unsupported=new File([unsupportedBytes],'unsupported.webm',{type:'video/webm'});const noCodec=await SuperCliVideoAttachments.prepare([unsupported],{slots:8});check(noCodec.files.length===1&&noCodec.files[0]===unsupported&&noCodec.warnings.length,'native unsupported codec preserves original');
 const ctl=new AbortController();ctl.abort();let cancelled=false;try{await SuperCliVideoAttachments.prepare([original],{slots:8,signal:ctl.signal})}catch(e){cancelled=e.name==='AbortError'}check(cancelled,'cancel');
 let rejected=false;try{await SuperCliVideoAttachments.prepare([original,original],{slots:1})}catch(e){rejected=true}check(rejected,'no silent truncation');
 const large=await makeVideoFixture(1600,900);const scaled=await SuperCliVideoAttachments.prepare([large],{slots:2});check(scaled.files.length===2,'large native decode '+scaled.warnings);const bitmap=await createImageBitmap(scaled.files[1]);check(bitmap.width===1280&&bitmap.height===720,'1280 edge scaling');bitmap.close();
 const unseekable=await makeVideoFixture(96,64,false);const noDuration=await SuperCliVideoAttachments.prepare([unseekable],{slots:8});check(noDuration.files.length===1&&noDuration.warnings.length,'unbounded duration preserved');
 const playlist=new File(['#EXTM3U\\nhttps://invalid.example/video.ts'],'disguised.mp4',{type:'video/mp4'});const rejectedPlaylist=await SuperCliVideoAttachments.prepare([playlist],{slots:8});check(rejectedPlaylist.files.length===1&&rejectedPlaylist.warnings.length,'playlist not loaded');
 const huge=new File([new Uint8Array(32*1024*1024+1)],'huge.txt');rejected=false;try{await SuperCliVideoAttachments.prepare([huge],{slots:8})}catch(e){rejected=true}check(rejected,'32 MiB limit');
 const a=new File([new Uint8Array(32*1024*1024)],'a.txt'),b=new File([a],'b.txt');rejected=false;try{await SuperCliVideoAttachments.prepare([a,b,original],{slots:8})}catch(e){rejected=true}check(rejected,'64 MiB originals limit');
 const nearlyFull=new File([b.slice(0,b.size-original.size-100)],'near.txt');const byteBound=await SuperCliVideoAttachments.prepare([original,a,nearlyFull],{slots:8});check(byteBound.files.length===3&&byteBound.warnings.length,'generated JPEG bytes included in total');
 const createURL=URL.createObjectURL,revokeURL=URL.revokeObjectURL;let created=0,revoked=0;const live=new AbortController();URL.createObjectURL=function(blob){created++;const url=createURL.call(URL,blob);queueMicrotask(()=>live.abort());return url};URL.revokeObjectURL=function(url){revoked++;return revokeURL.call(URL,url)};
 cancelled=false;try{await SuperCliVideoAttachments.prepare([original],{slots:8,signal:live.signal})}catch(e){cancelled=e.name==='AbortError'}finally{URL.createObjectURL=createURL;URL.revokeObjectURL=revokeURL}check(cancelled&&created===1&&revoked===1,'in-flight cancellation URL cleanup');
 return 'ok: native WebM -> six bounded JPEG frames; budgets, malformed, cancellation';
 })()`, &result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, "ok:") {
		t.Fatal(result)
	}
	t.Log(result)
}

const nativeVideoFixtureJS = `
async function makeVideoFixture(width=96,height=64,finiteDuration=true){
 const c=document.createElement('canvas');c.width=width;c.height=height;const x=c.getContext('2d'),chunks=[];
 const encoder=new VideoEncoder({output:chunk=>{const b=new Uint8Array(chunk.byteLength);chunk.copyTo(b);chunks.push(b)},error:e=>{throw e}});
 encoder.configure({codec:'vp8',width:width,height:height,bitrate:100000,framerate:20,hardwareAcceleration:'prefer-software'});
 for(let i=0;i<24;i++){x.fillStyle=['red','green','blue','yellow'][i%4];x.fillRect(0,0,width,height);const f=new VideoFrame(c,{timestamp:i*50000,duration:50000});encoder.encode(f,{keyFrame:true});f.close()}
 await encoder.flush();encoder.close();c.width=c.height=0;
 function cat(...a){return new Uint8Array(a.flatMap(v=>Array.from(v)))}
 function elem(id,data){let n=data.length,bytes=1;while(n>=Math.pow(2,7*bytes)-1)bytes++;const size=new Uint8Array(bytes);for(let i=bytes-1;i>=0;i--){size[i]=n&255;n=Math.floor(n/256)}size[0]|=1<<(8-bytes);return cat(id,size,data)}
 const duration=new Uint8Array(8);new DataView(duration.buffer).setFloat64(0,1200,false);
 const header=elem([0x1a,0x45,0xdf,0xa3],cat(elem([0x42,0x86],[1]),elem([0x42,0xf7],[1]),elem([0x42,0xf2],[4]),elem([0x42,0xf3],[8]),elem([0x42,0x82],new TextEncoder().encode('webm')),elem([0x42,0x87],[4]),elem([0x42,0x85],[2])));
 const info=elem([0x15,0x49,0xa9,0x66],cat(elem([0x2a,0xd7,0xb1],[0x0f,0x42,0x40]),finiteDuration?elem([0x44,0x89],duration):[]));
 const tracks=elem([0x16,0x54,0xae,0x6b],elem([0xae],cat(elem([0xd7],[1]),elem([0x73,0xc5],[1]),elem([0x83],[1]),elem([0x86],new TextEncoder().encode('V_VP8')),elem([0xe0],cat(elem([0xb0],[width>>8,width&255]),elem([0xba],[height>>8,height&255]))))));
 const blocks=chunks.map((b,i)=>elem([0xa3],cat([0x81,(i*50)>>8,(i*50)&255,0x80],b)));
 const cluster=elem([0x1f,0x43,0xb6,0x75],cat(elem([0xe7],[0]),...blocks));
 return new File([header,new Uint8Array([0x18,0x53,0x80,0x67,0x01,0xff,0xff,0xff,0xff,0xff,0xff,0xff]),info,tracks,cluster],'color-source.webm',{type:'video/webm'});
}
`
