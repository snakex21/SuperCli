import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript} from './harness.js';

function harness(host = 'test-phone.trycloudflare.com') {
  const listeners = {}, sockets = [], nodes = new Map(), calls = [], rendered = [];
  const element = () => ({hidden: false, disabled: false, textContent: '', classList: {add() {}, remove() {}}, addEventListener() {}, focus() {}});
  const $ = id => { if (!nodes.has(id)) nodes.set(id, element()); return nodes.get(id); };
  class Socket {
    constructor(url) { this.url = url; sockets.push(this); }
    close() { this.closed = true; }
    frame(frame) { this.onmessage({data: 'data: ' + JSON.stringify(frame) + '\n\n'}); }
  }
  let sequence = 0;
  const c = {console, Promise, Date, Object, AbortController,
    document: {hidden: false, addEventListener(name, fn) { listeners[name] = fn; }},
    window: {addEventListener(name, fn) { listeners[name] = fn; }},
    location: {hostname: host, host, protocol: 'https:'}, WebSocket: Socket, EventSource: Socket,
    newChatTurnID: () => 'client-' + ++sequence, activeRun: null, detachedRuns: {}, activeSessionID: 's1',
    streaming: false, runFinishing: false, runStart: 0, runTimer: null, runStatus: element(),
    setInterval: () => 1, clearInterval() {}, fmtDuration: ms => String(ms),
    runToolCount: 0, transcriptLiveAppend: false,
    abortCtl: null, questionOverlays: {}, pendingImmediate: null, pauseQueue: false,
    setTimeout: () => 1, clearTimeout() {}, $, $$: () => [], sendBtn: element(),
    t: x => x, finishRunPresentation() {}, closeQuestionOverlay(id) { calls.push(['closeQuestion', id]); },
    releaseLiveTranscriptBlocks() {}, setRunState() {}, toolRows: {}, openToolOrder: [],
    captureTranscriptScroll() {}, resumeTranscriptScroll() {}, settleTranscriptScroll() {},
    addUserMsg(prompt) { rendered.push(['user', prompt]); return {}; }, bindLiveMessageReceipt() {},
    handleEvent(ev, current) { rendered.push([ev.type, ev.text]); if (ev.type === 'question_closed') c.closeQuestionOverlay(ev.id); return current; },
    jpost: async (url, body) => { calls.push([url, body]); return {ok: true}; }, toast: x => { throw Error(x); },
    superCliUI: {decodeSSEFrame(data) { return JSON.parse(data.replace(/^data: /, '').trim()); }},
    projectEpoch: 0, sessionRuntimeReady: Promise.resolve(), transcriptAbortCtl: null,
    transcriptPageSize: 24, streamAppendTarget: null, composerDraftStore: {restore() {}}, promptEl: element(),
    renderStats() {}, restoreSessionRuntime() {}, transcriptHasMore: false, transcriptBeforeSeq: 0,
    queueDispatching: false, queueDispatchItem: null, promptQueue: [], detachActiveRun: () => false, failedRuns: {},
    j: async url => { calls.push(['transcript', url]); return {messages: [{seq: 1, role: 'user'}, {seq: 3, role: 'user'}, {seq: 4, role: 'assistant'}]}; },
    renderLoadedTranscript() { rendered.length = 0; c.loadedTranscriptMessages.forEach(m => rendered.push(['history', m.seq])); },
    reattachDetachedRun(id) { const run = c.syncRunFor(id); if (!run) return false; run.reattach(); return true; },
  };
  createSandbox(c);
  for (const name of ['05b-sync.js', '08-sessions.js']) runScript(readSource(sourcePath(testDir, '../../internal/webgui/assets/js', name), 'utf8'), c);
  const renderLoadedTranscript = c.renderLoadedTranscript;
  c.renderLoadedTranscript = () => { rendered.length = 0; c.loadedTranscriptMessages.forEach(m => rendered.push(['history', m.seq])); };
  c.loadSessions = async () => { calls.push(['sessions']); };
  c.syncSelectSession('s1');
  return {c, calls, rendered, listeners, sockets, renderLoadedTranscript, frame: frame => sockets.at(-1).frame(frame)};
}
// Exercise the real scroll helpers and saved-transcript renderer. Geometry is
// clamped like a browser's scroll box, including the temporary empty DOM on refresh.
function scrollHarness() {
  const h = harness(), c = h.c, events = {}, frames = new Map();
  let nextFrame = 0, height = 2000, top = 1600;
  const stage = {clientHeight: 400, addEventListener(name, fn) { events[name] = fn; }};
  Object.defineProperties(stage, {
    scrollTop: {get: () => top, set(value) { top = Math.max(0, Math.min(value, height - stage.clientHeight)); }},
    scrollHeight: {get: () => height, set(value) { height = value; stage.scrollTop = top; }}
  });
  const stream = {style: {}, addEventListener() {}, querySelectorAll: () => [],
    appendChild(fragment) { stage.scrollHeight += fragment.height; }};
  Object.defineProperty(stream, 'innerHTML', {set() {
    stage.scrollHeight = 400;
    events.scroll(); // A refresh clamps the scroll offset before history/replay grows.
  }});
  const originalDollar = c.$, addUserMsg = c.addUserMsg, handleEvent = c.handleEvent;
  c.$ = id => id === '#stage' ? stage : id === '#stream' ? stream : id === '#welcome' ? {style: {}} : originalDollar(id);
  c.requestAnimationFrame = fn => { frames.set(++nextFrame, fn); return nextFrame; };
  c.cancelAnimationFrame = id => frames.delete(id);
  runScript(readSource(sourcePath(testDir, '../../internal/webgui/assets/js/04-transcript.js'), 'utf8'), c);
  c.renderLoadedTranscript = h.renderLoadedTranscript;
  c.resetWorkerOverview = () => {};
  c.lastTurn = null; c.workersSeen = [];
  c.buildHistoryFragment = messages => {
    h.rendered.length = 0;
    messages.forEach(m => h.rendered.push(['history', m.seq]));
    return {height: messages.length * 600};
  };
  c.addUserMsg = prompt => {
    const node = addUserMsg(prompt);
    stage.scrollHeight += 300;
    c.smartScroll(true); // The live-run reattach also forces a scroll.
    return node;
  };
  c.handleEvent = (ev, current) => {
    stage.scrollHeight += 500;
    c.smartScroll();
    return handleEvent(ev, current);
  };
  h.paint = () => {
    events.scroll(); // Wake/resize scroll events need not come from user input.
    const batch = [...frames.values()]; frames.clear();
    batch.forEach(fn => fn());
    events.scroll();
  };
  return {...h, stage, events};
}
// Real transcript rows + real handleEvent, with deterministic clocks and a tiny
// DOM. No browser/Node dependencies are needed to test replayed timer labels.
for (const mode of ['normal', 'paused', 'immediate', 'afterRun', 'stopped', 'takeover']) {
  test('sync run_finished hands off queue: ' + mode, async () => {
    const h = harness(), c = h.c, sent = [];
    h.frame({type: 'snapshot', runs: [info], events: [activity]});
    await new Promise(resolve => setTimeout(resolve, 0));
    c.promptQueue = [{id: 'q', text: 'queued'}];
    c.pauseQueue = mode === 'paused' || mode === 'afterRun';
    if (mode === 'immediate' || mode === 'afterRun') c.pendingImmediate = {text: 'now', afterRun: mode === 'afterRun'};
    c.isAfterRunPrompt = item => !!item.afterRun;
    c.dispatchQueuedTask = item => { sent.push(item); };
    c.syncRefreshView = () => {};
    if (mode === 'takeover') { c.queueDispatching = true; c.activeRun = c.syncRuns.s1; }
    h.frame({type: 'run_finished', session_id: 's1', queue_paused: mode === 'stopped'});
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.equal(sent.length, mode === 'paused' || mode === 'stopped' ? 0 : 1);
    if (sent.length) assert.equal(sent[0].text, mode === 'immediate' || mode === 'afterRun' ? 'now' : 'queued');
    if (mode === 'takeover') assert.equal(c.queueDispatching, false);
  });
}

