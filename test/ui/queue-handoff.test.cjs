const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

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
  const calls = [], events = [], requests = [], sends = [];
  const arrivals = Array.from({length: 4}, deferred);
  const streams = new Map();
  const entered = Array.from({length: 4}, deferred);
  const deletion = deferred();
  const catalog = JSON.parse(fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/locales/en.json'), 'utf8'));
  const $ = selector => {
    if (!elements.has(selector)) elements.set(selector, element('div'));
    return elements.get(selector);
  };
  const context = {
    document: {hidden: false, hasFocus: () => true}, localStorage: {getItem: () => null},
    AbortController, Date, Promise, console, window: {},
    $, $$: () => [], el: element,
    i18nEl: (tag, cls, key) => element(tag, cls, catalog[key]),
    t: key => { assert.ok(catalog[key], 'missing translation: ' + key); return catalog[key]; },
    superCliUI: {
      createComposerDraftStore: () => ({scope: () => 'session:s1', clear() {}, restore() {}}),
      readSSE: async (body, emit) => {
        if (!(options.hideSession && body.index === 0)) {
          emit({type: 'session', session_id: 's1'});
          if (options.acceptMessage !== false) emit({type: 'session_activity', session_id: 's1'});
        }
        const done = deferred();
        streams.set(body.index, () => { emit({type: 'done'}); done.resolve(); });
        body.signal.addEventListener('abort', () => {
          const err = new Error('aborted'); err.name = 'AbortError'; done.reject(err);
        }, {once: true});
        entered[body.index].resolve();
        await done.promise;
      },
    },
    setInterval: () => 1, clearInterval() {},
    fetch: async (_url, request) => {
      const index = requests.length;
      requests.push(request);
      arrivals[index].resolve(request);
      return {ok: options.accepted !== false, status: 503, body: {index, signal: request.signal}};
    },
    j: async (url, opts) => {
      calls.push([url, opts]);
      if (url.startsWith('/api/chat/completion?id=')) {
        if (options.completionRequested) options.completionRequested.resolve();
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
    sessionByID: {s1: {model: 'old-model'}}, transcriptSessionID: 's1',
    transcriptAbortCtl: null, loadedTranscriptMessages: [], transcriptHasMore: false,
    transcriptBeforeSeq: 0, transcriptLiveAppend: false,
    stream: element('div'), toolRows: {}, openToolOrder: [],
    overlay: {hidden: true}, currentSection: '', sections: {},
    fmtDuration: () => '1s', clip: value => value,
  };
  vm.createContext(context);
  vm.runInContext(fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js/05-chat.js'), 'utf8'), context);
  context.activeSessionID = 's1';
  for (const name of ['updateAppBadge', 'wireQueuedTaskDrag', 'sealAssistantSegment',
    'closeQuestionOverlay', 'settleOpenTools', 'releaseLiveTranscriptBlocks', 'renderStats',
    'addFileChanges', 'addTurnMeta', 'loadSideGoal', 'closeAssistantReasoning', 'flushAssistantRender']) context[name] = () => {};
  context.toast = value => events.push(value);
  context.addEventLine = value => events.push(value);
  context.addUserMsg = () => element('div');
  context.addLatestMessageRewind = async (_node, _text, attempts) => {
    assert.equal(attempts, 1, 'handoff must not poll transcript repeatedly');
    return 0;
  };
  context.rememberSentAttachments = () => {};
  context.loadSessions = () => new Promise(() => {}); // slow sidebar must not block dispatch
  context.notifyDone = () => events.push('completed-notification');
  context.restoreSessionRuntime = async () => calls.push(['restore-runtime']);
  context.resumeSession = async (...args) => { calls.push(['resume', ...args]); return options.resume !== false; };
  const sendPrompt = context.sendPrompt;
  context.sendPrompt = (...args) => {
    const pending = sendPrompt(...args); sends.push(pending); return pending;
  };
  return {context, $, calls, events, requests, sends, arrivals, streams, entered, deletion};
}
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
