'use strict';
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'..'),base=process.argv[2],mode=process.argv[3],callID=process.argv[4];
if(!base || !/^http:\/\/127\.0\.0\.1:\d+$/.test(base))throw new Error('owned loopback fixture URL required');
if(!['cmd-if-exist','curl-gif','cmd-error','checkpoint-rejected'].includes(mode) || callID!=='command-end-'+mode)throw new Error('invalid command fixture');
const transcriptDOM=require('./transcript-dom-fixture.cjs');
let diagnostic=()=>({});
function deferred(){let resolve;const promise=new Promise(yes=>{resolve=yes});return {promise,resolve}}
(async()=>{
 const c=transcriptDOM(root),doc=c.document;
 const source=fs.readFileSync(path.join(root,'internal/webgui/assets/js/05-chat.js'),'utf8');
 for(const match of source.matchAll(/\$\("#([a-zA-Z0-9_-]+)"\)/g)){
  if(!doc.getElementById(match[1])){const n=doc.createElement(match[1]==='composer'?'form':match[1]==='prompt'?'textarea':'div');n.id=match[1];doc.body.appendChild(n)}
 }
 const storage=new Map();
 c.localStorage={getItem:k=>storage.get(k)||null,setItem:(k,v)=>storage.set(k,String(v)),removeItem:k=>storage.delete(k)};
 c.window.localStorage=c.localStorage;c.window.crypto=globalThis.crypto;doc.hasFocus=()=>true;
 const nextRequestSeen=deferred();
 const started=performance.now(),marks=[],requests=[],sends=[],events=[],errors=[],intervals=new Set();
 function mark(phase,detail){const value=Object.assign({phase,ms:performance.now()-started},detail);marks.push(value);console.log('command-end-event='+JSON.stringify(value))}
 c.fetch=async(url,opts)=>{
  if(url==='/api/chat'){
   const body=JSON.parse(opts.body);requests.push(body);mark('request-'+requests.length);
   if(requests.length===2)nextRequestSeen.resolve();
  }
  const response=await fetch(new URL(url,base),opts);
  if(url==='/api/chat')mark('headers-'+requests.length,{status:response.status});
  return response;
 };
 Object.assign(c,{TextDecoder,TextEncoder,
  setInterval(fn,ms){const id=setInterval(fn,ms);intervals.add(id);return id},
  clearInterval(id){intervals.delete(id);clearInterval(id)},
  setTimeout,clearTimeout,activeWorkspacePath:'',ui:{lang:'en',thinkingExpanded:false,toolsExpanded:false},
  overlay:doc.createElement('div'),sections:{},currentSection:'',normalizeLanguage:x=>x,saveUI(){},
  i18nEl:(tag,cls,key)=>c.el(tag,cls,key)});
 c.overlay.hidden=true;
 const catalog=JSON.parse(fs.readFileSync(path.join(root,'internal/webgui/assets/locales/en.json'),'utf8'));c.t=key=>catalog[key]||key;
 vm.runInContext(source,c);
 // Keep the production submit listeners, streaming reader, row renderer and
 // completion cleanup. Only unrelated presentation refreshes are disabled.
 for(const name of ['loadSessions','renderStats','loadSideGoal','notifyDone','addTurnMeta','addFileChanges','updateAppBadge'])c[name]=()=>{};
 c.toast=message=>errors.push(message);
 const resultSeen=deferred(),reader=c.superCliUI.readSSE;
 let firstRow=null,resultCount=0;
 function rowState(row){return row?{classes:row.className,clock:row._clock!==null&&row._clock!==undefined}:null}
 diagnostic=()=>({mode,requests:requests.length,events:events.map(ev=>({type:ev.type,id:ev.id,err:!!ev.err})),streaming:c.streaming,runFinishing:c.runFinishing,runTimer:c.runTimer!==null,openTools:c.openToolOrder,firstRow:rowState(firstRow),intervals:intervals.size,marks});
 c.superCliUI=Object.assign({},c.superCliUI,{readSSE:(body,onEvent)=>reader(body,event=>{
  events.push(event);mark('sse-'+event.type,{id:event.id||''});
  onEvent(event);
  if(event.type==='tool_call'&&event.id===callID){
   assert.equal(event.name,'ctx_execute');firstRow=c.toolRows[callID];
   assert(firstRow,'production tool row missing');assert(firstRow.classList.contains('running'));
   assert.notEqual(firstRow._clock,null,'live tool clock missing');
  }
  if(event.type==='tool_result'&&event.id===callID){
   resultCount++;assert(firstRow,'result without real tool_call row');
   assert.equal(!!event.err,mode==='cmd-error'||mode==='checkpoint-rejected','native command outcome changed');
   assert(firstRow.classList.contains((mode==='cmd-error'||mode==='checkpoint-rejected')?'failed':'done'),'tool_result did not settle row');
   assert.equal(firstRow.classList.contains('running'),false);assert.equal(firstRow._clock,null);
   assert.equal(c.openToolOrder.includes(callID),false);
   resultSeen.resolve();
  }
 })});
 const send=c.sendPrompt;c.sendPrompt=(...args)=>{const promise=send(...args);sends.push(promise);return promise};
 const form=doc.getElementById('composer'),prompt=doc.getElementById('prompt');
 function submit(text){prompt.value=text;form.dispatchEvent(new c.Event('submit',{bubbles:true,cancelable:true}))}
 function assertIdle(label){
  assert.equal(c.streaming,false,label+' streaming');assert.equal(c.runFinishing,false,label+' runFinishing');
  assert.equal(c.runTimer,null,label+' run timer');assert.equal(c.pendingImmediate,null,label+' pending prompt');
  assert.equal(c.queueDispatching,false,label+' queue dispatch');assert.equal(c.promptQueue.length,0,label+' queued prompts');
  assert.equal(c.openToolOrder.length,0,label+' open tools');
  assert.equal(doc.querySelectorAll('.tool-row.running').length,0,label+' running tool rows');
  for(const row of doc.querySelectorAll('.tool-row'))assert.equal(row._clock,null,label+' residual row clock');
  assert.equal(intervals.size,0,label+' residual interval');
  assert.equal(doc.getElementById('status-dot').classList.contains('busy'),false,label+' busy indicator');
  assert.equal(doc.getElementById('stop-run-btn').hidden,true,label+' Stop button');
 }
 mark('first-submit');
 submit('Run the requested native command using ctx_execute and then answer.');
 assert.equal(sends.length,1,'first form submit not accepted');
 await Promise.race([resultSeen.promise,sends[0].then(()=>{if(resultCount!==1)throw new Error('stream finished without exact native tool_result')})]);
 await sends[0];
 assert.equal(resultCount,1,'native tool result missing or duplicated');
 assert.equal(events.filter(ev=>ev.type==='tool_call'&&ev.id===callID).length,1);
 assert.equal(events.filter(ev=>ev.type==='done').length,1,'normal stream did not report done');
 assert.equal(events.filter(ev=>ev.type==='error').length,0,'native outcome incorrectly aborted stream');
 assertIdle('first completed command');
 const firstIdle=await (await fetch(base+'/fixture/active-work')).json();
 assert.equal(firstIdle.activeWork,false,'backend still active after normal EOF; close would confirm');
 const firstSession=c.activeSessionID;assert(firstSession,'first durable session missing');
 const firstCompletedAt=performance.now()-started;
 mark('next-submit');submit('Reply after completed command without rerunning it.');
 assert.equal(sends.length,2,'next form submit was not accepted immediately');
 // sendPrompt awaits the restored session runtime before issuing fetch.
 // Await that real request event without a timer or status polling.
 await Promise.race([nextRequestSeen.promise,sends[1].then(()=>{if(requests.length!==2)throw new Error('next turn finished without HTTP request')})]);
 assert.equal(requests.length,2,'next submit stayed queued');
 assert.equal(requests[1].session_id,firstSession,'next message changed conversation');
 await sends[1];
 assertIdle('next completed turn');
 assert.equal(events.filter(ev=>ev.type==='done').length,2,'next stream did not complete exactly once');
 assert.equal(events.filter(ev=>ev.type==='tool_call').length,1,'next turn reran command');
 assert(doc.getElementById('stream').textContent.includes('next-command-end response'),'next prompt did not reach provider');
 const nextIdle=await (await fetch(base+'/fixture/active-work')).json();
 assert.equal(nextIdle.activeWork,false,'backend still active after next turn');
 assert.equal(errors.length,0,'GUI errors: '+errors.join('; '));
 console.log('command-end-result='+JSON.stringify({renderer:'LinkeDOM DOM events and production JS; no WebView paint',mode,session_id:firstSession,requests:requests.length,tool_results:resultCount,done:2,activeWork:nextIdle.activeWork,first_completion_ms:firstCompletedAt,total_ms:performance.now()-started,marks}));
})().catch(error=>{console.error(error.stack);console.error('command-end-diagnostic='+JSON.stringify(diagnostic()));process.exitCode=1});
