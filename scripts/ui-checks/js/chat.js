import {check,equal,ready,noErrors,deferred,submit} from './common.js';

export async function run({kind,mode,callID}) {
  await ready();
  // Keep real submit listeners, HTTP/SSE reading, row rendering, and completion
  // cleanup. Only unrelated presentation refreshes are disabled as previously.
  Object.assign(ui,{lang:'en',thinkingExpanded:false,toolsExpanded:false});
  for(const name of ['loadSessions','renderStats','loadSideGoal','notifyDone','addTurnMeta','addFileChanges','updateAppBadge'])window[name]=()=>{};
  const errors=[];toast=message=>errors.push(message);
  const started=performance.now(),marks=[],requests=[],sends=[],events=[],intervals=new Set();
  const fetchOriginal=window.fetch.bind(window),sendOriginal=sendPrompt;
  const intervalOriginal=window.setInterval.bind(window),clearIntervalOriginal=window.clearInterval.bind(window);
  const reader=superCliUI.readSSE,resultSeen=deferred(),tool=deferred(),nextStarted=deferred(),nextMessage=deferred();
  let firstRow=null,resultCount=0;
  const command=kind==='chat-command-end-http';
  const mark=(phase,detail={})=>marks.push({phase,ms:performance.now()-started,...detail});
  const diagnostic=()=>({mode,requests:requests.length,events:events.map(ev=>({type:ev.type,id:ev.id,err:!!ev.err})),streaming,runFinishing,runTimer:runTimer!==null,openTools:openToolOrder,firstRow:firstRow?{classes:firstRow.className,clock:firstRow._clock!==null && firstRow._clock!==undefined}:null,intervals:intervals.size,marks});
  window.fetch=async(url,opts)=>{
    const path=new URL(url,location.href).pathname;mark('fetch',{url:String(url)});
    if(path==='/api/chat'){requests.push(JSON.parse(opts.body));mark('chat-request-'+requests.length);if(requests.length===2)nextStarted.resolve();}
    const response=await fetchOriginal(url,opts);if(path==='/api/chat')mark('chat-headers-'+requests.length,{status:response.status});return response;
  };
  window.setInterval=(fn,ms,...args)=>{const id=intervalOriginal(fn,ms,...args);intervals.add(id);return id;};
  window.clearInterval=id=>{intervals.delete(id);clearIntervalOriginal(id);};
  superCliUI=Object.assign({},superCliUI,{readSSE:(body,onEvent)=>reader(body,event=>{
    events.push(event);mark('sse-'+event.type,{id:event.id||''});onEvent(event);
    if(command && event.type==='tool_call' && event.id===callID){
      equal(event.name,'ctx_execute','native tool name');firstRow=toolRows[callID];check(firstRow,'production tool row exists');check(firstRow.classList.contains('running'),'live tool row running');check(firstRow._clock!==null,'live tool clock exists');
    }
    if(command && event.type==='tool_result' && event.id===callID){
      resultCount++;check(firstRow,'result has real tool_call row');equal(!!event.err,mode==='cmd-error','native command outcome preserved');
      check(firstRow.classList.contains(mode==='cmd-error'?'failed':'done'),'tool_result settles row');equal(firstRow.classList.contains('running'),false,'result clears running');equal(firstRow._clock,null,'result clears row clock');equal(openToolOrder.includes(callID),false,'result clears open tool');resultSeen.resolve();
    }
    if(!command && event.type==='tool_call' && event.id==='blocked-curl')tool.resolve();
    if(!command && event.type==='message' && event.text.includes('after stop response'))nextMessage.resolve();
  })});
  sendPrompt=(...args)=>{const promise=sendOriginal(...args);sends.push(promise);return promise;};
  function idle(label){
    equal(streaming,false,label+' streaming');equal(runFinishing,false,label+' runFinishing');equal(runTimer,null,label+' run timer');equal(pendingImmediate,null,label+' pending prompt');
    equal(queueDispatching,false,label+' queue dispatch');equal(promptQueue.length,0,label+' queued prompts');equal(openToolOrder.length,0,label+' open tools');
    equal(document.querySelectorAll('.tool-row.running').length,0,label+' running rows');
    for(const row of document.querySelectorAll('.tool-row'))equal(row._clock,null,label+' residual row clock');
    equal(intervals.size,0,label+' residual intervals');equal(document.querySelector('#status-dot').classList.contains('busy'),false,label+' busy indicator');equal(document.querySelector('#stop-run-btn').hidden,true,label+' Stop hidden');
  }
  try {
    mark('first-submit');submit(command?'Run the requested native command using ctx_execute and then answer.':'Fetch the local fixture using ctx_execute.');equal(sends.length,1,'first form submit accepted');
    if(command){
      await Promise.race([resultSeen.promise,sends[0].then(()=>{if(resultCount!==1)throw Error('stream finished without exact native tool_result');})]);await sends[0];
      equal(resultCount,1,'exactly one native tool result');equal(events.filter(ev=>ev.type==='tool_call' && ev.id===callID).length,1,'exactly one native tool call');equal(events.filter(ev=>ev.type==='done').length,1,'normal stream done once');equal(events.filter(ev=>ev.type==='error').length,0,'native outcome does not abort stream');idle('first completed command');
      const firstIdle=await(await fetch('/fixture/active-work')).json();equal(firstIdle.activeWork,false,'backend idle after normal EOF');
      const firstSession=activeSessionID;check(firstSession,'first durable session exists');const firstCompletedAt=performance.now()-started;
      mark('next-submit');submit('Reply after completed command without rerunning it.');equal(sends.length,2,'next submit accepted immediately');
      await Promise.race([nextStarted.promise,sends[1].then(()=>{if(requests.length!==2)throw Error('next turn ended without HTTP request');})]);
      equal(requests.length,2,'next submit not queued');equal(requests[1].session_id,firstSession,'next message keeps conversation');await sends[1];idle('next completed turn');
      equal(events.filter(ev=>ev.type==='done').length,2,'next done exactly once');equal(events.filter(ev=>ev.type==='tool_call').length,1,'next turn does not rerun command');check(document.querySelector('#stream').textContent.includes('next-command-end response'),'next prompt reaches fixture provider');
      const nextIdle=await(await fetch('/fixture/active-work')).json();equal(nextIdle.activeWork,false,'backend idle after next turn');equal(errors.length,0,'GUI errors: '+errors.join('; '));noErrors();
      Object.assign(window.__uiCheckMetrics,{renderer:'Chrome native DOM events, HTTP/SSE and production JS',mode,session_id:firstSession,requests:requests.length,tool_results:resultCount,done:2,activeWork:nextIdle.activeWork,first_completion_ms:firstCompletedAt,total_ms:performance.now()-started,marks});
    } else {
      await Promise.race([tool.promise,sends[0].then(()=>{throw Error('GUI stream ended before command: '+document.querySelector('#stream').textContent);})]);
      if(mode==='running-command')equal((await fetch('/fixture/command-ready')).status,204,'native command ready');
      mark('stop-click');document.querySelector('#stop-run-btn').dispatchEvent(new Event('click'));
      equal(document.querySelector('#stop-run-btn').hidden,true,'Stop hides immediately');equal(document.querySelector('#status-dot').classList.contains('busy'),false,'Stop clears busy immediately');
      mark('next-submit');submit('Reply after Stop without rerunning curl.');equal(pendingImmediate.text,'Reply after Stop without rerunning curl.','next prompt retained during stop');
      await nextStarted.promise;await nextMessage.promise;await sends[0];await sends[1];
      if(mode==='running-command')equal((await fetch('/fixture/command-canceled')).status,204,'native command canceled');
      equal(requests.length,2,'two HTTP requests');equal(requests[1].session_id,requests[0].session_id || activeSessionID,'after Stop conversation preserved');
      equal(streaming,false,'after Stop streaming');equal(runFinishing,false,'after Stop runFinishing');equal(pendingImmediate,null,'after Stop pending prompt');equal(queueDispatching,false,'after Stop queue dispatch');
      equal(document.querySelector('#status-dot').classList.contains('busy'),false,'after Stop busy indicator');check(document.querySelector('#stream').textContent.includes('after stop response'),'after Stop response rendered');equal(errors.length,0,'GUI errors: '+errors.join('; '));noErrors();
      const stop=marks.find(m=>m.phase==='stop-click').ms,second=marks.find(m=>m.phase==='chat-request-2').ms,lastMessage=marks.filter(m=>m.phase==='sse-message').at(-1).ms;
      Object.assign(window.__uiCheckMetrics,{renderer:'Chrome native DOM events, HTTP/SSE and production JS',mode,requests:requests.length,stop_to_next_request_ms:second-stop,stop_to_next_message_ms:lastMessage-stop,total_ms:performance.now()-started,marks});
    }
  } catch(error){window.__uiCheckMetrics.diagnostic=diagnostic();throw Error((error.stack||String(error))+'\nchat-diagnostic='+JSON.stringify(diagnostic()));}
  finally {window.fetch=fetchOriginal;sendPrompt=sendOriginal;window.setInterval=intervalOriginal;window.clearInterval=clearIntervalOriginal;superCliUI=Object.assign({},superCliUI,{readSSE:reader});for(const id of intervals)clearIntervalOriginal(id);}
}
