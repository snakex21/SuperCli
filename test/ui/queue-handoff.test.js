import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript, parseHTML} from './harness.js';

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
}
function element(_tag, className = '', text = '') {
  const classes = new Set(className.split(' ').filter(Boolean));
  return {
    children: [], textContent: text, innerHTML: '', hidden: false, value: '', style: {},
    attributes: {}, events: {}, dataset: {}, isConnected: true,
    classList: {
      add(...names) { names.forEach(n => classes.add(n)); },
      remove(...names) { names.forEach(n => classes.delete(n)); },
      contains(name) { return classes.has(name); },
      toggle(name, enabled) { if (enabled) classes.add(name); else classes.delete(name); },
    },
    appendChild(child) { this.children.push(child); return child; },
    setAttribute(name, value) { this.attributes[name] = value; },
    addEventListener(name, callback) { this.events[name] = callback; },
    querySelector() { return null; },
    focus() {},
  };
}
function harness(options = {}) {
  const elements = new Map();
  const nativeDOM = options.editorDOM ? parseHTML('<!doctype html><html><body></body></html>').document : null;
  if (nativeDOM) Object.defineProperty(nativeDOM, 'hidden', {configurable: true, value: false});
  const makeElement = nativeDOM ? (tag, cls = '', text = '') => {
    const n = nativeDOM.createElement(tag); n.className = cls; n.textContent = text; return n;
  } : element;
  const calls = [], events = [], requests = [], sends = [];
  const arrivals = Array.from({length: 4}, deferred);
  const streams = new Map(), terminals = new Map(), finishing = new Map(), intervals = new Map(), failures = new Map();
  let nativeReadSSE;
  const entered = Array.from({length: 4}, deferred);
  const deletion = deferred();
  const catalog = JSON.parse(readSource(sourcePath(testDir, '../../internal/webgui/assets/locales/en.json'), 'utf8'));
  const $ = selector => {
    if (!elements.has(selector)) {
      const n = makeElement(selector === '#prompt' ? 'textarea' : selector === '#chat-form' ? 'form' : 'div');
      if (nativeDOM) { n.id = selector.slice(1); nativeDOM.body.appendChild(n); }
      elements.set(selector, n);
    }
    return elements.get(selector);
  };
  const context = {
    document: nativeDOM || {hidden: false, hasFocus: () => true, createElement: element}, localStorage: {getItem: () => null},
    activeAppDialog: null,
    AbortController, Date, Promise, console, window: new EventTarget(),
    $, $$: () => [], el: makeElement,
    i18nEl: (tag, cls, key) => makeElement(tag, cls, catalog[key]),
    t: key => { assert.ok(catalog[key], 'missing translation: ' + key); return catalog[key]; },
    superCliUI: {
      createComposerDraftStore: () => ({scope: () => options.dynamicScope ? (context.activeSessionID ? 'session:' + context.activeSessionID : 'new') : 'session:s1', clear() {}, restore() {}}),
      readSSE: async (body, emit) => {
        if (body.claimed) { emit({type: 'queue_claimed'}); return; }
        const done = deferred();
        let send = emit, finish = done.resolve, fail = done.reject, reading;
        if (options.nativeSSE) {
          const stream = new ReadableStream({start(controller) {
            send = event => controller.enqueue(new TextEncoder().encode('data: ' + JSON.stringify(event) + '\n\n'));
            finish = () => controller.close();
            fail = error => controller.error(error);
          }});
          reading = nativeReadSSE(stream, emit);
        } else reading = done.promise;
        const sessionID = options.sessionIDs ? options.sessionIDs[body.index] : 's1';
        if (!(options.hideSession && body.index === 0)) {
          send({type: 'session', session_id: sessionID});
          if (options.acceptMessage !== false && !(options.rejectSecondMessage && body.index === 1)) send(options.userSeq ? {type: 'session_activity', session_id: sessionID, user_seq: 3} : {type: 'session_activity', session_id: sessionID});
        }
        finishing.set(body.index, () => send({type: 'finishing'}));
        terminals.set(body.index, () => send(options.failRun ? {type: 'error', err: 'provider overloaded'} : {type: 'done', queue_paused: !!options.remoteStop}));
        streams.set(body.index, () => { if (!options.separateTerminal) terminals.get(body.index)(); finish(); });
        failures.set(body.index, fail);
        body.signal.addEventListener('abort', () => fail(body.signal.reason), {once: true});
        entered[body.index].resolve();
        await reading;
      },
    },
    setInterval: callback => { const id = intervals.size + 1; intervals.set(id, callback); return id; },
    clearInterval(id) { intervals.delete(id); },
    fetch: async (_url, request) => {
      const index = requests.length;
      requests.push(request);
      arrivals[index].resolve(request);
      if (options.responseGate && index === 0) await options.responseGate.promise;
      if (options.loseResponse) throw new TypeError('response lost before headers');
      return {ok: options.accepted !== false && !(options.rejectSecondHTTP && index === 1), status: 503, body: {index, signal: request.signal}};
    },
    j: async (url, opts) => {
      calls.push([url, opts]);
      if (options.api && url.startsWith('/api/tasks')) return options.api(url, opts);
      if (url.startsWith('/api/chat/completion?id=')) {
        if (options.status && url.includes('&status=1')) return options.status(url, opts);
        if (options.completionRequested) options.completionRequested.resolve();
        if (options.completionFails) throw new Error("completion unavailable");
        if (options.completion) return await options.completion.promise;
        return {session_id: 's1', accepted: true};
      }
      if (url.startsWith('/api/tasks?id=')) {
        if (options.deleteFails) throw new Error('queue database unavailable');
        await deletion.promise;
        return {};
      }
      throw new Error('unexpected API: ' + url);
    },
    jpost: async (url, body) => {
      assert.equal(url, '/api/sync/stop'); assert.ok(body.turn_id); calls.push([url, body]);
      if (options.stopRequested) options.stopRequested.resolve();
      if (options.stopFails) throw options.stopFails;
      if (options.stopResponse) return await options.stopResponse.promise;
      return {ok: true};
    },
    sessionByID: {s1: {model: 'old-model'}}, transcriptSessionID: 's1',
    transcriptAbortCtl: null, loadedTranscriptMessages: [], transcriptHasMore: false,
    transcriptBeforeSeq: 0, transcriptLiveAppend: false,
    stream: element('div'), toolRows: {}, openToolOrder: [],
    overlay: {hidden: true}, currentSection: '', sections: {},
    fmtDuration: () => '1s', clip: value => value,
  };
  context.superCliUI.chatResponse = context.fetch;
  createSandbox(context);
  if (options.nativeSSE) {
    runScript(readSource(sourcePath(testDir, '../../internal/webgui/shared-ui/runtime.js'), 'utf8'), context);
    nativeReadSSE = context.window.SuperCliUI.readSSE;
  }
  runScript(readSource(sourcePath(testDir, '../../internal/webgui/assets/js/05-chat.js'), 'utf8'), context);
  runScript(readSource(sourcePath(testDir, '../../internal/webgui/assets/js/05a-lightbox.js'), 'utf8'), context);
  context.activeSessionID = 's1';
  for (const name of ['updateAppBadge', 'wireQueuedTaskDrag', 'sealAssistantSegment',
    'closeQuestionOverlay', 'settleOpenTools', 'releaseLiveTranscriptBlocks', 'renderStats',
    'addFileChanges', 'addTurnMeta', 'loadSideGoal', 'closeAssistantReasoning', 'flushAssistantRender',
    'releaseAssistantParagraphCaches', 'pruneWorkerOverview']) context[name] = () => {};
  context.toast = value => events.push(value);
  context.addEventLine = value => events.push(value);
  context.addUserMsg = () => element('div');
	context.bindLiveMessageReceipt = () => {}; // 04-transcript is stubbed in this queue fixture.
  context.addLatestMessageRewind = async (_node, _text, attempts) => {
    assert.equal(attempts, 1, 'handoff must not poll transcript repeatedly');
    return 0;
  };
  context.rememberSentAttachments = () => {};
  context.clearAttachments = () => { context.pendingAttachments = []; };
  context.addAttachmentPaths = paths => { context.pendingAttachments = [...new Set([...context.pendingAttachments, ...paths])]; };
  context.loadSessions = () => new Promise(() => {}); // slow sidebar must not block dispatch
  context.notifyDone = () => events.push('completed-notification');
  context.restoreSessionRuntime = async () => calls.push(['restore-runtime']);
  context.resumeSession = async (...args) => { calls.push(['resume', ...args]); return options.resume !== false; };
  const sendPrompt = context.sendPrompt;
  context.sendPrompt = (...args) => {
    const pending = sendPrompt(...args); sends.push(pending); return pending;
  };
  return {context, $, calls, events, requests, sends, arrivals, streams, terminals, finishing, intervals, entered, deletion, failures};
}
const nextBrowserTask = () => new Promise(resolve => setTimeout(resolve, 0));

