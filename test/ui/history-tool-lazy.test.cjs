const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// Keep the fixture independent of a browser install. textContent creates text
// nodes, and document fragments transfer their children as the DOM does.
function domNode(tag, type = 1) {
  const node = {tag, nodeType: type, children: [], style: {}, dataset: {}, open: false, listeners: new Map(),
    appendChild(child) { return this.insertBefore(child, null); },
    insertBefore(child, before) {
      if (child.nodeType === 11) {
        child.children.slice().forEach(value => this.insertBefore(value, before));
      } else {
        if (child.parentNode) child.parentNode.children.splice(child.parentNode.children.indexOf(child), 1);
        child.parentNode = this;
        this.children.splice(before ? this.children.indexOf(before) : this.children.length, 0, child);
      }
      return child;
    },
    remove() {
      if (this.parentNode) this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1);
      this.parentNode = null;
    },
    addEventListener(name, listener) {
      if (!this.listeners.has(name)) this.listeners.set(name, new Set());
      this.listeners.get(name).add(listener);
    },
    removeEventListener(name, listener) { this.listeners.get(name)?.delete(listener); },
    dispatch(name) { Array.from(this.listeners.get(name) || []).forEach(listener => listener({target: this})); },
    setAttribute() {}, removeAttribute() {}, focus() {},
    querySelector(selector) { return this.querySelectorAll(selector)[0] || null; },
    querySelectorAll(selector) {
      return descendants(this).filter(child => selector.startsWith('.') &&
        String(child.className || '').split(' ').includes(selector.slice(1)));
    },
  };
  node.classList = {add() {}, remove() {}, toggle() {}};
  Object.defineProperty(node, 'isConnected', {get: () => !!node.parentNode});
  Object.defineProperty(node, 'firstChild', {get: () => node.children[0] || null});
  Object.defineProperty(node, 'nextSibling', {get() { return this.parentNode ? this.parentNode.children[this.parentNode.children.indexOf(this) + 1] || null : null; }});
  Object.defineProperty(node, 'childNodes', {get: () => node.children});
  Object.defineProperty(node, 'textContent', {
    get() { return this.nodeType === 3 ? this.text : this.children.map(child => child.textContent).join(''); },
    set(value) {
      this.children = [];
      if (String(value) !== '') this.children.push({nodeType: 3, text: String(value), textContent: String(value), children: []});
    },
  });
  Object.defineProperty(node, 'innerHTML', {set(value) { this.children = []; this.html = String(value); }, get() { return this.html || ''; }});
  return node;
}
function descendants(node) { return node.children.flatMap(child => [child, ...descendants(child)]); }
function countNodes(node, elementsOnly = false) {
  return [node, ...descendants(node)].filter(child => elementsOnly ? child.nodeType === 1 : child.nodeType !== 11).length;
}
function harness() {
  const nodes = new Map(), calls = [];
  let created = 0;
  const $ = selector => {
    if (!nodes.has(selector)) nodes.set(selector, domNode('div'));
    return nodes.get(selector);
  };
  function el(tag, cls, text) {
    created++;
    const node = domNode(tag);
    if (cls) node.className = cls;
    if (text != null) node.textContent = text;
    return node;
  }
  const c = {
    $, $$: () => [], el, i18nEl: (tag, cls, key) => el(tag, cls, key), t: key => key,
    document: {createElement: el, createDocumentFragment: () => domNode('#fragment', 11)},
    window: {}, superCliUI: {fileMutationTools: {}, mutationKind: () => ''},
    requestAnimationFrame: fn => { calls.push('scroll'); return 1; },
    prettyJSON: text => { try { return JSON.stringify(JSON.parse(text), null, 2); } catch { return text; } },
    toolHint: name => ({name, hint: 'file contents'}), toolDisplayName: name => name,
    clip: (text, n) => String(text).slice(0, n), parseTaskNotification: () => null,
    AbortController, Promise, streaming: false, queueDispatching: false,
    activeSessionID: '', projectEpoch: 1, sessionRuntimeReady: Promise.resolve(), lastTurn: null, workersSeen: [],
    composerDraftStore: {restore() {}}, promptEl: {focus() {}}, ui: {},
    resetWorkerOverview() {}, sentAttachmentsFor: () => [], toast: text => {throw new Error(text);},
  };
  vm.createContext(c);
  for (const file of ['04-transcript.js', '08-sessions.js']) {
    vm.runInContext(fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js', file), 'utf8'), c);
  }
  c.resetWorkerOverview = () => {};
  c.loadSessions = () => calls.push('sessions');
  c.renderStats = () => calls.push('stats');
  c.restoreSessionRuntime = () => calls.push('runtime');
  c.setSessionOpening = () => {};
  return {c, calls, created: () => created};
}
function readLines(lines = 5000) {
  return {seq: 2, role: 'tool', name: 'read_lines', tool_call_id: 'read-1',
    content: Array.from({length: lines}, (_, i) => (i + 1) + ' | payload-' + i).join('\n')};
}

test('a folded 5000-line history read creates only its summary until expanded once', t => {
  const message = readLines();
  const eager = harness();
  eager.c.appendHistoryToolPayload = (row, body, args, text, name) => eager.c.appendToolPayload(body, 'tool.output', text, name, false);
  const beforeRow = eager.c.buildHistoryFragment([message]).children[0];
  const h = harness();
  const row = h.c.buildHistoryFragment([message]).children[0];
  const body = row.children[1];
  assert.equal(body.children.length, 0, 'folded body remains unmaterialized');
  assert.equal(countNodes(beforeRow, true), 15007);
  assert.equal(countNodes(row, true), 6);
  assert.equal(countNodes(beforeRow), 25009);
  assert.equal(countNodes(row), 8);
  t.diagnostic('5000-line read_lines: DOM nodes 25009 -> 8 while folded (elements 15007 -> 6)');
  row.dispatch('toggle'); // Browsers may report the initial closed state.
  assert.equal(body.children.length, 0);
  row.open = true; row.dispatch('toggle');
  assert.equal(countNodes(row), countNodes(beforeRow));
  assert.equal(body.textContent, beforeRow.children[1].textContent);
  const viewer = body.children[0];
  assert.equal(viewer.children.length, 5000);
  assert.equal(viewer.children[0].children[0].textContent, '1');
  assert.equal(viewer.children[4999].children[1].textContent, 'payload-4999');
  row.open = false; row.dispatch('toggle'); row.open = true; row.dispatch('toggle');
  assert.equal(countNodes(row), 25009, 'reopening never duplicates the viewer');
  assert.equal(row.listeners.get('toggle').size, 0, 'release the rendering listener after expansion');
  assert.equal(message.content, readLines().content, 'original transcript remains intact');
});

test('history rows already open render input and output immediately with the original text', () => {
  const {c} = harness(), row = domNode('details'), body = domNode('div');
  row.open = true; row.appendChild(body);
  const args = '{"query":"a & b"}', output = 'line 1\nline 2 <unsafe>';
  c.appendHistoryToolPayload(row, body, args, output, 'search_code');
  assert.equal(body.children.length, 4);
  assert.equal(body.children[1].textContent, JSON.stringify(JSON.parse(args), null, 2));
  assert.equal(body.children[3].textContent, output);
  const count = countNodes(row);
  row.dispatch('toggle'); row.open = false; row.dispatch('toggle'); row.open = true; row.dispatch('toggle');
  assert.equal(countNodes(row), count);
});

test('opening a session and loading an older page preserve paging, raw results and scroll behavior', async () => {
  const h = harness(), latest = readLines(), older = {...readLines(3), seq: 1};
  const requests = [];
  h.c.j = async url => {
    requests.push(url);
    return url.includes('&before=2') ? {messages: [older], has_more: false, before_seq: 1} :
      {messages: [latest], has_more: true, before_seq: 2};
  };
  assert.equal(await h.c.resumeSession('old-chat', {}), true);
  assert.deepEqual(requests, ['/api/transcript?id=old-chat&limit=60']);
  assert.equal(h.c.loadedTranscriptMessages[0].content, latest.content);
  assert.equal(h.c.transcriptBeforeSeq, 2);
  assert.equal(h.c.stream.querySelectorAll('.tool-file-view').length, 0);
  assert.equal(h.calls.filter(call => call === 'scroll').length, 1, 'latest page follows the tail');
  h.c.stage.scrollHeight = 1000; h.c.stage.scrollTop = 30;
  await h.c.loadOlderTranscript();
  assert.deepEqual(requests, ['/api/transcript?id=old-chat&limit=60', '/api/transcript?id=old-chat&limit=60&before=2']);
  assert.equal(h.c.loadedTranscriptMessages.length, 2);
  assert.equal(h.c.loadedTranscriptMessages[0].seq, 1);
  assert.equal(h.c.loadedTranscriptMessages[1].content, latest.content);
  assert.equal(h.c.transcriptHasMore, false);
  assert.equal(h.c.stage.scrollTop, 30, 'older page retains its scroll anchor');
  assert.equal(h.c.stream.querySelectorAll('.tool-file-view').length, 0, 'older-page rerender stays lazy too');
  const rows = h.c.stream.querySelectorAll('.tool-row');
  assert.equal(rows.length, 2);
  rows.forEach(row => { row.open = true; row.dispatch('toggle'); });
  assert.equal(h.c.stream.querySelectorAll('.tool-file-view').length, 2, 'all details remain available');
});

function resultRow(h, name) {
  const row = domNode('details'); row.className = 'tool-row';
  row._body = domNode('div'); row._stat = domNode('span'); row._tname = domNode('span');
  row._toolName = name; row._t0 = 0; row._clock = 7;
  row.appendChild(row._stat); row.appendChild(row._body);
  h.c.performance = {now: () => 100}; h.c.fmtDuration = () => '0.1s';
  h.c.clearInterval = () => {}; h.c.toolRows = {'read-1': row}; h.c.openToolOrder = ['read-1'];
  return row;
}

test('live folded results defer the large viewer while preserving completion and exact output', t => {
  const output = readLines().content;
  const eager = harness(); eager.c.renderToolPayloadWhenOpen = (row, render) => render();
  const before = resultRow(eager, 'read_lines'); eager.c.addToolResult('read-1', output, '');
  const h = harness(), row = resultRow(h, 'read_lines');
  h.c.addToolResult('read-1', output, '');
  assert.equal(row._clock, null); assert.equal(h.c.openToolOrder.length, 0);
  assert.equal(row._body.children.length, 0); assert.equal(row._stat.textContent, '0.1s');
  assert.equal(countNodes(row), 5); assert.equal(countNodes(before), 25006);
  t.diagnostic('live folded 5000-line read: DOM nodes 25006 -> 5');
  row.open = true; row.dispatch('toggle');
  assert.equal(row._body.textContent, before._body.textContent);
  assert.equal(row._body.children[0].children.length, 5000);
  const expanded = countNodes(row);
  row.open = false; row.dispatch('toggle'); row.open = true; row.dispatch('toggle');
  assert.equal(countNodes(row), expanded);
});

test('open live results render immediately and failures retain their diagnostic on expansion', () => {
  const h = harness(), row = resultRow(h, 'ctx_execute');
  row.open = true;
  h.c.addToolResult('read-1', JSON.stringify({exit_code:0,stdout:'hello',duration_ms:100}), '');
  assert.ok(row._body.textContent.includes('hello'));
  const failed = harness(), errorRow = resultRow(failed, 'ctx_execute');
  failed.c.addToolResult('read-1', '', 'command_failed exit=7\nmissing file');
  assert.equal(errorRow._body.children.length, 0);
  assert.ok(errorRow._stat.textContent.includes('×'));
  errorRow.open = true; errorRow.dispatch('toggle');
  assert.equal(errorRow._body.children[0].textContent, 'tool.error');
  assert.equal(errorRow._body.children[1].textContent, 'command_failed exit=7\nmissing file');
});


test('folded task reports render their complete source only once on expansion', () => {
  const {c} = harness(), calls = [];
  c.renderText = source => { calls.push(source); return '<p>' + source + '</p>'; };
  const note = {id: 'worker-lazy', agent: 'worker', status: 'done', summary: 'completed', result: 'Full **report** & details'};
  const row = c.addHistoryTask(note), report = row.querySelector('.task-report');
  assert.equal(report.innerHTML, ''); assert.deepEqual(calls, []);
  assert.equal(row._thint.textContent, 'completed');
  row.dispatch('toggle'); assert.deepEqual(calls, [], 'initial closed toggle does not render');
  row.open = true; row.dispatch('toggle');
  assert.deepEqual(calls, [note.result]); assert.equal(report.innerHTML, '<p>' + note.result + '</p>');
  row.open = false; row.dispatch('toggle'); row.open = true; row.dispatch('toggle');
  assert.deepEqual(calls, [note.result]); assert.equal(row.querySelector('.task-report'), report);
  assert.equal(row.listeners.get('toggle').size, 0); assert.equal(row._cancelTaskReport, null);
  assert.equal(note.result, 'Full **report** & details');
});

test('task report replacement cancels stale folded callbacks and preserves open updates', () => {
  const {c} = harness(), calls = [];
  c.renderText = source => { calls.push(source); return source; };
  const note = {id: 'worker-reuse', agent: 'worker', status: 'done', summary: 'completed', result: 'Old result'};
  const row = c.addHistoryTask(note);
  c.renderTaskResult(row, {...note, result: 'Current result'}, null, '', false);
  assert.equal(row.listeners.get('toggle').size, 1, 'only the current callback remains');
  row.open = true; row.dispatch('toggle');
  assert.deepEqual(calls, ['Current result']); assert.equal(row.querySelector('.task-report').innerHTML, 'Current result');
  c.renderTaskResult(row, {...note, result: 'Updated while open'}, null, '', false);
  assert.deepEqual(calls, ['Current result', 'Updated while open']);
  assert.equal(row.querySelector('.task-report').innerHTML, 'Updated while open');
  assert.equal(row.listeners.get('toggle').size, 0);
  row.open = false; row.dispatch('toggle');
  c.renderTaskResult(row, {...note, result: 'Pending replacement'}, null, '', false);
  c.renderTaskResult(row, {...note, result: ''}, null, '', false);
  row.open = true; row.dispatch('toggle');
  assert.deepEqual(calls, ['Current result', 'Updated while open']);
  assert.equal(row.querySelector('.task-report'), null);
  assert.equal(row.listeners.get('toggle').size, 0); assert.equal(row._cancelTaskReport, null);
});

test('task history paging retains raw reports and defers newly received rows', async () => {
  const h = harness(), calls = [];
  h.c.renderText = source => { calls.push(source); return source; };
  const content = result => '<task-notification><task-id>worker-' + result + '</task-id><agent>worker</agent><status>done</status><summary>completed</summary><result>' + result + '</result></task-notification>';
  const latest = {seq: 2, role: 'tool', name: 'task', content: content('latest')};
  const older = {seq: 1, role: 'tool', name: 'task', content: content('older')};
  h.c.j = async url => url.includes('&before=2') ? {messages: [older], has_more: false, before_seq: 1} : {messages: [latest], has_more: true, before_seq: 2};
  assert.equal(await h.c.resumeSession('task-chat', {}), true);
  assert.deepEqual(calls, []); assert.equal(h.c.loadedTranscriptMessages[0].content, latest.content);
  h.c.stage.scrollHeight = 1000; h.c.stage.scrollTop = 30;
  await h.c.loadOlderTranscript();
  assert.deepEqual(calls, []); assert.equal(h.c.stage.scrollTop, 30);
  const rows = h.c.stream.querySelectorAll('.task-row');
  assert.equal(rows.length, 2); rows.forEach(row => { row.open = true; row.dispatch('toggle'); });
  assert.deepEqual(calls, ['older', 'latest']);
  assert.equal(h.c.loadedTranscriptMessages[1].content, latest.content);
});


test('older paging retains expanded rows and live messages while rendering only the incoming page', async t => {
  const h = harness();
  const latest = Array.from({length: 60}, (_, i) => ({...readLines(3), seq: 61 + i, tool_call_id: 'latest-' + i}));
  const older = Array.from({length: 60}, (_, i) => ({...readLines(3), seq: 1 + i, tool_call_id: 'older-' + i}));
  h.c.j = async url => url.includes('&before=61') ? {messages: older, has_more: true, before_seq: 1} :
    {messages: latest, has_more: true, before_seq: 61};
  assert.equal(await h.c.resumeSession('retained-chat', {}), true);
  const rows = h.c.stream.querySelectorAll('.tool-row');
  rows[0].open = true; rows[0].dispatch('toggle');
  const expandedBody = rows[0].children[1], viewer = expandedBody.children[0];
  const live = domNode('div'); live.className = 'live-segment'; live.textContent = 'received after resume';
  h.c.stream.appendChild(live);
  h.c.streaming = true; h.c.transcriptLiveAppend = true;
  let height = 1000;
  Object.defineProperty(h.c.stage, 'scrollHeight', {get: () => height});
  h.c.stage.scrollTop = 30;
  const oldInsert = h.c.stream.insertBefore;
  h.c.stream.insertBefore = function(child, before) { if (child.nodeType === 11) height += 600; return oldInsert.call(this, child, before); };
  const created = h.created();
  await h.c.loadOlderTranscript();
  const appendedElements = h.created() - created;
  assert.equal(appendedElements, 360, 'only six summary elements for each of the 60 newly received rows');
  t.diagnostic('loading 60 older rows after 60 retained rows: summary elements created 720 -> 360');
  assert.equal(h.c.stream.querySelectorAll('.tool-row').length, 120);
  assert.equal(h.c.stream.querySelectorAll('.tool-row')[60], rows[0]);
  assert.equal(rows[0].open, true); assert.equal(rows[0].children[1], expandedBody); assert.equal(expandedBody.children[0], viewer);
  assert.equal(live.parentNode, h.c.stream); assert.equal(live.textContent, 'received after resume');
  assert.equal(h.c.transcriptLiveAppend, true); assert.equal(h.c.streamAppendTarget, null);
  assert.equal(h.c.stage.scrollTop, 630); assert.equal(h.c.stream.querySelector('.history-older').disabled, false);
  assert.equal(h.c.transcriptBeforeSeq, 1);
});

test('paging an older task notification preserves the latest worker backlink and its open report', async () => {
  const h = harness(); h.c.renderText = source => source;
  const task = (seq, result) => ({seq, role: 'tool', name: 'task', content:
    '<task-notification><task-id>worker-shared</task-id><agent>worker</agent><status>done</status><summary>completed</summary><result>' + result + '</result></task-notification>'});
  h.c.j = async url => url.includes('&before=2') ? {messages: [task(1, 'old report')], has_more: false, before_seq: 1} :
    {messages: [task(2, 'current report')], has_more: true, before_seq: 2};
  assert.equal(await h.c.resumeSession('worker-chat', {}), true);
  const current = h.c.workerRows['worker-shared']; current.open = true; current.dispatch('toggle');
  const report = current.querySelector('.task-report');
  await h.c.loadOlderTranscript();
  assert.equal(h.c.workerRows['worker-shared'], current); assert.equal(current.open, true);
  assert.equal(current.querySelector('.task-report'), report); assert.equal(report.innerHTML, 'current report');
  assert.equal(h.c.stream.querySelectorAll('.task-row').length, 2); assert.equal(h.c.stream.querySelector('.history-older'), null);
});


test('failed older-page rendering releases temporary worker references without clearing current content', async () => {
  const h = harness();
  h.c.j = async url => url.includes('&before=2') ? {messages: [readLines(1)], has_more: false, before_seq: 1} :
    {messages: [readLines(3)], has_more: true, before_seq: 2};
  assert.equal(await h.c.resumeSession('failed-page', {}), true);
  const existing = h.c.stream.querySelector('.tool-row'), workers = h.c.workerRows;
  const worker = domNode('details'); workers.current = worker;
  h.c.transcriptLiveAppend = true;
  h.c.buildHistoryFragment = () => { h.c.workerRows.orphan = domNode('details'); throw Error('fixture render failed'); };
  const notices = []; h.c.toast = text => notices.push(text);
  await h.c.loadOlderTranscript();
  assert.equal(h.c.workerRows, workers); assert.equal(h.c.workerRows.current, worker); assert.equal(h.c.workerRows.orphan, undefined);
  assert.equal(h.c.stream.querySelector('.tool-row'), existing); assert.equal(h.c.loadedTranscriptMessages.length, 1);
  assert.equal(h.c.transcriptLiveAppend, true); assert.equal(h.c.stream.querySelector('.history-older').disabled, false);
  assert.ok(notices[0].includes('fixture render failed'));
});
