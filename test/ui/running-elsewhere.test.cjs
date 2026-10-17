const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const assets = path.resolve(__dirname, '../../internal/webgui/assets');
const english = JSON.parse(fs.readFileSync(path.join(assets, 'locales/en.json'), 'utf8'));
function element(tag, className = '', textContent = '') {
  return {tag, className, textContent, children: [], hidden: false, listeners: {}, attributes: {},
    appendChild(child) { this.children.push(child); return child; },
    addEventListener(name, fn) { this.listeners[name] = fn; },
    setAttribute(name, value) { this.attributes[name] = value; },
    set innerHTML(value) { this.children = []; }};
}
function harness() {
  const nodes = new Map(), calls = [], timers = new Map(); let timerID = 0;
  const $ = id => { if (!nodes.has(id)) nodes.set(id, element('div')); return nodes.get(id); };
  const c = {console, Date, Promise, Object, String, Math, setTimeout() {}, clearTimeout() {},
    window: {addEventListener() {}}, document: {addEventListener() {}, createTextNode: text => element('text', '', text)},
    $ , el: element, i18nEl: (tag, cls, key) => element(tag, cls, english[key]), t: key => english[key] || key,
    setInterval(fn) { const id = ++timerID; timers.set(id, fn); return id; }, clearInterval(id) {timers.delete(id);},
    newChatTurnID: () => 'this-client', activeSessionID: '', activeRun: null, detachedRuns: {},
    streaming: false, projectEpoch: 0, syncListPending: true,
    syncSettleTranscriptScroll() {}, loadSessions() {}, loadProjects() {},
    resumeSession: async id => {calls.push(['resume', id]); return true;},
    checkHealth: async () => {calls.push(['health']);}, newSession() {calls.push(['new']);}, loadPromptQueue() {},
    toast: message => calls.push(['toast',message]),
    jpost: async (url, body) => {calls.push([url,body]); return {};},
    j: async () => ({projects:[{path:'/b',name:'Project B',cwd:true}]})
  };
  vm.createContext(c);
  vm.runInContext(fs.readFileSync(path.join(assets,'js/05b-sync.js'),'utf8'),c);
  vm.runInContext(fs.readFileSync(path.join(assets,'js/09-projects.js'),'utf8'),c);
  c.syncLoadSessions = () => {}; c.syncRefreshView = () => {}; c.syncRecoverSession = () => {}; c.syncSettleTranscriptScroll = () => {};
  c.runningElsewhereProjectPath = '/b';
  return {c,$,calls,timers};
}
function info(id, project, overrides = {}) {
  return {session_id:id,turn_id:'turn-'+id,client_id:'other-client',project_path:project,
    project_name:'Project '+project.slice(1),title:'Conversation '+id,state:'running',
    started_at_ms:Date.now()-125000,...overrides};
}

test('running elsewhere shows live summaries only for other projects, with server elapsed time', () => {
  const {c,$,timers} = harness(); const serverNow = Date.now()+60000;
  const a = info('a','/a',{started_at_ms:serverNow-125000});
  c.syncHandleFrame({type:'snapshot',server_now_ms:serverNow,runs:[a,info('b','/b')]});
  assert.equal($('#running-elsewhere').hidden,false);
  let rows = $('#running-elsewhere-list').children;
  assert.equal(rows.length,1); assert.equal(rows[0].children[0].textContent,'Conversation a');
  assert.equal(rows[0].children[1].textContent,'Project a');
  assert.match(rows[0].children[2].textContent,/Working · 2:05/);
  assert.match(rows[0].attributes['aria-label'],/Open running conversation/);
  assert.equal(timers.size,1);
  c.syncHandleFrame({...a,type:'run_updated',state:'waiting_answer'});
  assert.match($('#running-elsewhere-list').children[0].children[2].textContent,/Waiting for an answer/);
  c.syncHandleFrame({...info('c','/c'),type:'run_started'});
  assert.equal($('#running-elsewhere-list').children.length,2);
  c.syncHandleFrame({...a,type:'run_finished'});
  assert.equal($('#running-elsewhere-list').children.length,1);
  c.syncHandleFrame({type:'snapshot',runs:[info('b','/b')]});
  assert.equal($('#running-elsewhere').hidden,true); assert.equal(timers.size,0);
});

test('own client runs are listed and reconnect preserves waiting state and original timestamp', () => {
  const {c,$} = harness(); const a = info('own','/a',{client_id:'this-client',state:'waiting_answer'});
  c.syncHandleFrame({type:'snapshot',runs:[a]});
  assert.equal(c.syncRuns.own.remote,undefined);
  assert.match($('#running-elsewhere-list').children[0].children[2].textContent,/Waiting for an answer/);
  c.syncHandleFrame({type:'snapshot',runs:[a]});
  assert.equal(c.syncRuns.own.started_at_ms,a.started_at_ms);
  assert.equal($('#running-elsewhere-list').children.length,1);
});

test('opening a run switches project before resuming and does not attach on failed switch', async () => {
  const {c,calls} = harness(); c.syncRuns.a = c.syncMakeRun(info('a','/a'));
  c.projectAction = async (action,target) => {calls.push([action,target]); c.runningElsewhereProjectPath=target; return true;};
  await c.openRunningElsewhere('a');
  assert.deepEqual(calls,[['use','/a'],['resume','a']]);
  calls.length=0; c.runningElsewhereProjectPath='/b'; c.projectAction=async()=>false;
  await c.openRunningElsewhere('a'); assert.deepEqual(calls,[]);
});

test('project switch return value signals success without changing existing detach path', async () => {
  const {c,calls} = harness();
  assert.equal(await c.projectAction('use','/a'),true);
  assert.equal(calls[0][0],'/api/projects');
  assert.equal(calls[0][1].target,'/a');
  assert.ok(calls.some(call=>call[0]==='new'));
});

test('phone summaries use ellipsis, full accessible titles, and touch-sized rows', () => {
  const css = fs.readFileSync(path.join(assets,'phone.css'),'utf8');
  assert.match(css,/\.running-elsewhere-item[^}]*min-width:0[^}]*min-height:44px/);
  assert.match(css,/\.running-elsewhere-item > span[^}]*text-overflow:ellipsis[^}]*white-space:nowrap/);
});