for (const session of ['', 's1']) test('lost POST headers reconcile exact durable turn before following: ' + (session || 'fresh'), async () => {
  const h = harness({loseResponse:true, status:async url => {
    assert.equal(new URL('http://fixture'+url).searchParams.get('id'), JSON.parse(h.requests[0].body).turn_id);
    return {accepted:true, session_id:'s1', done:false};
  }}), c = h.context;
  c.activeSessionID = session;
  const cleared = []; c.composerDraftStore.clear = (...args) => cleared.push(args);
  const item = {id:'lost-task', prompt:'mutate once', session_id:session};
  c.promptQueue = [item];
  let followed = null;
  c.syncFollowRun = run => { followed = run; run.following = true; };
  h.deletion.resolve();
  await c.sendPrompt('mutate once', [], {scope:'session:s1',text:'mutate once'}, item);
  assert.equal(h.requests.length, 1, 'never resend chat POST');
  assert.ok(followed && followed.accepted, 'follow only confirmed durable acceptance');
  assert.equal(followed.sessionID, 's1');
  assert.equal(cleared.length, 1);
  assert.equal(c.promptQueue.length, 0);
});
for (const outcome of ['rejected', 'unknown', 'pending']) test('lost headers ' + outcome + ' retains draft and queue without ghost busy', async () => {
  let reads = 0;
  const h = harness({loseResponse:true, status:async () => {
    reads++;
    if (outcome === 'unknown') throw new Error('404 not yet registered');
    return {accepted:false, done:outcome === 'rejected'};
  }}), c = h.context, restored = [], cleared = [];
  c.composerDraftStore.restore = scope => restored.push(scope);
  c.composerDraftStore.clear = scope => cleared.push(scope);
  const item = {id:'retained',text:'keep me',session_id:'s1'}; c.promptQueue=[item];
  await c.sendPrompt('keep me', [], {scope:'session:s1',text:'keep me'}, item);
  assert.equal(h.requests.length,1); assert.equal(c.promptQueue[0],item);
  assert.deepEqual(cleared,[]); assert.deepEqual(restored,['session:s1']);
  assert.equal(c.pauseQueue,true); assert.equal(c.streaming,false); assert.equal(c.activeRun,null);
  assert.equal(c.queueDispatchItem,null); assert.equal(h.intervals.size,0);
  assert.equal(reads,outcome === 'rejected' ? 1 : 4);
});

test('delayed receipt is unknown until durable acceptance and never drains the queue meanwhile', async () => {
  let reads=0;const cleared=[];
  const h=harness({loseResponse:true,status:async()=>{
    reads++;assert.equal(h.requests.length,1);assert.equal(cleared.length,0);
    assert.equal(h.context.promptQueue.length,1);
    return reads<3 ? {accepted:false,done:false} : {accepted:true,session_id:'s1',done:false};
  }}),c=h.context;
  c.composerDraftStore.clear=(...args)=>cleared.push(args);c.promptQueue=[{id:'later',text:'do not drain',session_id:'s1'}];
  c.syncFollowRun=run=>{run.following=true;};
  await c.sendPrompt('delayed',[],{scope:'session:s1',text:'delayed'});
  assert.equal(reads,3);assert.equal(cleared.length,1);assert.equal(h.requests.length,1);
  c.finishRunPresentation();
});

test('offline status reads abort within the observation budget and release busy state', async () => {
  let reads=0;
  const h=harness({loseResponse:true,status:(_url,opts)=>new Promise((_resolve,reject)=>{
    reads++;opts.signal.addEventListener('abort',()=>reject(opts.signal.reason),{once:true});
  })}),c=h.context;
  c.setTimeout=fn=>setTimeout(fn,1);c.clearTimeout=clearTimeout;
  await c.sendPrompt('offline');
  assert.equal(reads,4);assert.equal(h.requests.length,1);assert.equal(c.pauseQueue,true);
  assert.equal(c.activeRun,null);assert.equal(c.streaming,false);assert.equal(h.intervals.size,0);
});

test('already completed lost response confirms acceptance but pauses unobserved queue outcome', async () => {
  const h=harness({loseResponse:true,status:async()=>({accepted:true,session_id:'s1',done:true})}),c=h.context;
  const cleared=[];c.composerDraftStore.clear=(...args)=>cleared.push(args);
  c.promptQueue=[{id:'next',text:'not automatic',session_id:'s1'}];
  await c.sendPrompt('done',[],{scope:'session:s1',text:'done'});
  assert.equal(cleared.length,1);assert.equal(c.pauseQueue,true);assert.equal(h.requests.length,1);
  assert.equal(c.streaming,false);assert.equal(c.activeRun,null);assert.equal(h.intervals.size,0);
});

for (const change of ['project','session','new-conversation','foreground']) test('late acceptance cannot clobber ' + change, async () => {
  const status = deferred(), requested = deferred();
  const h = harness({loseResponse:true,status:async()=>{requested.resolve();return status.promise;}}),c=h.context;
  const cleared=[];c.composerDraftStore.clear=(...args)=>cleared.push(args);
  const item={id:'stale',text:'old',session_id:'s1'};c.promptQueue=[item];
  const first=c.sendPrompt('old',[],{scope:'session:s1',text:'old'},item);await requested.promise;
  const newer={turnID:'newer'};
  if(change==='project')c.projectEpoch++;
  if(change==='session')c.activeSessionID='s2';
  if(change==='new-conversation'){c.activeSessionID='';c.activeRun=null;}
  if(change==='foreground')c.activeRun=newer;
  status.resolve({accepted:true,session_id:'stale-session',done:false});await first;
  assert.equal(h.requests.length,1);assert.deepEqual(cleared,[]);assert.equal(c.promptQueue[0],item);
  assert.notEqual(c.activeSessionID,'stale-session');
  if(change==='foreground')assert.equal(c.activeRun,newer);
  // The navigation path owns presentation cleanup, not the stale request.
  c.finishRunPresentation();
});

test('Stop during lost-header reconciliation targets the turn before acceptance and never resends', async () => {
  const status=deferred(),requested=deferred();
  const h=harness({loseResponse:true,status:async()=>{requested.resolve();return status.promise;}}),c=h.context;
  const first=c.sendPrompt('stop me');await requested.promise;h.$('#stop-run-btn').events.click();
  assert.equal(h.calls.filter(x=>x[0]==='/api/sync/stop').length,1);
  assert.equal(h.calls.find(x=>x[0]==='/api/sync/stop')[1].turn_id,JSON.parse(h.requests[0].body).turn_id);
  status.resolve({accepted:true,session_id:'s1',done:false});await first;
  assert.equal(h.requests.length,1);assert.equal(h.calls.filter(x=>x[0]==='/api/sync/stop').length,1);
  assert.equal(c.pauseQueue,true);assert.equal(c.streaming,false);assert.equal(c.activeRun,null);
});

test('Stop registration race retries unsuccessful Stop on pending receipt observations without another POST', async () => {
  const pending=deferred(),observing=deferred();let stops=0,reads=0;
  const h=harness({loseResponse:true,status:async()=>{
    reads++;if(reads===1){observing.resolve();return pending.promise;}
    return {accepted:false,done:stops>=2};
  }}),c=h.context;
  c.jpost=async(url,body)=>{assert.equal(url,'/api/sync/stop');assert.equal(body.turn_id,JSON.parse(h.requests[0].body).turn_id);stops++;if(stops===1)throw new Error('410 not registered yet');return {ok:true};};
  const first=c.sendPrompt('stop before registration');await observing.promise;
  h.$('#stop-run-btn').events.click();await nextBrowserTask();
  assert.equal(stops,1);pending.resolve({accepted:false,done:false});await first;
  assert.equal(stops,2,'retry failed Stop after the turn registers, even before acceptance');
  assert.equal(h.requests.length,1);assert.equal(c.streaming,false);assert.equal(c.pauseQueue,true);
});

for(const failDelete of [false,true]) test('recovered queued DELETE after real detach and navigation hands off old run without foreground mutations: '+(failDelete?'failure':'success'),async()=>{
  const deleting=deferred(),deleteStarted=deferred();
  const h=harness({editorDOM:true,loseResponse:true,status:async()=>({accepted:true,session_id:'s1',done:false}),api:async(url,opts)=>{
    assert.equal(opts.method,'DELETE');deleteStarted.resolve();await deleting.promise;
    if(failDelete)throw new Error('old DELETE failed');return {};
  }}),c=attachQueueSync(h);
  c.syncLoadProjects=()=>{};c.loadPromptQueue=()=>{};c.syncSettleTranscriptScroll=()=>{};
  let connects=0;c.syncConnect=()=>{connects++;};
  const item={id:'shared-id',text:'old queued',session_id:'s1'};c.promptQueue=[item];c.queueDispatchItem=item;
  const first=c.sendPrompt(item.text,[],null,item);await deleteStarted.promise;
  const old=c.activeRun;assert.equal(old.accepted,true);assert.equal(c.detachActiveRun(),true);
  assert.equal(c.detachedRuns.s1,old);
  c.projectEpoch++;c.activeSessionID='s2';c.transcriptSessionID='s2';c.syncSession='s2';
  const newer={turnID:'new-foreground',sessionID:'s2'},next={id:'shared-id',text:'new project queue',session_id:'s2'};
  c.activeRun=newer;c.streaming=true;c.queueDispatching=true;c.queueDispatchItem=next;c.promptQueue=[next];c.pauseQueue=false;
  const toasts=h.events.length;deleting.resolve();await first;
  assert.equal(c.detachedRuns.s1,undefined,'old transport must no longer own detached run');
  assert.equal(old.following,true);assert.equal(connects,1);
  const info={turn_id:old.turnID,session_id:'s1',client_id:'original',prompt:'old queued'};
  assert.ok(!c.syncOwnRun(info),'sync may observe original run');
  c.syncHandleFrame({type:'snapshot',runs:[info],events:[]});
  assert.equal(c.syncRuns.s1.remote,true,'old run is now an observable sync run');
  assert.equal(c.activeRun,newer);assert.equal(c.activeSessionID,'s2');assert.equal(c.transcriptSessionID,'s2');
  assert.equal(c.queueDispatchItem,next);assert.equal(c.queueDispatching,true);assert.equal(c.promptQueue[0],next);
  assert.equal(c.pauseQueue,false);assert.equal(c.streaming,true);assert.equal(h.events.length,toasts);
  c.finishRunPresentation();
});

