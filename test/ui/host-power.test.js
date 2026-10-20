import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript, parseHTML} from './harness.js';

const assets = sourcePath(testDir, '../../internal/webgui/assets');
const source = readSource(sourcePath(assets, 'js/05c-host-power.js'), 'utf8');
const english = JSON.parse(readSource(sourcePath(assets, 'locales/en.json'), 'utf8'));
const flush = async () => { for (let i=0; i<12; i++) await Promise.resolve(); };
const snapshot = (status, extra={}) => ({status, job_id:'job', goal_id:'goal', goal:'Build /a/very/long/path', deadline:0, ...extra});

function harness(initial=snapshot('idle')) {
  const {document} = parseHTML('<!doctype html><html><body><div id="chat-stream"></div></body></html>');
  Object.defineProperty(document,'hidden',{configurable:true,value:false});
  const listeners={}, timeouts=new Map(), intervals=new Map(), requests=[], queue=[];
  let nextID=0, now=100000;
  const c = createSandbox({document, Date:{now:()=>now}, Number, Object, JSON, Promise,
    el(tag, cls, text) {const n=document.createElement(tag);n.className=cls||'';if(text!=null)n.textContent=text;return n;},
    t:key=>english[key]||key,
    registerLanguageRefresh(node,fn){node._languageRefresh=fn;node.setAttribute('data-i18n-refresh','');},
    setTimeout(fn, ms) {const id=++nextID;timeouts.set(id,{fn,ms});return id;},
    clearTimeout:id=>timeouts.delete(id),
    setInterval(fn, ms) {const id=++nextID;intervals.set(id,{fn,ms});return id;},
    clearInterval:id=>intervals.delete(id),
    addEventListener(name, fn) {listeners[name]=fn;},
    fetch(url, opts) {
      requests.push({url,opts});
      const item=queue.length ? queue.shift() : {ok:true,json:async()=>initial};
      if (item instanceof Error) return Promise.reject(item);
      return Promise.resolve(item);
    }
  });
  runScript(source,c);
  return {c,document,listeners,timeouts,intervals,requests,queue,now:value=>{now=value;},
    reply(value,ok=true){queue.push({ok,status:ok?200:503,json:async()=>value});},
    state:()=>c.hostPowerState,
    banner:()=>document.querySelector('#host-power'),
    message:()=>document.querySelector('.host-power-message').textContent,
    warning:()=>document.querySelector('.host-power-warning').textContent,
    cancel:()=>document.querySelector('.host-power-cancel')};
}

test('host power initial GET recovers shared countdown using the actual deadline, not a fresh 60 seconds', async()=>{
  const h=harness(snapshot('countdown',{deadline:112400}));await flush();
  assert.equal(h.requests[0].url,'/api/host-power');
  assert.match(h.message(),/13s/);assert.equal(h.banner().hidden,false);
  h.now(105001);h.c.renderHostPower();assert.match(h.message(),/8s/);
  h.now(200000);h.c.renderHostPower();assert.match(h.message(),/0s/);
  assert.equal(h.state().status,'countdown');assert.equal(h.cancel().hidden,false);
  assert.equal(h.requests.length,1,'render clock must never execute, arm or cancel');
});

test('host power countdown honors existing server clock synchronization on a skewed phone',async()=>{
 const h=harness(snapshot('countdown',{deadline:145000}));await flush();
 h.c.syncClockOffsetMS=40000;h.c.renderHostPower();assert.match(h.message(),/5s/);
});

test('host power never polls idle or terminal states and only ticks the countdown',async()=>{
 const h=harness();await flush();assert.equal(h.timeouts.size,0);assert.equal(h.intervals.size,0);assert.equal(h.banner().hidden,true);
 for(const state of ['cancelled','blocked','failed','executed']) {
  h.c.applyHostPower(snapshot(state));assert.equal(h.timeouts.size,0,state);assert.equal(h.intervals.size,0,state);
 }
 h.c.applyHostPower(snapshot('armed'));assert.equal(h.timeouts.size,1);assert.equal(h.intervals.size,0);
 h.c.applyHostPower(snapshot('countdown',{deadline:110000}));assert.equal(h.timeouts.size,1);assert.equal(h.intervals.size,1);
});

