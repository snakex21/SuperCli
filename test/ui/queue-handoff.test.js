import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript} from './harness.js';

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
  const streams = new Map(), terminals = new Map(), finishing = new Map(), intervals = new Map(), failures = new Map();
  let nativeReadSSE;
  const entered = Array.from({length: 4}, deferred);
  const deletion = deferred();
  const catalog = JSON.parse(readSource(sourcePath(testDir, '../../internal/webgui/assets/locales/en.json'), 'utf8'));
  const $ = selector => {
    if (!elements.has(selector)) elements.set(selector, element('div'));
    return elements.get(selector);
  };
  const context = {
    document: {hidden: false, hasFocus: () => true, createElement: element}, localStorage: {getItem: () => null},
    AbortController, Date, Promise, console, window: {},
    $, $$: () => [], el: element,
    i18nEl: (tag, cls, key) => element(tag, cls, catalog[key]),
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
      return {ok: options.accepted !== false && !(options.rejectSecondHTTP && index === 1), status: 503, body: {index, signal: request.signal}};
    },
    j: async (url, opts) => {
      calls.push([url, opts]);
      if (url.startsWith('/api/chat/completion?id=')) {
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