test('delayed receipt hands lost fresh POST to production sync snapshot and replay once', async () => {
  const h=harness({editorDOM:true,loseResponse:true,status:async()=>({accepted:true,session_id:'recovered',done:false})}),c=attachQueueSync(h);
  c.activeSessionID=''; c.syncSession=''; c.syncLoadProjects=()=>{};c.loadPromptQueue=()=>{};
  c.syncSettleTranscriptScroll=()=>{};
  let connects=0, visible=0;c.addUserMsg=()=>{visible++;return element('div');};
  c.syncConnect=()=>{connects++;c.syncHandleFrame({type:'snapshot',runs:[{turn_id:JSON.parse(h.requests[0].body).turn_id,session_id:'recovered',client_id:'original',prompt:'once'}],events:[{type:'session_activity',session_id:'recovered',user_seq:1},{type:'message',text:'existing run'}]});};
  const cleared=[];c.composerDraftStore.clear=(...args)=>cleared.push(args);
  await c.sendPrompt('once',[],{scope:'session:s1',text:'once'});
  assert.equal(h.requests.length,1);assert.equal(connects,1);assert.equal(cleared.length,1);
  assert.equal(c.activeSessionID,'recovered');assert.equal(c.syncRuns.recovered.remote,true);
  assert.equal(c.syncRuns.recovered.userSeq,1);assert.equal(c.syncRuns.recovered.events.length,2);
  assert.equal(visible,1,'optimistic message not appended twice before durable transcript refresh');
  assert.equal(c.activeRun,null);assert.equal(c.streaming,false);assert.equal(h.intervals.size,0);
});

function queueEditRunFixture(mode = 'off') {
  const state = {tasks: [
    {id:'edit-a', prompt:'original A', session_id:'s1', attachments:['/stage/original.txt']},
    {id:'edit-b', prompt:'original B', session_id:'s1', attachments:[]}
  ], mode, patch: null, reload: null, failPatch: false, failLoad: false, merges: [], mergeGate: null, mergeStarted: deferred()};
  const h = harness({nativeSSE:true, editorDOM:true, api:async(url,opts={})=>{
    if (opts.method === 'PATCH') {
      if (state.patch) await state.patch.promise;
      if (state.failPatch) throw new Error('409 task was claimed remotely');
      const body=JSON.parse(opts.body), item=state.tasks.find(q=>q.id===body.id);
      if (!item) throw new Error('409 task was claimed remotely');
      Object.assign(item,{prompt:body.prompt,attachments:body.attachments});return {...item};
    }
    if (opts.method === 'DELETE') {state.tasks=state.tasks.filter(q=>q.id!==new URL('http://fixture'+url).searchParams.get('id'));return {};}
    if (state.reload) await state.reload.promise;
    if (state.failLoad) throw new Error('queue reload unavailable');
    return JSON.parse(JSON.stringify({tasks:state.tasks,queue_merge:state.mode,running:true}));
  }});
  const c=h.context;
  c.queueMergeMode=mode;
  const post=c.jpost;
  c.jpost=async(url,body)=>{
    if(url!=='/api/tasks?merge=1')return post(url,body);
    state.merges.push(body);state.mergeStarted.resolve();if(state.mergeGate)await state.mergeGate.promise;
    state.tasks=[{id:'auto-merged',prompt:state.tasks.map(q=>q.prompt).join('\n'),session_id:'s1',attachments:state.tasks.flatMap(q=>q.attachments),merged:true}];
    return JSON.parse(JSON.stringify(state.tasks));
  };
  c.promptQueueLoadedEpoch=c.projectEpoch;
  c.promptQueue=state.tasks.map(q=>({...q,text:q.prompt,attachments:[...q.attachments]}));
  c.renderPromptQueue();
  h.openEdit=(id='edit-a')=>{
    h.$('#prompt-queue').querySelector('[data-queue-edit-id="'+id+'"]').click();
    assert.ok(c.queueEdit);return c.queueEdit;
  };
  h.typeEdit=text=>{c.queueEdit.input.value=text;c.queueEdit.input.dispatchEvent(new Event('input',{bubbles:true}));};
  h.editButton=key=>Array.from(c.queueEdit.dialog.querySelectorAll('button')).find(n=>n.textContent===c.t(key));
  h.cleanup=async()=>{c.pauseQueue=true;h.deletion.resolve();for(const end of h.streams.values()) {try{end();}catch(_){}} await Promise.all(h.sends);};
  const save=c.saveQueuedTaskEdit;
  h.save=()=>{let result;c.saveQueuedTaskEdit=(...args)=>{result=save(...args);return result;};h.editButton('common.save').click();c.saveQueuedTaskEdit=save;return result;};
  h.cancel=()=>h.editButton('common.cancel').click();
  h.state=state;return h;
}

test('native SSE EOF while head editor is open retains draft and never dispatches original head',async()=>{
  const h=queueEditRunFixture(),c=h.context;
  try {
    const first=c.sendPrompt('primary working');await h.entered[0].promise;
    const edit=h.openEdit();h.typeEdit('revised A');
    h.streams.get(0)();await first;await nextBrowserTask();
    assert.equal(h.requests.length,1,'EOF must not auto-send old A while its editor is open');
    assert.equal(c.queueEdit,edit);assert.equal(edit.input.value,'revised A');assert.equal(edit.dialog.open,true);
  } finally {await h.cleanup();}
});

test('head Save waits for PATCH and durable reload then sends exactly the saved text and attachments once',async()=>{
 const h=queueEditRunFixture(),c=h.context;
 try {
  const first=c.sendPrompt('primary');await h.entered[0].promise;const edit=h.openEdit();h.typeEdit('SAVED A');edit.attachments=['/stage/revised.txt'];
  h.streams.get(0)();await first;await nextBrowserTask();
  h.state.patch=deferred();const saving=h.save();await nextBrowserTask();assert.equal(h.requests.length,1);assert.equal(c.queueEdit,edit);
  h.state.reload=deferred();h.state.patch.resolve();await nextBrowserTask();assert.equal(h.requests.length,1,'POST success is not durable reload completion');assert.equal(c.queueEdit,edit);
  h.state.reload.resolve();await saving;await h.entered[1].promise;await nextBrowserTask();
  const sent=JSON.parse(h.requests[1].body);assert.equal(sent.prompt,'SAVED A');assert.deepEqual(sent.attachments,['/stage/revised.txt']);assert.equal(sent.queue_task_id,'edit-a');assert.equal(c.queueEdit,null);assert.equal(h.requests.length,2);
 } finally {await h.cleanup();}
});

test('Cancel resumes original head exactly once only when an automatic cycle waited for its editor',async()=>{
 const h=queueEditRunFixture(),c=h.context;
 try {const first=c.sendPrompt('primary');await h.entered[0].promise;h.openEdit();h.typeEdit('discarded A');h.streams.get(0)();await first;
  h.cancel();await h.entered[1].promise;await nextBrowserTask();assert.equal(h.requests.length,2);const sent=JSON.parse(h.requests[1].body);assert.equal(sent.prompt,'original A');assert.deepEqual(sent.attachments,['/stage/original.txt']);
 } finally {await h.cleanup();}
});

for(const stop of ['paused','Stop','project','session']) test('blocked edit handoff never auto-resumes after '+stop,async()=>{
 const h=queueEditRunFixture(),c=h.context;
 try {const first=c.sendPrompt('primary');await h.entered[0].promise;h.openEdit();h.streams.get(0)();await first;
  if(stop==='paused')c.pauseQueue=true;
  if(stop==='Stop')h.$('#stop-run-btn').click();
  if(stop==='project')c.projectEpoch++;
  if(stop==='session')c.activeSessionID='different-session';
  h.cancel();await nextBrowserTask();assert.equal(h.requests.length,1);assert.equal(c.queueAutoDrain,null);
 } finally {await h.cleanup();}
});

for(const paused of [false,true]) test('opening and closing an idle '+(paused?'paused':'unpaused')+' queue editor never starts a job',async()=>{
 const h=queueEditRunFixture(),c=h.context;
 try {c.pauseQueue=paused;h.openEdit();h.typeEdit('idle revision');await h.save();await nextBrowserTask();assert.equal(h.requests.length,0);h.openEdit();h.cancel();await nextBrowserTask();assert.equal(h.requests.length,0);}
 finally{await h.cleanup();}
});

test('editing later B permits earlier A and waits only once B becomes head',async()=>{
 const h=queueEditRunFixture(),c=h.context;
 try {const first=c.sendPrompt('primary');await h.entered[0].promise;const edit=h.openEdit('edit-b');h.typeEdit('revised B');h.streams.get(0)();await first;await h.entered[1].promise;
  assert.equal(JSON.parse(h.requests[1].body).prompt,'original A');assert.equal(c.queueEdit,edit);
  h.streams.get(1)();await h.sends[1];await nextBrowserTask();assert.equal(h.requests.length,2);assert.equal(c.queueEdit,edit);
  await h.save();await h.entered[2].promise;assert.equal(JSON.parse(h.requests[2].body).prompt,'revised B');assert.equal(h.requests.length,3);
 }finally{await h.cleanup();}
});