test('host power keeps polling committed execution until the authoritative terminal result',async()=>{
 for(const terminal of ['executed','failed']) {
  for(const entry of ['get','cancel']) {
   const h=harness(snapshot(entry==='get'?'executing':'countdown',{deadline:135000}));await flush();
   if(entry==='cancel') {
    h.reply(snapshot('executing'));await h.c.cancelHostPower();await flush();
    assert.match(h.warning(),/not confirmed/);
   }
   assert.equal(h.state().status,'executing');assert.equal(h.cancel().hidden,true);
   assert.equal(h.intervals.size,0);
   const poll=Array.from(h.timeouts.entries()).find(([,timer])=>timer.ms===2000);
   assert.ok(poll,'committed execution must still be polled');
   h.reply(snapshot(terminal));h.timeouts.delete(poll[0]);await poll[1].fn();await flush();
   assert.equal(h.state().status,terminal);assert.equal(h.message(),english['hostPower.'+terminal]);
   assert.equal(h.timeouts.size,0);assert.equal(h.intervals.size,0);
  }
 }
});

test('host power renders all authoritative remote statuses and dismissal is memory-only',async()=>{
 const h=harness();await flush();
 for(const status of ['armed','countdown','cancelled','blocked','failed','executing','executed']) {
  h.c.applyHostPower(snapshot(status,{deadline:110000}));
  assert.equal(h.banner().hidden,false,status);assert.equal(h.banner().dataset.status,status);
  assert.equal(h.message(),h.c.localizedHostPowerText('hostPower.'+status,{seconds:10}));
 }
 h.document.querySelector('.host-power-dismiss').click();assert.equal(h.banner().hidden,true);
 h.c.applyHostPower(snapshot('armed'));assert.equal(h.banner().hidden,false);
 assert.equal(h.cancel().hidden,false);h.document.querySelector('.host-power-dismiss').click();assert.equal(h.banner().hidden,false);
});

test('host power successful cancel only claims cancellation after an actual successful snapshot',async()=>{
 const h=harness(snapshot('countdown',{deadline:135000}));await flush();
 let finish;h.queue.push(new Promise(resolve=>{finish=resolve;}));
 const pending=h.c.cancelHostPower();assert.equal(h.cancel().disabled,true);assert.equal(h.state().status,'countdown');
 finish({ok:true,json:async()=>snapshot('cancelled')});await pending;await flush();
 assert.equal(h.requests[1].url,'/api/host-power/cancel');assert.equal(h.requests[1].opts.method,'POST');
 assert.equal(h.state().status,'cancelled');assert.equal(h.cancel().hidden,true);assert.equal(h.timeouts.size,0);assert.equal(h.intervals.size,0);
});

test('host power network cancel failure preserves countdown/deadline with prominent offline warning and retry',async()=>{
 const h=harness(snapshot('countdown',{deadline:135000}));await flush();
 h.queue.push(new Error('offline'));await h.c.cancelHostPower();await flush();
 assert.equal(h.state().status,'countdown');assert.equal(h.state().deadline,135000);assert.equal(h.cancel().hidden,false);assert.equal(h.cancel().disabled,false);
 assert.match(h.warning(),/host may still shut down/);assert.match(h.warning(),/not confirmed/);assert.match(h.message(),/35s/);
 h.reply(snapshot('cancelled'));await h.c.cancelHostPower();await flush();assert.equal(h.state().status,'cancelled');assert.equal(h.warning().trim(),'');
});

test('host power HTTP error and malformed cancellation are not optimistic successes',async()=>{
 for(const reply of [{ok:false,status:500,json:async()=>snapshot('cancelled')},{ok:true,json:async()=>({status:'mystery'})},{ok:true,json:async()=>snapshot('countdown')},{ok:true,json:async()=>snapshot('idle')}]) {
  const h=harness(snapshot('countdown',{deadline:135000}));await flush();h.queue.push(reply);
  await h.c.cancelHostPower();await flush();assert.equal(h.state().status,'countdown');assert.equal(h.state().deadline,135000);assert.equal(h.cancel().hidden,false);assert.match(h.warning(),/not confirmed/);
 }
});

test('host power successful HTTP reply reports execution race honestly instead of cancelled',async()=>{
 const h=harness(snapshot('countdown',{deadline:100010}));await flush();h.reply(snapshot('executed'));
 await h.c.cancelHostPower();await flush();assert.equal(h.state().status,'executed');assert.equal(h.message(),english['hostPower.executed']);assert.match(h.warning(),/not confirmed/);
});

test('host power stale GET cannot resurrect plan after cancellation',async()=>{
 const h=harness(snapshot('armed'));await flush();let finish;
 h.queue.push(new Promise(resolve=>{finish=resolve;}));const get=h.c.refreshHostPower();
 h.reply(snapshot('cancelled'));await h.c.cancelHostPower();finish({ok:true,json:async()=>snapshot('countdown',{deadline:140000})});await get;await flush();
 assert.equal(h.state().status,'cancelled');assert.equal(h.cancel().hidden,true);
});

