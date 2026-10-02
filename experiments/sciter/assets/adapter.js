import {TCP} from '@sys';
import {encode,decode} from '@sciter';
const origin = 'TRIAL_ORIGIN';
const nativeFetch = fetch;
globalThis.trialErrors=[];
console.reportException=e=>trialErrors.push(String(e)+'\n'+(e.stack||''));
if (!('dataset' in Element.prototype)) Object.defineProperty(Element.prototype,'dataset',{get(){const node=this;return new Proxy({}, {get:(_,key)=>node.getAttribute('data-'+String(key).replace(/[A-Z]/g,c=>'-'+c.toLowerCase()))||undefined,set:(_,key,v)=>{node.setAttribute('data-'+String(key).replace(/[A-Z]/g,c=>'-'+c.toLowerCase()),String(v));return true}})}});
if(!('innerWidth' in globalThis)) Object.defineProperty(globalThis,'innerWidth',{get:()=>Window.this.box('width','client')});
if(!('innerHeight' in globalThis)) Object.defineProperty(globalThis,'innerHeight',{get:()=>Window.this.box('height','client')});
if(!document.hasFocus) document.hasFocus=()=>Window.this.isActive;
if(!window.matchMedia) window.matchMedia=query=>({get matches(){const max=/max-width:\s*(\d+)/.exec(query);return max?window.innerWidth<=Number(max[1]):false},addEventListener(){},removeEventListener(){}});
if(!globalThis.AbortController) globalThis.AbortController=class {constructor(){this.signal={aborted:false,listeners:[],addEventListener:(type,fn)=>this.signal.listeners.push(fn),removeEventListener:(type,fn)=>{this.signal.listeners=this.signal.listeners.filter(x=>x!==fn)}}}abort(){if(this.signal.aborted)return;this.signal.aborted=true;this.signal.listeners.slice().forEach(fn=>fn())}};
if(!globalThis.TextDecoder) globalThis.TextDecoder=class{constructor(){this.pending=new Uint8Array(0)}decode(input,options){var bytes=input?new Uint8Array(input.buffer||input,input.byteOffset||0,input.byteLength):new Uint8Array(0);var all=new Uint8Array(this.pending.length+bytes.length);all.set(this.pending);all.set(bytes,this.pending.length);var end=all.length;if(options&&options.stream&&end){var start=end-1;while(start>=0&&(all[start]&0xc0)===0x80)start--;if(start>=0){var first=all[start];var need=first>=0xf0?4:first>=0xe0?3:first>=0xc0?2:1;if(end-start<need)end=start}}this.pending=all.slice(end);return decode(all.slice(0,end).buffer)}};
if(!globalThis.Image)globalThis.Image=class{constructor(){return document.createElement('img')}};
const sockets=new Set();
function aborted(){const e=new Error('Request cancelled');e.name='AbortError';return e}
async function streamingFetch(url,options) {
 const socket=new TCP(); sockets.add(socket);
 const signal=options.signal;
 const stop=()=>{sockets.delete(socket);socket.close()};
 if(signal?.aborted) throw aborted();
 signal?.addEventListener('abort',stop);
 try {
  const authority=origin.slice(7),port=Number(authority.split(':')[1]);
  await socket.connect({ip:'127.0.0.1',port});
  if(signal?.aborted)throw aborted();
  const body=options.body instanceof ArrayBuffer?options.body:encode(options.body||'');
  const extra=Object.entries(options.headers||{}).map(([k,v])=>k+': '+v+'\r\n').join('');
  await socket.write(encode((options.method||'GET')+' '+url+' HTTP/1.1\r\nHost: '+authority+'\r\n'+extra+'Content-Length: '+body.byteLength+'\r\nConnection: close\r\n\r\n'));
  if(body.byteLength)await socket.write(body);
  let buffer=new Uint8Array(0),eof=false;
  async function more(){
   if(signal?.aborted)throw aborted();
   const data=await socket.read();
   if(data===undefined){eof=true;return false}
   const bytes=new Uint8Array(data), combined=new Uint8Array(buffer.length+bytes.length);
   combined.set(buffer);combined.set(bytes,buffer.length);buffer=combined;return true;
  }
  async function line(){
   for(;;){
    for(let i=0;i+1<buffer.length;i++)if(buffer[i]===13&&buffer[i+1]===10){
     const value=decode(buffer.slice(0,i).buffer);buffer=buffer.slice(i+2);return value;
    }
    if(buffer.length>65536)throw Error('HTTP line exceeds limit');
    if(!await more())throw Error('Unexpected HTTP EOF');
   }
  }
  const statusLine=await line(),status=Number(statusLine.split(' ')[1]),headers={};
  for(;;){const h=await line();if(!h)break;const colon=h.indexOf(':');headers[h.slice(0,colon).toLowerCase()]=h.slice(colon+1).trim()}
  const chunked=/chunked/i.test(headers['transfer-encoding']||'');
  let remaining=headers['content-length']===undefined?Infinity:Number(headers['content-length']),finished=false;
  const cleanup=()=>{signal?.removeEventListener('abort',stop);stop()};
  const reader={
   async cancel(){finished=true;cleanup()},
   async read(){
    try {
     if(finished)return {done:true};
     if(signal?.aborted)throw aborted();
     if(chunked){
      const size=parseInt((await line()).split(';')[0],16);
      if(!Number.isFinite(size)||size<0||size>8*1024*1024)throw Error('Invalid HTTP chunk size');
      if(size===0){while(await line()){}finished=true;cleanup();return {done:true}}
      while(buffer.length<size+2)if(!await more())throw Error('Truncated HTTP chunk');
      const value=buffer.slice(0,size);
      if(buffer[size]!==13||buffer[size+1]!==10)throw Error('Invalid HTTP chunk boundary');
      buffer=buffer.slice(size+2);return {done:false,value};
     }
     if(remaining===0){finished=true;cleanup();return {done:true}}
     while(!buffer.length&&!eof)await more();
     if(!buffer.length){if(remaining!==Infinity&&remaining!==0)throw Error('Truncated HTTP body');finished=true;cleanup();return {done:true}}
     const count=Math.min(buffer.length,remaining),value=buffer.slice(0,count);
     buffer=buffer.slice(count);if(remaining!==Infinity)remaining-=count;return {done:false,value};
    }catch(e){cleanup();throw signal?.aborted?aborted():e}
   }
  };
  async function readAll(){let data='',decoder=new TextDecoder();for(;;){const chunk=await reader.read();if(chunk.done)break;data+=decoder.decode(chunk.value,{stream:true});if(data.length>32*1024*1024){cleanup();throw Error('Response exceeds trial limit')}}return data+decoder.decode()}
  return {ok:status>=200&&status<300,status,headers:{get:name=>headers[String(name).toLowerCase()]||null},body:{getReader(){return reader}},text:readAll,json:async()=>JSON.parse(await readAll())};
 }catch(e){signal?.removeEventListener('abort',stop);stop();throw signal?.aborted?aborted():e}
}
globalThis.fetch=function(url,options){
 options=options||{};
 if(typeof url!=='string')return Promise.reject(Error('Only local string URLs are supported in the trial'));
 if(url.startsWith(origin+'/'))url=url.slice(origin.length);
 if(!url.startsWith('/')){if(url.indexOf('://')>=0)return Promise.reject(Error('The Sciter trial accepts only its local Go backend'));url='/'+url}
 return streamingFetch(url,options);
};
if(!Element.prototype.requestSubmit)Element.prototype.requestSubmit=function(){this.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}))};
document.on('document-before-unload',()=>{sockets.forEach(s=>s.close());sockets.clear()});