for(const failure of ['PATCH','reload']) test('failed '+failure+' keeps edit intent and no stale dispatch until successful retry',async()=>{
 const h=queueEditRunFixture(),c=h.context;
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;const edit=h.openEdit();h.typeEdit('retry revision');h.streams.get(0)();await first;
  h.state.failPatch=failure==='PATCH';h.state.failLoad=failure==='reload';await h.save();await nextBrowserTask();assert.equal(h.requests.length,1);assert.equal(c.queueEdit,edit);assert.equal(edit.input.value,'retry revision');assert.ok(edit.error.textContent);
  h.state.failPatch=false;h.state.failLoad=false;await h.save();await h.entered[1].promise;assert.equal(JSON.parse(h.requests[1].body).prompt,'retry revision');
 }finally{await h.cleanup();}
});

test('remote removal plus 409 preserves unsaved draft, surfaces conflict, and cannot drain the next item',async()=>{
 const h=queueEditRunFixture(),c=h.context;
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;const edit=h.openEdit();h.typeEdit('do not lose me');h.streams.get(0)();await first;
  h.state.tasks=h.state.tasks.slice(1);h.state.failPatch=true;await h.save();await nextBrowserTask();assert.equal(c.queueEdit,edit);assert.equal(edit.input.value,'do not lose me');assert.match(edit.error.textContent,/409/);assert.equal(h.requests.length,1);assert.equal(c.queueAutoDrain,null);
  h.cancel();await nextBrowserTask();assert.equal(h.requests.length,1);
 }finally{await h.cleanup();}
});

for(const failure of [false,true]) test('pending upload '+(failure?'failure':'success')+' retains editor across EOF and requires Save or Cancel',async()=>{
 const h=queueEditRunFixture(),c=h.context;const upload=deferred();
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;const edit=h.openEdit();h.typeEdit('with upload');c.fetch=()=>upload.promise;
  const paste=new Event('paste',{bubbles:true,cancelable:true});Object.defineProperty(paste,'clipboardData',{value:{files:[new File(['fixture'],'new.txt')],getData:()=>''}});edit.dialog.dispatchEvent(paste);await nextBrowserTask();assert.equal(edit.uploading,true);
  h.streams.get(0)();await first;assert.equal(h.requests.length,1);assert.equal(c.queueEdit,edit);
  if(failure)upload.reject(new Error('upload offline'));else upload.resolve({ok:true,json:async()=>({paths:['/stage/new.txt']})});await nextBrowserTask();assert.equal(h.requests.length,1);assert.equal(c.queueEdit,edit);assert.equal(edit.input.value,'with upload');
  if(failure){assert.ok(edit.error.textContent);h.cancel();}else await h.save();await h.entered[1].promise;
  const sent=JSON.parse(h.requests[1].body);assert.equal(sent.prompt,failure?'original A':'with upload');assert.deepEqual(sent.attachments,failure?['/stage/original.txt']:['/stage/original.txt','/stage/new.txt']);
 }finally{await h.cleanup();}
});

function attachQueueSync(h) {
 const c=h.context;c.location={hostname:'localhost',protocol:'http:',host:'fixture'};
 runScript(readSource(sourcePath(testDir,'../../internal/webgui/assets/js/05b-sync.js'),'utf8'),c);
 c.syncRefreshView=()=>{};c.syncLoadSessions=()=>{};c.syncRenderElsewhere=()=>{};c.syncSession='s1';
 return c;
}

test('sync observer completion uses the same editor gate and resumes saved head exactly once',async()=>{
 const h=queueEditRunFixture(),c=attachQueueSync(h);
 try{const run={remote:true,sessionID:'s1',turn_id:'remote-root'};c.syncRuns.s1=run;c.activeRun=run;c.streaming=true;
  const edit=h.openEdit();h.typeEdit('observer saved A');
  c.syncHandleFrame({type:'run_finished',session_id:'s1'});await nextBrowserTask();assert.equal(h.requests.length,0);assert.equal(c.queueEdit,edit);assert.ok(c.queueAutoDrain);
  await h.save();await h.entered[0].promise;assert.equal(JSON.parse(h.requests[0].body).prompt,'observer saved A');assert.equal(h.requests.length,1);
 }finally{await h.cleanup();}
});

test('Cancel reload plus sync completion and duplicate EOF paths cannot send the head twice',async()=>{
 const h=queueEditRunFixture(),c=attachQueueSync(h);
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;h.openEdit();h.streams.get(0)();await first;
  h.state.reload=deferred();h.cancel();await nextBrowserTask();assert.equal(h.requests.length,1);
  c.syncRuns.s1={remote:true,sessionID:'s1',turn_id:'overlapping-root'};c.syncHandleFrame({type:'run_finished',session_id:'s1'});await nextBrowserTask();assert.equal(h.requests.length,1,'sync must not bypass pending durable reload');
  h.state.reload.resolve();await h.entered[1].promise;await nextBrowserTask();assert.equal(h.requests.length,2);assert.equal(JSON.parse(h.requests[1].body).prompt,'original A');
 }finally{await h.cleanup();}
});

test('Stop while resumed cancel is waiting for durable reload invalidates its fixed scope',async()=>{
 const h=queueEditRunFixture(),c=h.context;
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;h.openEdit();h.streams.get(0)();await first;
  h.state.reload=deferred();h.cancel();await nextBrowserTask();h.$('#stop-run-btn').click();h.state.reload.resolve();await nextBrowserTask();assert.equal(h.requests.length,1);assert.equal(c.queueAutoDrain,null);
 }finally{await h.cleanup();}
});

test('project change while resumed editor handoff waits for model readiness cannot dispatch stale work',async()=>{
 const h=queueEditRunFixture(),c=h.context;const ready=deferred();
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;h.openEdit();h.streams.get(0)();await first;
  c.sessionRuntimeReady=ready.promise;h.cancel();await nextBrowserTask();assert.equal(h.requests.length,1);c.projectEpoch++;ready.resolve();await nextBrowserTask();assert.equal(h.requests.length,1);assert.equal(c.queueDispatching,false);
 }finally{ready.resolve();await h.cleanup();}
});

test('merging a blocked editor never auto-dispatches inside the mutation and sends merged head once afterward',async()=>{
 const h=queueEditRunFixture(),c=h.context,merge=deferred();
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;h.openEdit();h.streams.get(0)();await first;
  c.jpost=async(url)=>{assert.equal(url,'/api/tasks?merge=1');await merge.promise;h.state.tasks=[{id:'merged',prompt:'merged saved tasks',session_id:'s1',attachments:[],merged:true}];return JSON.parse(JSON.stringify(h.state.tasks));};
  const merging=c.mergeQueuedMessages(true);await nextBrowserTask();assert.equal(h.requests.length,1);assert.equal(c.queueMerging,true);assert.ok(c.queueEdit);
  merge.resolve();await merging;await h.entered[1].promise;assert.equal(c.queueMerging,false);assert.equal(c.queueEdit,null);assert.equal(h.requests.length,2);assert.equal(JSON.parse(h.requests[1].body).prompt,'merged saved tasks');
 }finally{merge.resolve();await h.cleanup();}
});

test('failed fresh queue reload after Cancel pauses honestly instead of dispatching cached work',async()=>{
 const h=queueEditRunFixture(),c=h.context;
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;h.openEdit();h.streams.get(0)();await first;
  h.state.failLoad=true;h.cancel();await nextBrowserTask();assert.equal(h.requests.length,1);assert.equal(c.pauseQueue,true);assert.equal(c.queueAutoDrain,null);assert.ok(h.events.includes(c.t('chat.connError')));assert.equal(c.promptQueue[0].text,'original A');
 }finally{await h.cleanup();}
});

test('closing a pending Save cannot overlap a second PATCH or release its head dispatch barrier',async()=>{
 const h=queueEditRunFixture(),c=h.context;const patch=deferred();
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;h.openEdit();h.typeEdit('first saved A');h.state.patch=patch;const saving=h.save();
  h.streams.get(0)();await first;h.cancel();h.openEdit();h.typeEdit('second draft A');assert.equal(h.editButton('common.save').disabled,true);assert.equal(h.save(),undefined);assert.equal(h.calls.filter(x=>x[1]&&x[1].method==='PATCH').length,1);assert.equal(h.requests.length,1);
  patch.resolve();await saving;await nextBrowserTask();assert.equal(h.requests.length,1);assert.equal(c.queueEdit.input.value,'second draft A');assert.equal(h.editButton('common.save').disabled,false);
  h.state.patch=null;await h.save();await h.entered[1].promise;assert.equal(JSON.parse(h.requests[1].body).prompt,'second draft A');assert.equal(h.requests.length,2);
 }finally{patch.resolve();await h.cleanup();}
});