test('queue_changed and reconnect snapshot refresh the durable queue', () => {
  const h = harness();
  let refreshes = 0;
  h.c.loadPromptQueue = () => { refreshes++; };
  h.frame({type: 'queue_changed', project_path: 'workspace'});
  h.frame({type: 'snapshot', runs: [], events: []});
  assert.equal(refreshes, 2);
});

function timerHarness(skew = 300000) {
  const h = harness(), c = h.c, intervals = new Map();
  let now = 1000000, mono = 500, sequence = 0;
  function node(tag, cls = '', text = '') {
    const n = {tag, className: cls, textContent: text, children: [], style: {}, dataset: {}, open: false,
      appendChild(child) { this.children.push(child); return child; }, addEventListener() {},
      querySelectorAll() { return []; }, setAttribute() {}, removeAttribute() {},
      querySelector(selector) { return this.children.find(v => v.className === selector.slice(1)) || null; }};
    n.classList = {add() {}, remove() {}, toggle() {}, contains() { return false; }};
    return n;
  }
  c.Date = {now: () => now + skew}; c.performance = {now: () => mono};
  c.setInterval = fn => { intervals.set(++sequence, fn); return sequence; };
  c.clearInterval = id => intervals.delete(id);
  c.document.createElement = node; c.document.createDocumentFragment = () => node('fragment');
  c.el = node; c.i18nEl = (tag, cls, key) => node(tag, cls, key);
  c.requestAnimationFrame = () => 1; c.cancelAnimationFrame = () => {};
  const source = name => readSource(sourcePath(testDir, '../../internal/webgui/assets/js', name), 'utf8');
  runScript(source('04-transcript.js'), c);
  const chat = source('05-chat.js');
  runScript(chat.slice(chat.indexOf('function handleEvent('), chat.indexOf('\nfunction closeQuestionOverlay(')), c);
  c.appendStream = () => {}; c.smartScroll = () => {}; c.workersSeen = [];
  c.addUserMsg = () => node('div'); c.bindLiveMessageReceipt = () => {};
  c.syncRefreshView = () => {}; c.syncSettleTranscriptScroll = () => {};
  c.toolDisclosureOpen = () => false;
  c.renderToolPayloadWhenOpen = () => () => {};
  c.applyToolKind = () => {}; c.applyActivityToolKind = () => {};
  c.toolHint = name => ({name, hint: '', agent: 'code', prompt: 'work'});
  c.parseTaskNotification = () => null;
  c.toolChangeStats = () => ({}); c.mutationOutcomeLabel = () => '';
  c.appendToolMediaPreview = () => {}; c.appendNativeToolImages = () => {};
  c.setToolResultStatus = (row, ms) => { row._stat.textContent = 'done ' + ms; };
  c.appendTaskBacklink = () => {}; c.updateWorkerOverview = () => {};
  c.taskRowTitle = () => 'worker'; c.taskStatusLabel = status => status;
  c.clip = text => text; c.toolDisplayName = name => name;
  return {...h, intervals, advance(ms) { now += ms; mono += ms; intervals.forEach(fn => fn()); }};
}