test('host power offline GET preserves last known deadline and online lifecycle recovers remote cancellation',async()=>{
 const h=harness(snapshot('countdown',{deadline:140000}));await flush();h.queue.push(new Error('lost'));
 await h.c.refreshHostPower();assert.equal(h.state().deadline,140000);assert.match(h.warning(),/reconnect to cancel/);
 h.now(120000);h.c.renderHostPower();assert.match(h.message(),/20s/);
 h.reply(snapshot('cancelled'));h.listeners.online();await flush();assert.equal(h.state().status,'cancelled');assert.equal(h.warning(),'');
});

test('host power page visibility stops timers and recovers without new job/session work',async()=>{
 const h=harness(snapshot('countdown',{deadline:140000}));await flush();
 Object.defineProperty(h.document,'hidden',{configurable:true,value:true});h.document.dispatchEvent(new Event('visibilitychange'));
 assert.equal(h.timeouts.size,0);assert.equal(h.intervals.size,0);
 h.reply(snapshot('blocked'));Object.defineProperty(h.document,'hidden',{configurable:true,value:false});h.document.dispatchEvent(new Event('visibilitychange'));await flush();
 assert.equal(h.state().status,'blocked');assert.equal(h.timeouts.size,0);assert.equal(h.intervals.size,0);
});

test('host power long goal/path and localized confirmation remain text, not HTML or recursive templates',async()=>{
 const goal='<img src=x onerror="window.pwned=true">'+('/very-long-path'.repeat(100))+' {seconds} {goal}';
 const h=harness(snapshot('armed',{goal}));await flush();
 assert.equal(h.document.querySelector('.host-power-goal').textContent,goal);assert.equal(h.banner().querySelector('img'),null);
 const text=h.c.localizedHostPowerText('hostPower.confirm',{goal});assert.ok(text.includes(goal));assert.ok(text.includes('{seconds} {goal}'));
 h.c.t=key=>key==='hostPower.armed'?'Translated armed state':english[key]||key;h.banner()._languageRefresh();assert.equal(h.message(),'Translated armed state');
});

test('host power requires a numeric absolute deadline and never invents a countdown',async()=>{
 const h=harness();await flush();
 for(const deadline of [undefined,null,0,-1,'125000',Infinity]) {
  assert.throws(()=>h.c.applyHostPower(snapshot('countdown',{deadline})),/Missing host power deadline/);
  assert.equal(h.state().status,'idle');
 }
 h.c.applyHostPower(snapshot('countdown',{deadline:125000}));assert.match(h.message(),/25s/);
});

test('host power timeout aborts a stalled cancellation and preserves the live deadline',async()=>{
 const h=harness(snapshot('countdown',{deadline:135000}));await flush();
 h.c.fetch=(url,options)=>new Promise((resolve,reject)=>{options.signal.addEventListener('abort',()=>reject(new Error('aborted')));});
 const pending=h.c.cancelHostPower();const timeout=Array.from(h.timeouts.values()).find(x=>x.ms===8000);assert.ok(timeout);timeout.fn();await pending;await flush();
 assert.equal(h.state().status,'countdown');assert.equal(h.state().deadline,135000);assert.equal(h.cancel().disabled,false);assert.match(h.warning(),/not confirmed/);
});

test('host power lifecycle event during stale idle GET queues one follow-up to discover arming',async()=>{
 const h=harness();await flush();let finish;
 h.queue.push(new Promise(resolve=>{finish=resolve;}));const first=h.c.refreshHostPower();
 h.reply(snapshot('armed'));h.c.refreshHostPower();h.c.refreshHostPower();
 finish({ok:true,json:async()=>snapshot('idle')});await first;await flush();
 assert.equal(h.state().status,'armed');assert.equal(h.requests.length,3,'one coalesced follow-up, not a busy loop');
});

test('host power GUI sync refresh listens to existing lifecycle/tool results, never timer-polls idle',()=>{
 const sync=readSource(sourcePath(assets,'js/05b-sync.js'),'utf8');assert.match(sync,/tool_result\|ask_user\|done\|error/);assert.match(sync,/refreshHostPower/);
 const chat=readSource(sourcePath(assets,'js/05-chat.js'),'utf8');assert.match(chat,/q\.confirmation && q\.question_key === "hostPower.confirm"/);assert.match(chat,/questionText\.textContent/);
});