for(const finish of ['Save','Cancel']) test('auto merge skips later B editor and '+finish+' resolves B only when it becomes head',async()=>{
 const h=queueEditRunFixture('auto'),c=h.context;
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;const edit=h.openEdit('edit-b');h.typeEdit('edited B auto');h.streams.get(0)();await first;await h.entered[1].promise;
  assert.equal(JSON.parse(h.requests[1].body).prompt,'original A');assert.equal(c.queueEdit,edit);assert.equal(edit.input.value,'edited B auto');assert.equal(h.state.merges.length,0);
  h.streams.get(1)();await h.sends[1];await nextBrowserTask();assert.equal(h.requests.length,2);assert.equal(c.queueEdit,edit);
  if(finish==='Save')await h.save();else h.cancel();await h.entered[2].promise;
  assert.equal(JSON.parse(h.requests[2].body).prompt,finish==='Save'?'edited B auto':'original B');assert.equal(h.requests.length,3);
 }finally{await h.cleanup();}
});

test('auto merge skips a detached pending PATCH for B while sending A separately',async()=>{
 const h=queueEditRunFixture('auto'),c=h.context,patch=deferred();
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;const edit=h.openEdit('edit-b');h.typeEdit('pending saved B');h.state.patch=patch;const saving=h.save();h.cancel();
  h.streams.get(0)();await first;await h.entered[1].promise;assert.equal(JSON.parse(h.requests[1].body).prompt,'original A');assert.equal(c.queueEditWrite,edit);assert.equal(h.state.merges.length,0);
  patch.resolve();await saving;h.streams.get(1)();await h.sends[1];await h.entered[2].promise;assert.equal(JSON.parse(h.requests[2].body).prompt,'pending saved B');
 }finally{patch.resolve();await h.cleanup();}
});

for(const phase of ['prepare','preflight'])test('auto merge rechecks editors opened during async '+phase,async()=>{
 const h=queueEditRunFixture('auto'),c=h.context,gate=deferred(),entered=deferred();const prepare=c.prepareQueuedTask;
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;
  if(phase==='prepare')c.prepareQueuedTask=async(item)=>{entered.resolve();await gate.promise;return prepare(item);};
  else{h.state.reload=gate;const api=c.j;c.j=(url,opts)=>{if(url.startsWith('/api/tasks?state'))entered.resolve();return api(url,opts);};}
  h.streams.get(0)();await first;await entered.promise;const edit=h.openEdit('edit-b');h.typeEdit('opened during '+phase);gate.resolve();await h.entered[1].promise;
  assert.equal(JSON.parse(h.requests[1].body).prompt,'original A');assert.equal(h.state.merges.length,0);assert.equal(c.queueEdit,edit);assert.equal(edit.input.value,'opened during '+phase);
 }finally{gate.resolve();await h.cleanup();}
});

test('Stop during slow auto merge never clears the new pause or sends the old handoff',async()=>{
 const h=queueEditRunFixture('auto'),c=h.context,merge=deferred();
 try{h.state.mergeGate=merge;const first=c.sendPrompt('primary');await h.entered[0].promise;h.streams.get(0)();await first;await h.state.mergeStarted.promise;
  const old=c.runStopRevision;h.$('#stop-run-btn').click();assert.equal(c.runStopRevision,old+1);merge.resolve();await nextBrowserTask();await nextBrowserTask();
  assert.equal(h.requests.length,1,'old automatic scope must not be renewed after merge');assert.equal(c.pauseQueue,true);assert.equal(c.queueDispatching,false);
 }finally{merge.resolve();await h.cleanup();}
});

test('Stop during auto merge preflight prevents even the merge mutation',async()=>{
 const h=queueEditRunFixture('auto'),c=h.context,gate=deferred(),entered=deferred();
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;h.state.reload=gate;const api=c.j;c.j=(url,opts)=>{if(url.startsWith('/api/tasks?state'))entered.resolve();return api(url,opts);};
  h.streams.get(0)();await first;await entered.promise;h.$('#stop-run-btn').click();gate.resolve();await nextBrowserTask();assert.equal(h.state.merges.length,0);assert.equal(h.requests.length,1);assert.equal(c.pauseQueue,true);
 }finally{gate.resolve();await h.cleanup();}
});

test('submitted auto merge blocks new editor opens until the HTTP mutation settles',async()=>{
 const h=queueEditRunFixture('auto'),c=h.context,merge=deferred();
 try{h.state.mergeGate=merge;const first=c.sendPrompt('primary');await h.entered[0].promise;h.streams.get(0)();await first;await h.state.mergeStarted.promise;
  c.editQueuedTask(c.promptQueue[1]);assert.equal(c.queueEdit,null);assert.equal(c.queueMerging,true);
  h.$('#stop-run-btn').click();merge.resolve();await nextBrowserTask();assert.equal(h.requests.length,1);
 }finally{merge.resolve();await h.cleanup();}
});

test('Stop during post-merge prepare cannot renew the original dispatch scope',async()=>{
 const h=queueEditRunFixture('auto'),c=h.context,gate=deferred(),entered=deferred();const prepare=c.prepareQueuedTask;let count=0;
 try{const first=c.sendPrompt('primary');await h.entered[0].promise;c.prepareQueuedTask=async(item)=>{if(++count===2){entered.resolve();await gate.promise;}return prepare(item);};
  h.streams.get(0)();await first;await entered.promise;h.$('#stop-run-btn').click();gate.resolve();await nextBrowserTask();assert.equal(h.requests.length,1);assert.equal(c.pauseQueue,true);assert.equal(c.queueDispatching,false);
 }finally{gate.resolve();await h.cleanup();}
});

function completionCalls(h) { return h.calls.filter(call => call[0].startsWith('/api/chat/completion?id=')); }
function assertNoConnectionError(h) {
  assert.equal(h.events.some(event => String(event).includes(h.context.t('chat.connError'))), false);
  assert.equal(h.events.some(event => String(event).includes('undefined')), false);
}

test('Stop {ok:true} aborts active native SSE with AbortError and waits for its exact turn receipt', async () => {
  const completion = deferred(), completionRequested = deferred();
  const h = harness({nativeSSE: true, completion, completionRequested});
  const first = h.context.sendPrompt('active SSE');
  await h.entered[0].promise; await nextBrowserTask();
  const request = h.requests[0], turnID = JSON.parse(request.body).turn_id;
  h.$('#stop-run-btn').events.click();
  await completionRequested.promise;
  assert.equal(request.signal.aborted, true);
  assert.equal(request.signal.reason.name, 'AbortError');
  assert.ok(request.signal.reason.message);
  const stops = h.calls.filter(call => call[0] === '/api/sync/stop');
  assert.equal(stops.length, 1); assert.equal(stops[0][1].turn_id, turnID);
  assert.deepEqual(completionCalls(h).map(call => call[0]), ['/api/chat/completion?id=' + encodeURIComponent(turnID)]);
  assertNoConnectionError(h);
  assert.equal(h.context.streaming, true, 'receipt still owns the serialization fence');
  completion.resolve({session_id: 's1', accepted: true}); await first;
  assert.equal(h.context.streaming, false); assert.equal(h.requests.length, 1);
  assertNoConnectionError(h);
});

test('Stop before response acceptance waits for completion before sending the next prompt once', async () => {
  const responseGate = deferred(), completion = deferred(), completionRequested = deferred();
  const h = harness({nativeSSE: true, responseGate, completion, completionRequested});
  const first = h.context.sendPrompt('awaiting response');
  await h.arrivals[0].promise;
  h.$('#stop-run-btn').events.click();
  assert.equal(h.requests[0].signal.aborted, false, 'server must accept before a Stop can own the turn');
  assert.equal(h.calls.filter(call => call[0] === '/api/sync/stop').length, 0);
  h.$('#prompt').value = 'after receipt';
  await h.$('#composer').events.submit({preventDefault() {}});
  responseGate.resolve(); await completionRequested.promise;
  assert.equal(h.requests.length, 1); assert.equal(h.requests[0].signal.reason.name, 'AbortError');
  const turnID = JSON.parse(h.requests[0].body).turn_id;
  assert.deepEqual(completionCalls(h).map(call => call[0]), ['/api/chat/completion?id=' + encodeURIComponent(turnID)]);
  completion.resolve({session_id: 's1', accepted: true}); await first;
  await h.entered[1].promise; await nextBrowserTask();
  assert.equal(JSON.parse(h.requests[1].body).prompt, 'after receipt');
  h.streams.get(1)(); await h.sends[1];
  assert.equal(h.requests.length, 2); assert.equal(completionCalls(h).length, 1);
  assertNoConnectionError(h);
});

test('interrupt-send with Stop {ok:true} waits for completion and dispatches only the first next prompt', async () => {
  const completion = deferred(), completionRequested = deferred();
  const h = harness({nativeSSE: true, completion, completionRequested});
  const first = h.context.sendPrompt('working');
  await h.entered[0].promise; await nextBrowserTask();
  h.context.interruptAndSend('next exactly once');
  h.context.interruptAndSend('duplicate click');
  await completionRequested.promise;
  assert.equal(h.requests.length, 1); assert.equal(h.requests[0].signal.reason.name, 'AbortError');
  const turnID = JSON.parse(h.requests[0].body).turn_id;
  assert.deepEqual(completionCalls(h).map(call => call[0]), ['/api/chat/completion?id=' + encodeURIComponent(turnID)]);
  completion.resolve({session_id: 's1', accepted: true}); await first;
  await h.entered[1].promise; await nextBrowserTask();
  assert.equal(JSON.parse(h.requests[1].body).prompt, 'next exactly once');
  h.streams.get(1)(); await h.sends[1];
  assert.equal(h.requests.length, 2);
  assert.equal(h.calls.filter(call => call[0] === '/api/sync/stop').length, 1);
  assertNoConnectionError(h);
});