for (const skew of [300000, -300000]) test('late replay uses server elapsed with phone clock skew ' + skew, () => {
  const h = timerHarness(skew), c = h.c;
  h.frame({type: 'snapshot', server_now_ms: 1000000,
    runs: [{...info, started_at_ms: 940000}], events: [activity,
      {type: 'tool_call', id: 'tool', name: 'shell', args: '{}', at: 945000},
      {type: 'tool_call', id: 'task', name: 'task', args: '{}', at: 950000},
      {type: 'worker_progress', kind: 'started', parent_call_id: 'task', id: 'w', name: 'code', at: 951000},
      {type: 'tool_call', id: 'send', name: 'send_message', args: '{}', at: 960000},
      {type: 'tool_call', id: 'finished', name: 'shell', args: '{}', at: 941000},
      {type: 'tool_result', id: 'finished', output: 'ok', at: 961000}
    ]});
  c.syncRuns.s1.reattach();
  assert.equal(c.runStatus.textContent, 'composer.working 60000');
  assert.equal(c.toolRows.tool._stat.textContent, 'tool.running · 55000');
  assert.equal(c.toolRows.task._stat.textContent, 'tool.running · 50000');
  assert.equal(c.toolRows.send._stat.textContent, 'tool.running · 40000');
  assert.equal(c.toolRows.finished._stat.textContent, 'done 20000');
  h.advance(2500);
  assert.equal(c.runStatus.textContent, 'composer.working 62500');
  assert.equal(c.toolRows.task._stat.textContent, 'tool.running · 52500');
  assert.equal(c.toolRows.finished._stat.textContent, 'done 20000');
  h.frame({type: 'run_event', session_id: 's1', turn_id: 'turn', server_now_ms: 1002500,
    event: {type: 'tool_result', id: 'tool', output: 'ok', at: 1002000}});
  assert.equal(c.toolRows.tool._stat.textContent, 'done 57000');
  c.handleEvent({type: 'worker_progress', kind: 'finished', parent_call_id: 'task', id: 'w', status: 'done', at: 1001000}, null);
  assert.equal(c.toolRows.task._stat.textContent, 'done · 51000');
  // A later worker notification must use its event time, not replay/render time.
  let workerElapsed;
  c.renderTaskResult = (row, task, ms) => { workerElapsed = ms; };
  c.handleEvent({type: 'worker', id: 'w', name: 'code', status: 'done', at: 1001500}, null);
  assert.equal(workerElapsed, 51500);
  let turnElapsed;
  c.closeAssistantReasoning = () => {}; c.flushAssistantRender = () => {};
  c.releaseAssistantParagraphCaches = () => {}; c.addFileChanges = () => {};
  c.addTurnMeta = (ev, ms) => { turnElapsed = ms; };
  c.promptQueue = []; c.notifyDone = () => {};
  c.handleEvent({type: 'done', at: 1002000}, null);
  assert.equal(turnElapsed, 62000);
});

