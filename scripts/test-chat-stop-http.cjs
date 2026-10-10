'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'..'),base=process.argv[2],mode=process.argv[3]||'running-command';
if(!['running-command','checkpoint-store-held'].includes(mode))throw new Error('invalid fixture mode');
if(!base || !/^http:\/\/127\.0\.0\.1:\d+$/.test(base)) throw new Error('local test fixture URL required');
const transcriptDOM=require('./transcript-dom-fixture.cjs');
function openGet(){return this.hasAttribute('open')} function openSet(v){this.toggleAttribute('open',!!v)}
function deferred(){let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}
(async()=>{
 const c=transcriptDOM(root),doc=c.document;
 Object.defineProperty(doc.defaultView.HTMLElement.prototype,'open',{configurable:true,get:openGet,set:openSet});
 const source=fs.readFileSync(path.join(root,'internal/webgui/assets/js/05-chat.js'),'utf8');
 // All actual chat listeners bind to DOM nodes, not mocked callbacks.
 for(const match of source.matchAll(/\$\("#([a-zA-Z0-9_-]+)"\)/g)){
  if(!doc.getElementById(match[1])){const n=doc.createElement(match[1]==='composer'?'form':match[1]==='prompt'?'textarea':'div');n.id=match[1];doc.body.appendChild(n)}
 }
 const storage=new Map();c.localStorage={getItem:k=>storage.get(k)||null,setItem:(k,v)=>storage.set(k,String(v)),removeItem:k=>storage.delete(k)};
 c.window.localStorage=c.localStorage;c.window.crypto=globalThis.crypto;
 doc.hasFocus=()=>true;
 const started=performance.now(),marks=[],requests=[],sends=[];
 const tool=deferred(),nextStarted=deferred(),nextMessage=deferred();
 c.fetch=async(url,opts)=>{
  const mark={phase:'fetch',url:String(url),ms:performance.now()-started};marks.push(mark);
  if(url==='/api/chat'){
   const body=JSON.parse(opts.body);requests.push(body);
   marks.push({phase:'chat-request-'+requests.length,ms:mark.ms});
   if(requests.length===2)nextStarted.resolve();
  }
  const response=await fetch(new URL(url,base),opts);
  if(url==='/api/chat')marks.push({phase:'chat-headers-'+requests.length,ms:performance.now()-started});
  return response;
 };
 Object.assign(c,{TextDecoder,TextEncoder,setInterval,clearInterval,setTimeout,clearTimeout,activeWorkspacePath:'',ui:{lang:'en',thinkingExpanded:false,toolsExpanded:false},
  overlay:doc.createElement('div'),sections:{},currentSection:'',normalizeLanguage:x=>x,saveUI(){},i18nEl:(tag,cls,key)=>c.el(tag,cls,key)});
 c.overlay.hidden=true;
 const catalog=JSON.parse(fs.readFileSync(path.join(root,'internal/webgui/assets/locales/en.json'),'utf8'));c.t=key=>catalog[key]||key;
 vm.runInContext(source,c);
 // Sidebar/desktop notifications are outside the serialized chat critical path.
 for(const name of ['loadSessions','renderStats','loadSideGoal','notifyDone','addTurnMeta','addFileChanges','updateAppBadge'])c[name]=()=>{};
 const errors=[];c.toast=message=>errors.push(message);
 const reader=c.superCliUI.readSSE;
 c.superCliUI=Object.assign({},c.superCliUI,{readSSE:(body,onEvent)=>reader(body,event=>{
  marks.push({phase:'sse-'+event.type,ms:performance.now()-started});
  onEvent(event);
  if(event.type==='tool_call'&&event.id==='blocked-curl')tool.resolve();
  if(event.type==='message'&&event.text.includes('after stop response'))nextMessage.resolve();
 })});
 const send=c.sendPrompt;c.sendPrompt=(...args)=>{const promise=send(...args);sends.push(promise);return promise};
 const form=doc.getElementById('composer'),prompt=doc.getElementById('prompt');
 function submit(text){prompt.value=text;form.dispatchEvent(new c.Event('submit',{bubbles:true,cancelable:true}))}
 marks.push({phase:'first-submit',ms:performance.now()-started});submit('Fetch the local fixture using ctx_execute.');
 await Promise.race([tool.promise,sends[0].then(()=>{throw new Error("GUI stream ended before command: "+doc.getElementById("stream").textContent+" marks="+JSON.stringify(marks))})]);
 if(mode==='running-command'){const ready=await fetch(base+'/fixture/command-ready');assert.equal(ready.status,204);}
 marks.push({phase:'stop-click',ms:performance.now()-started});doc.getElementById('stop-run-btn').dispatchEvent(new c.Event('click'));
 assert.equal(doc.getElementById('stop-run-btn').hidden,true);assert.equal(doc.getElementById('status-dot').classList.contains('busy'),false);
 marks.push({phase:'next-submit',ms:performance.now()-started});submit('Reply after Stop without rerunning curl.');
 assert.equal(c.pendingImmediate.text,'Reply after Stop without rerunning curl.');
 await nextStarted.promise;await nextMessage.promise;await sends[0];await sends[1];
 if(mode==='running-command'){const canceled=await fetch(base+'/fixture/command-canceled');assert.equal(canceled.status,204);}
 assert.equal(requests.length,2);assert.equal(requests[1].session_id,requests[0].session_id||c.activeSessionID);
 assert.equal(c.streaming,false);assert.equal(c.runFinishing,false);assert.equal(c.pendingImmediate,null);assert.equal(c.queueDispatching,false);
 assert.equal(doc.getElementById('status-dot').classList.contains('busy'),false);
 assert(doc.getElementById('stream').textContent.includes('after stop response'));
 assert.equal(errors.length,0,'GUI errors: '+errors.join('; '));
 const stop=marks.find(m=>m.phase==='stop-click').ms,second=marks.find(m=>m.phase==='chat-request-2').ms;
 const lastMessage=marks.filter(m=>m.phase==='sse-message').at(-1).ms;
 console.log(JSON.stringify({renderer:'LinkeDOM: real DOM events and production JS, no WebView paint',mode,requests:requests.length,
  stop_to_next_request_ms:second-stop,stop_to_next_message_ms:lastMessage-stop,total_ms:performance.now()-started,marks},null,2));
})().catch(error=>{console.error(error.stack);process.exitCode=1});