for (const [label, failure, message] of [
  ['Error', new Error('real wire error'), 'real wire error'],
  ['string', 'wire rejection', 'wire rejection'],
  ['null', null, ''], ['undefined', undefined, ''], ['object', {}, ''],
]) {
  test('native SSE ' + label + ' rejection remains connError without undefined or a false Stop receipt', async () => {
    const h = harness({nativeSSE: true});
    const first = h.context.sendPrompt('network failure');
    await h.entered[0].promise; await nextBrowserTask();
    h.failures.get(0)(failure); await first;
    assert.equal(h.requests[0].signal.aborted, false);
    const connectionErrors = h.events.filter(event => String(event).includes(h.context.t('chat.connError')));
    assert.deepEqual(connectionErrors, [h.context.t('chat.connError') + (message ? ': ' + message : '')]);
    assert.equal(h.events.some(event => String(event).includes('undefined')), false);
    assert.equal(h.events.includes(h.context.t('chat.stopped')), false);
    assert.equal(completionCalls(h).length, 0);
  });
}

test('failed sync Stop does not mask a subsequent real native SSE error as successful cancellation', async () => {
  const h = harness({nativeSSE: true, stopFails: new Error('sync Stop denied')});
  const first = h.context.sendPrompt('working');
  await h.entered[0].promise; await nextBrowserTask();
  h.$('#stop-run-btn').events.click(); await nextBrowserTask();
  assert.equal(h.requests[0].signal.aborted, false);
  h.failures.get(0)(new Error('transport failed after rejected Stop')); await first;
  assert.ok(h.events.includes('sync Stop denied'));
  assert.ok(h.events.includes(h.context.t('chat.connError') + ': transport failed after rejected Stop'));
  assert.equal(h.events.includes(h.context.t('chat.stopped')), false);
  assert.equal(completionCalls(h).length, 0);
  assert.equal(h.events.some(event => String(event).includes('undefined')), false);
});

test('delayed Stop acknowledgement for detached A cannot abort or overwrite foreground B', async () => {
  const stopResponse = deferred(), stopRequested = deferred();
  const h = harness({nativeSSE: true, stopResponse, stopRequested, sessionIDs: ['s1', 's2']});
  const c = h.context, first = c.sendPrompt('background A');
  await h.entered[0].promise; await nextBrowserTask();
  const runA = c.activeRun;
  assert.equal(c.detachActiveRun(), true);
  c.activeSessionID = 's2'; c.transcriptSessionID = 's2';
  const second = c.sendPrompt('foreground B');
  await h.entered[1].promise; await nextBrowserTask();
  const runB = c.activeRun, statusB = h.$('#run-status').textContent;
  assert.notEqual(runA, runB); assert.notEqual(h.requests[0].signal, h.requests[1].signal);
  runA.abortCtl.abort(); await stopRequested.promise;
  assert.equal(h.calls.find(call => call[0] === '/api/sync/stop')[1].turn_id, JSON.parse(h.requests[0].body).turn_id);
  stopResponse.resolve({ok: true}); await first;
  assert.equal(h.requests[0].signal.reason.name, 'AbortError');
  assert.equal(h.requests[1].signal.aborted, false);
  assert.equal(c.activeRun, runB); assert.equal(c.abortCtl, runB.abortCtl);
  assert.equal(c.activeSessionID, 's2'); assert.equal(c.streaming, true);
  assert.equal(h.$('#run-status').textContent, statusB);
  assert.equal(h.$('#stop-run-btn').hidden, false);
  assert.equal(c.failedRuns.s1, undefined); assert.equal(c.detachedRuns.s1, undefined);
  assert.equal(completionCalls(h).length, 0); assertNoConnectionError(h);
  h.streams.get(1)(); await second;
  assert.equal(h.requests.length, 2);
});

test('queue refresh during a server run does not pause; idle startup still requires Resume', async () => {
  for (const running of [false, true]) {
    const h = harness(), c = h.context;
    c.j = async url => {
      assert.equal(url, '/api/tasks?state=1');
      return {tasks: [{id: 'q', prompt: 'next'}], running};
    };
    await c.loadPromptQueue();
    assert.equal(c.pauseQueue, !running);
    assert.equal(c.promptQueue[0].text, 'next');
    c.pauseQueue = false;
    await c.loadPromptQueue(); // opening Tasks, now idle, must not add a pause
    assert.equal(c.pauseQueue, false);
    c.pauseQueue = true; // explicit Stop/failure survives list refresh
    await c.loadPromptQueue();
    assert.equal(c.pauseQueue, true);
  }
});

test('queue refresh ignores responses from a previous project', async () => {
  const h = harness(), c = h.context, response = deferred();
  c.j = () => response.promise;
  const pending = c.loadPromptQueue();
  c.projectEpoch++;
  response.resolve({tasks: [{id: 'old', prompt: 'old'}], running: false});
  await pending;
  assert.equal(c.promptQueue.length, 0);
  assert.equal(c.pauseQueue, false);
});

test('two clients racing a queued ID start one run; loser follows sync without error or pause', async () => {
  const clients = [harness(), harness()];
  let owner = null, requests = 0, joined = 0;
  for (const h of clients) {
    const c = h.context, transport = c.superCliUI.chatResponse;
    c.syncConnect = () => { joined++; };
    c.j = async () => ({tasks: [], running: true});
    c.superCliUI.chatResponse = async (url, opts) => {
      const body = JSON.parse(opts.body);
      assert.equal(body.queue_task_id, 'shared');
      requests++;
      if (owner) return {ok: true, status: 200, body: {claimed: true}};
      owner = h;
      return transport(url, opts);
    };
    c.promptQueue = [{id: 'shared', text: 'next', session_id: 's1'}];
  }
  await Promise.all(clients.map(h => h.context.dispatchQueuedTask(h.context.promptQueue[0])));
  await owner.entered[0].promise;
  const loser = clients.find(h => h !== owner);
  await loser.sends[0];
  assert.equal(requests, 2);
  assert.equal(owner.requests.length, 1);
  assert.equal(loser.requests.length, 0);
  assert.equal(joined, 1);
  assert.equal(loser.context.pauseQueue, false);
  assert.equal(loser.context.queueDispatching, false);
  assert.equal(loser.context.promptQueue.length, 0);
  assert.equal(loser.events.length, 0);
  owner.deletion.resolve();
  owner.streams.get(0)();
  await owner.sends[0];
});

test('Stop from another device pauses even the sender HTTP queue before EOF', async () => {
  const h = harness({remoteStop: true}), c = h.context;
  c.sendPrompt('running', [], null);
  await h.entered[0].promise;
  c.promptQueue = [{id: 'q', text: 'must wait', session_id: 's1'}];
  h.streams.get(0)();
  await h.sends[0];
  assert.equal(c.pauseQueue, true);
  assert.equal(h.requests.length, 1);
  assert.equal(c.promptQueue.length, 1);
});