test('legacy replay and originating client retain local monotonic tool timers', () => {
  const h = timerHarness(), c = h.c;
  h.frame({type: 'snapshot', runs: [info], events: [activity]});
  c.syncRuns.s1.reattach();
  assert.equal(c.runStatus.textContent, 'composer.working 0');
  h.advance(500);
  assert.equal(c.runStatus.textContent, 'composer.working 500');
  c.handleEvent({type: 'tool_call', id: 'legacy', name: 'shell', args: '{}'}, null);
  c.activeRun = {remote: false};
  c.handleEvent({type: 'tool_call', id: 'local', name: 'shell', args: '{}', at: 900000}, null);
  h.advance(1500);
  for (const id of ['legacy', 'local']) {
    assert.equal(c.toolRows[id]._stat.textContent, 'tool.running · 1500');
    c.handleEvent({type: 'tool_result', id, output: 'ok', at: 900100}, null);
    assert.equal(c.toolRows[id]._stat.textContent, 'done 1500');
  }
});

const tick = () => new Promise(resolve => setTimeout(resolve, 0));
const info = {session_id: 's1', turn_id: 'turn', client_id: 'other', prompt: 'hello'};
const activity = {type: 'session_activity', user_seq: 3};

test('resume preserves hide-time scroll intent through refresh and live replay', async () => {
  for (const lifecycle of ['visibilitychange', 'pageshow']) {
    for (const mode of ['idle', 'live', 'finished while hidden']) {
      for (const followTail of [true, false]) {
        const h = scrollHarness(), c = h.c;
        const label = `${lifecycle}, ${mode}, follow=${followTail}`;
        h.frame({type: 'snapshot', runs: [], events: []}); await tick(); h.paint();
        h.stage.scrollTop = followTail ? h.stage.scrollHeight : 350;
        h.events.scroll();
        const savedTop = h.stage.scrollTop;
        if (mode !== 'idle') c.activeRun = {remote: false, sessionID: 's1', turnID: 'turn', disconnect() {}};
        c.document.hidden = true;
        h.listeners[lifecycle === 'pageshow' ? 'pagehide' : 'visibilitychange']();
        h.stage.scrollHeight += 800; h.events.scroll();
        h.listeners.pagehide(); // Do not recapture geometry on a duplicate hide.
        assert.equal(c.transcriptFollowTail, followTail, label);
        c.document.hidden = false;
        h.listeners[lifecycle]();
        h.stage.scrollHeight += 600; h.paint(); // No snapshot/ready yet.
        assert.equal(c.transcriptFollowTail, followTail, label);
        let release;
        const page = new Promise(resolve => { release = resolve; });
        c.j = () => page;
        const live = mode === 'live';
        h.frame({type: 'snapshot', runs: live ? [info] : [], events: live ? [activity, {type: 'message', text: 'replay after sleep'}] : []});
        await tick(); h.paint();
        c.syncRefreshView(); // Another refresh requested while the first is pending.
        release({messages: [{seq: 1}, {seq: 3}, {seq: 4}]});
        await tick();
        assert.equal(c.syncRefreshPending, false, label);
        assert.equal(c.streaming, live, label);
        if (live) assert(h.rendered.some(row => row[0] === 'message' && row[1] === 'replay after sleep'), label);
        h.paint();
        h.stage.scrollHeight += 700; h.events.scroll(); // Viewport/layout settles a frame later.
        h.paint();
        assert.equal(h.stage.scrollTop, followTail ? h.stage.scrollHeight - h.stage.clientHeight : savedTop, label);
        assert.equal(c.transcriptFollowTail, followTail, label);
        assert.equal(c.transcriptResumeScroll, null, label);
      }
    }
  }
});