test('a project switch detaches an accepted run without stopping it', async () => {
  const h = harness();
  h.context.sendPrompt('first', [], null);
  await h.entered[0].promise;
  assert.equal(h.context.canDetachActiveRun(), true);
  assert.equal(h.context.detachActiveRun(), true);
  assert.equal(h.context.streaming, false);
  assert.equal(h.requests[0].signal.aborted, false, 'detaching must not cancel the server run');
  h.streams.get(0)();
  await h.sends[0];
  assert.equal(h.context.streaming, false);
  assert.equal(h.calls.some(c => String(c[0]).startsWith('/api/chat/completion')), false);
  assert.equal(h.events.includes(h.context.t('chat.stopped')), false);
});
test('a detached run is shown again when its conversation is reopened', async () => {
  const h = harness({userSeq: true});
  h.context.sendPrompt('first', [], null);
  await h.entered[0].promise;
  assert.equal(h.context.detachActiveRun(), true);
  assert.ok(h.context.detachedRunFor('s1'), 'background run is not offered for reopening');
  assert.equal(h.context.reattachDetachedRun('s1'), true);
  assert.equal(h.context.streaming, true);
  assert.equal(h.requests[0].signal.aborted, false, 'reopening must not restart or cancel the run');
  assert.ok(!h.context.detachedRunFor('s1'));
  h.streams.get(0)();
  await h.sends[0];
  assert.equal(h.context.streaming, false);
  assert.equal(h.events.includes('completed-notification'), true, 'the reopened view did not receive the end');
});
test('a background run that fails keeps its error for the conversation', async () => {
  const h = harness({userSeq: true, failRun: true});
  h.context.sendPrompt('first', [], null);
  await h.entered[0].promise;
  assert.equal(h.context.detachActiveRun(), true);
  h.context.activeSessionID = 's2';
  h.streams.get(0)();
  await h.sends[0];
  assert.equal(h.context.failedRuns.s1.err, 'provider overloaded', 'background failure was lost');
  assert.ok(h.events.includes(h.context.t('common.error') + ': provider overloaded'), 'background failure was not announced');
});
test('a background run that completes leaves no error', async () => {
  const h = harness({userSeq: true});
  h.context.sendPrompt('first', [], null);
  await h.entered[0].promise;
  assert.equal(h.context.detachActiveRun(), true);
  h.streams.get(0)();
  await h.sends[0];
  assert.equal(h.context.failedRuns.s1, undefined);
});
test('a run without a known first message stays in the background', async () => {
  const h = harness();
  h.context.sendPrompt('first', [], null);
  await h.entered[0].promise;
  assert.equal(h.context.detachActiveRun(), true);
  assert.ok(!h.context.detachedRunFor('s1'));
  assert.equal(h.context.reattachDetachedRun('s1'), false);
  h.streams.get(0)();
  await h.sends[0];
});
test('a run is not detached before the server accepted its prompt', async () => {
  const h = harness({acceptMessage: false});
  h.context.sendPrompt('first', [], null);
  await h.entered[0].promise;
  assert.equal(h.context.canDetachActiveRun(), false);
  assert.equal(h.context.detachActiveRun(), false);
  assert.equal(h.context.streaming, true);
  h.streams.get(0)();
  await h.sends[0];
});
test('same-session queue keeps the current runtime and waits for a pending model change', async () => {
  const h = harness();
  const ready = deferred();
  h.context.sessionRuntimeReady = ready.promise;
  const item = {id: 'q1', text: 'next', session_id: 's1'};
  h.context.promptQueue = [item];
  await h.context.runQueuedTask(item, true);
  assert.equal(h.requests.length, 0);
  assert.equal(h.context.queueDispatching, true);
  assert.equal(h.calls.some(c => c[0] === 'restore-runtime'), false);
  ready.resolve();
  await h.entered[0].promise;
  assert.equal(h.requests.length, 1);
  h.deletion.resolve();
  h.context.pauseQueue = true;
  h.streams.get(0)();
  await h.sends[0];
  assert.equal(h.context.promptQueue.length, 0);
});
test('forced send stays busy, highlights the chosen row and dispatches it once before sidebar refresh', async () => {
  const h = harness();
  const earlier = {id: 'q1', text: 'earlier', session_id: 's1'};
  const chosen = {id: 'q2', text: 'urgent', session_id: 's1'};
  h.context.promptQueue = [earlier, chosen];
  const first = h.context.sendPrompt('working');
  await h.entered[0].promise;
  await h.context.runQueuedTask(chosen, true);
  assert.equal(h.$('#status-dot').classList.contains('busy'), true);
  assert.equal(h.$('#run-status').textContent, h.context.t('composer.sendingQueued'));
  assert.equal(h.context.isQueuedTaskTransition(chosen), true);
  const row = element('div'), button = element('button');
  h.context.styleQueuedTaskTransition(row, chosen, button);
  assert.equal(row.attributes['aria-busy'], 'true');
  assert.equal(row.classList.contains('queue-transition'), true);
  assert.equal(button.disabled, true);
  await h.context.runQueuedTask(chosen, true); // duplicate click
  await first;
  await h.entered[1].promise;
  assert.equal(h.requests.length, 2);
  assert.equal(JSON.parse(h.requests[1].body).prompt, 'urgent');
  assert.equal(h.$('#status-dot').classList.contains('busy'), true);
  assert.equal(h.calls.some(c => c[0] === 'restore-runtime'), false);
  assert.equal(h.context.promptQueue.length, 2, 'queue entry is retained until acceptance/removal succeeds');
  h.deletion.resolve();
  h.context.pauseQueue = true;
  h.streams.get(1)();
  await h.sends[1];
  assert.deepEqual(Array.from(h.context.promptQueue, q => q.id), ['q1']);
});
test('failed session switch preserves the queued message and releases the composer', async () => {
  const h = harness({resume: false});
  const item = {id: 'q1', text: 'other project', session_id: 's2'};
  h.context.promptQueue = [item];
  await h.context.runQueuedTask(item, true);
  assert.equal(h.requests.length, 0);
  assert.equal(h.context.promptQueue[0].id, item.id);
  assert.equal(h.context.queueDispatching, false);
  assert.equal(h.context.pauseQueue, true);
  assert.equal(h.$('#status-dot').classList.contains('busy'), false);
  assert.equal(h.calls[0][0], 'resume');
  assert.equal(h.calls[0][3], true);
});
test('rejected chat and failed queue deletion never silently discard or repeat a queued message', async () => {
  for (const opts of [{accepted: false}, {deleteFails: true}]) {
    const h = harness(opts);
    const item = {id: 'q1', text: 'keep this', session_id: 's1'};
    h.context.promptQueue = [item];
    await h.context.runQueuedTask(item, true);
    await h.arrivals[0].promise;
    if (opts.deleteFails) {
      await h.entered[0].promise;
      h.streams.get(0)();
    }
    await h.sends[0];
    assert.equal(h.context.promptQueue.length, 1);
    assert.equal(h.requests.length, 1);
    assert.equal(h.context.pauseQueue, true);
    assert.equal(h.context.queueDispatching, false);
  }
});
test('Stop after a forced send cancels the pending handoff and keeps the queue intact', async () => {
  const h = harness();
  const item = {id: 'q1', text: 'later', session_id: 's1'};
  h.context.promptQueue = [item];
  const first = h.context.sendPrompt('working');
  await h.entered[0].promise;
  await h.context.runQueuedTask(item, true);
  h.$('#stop-run-btn').events.click();
  await first;
  assert.equal(h.requests.length, 1);
  assert.equal(h.context.promptQueue.length, 1);
  assert.equal(h.context.pendingImmediate, null);
  assert.equal(h.$('#status-dot').classList.contains('busy'), false);
});

test('Stop while a queued message waits for runtime readiness leaves it queued', async () => {
  const h = harness();
  const ready = deferred();
  h.context.sessionRuntimeReady = ready.promise;
  const item = {id: 'q1', text: 'keep waiting', session_id: 's1'};
  h.context.promptQueue = [item];
  await h.context.runQueuedTask(item, true);
  h.$('#stop-run-btn').events.click();
  ready.resolve();
  await h.sends[0];
  assert.equal(h.requests.length, 0);
  assert.equal(h.context.promptQueue.length, 1);
  assert.equal(h.context.queueDispatching, false);
  assert.equal(h.$('#status-dot').classList.contains('busy'), false);
});
test('automatic queue order stays intact and a completed last item returns to ready', async () => {
  const h = harness();
  const item = {id: 'q1', text: 'next', session_id: 's1'};
  h.context.promptQueue = [item];
  const first = h.context.sendPrompt('working');
  await h.entered[0].promise;
  h.streams.get(0)();
  await first;
  await h.entered[1].promise;
  assert.equal(JSON.parse(h.requests[1].body).prompt, 'next');
  // Complete before DELETE returns: the cleanup must still await that
  // acknowledgement and must not start the same item for a second time.
  h.streams.get(1)();
  h.deletion.resolve();
  await h.sends[1];
  assert.equal(h.requests.length, 2);
  assert.equal(h.context.promptQueue.length, 0);
  assert.equal(h.$('#status-dot').classList.contains('busy'), false);
  assert.equal(h.$('#run-status').textContent, h.context.t('composer.ready'));
});

test('early Stop waits once for durable completion and sends the next prompt in the recovered session', async () => {
  const completion = deferred(), completionRequested = deferred();
  const h = harness({completion, completionRequested, hideSession: true});
  h.context.activeSessionID = '';
  h.context.transcriptSessionID = '';
  const item = {id: 'q1', text: 'continue', session_id: ''};
  h.context.promptQueue = [item];
  const first = h.context.sendPrompt('fresh conversation');
  await h.entered[0].promise;
  await h.context.runQueuedTask(item, true);
  await completionRequested.promise;
  assert.equal(h.requests.length, 1, 'next request started before the previous write finished');
  assert.equal(h.$('#status-dot').classList.contains('busy'), true);
  assert.equal(h.calls.filter(c => c[0].startsWith('/api/chat/completion')).length, 1);
  completion.resolve({session_id: 'recovered-session', accepted: true});
  await first;
  await h.entered[1].promise;
  assert.equal(JSON.parse(h.requests[1].body).session_id, 'recovered-session');
  h.deletion.resolve();
  h.context.pauseQueue = true;
  h.streams.get(1)();
  await h.sends[1];
});

test('an SSE setup error before session_activity keeps the queue entry available', async () => {
  const h = harness({acceptMessage: false});
  const item = {id: 'q1', text: 'retry after provider setup', session_id: 's1'};
  h.context.promptQueue = [item];
  await h.context.runQueuedTask(item, true);
  await h.entered[0].promise;
  h.streams.get(0)();
  await h.sends[0];
  assert.equal(h.context.promptQueue.length, 1);
  assert.equal(h.context.pauseQueue, true);
  assert.equal(h.calls.some(c => c[0].startsWith('/api/tasks?id=')), false);
});


test('terminal frame releases Stop and the clock before a delayed HTTP EOF', async () => {
  const h = harness({separateTerminal: true});
  const first = h.context.sendPrompt('working');
  await h.entered[0].promise;
  const delayedTick = [...h.intervals.values()][0];
  h.terminals.get(0)();
  assert.equal(h.$('#stop-run-btn').hidden, true);
  assert.equal(h.$('#send-btn').textContent, h.context.t('composer.send'));
  assert.equal(h.intervals.size, 0);
  const completed = h.$('#run-status').textContent;
  delayedTick(); // a tick already scheduled before clearInterval
  assert.equal(h.$('#run-status').textContent, completed);
  assert.equal(h.$('#status-dot').classList.contains('busy'), false);
  h.streams.get(0)();
  await first;
});