test('explicit input releases the resume guard and session switches discard it', () => {
  for (const action of ['touchstart', 'wheel', 'pointerdown', 'keydown', 'switch']) {
    const h = scrollHarness(), c = h.c;
    c.document.hidden = true; h.listeners.visibilitychange();
    c.document.hidden = false; h.listeners.visibilitychange();
    c.settleTranscriptScroll(() => true); // A pending restoration must be invalidated.
    h.stage.scrollTop = 250;
    if (action === 'switch') { c.activeSessionID = 's2'; c.syncSelectSession('s2'); }
    else h.events[action]();
    assert.equal(c.savedTranscriptScroll(), null, action);
    c.smartScroll(); h.paint(); h.paint();
    assert.equal(h.stage.scrollTop, 250, action);
  }
});

test('observer replays above persisted user sequence, streams once and reloads durable end', async () => {
  const h = harness();
  h.frame({type: 'snapshot', runs: [info], events: [activity, {type: 'message', text: 'part'}]});
  await tick();
  assert.deepEqual(h.rendered, [['history', 1], ['user', 'hello'], ['message', 'part']]);
  h.frame({type: 'run_event', ...info, event: {type: 'message', text: ' live'}});
  assert.deepEqual(h.rendered.at(-1), ['message', ' live']);
  h.frame({type: 'run_event', ...info, event: {type: 'question_closed', id: 'q'}});
  assert(h.calls.some(x => x[0] === 'closeQuestion' && x[1] === 'q'));
  h.frame({type: 'run_finished', ...info}); await tick();
  assert.deepEqual(h.rendered, [['history', 1], ['history', 3], ['history', 4]]);
  assert.equal(h.c.streaming, false);
  assert.equal(h.c.syncRuns.s1, undefined);
});

test('sender does not render its live run twice and observer Stop uses explicit server endpoint', async () => {
  const h = harness();
  const own = {...info, client_id: h.c.syncClientID};
  h.c.activeRun = {remote: false, sessionID: 's1', turnID: 'turn'};
  h.frame({type: 'snapshot', runs: [own], events: []});
  h.frame({type: 'run_event', ...own, event: {type: 'message', text: 'duplicate'}});
  await tick(); assert.equal(h.rendered.length, 0);
  h.c.activeRun = null;
  h.frame({type: 'snapshot', runs: [info], events: [activity]}); await tick();
  h.c.abortCtl.abort(); await tick();
  assert(h.calls.some(x => x[0] === '/api/sync/stop' && x[1].turn_id === 'turn'));
});