test('Enter after Stop sends once after the durable receipt, preserving a paused older queue and attachments', async () => {
  const completion = deferred(), completionRequested = deferred();
  const h = harness({completion, completionRequested});
  h.context.clearAttachments = () => { h.context.pendingAttachments = []; };
  const earlier = {id: 'earlier', text: 'paused task', session_id: 's1'};
  h.context.promptQueue = [earlier];
  const first = h.context.sendPrompt('working');
  await h.entered[0].promise;
  h.$('#stop-run-btn').events.click();
  assert.equal(h.$('#stop-run-btn').hidden, true);
  assert.equal(h.$('#status-dot').classList.contains('busy'), false);
  assert.equal(h.intervals.size, 0);
  await completionRequested.promise;
  h.context.pendingAttachments = ['image.png'];
  h.$('#prompt').value = 'new instruction';
  await h.$('#composer').events.submit({preventDefault() {}});
  assert.equal(h.requests.length, 1, 'next turn overlapped unfinished writes');
  assert.equal(h.context.pendingImmediate.text, 'new instruction');
  completion.resolve({session_id: 's1', accepted: true});
  await first;
  await h.entered[1].promise;
  assert.equal(JSON.parse(h.requests[1].body).prompt, 'new instruction');
  assert.deepEqual(JSON.parse(h.requests[1].body).attachments, ['image.png']);
  h.streams.get(1)();
  await h.sends[1];
  assert.equal(h.requests.length, 2);
  assert.equal(h.context.promptQueue[0].id, 'earlier');
  assert.equal(h.context.pauseQueue, true);
});

test('Enter after normal completion is accepted before EOF and starts exactly once after it', async () => {
  const h = harness({separateTerminal: true});
  h.context.clearAttachments = () => { h.context.pendingAttachments = []; };
  const first = h.context.sendPrompt('working');
  await h.entered[0].promise;
  h.terminals.get(0)();
  h.$('#prompt').value = 'continue';
  await h.$('#composer').events.submit({preventDefault() {}});
  assert.equal(h.requests.length, 1);
  h.streams.get(0)();
  await first;
  await h.entered[1].promise;
  assert.equal(JSON.parse(h.requests[1].body).prompt, 'continue');
  h.terminals.get(1)();
  h.streams.get(1)();
  await h.sends[1];
  assert.equal(h.requests.length, 2);
});


test('Stop cancels a manual next prompt waiting for model readiness and restores its draft and attachment', async () => {
 const h=harness({separateTerminal:true});
 const ready=deferred();
 const first=h.context.sendPrompt('working');await h.entered[0].promise;
 h.terminals.get(0)();
 h.$('#prompt').value='later';h.context.pendingAttachments=['later.png'];
 await h.$('#composer').events.submit({preventDefault(){}});
 h.context.sessionRuntimeReady=ready.promise;
 h.streams.get(0)();await first;
 assert.equal(h.context.queueDispatching,true);
 h.$('#stop-run-btn').events.click();ready.resolve();
 await h.sends[1];
 assert.equal(h.requests.length,1,'Stop must cancel the manual bypass of a paused queue');
 assert.equal(h.$('#prompt').value,'later');
 assert.deepEqual(Array.from(h.context.pendingAttachments),['later.png']);
});

test('Stop during receipt restores the canceled next prompt rather than silently losing it',async()=>{
 const completion=deferred(),completionRequested=deferred();
 const h=harness({completion,completionRequested});
 const first=h.context.sendPrompt('working');await h.entered[0].promise;
 h.$('#stop-run-btn').events.click();await completionRequested.promise;
 h.$('#prompt').value='keep draft';h.context.pendingAttachments=['kept.png'];
 await h.$('#composer').events.submit({preventDefault(){}});
 h.$('#stop-run-btn').events.click();
 assert.equal(h.$('#prompt').value,'keep draft');
 assert.deepEqual(Array.from(h.context.pendingAttachments),['kept.png']);
 completion.resolve({session_id:'s1',accepted:true});await first;
 assert.equal(h.requests.length,1);
});

test('failed durable receipt restores the manual next prompt and does not send it',async()=>{
 const completion=deferred(),completionRequested=deferred();
 const h=harness({completion,completionRequested});
 const first=h.context.sendPrompt('working');await h.entered[0].promise;
 h.$('#stop-run-btn').events.click();await completionRequested.promise;
 h.$('#prompt').value='keep after error';h.context.pendingAttachments=['kept.png'];
 await h.$('#composer').events.submit({preventDefault(){}});
 completion.reject(new Error('receipt failed'));await first;
 assert.equal(h.requests.length,1);
 assert.equal(h.$('#prompt').value,'keep after error');
 assert.deepEqual(Array.from(h.context.pendingAttachments),['kept.png']);
});


test('rejection of the manual next turn restores its unsent attachment as well as text',async()=>{
 for(const opts of [{rejectSecondHTTP:true},{rejectSecondMessage:true}]){
  const h=harness(opts);const first=h.context.sendPrompt('working');await h.entered[0].promise;
  h.$('#stop-run-btn').events.click();
  h.$('#prompt').value='keep rejected';h.context.pendingAttachments=['kept.png'];
  await h.$('#composer').events.submit({preventDefault(){}});
  await first;
  if(opts.rejectSecondMessage){await h.entered[1].promise;h.streams.get(1)();}
  await h.sends[1];
  assert.equal(h.requests.length,2);
  assert.equal(h.$('#prompt').value,'keep rejected');
  assert.deepEqual(Array.from(h.context.pendingAttachments),['kept.png']);
 }
});


test('a failed manual next prompt restores its draft after an early Stop recovered a new session ID',async()=>{
 const completion=deferred(),completionRequested=deferred();
 const h=harness({completion,completionRequested,hideSession:true,dynamicScope:true,rejectSecondHTTP:true});
 h.context.activeSessionID='';h.context.transcriptSessionID='';
 const first=h.context.sendPrompt('new conversation');await h.entered[0].promise;
 h.$('#stop-run-btn').events.click();await completionRequested.promise;
 h.$('#prompt').value='keep after recovered ID';h.context.pendingAttachments=['kept.png'];
 await h.$('#composer').events.submit({preventDefault(){}});
 completion.resolve({session_id:'s1',accepted:true});await first;await h.sends[1];
 assert.equal(h.requests.length,2);
 assert.equal(h.$('#prompt').value,'keep after recovered ID');
 assert.deepEqual(Array.from(h.context.pendingAttachments),['kept.png']);
});


test('generation completion releases GUI while checkpoint writes still hold the terminal/EOF fence',async()=>{
 const h=harness({separateTerminal:true});
 const first=h.context.sendPrompt('working');await h.entered[0].promise;
 h.finishing.get(0)();
 assert.equal(h.$('#stop-run-btn').hidden,true);
 assert.equal(h.intervals.size,0);
 assert.equal(h.$('#status-dot').classList.contains('busy'),false);
 h.$('#prompt').value='next during save';
 await h.$('#composer').events.submit({preventDefault(){}});
 assert.equal(h.requests.length,1);
 h.terminals.get(0)();h.streams.get(0)();await first;await h.entered[1].promise;
 assert.equal(JSON.parse(h.requests[1].body).prompt,'next during save');
 h.finishing.get(1)();h.terminals.get(1)();h.streams.get(1)();await h.sends[1];
 assert.equal(h.requests.length,2);
});

test('submit with an attachment while working enters the durable queue and dispatches with it', async () => {
  const h = harness();
  const shot = '/work/.supercli/attachments/u1/shot.png';
  const posted = [];
  h.context.jpost = async (url, body) => {
    if (url === '/api/tasks') { posted.push(body); return {id: 'q9', prompt: body.prompt, session_id: body.session_id, attachments: body.attachments}; }
    assert.equal(url, '/api/sync/stop'); return {ok: true};
  };
  const first = h.context.sendPrompt('working');
  await h.entered[0].promise;
  h.context.pendingAttachments = [shot];
  h.$('#prompt').value = 'look at this';
  await h.$('#composer').events.submit({preventDefault() {}});
  assert.equal(h.events.includes(h.context.t('composer.queued')), false);
  assert.equal(posted.length, 1);
  assert.deepEqual(Array.from(posted[0].attachments), [shot]);
  assert.equal(posted[0].prompt, 'look at this');
  assert.equal(h.requests.length, 1, 'queued message must not interrupt the running turn');
  assert.equal(h.context.promptQueue.length, 1);
  assert.deepEqual(Array.from(h.context.promptQueue[0].attachments), [shot]);
  assert.equal(h.context.pendingAttachments.length, 0, 'composer selection moves into the queue');
  const row = h.$('#prompt-queue').children.find(c => c.children && c.children.some(x => x.classList && x.classList.contains('queue-text')));
  const text = row.children.find(x => x.classList.contains('queue-text'));
  const badge = text.children.find(x => x.classList.contains('queue-attach-count'));
  assert.ok(badge, 'queue row shows the attachment count');
  assert.match(badge.textContent, /1$/);
  h.streams.get(0)();
  await first;
  await h.entered[1].promise;
  const sent = JSON.parse(h.requests[1].body);
  assert.equal(sent.prompt, 'look at this');
  assert.deepEqual(sent.attachments, [shot]);
  h.deletion.resolve();
  h.context.pauseQueue = true;
  h.streams.get(1)();
  await h.sends[1];
  const removal = h.calls.find(c => String(c[0]).startsWith('/api/tasks?id=q9'));
  assert.ok(removal && removal[0].includes('keep_attachments=1'), 'dispatch removal keeps files owned by the started run');
  assert.equal(h.context.promptQueue.length, 0);
});