test('phone visibility reconnect uses WS, selected session/client and replaces replay instead of appending twice', async () => {
  const h = harness();
  assert.match(h.sockets[0].url, /^wss:\/\/test-phone.*\/api\/sync\/ws\?session=s1&client=client-1$/);
  h.frame({type: 'snapshot', runs: [info], events: [activity, {type: 'message', text: 'part'}]}); await tick();
  h.c.document.hidden = true; h.listeners.visibilitychange(); assert(h.sockets[0].closed);
  h.c.document.hidden = false; h.listeners.visibilitychange();
  h.frame({type: 'snapshot', runs: [info], events: [activity, {type: 'message', text: 'part plus slept'}]}); await tick();
  assert.deepEqual(h.rendered, [['history', 1], ['user', 'hello'], ['message', 'part plus slept']]);
  h.c.activeSessionID = 's2'; h.c.syncSelectSession('s2'); assert.match(h.sockets.at(-1).url, /session=s2&client=/);
  for (const event of ['online', 'pageshow']) { const n = h.sockets.length; h.listeners[event](); assert.equal(h.sockets.length, n + 1); }
});

test('returning sender relinquishes dead chat ownership and requests its own replay', async () => {
  const h = harness(); let disconnects = 0;
  const ownClient = h.c.syncClientID;
  h.c.activeRun = {remote: false, accepted: true, sessionID: 's1', turnID: 'turn', disconnect() { disconnects++; }};
  h.c.document.hidden = true; h.listeners.visibilitychange();
  h.c.document.hidden = false; h.listeners.visibilitychange();
  assert.equal(disconnects, 1); assert.notEqual(h.c.syncClientID, ownClient);
  h.frame({type: 'snapshot', runs: [{...info, client_id: ownClient}], events: [activity, {type: 'message', text: 'while asleep'}]}); await tick();
  assert.deepEqual(h.rendered, [['history', 1], ['user', 'hello'], ['message', 'while asleep']]);
  assert.equal(h.c.activeRun.remote, true);
});

test('opening a new conversation does not resurrect the previously followed run', async () => {
  const h = harness();
  h.c.activeRun = {sessionID: 's1', turnID: 'turn', disconnect() {}};
  h.c.syncFollowRun(h.c.activeRun);
  h.c.activeSessionID = ''; h.c.syncSelectSession('');
  h.c.j = async () => { throw Error('must not fetch an old receipt'); };
  h.frame({type: 'snapshot', runs: []}); await tick();
  assert.equal(h.c.activeSessionID, ''); assert.equal(h.c.syncFollowingTurn, '');
});

test('a lost fresh-session frame recovers the durable conversation from its turn receipt', async () => {
  const h = harness();
  h.c.activeSessionID = ''; h.c.syncSession = '';
  h.c.activeRun = {sessionID: '', turnID: 'lost-turn', disconnect() {}};
  h.c.j = async url => { assert.match(url, /completion\?id=lost-turn$/); return {session_id: 'recovered', accepted: true}; };
  h.c.syncFollowRun(h.c.activeRun);
  h.frame({type: 'snapshot', runs: [], events: []}); await tick();
  assert.equal(h.c.activeSessionID, 'recovered');
  assert.match(h.sockets.at(-1).url, /session=recovered&client=/);
});

test('loopback uses SSE and a finished snapshot refreshes missed completion', async () => {
  const h = harness('localhost'); assert.match(h.sockets[0].url, /^\/api\/sync\?session=s1&client=/);
  h.sockets[0].onmessage({data: JSON.stringify({type: 'snapshot', runs: []})}); await tick();
  assert.deepEqual(h.rendered, [['history', 1], ['history', 3], ['history', 4]]);
});
