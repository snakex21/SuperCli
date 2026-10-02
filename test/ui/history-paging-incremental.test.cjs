const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// No browser, dependencies, image decoding or network. The counters measure DOM
// construction/source assignments, not WebView memory, network requests or FPS.
function harness(rebuild = false) {
  const nodes = new Map(), created = [], notices = [], stats = {imageSources: 0};
  let document;
  function descendants(node) { return node.children.flatMap(child => [child, ...descendants(child)]); }
  function element(tag, className = '', text, nodeType = 1) {
    const node = {tag, nodeType, className, children: [], style: {}, dataset: {}, attributes: {}, events: new Map(),
      open: false, parentNode: null, height: tag === 'button' ? 30 : 50,
      appendChild(child) { return this.insertBefore(child, null); },
      insertBefore(child, before) {
        if (child.nodeType === 11) { child.children.slice().forEach(value => this.insertBefore(value, before)); return child; }
        child.remove();
        const index = before ? this.children.indexOf(before) : this.children.length;
        assert.ok(index >= 0, 'insertBefore target must belong to parent');
        this.children.splice(index, 0, child); child.parentNode = this; return child;
      },
      remove() { if (this.parentNode) this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1); this.parentNode = null; },
      replaceChildren(...children) { this.children.slice().forEach(child => child.remove()); children.forEach(child => this.appendChild(child)); },
      setAttribute(name, value) { this.attributes[name] = value; },
      removeAttribute(name) { delete this.attributes[name]; },
      addEventListener(name, fn) { if (!this.events.has(name)) this.events.set(name, new Set()); this.events.get(name).add(fn); },
      removeEventListener(name, fn) { this.events.get(name)?.delete(fn); },
      dispatch(name) { Array.from(this.events.get(name) || []).forEach(fn => fn.call(this, {target: this})); },
      querySelector(selector) { return this.querySelectorAll(selector)[0] || null; },
      querySelectorAll(selector) {
        return descendants(this).filter(child => selector.split(',').some(value => {
          value = value.trim();
          return value.startsWith('.') ? child.classList?.contains(value.slice(1)) : child.tag === value;
        }));
      },
      focus() { document.activeElement = this; },
    };
    node.classList = {
      contains(name) { return node.className.split(/\s+/).includes(name); },
      add(...names) { node.className = Array.from(new Set(node.className.split(/\s+/).filter(Boolean).concat(names))).join(' '); },
      remove(...names) { node.className = node.className.split(/\s+/).filter(name => !names.includes(name)).join(' '); },
      toggle(name, force) { const yes = force === undefined ? !this.contains(name) : force; if (yes) this.add(name); else this.remove(name); },
    };
    Object.defineProperties(node, {
      childNodes: {get() { return this.children; }},
      firstChild: {get() { return this.children[0] || null; }},
      nextSibling: {get() { return this.parentNode ? this.parentNode.children[this.parentNode.children.indexOf(this) + 1] || null : null; }},
      isConnected: {get() { return Array.from(nodes.values()).some(root => root === this || descendants(root).includes(this)); }},
      textContent: {get() { return this.nodeType === 3 ? this.text : this.children.map(child => child.textContent).join(''); }, set(value) {
        this.replaceChildren();
        if (String(value)) { const child = element('#text', '', undefined, 3); child.text = String(value); this.appendChild(child); }
      }},
      innerHTML: {get() { return this.html || ''; }, set(value) { this.replaceChildren(); this.html = String(value); }},
      src: {get() { return this.source || ''; }, set(value) { this.source = value; if (tag === 'img') stats.imageSources++; }},
    });
    if (text != null) node.textContent = text;
    if (nodeType === 1) created.push(node);
    return node;
  }
  const $ = selector => { if (!nodes.has(selector)) nodes.set(selector, element('div')); return nodes.get(selector); };
  document = {hidden: false, hasFocus: () => true, createElement: element, createDocumentFragment: () => element('#fragment', '', undefined, 11)};
  const c = {$, $$: () => [], el: element, i18nEl: (tag, cls, key) => element(tag, cls, key), t: key => key,
    document, localStorage: {getItem: () => null}, window: {}, AbortController, Promise,
    superCliUI: {fileMutationTools: {}, mutationKind: () => '', createComposerDraftStore: () => ({restore() {}})},
    requestAnimationFrame: () => 1, clearInterval() {}, performance: {now: () => 100},
    prettyJSON: value => { try { return JSON.stringify(JSON.parse(value), null, 2); } catch { return value; }},
    toolHint: (name, args) => ({name, hint: args}), toolDisplayName: name => name,
    clip: (value, length) => String(value).slice(0, length), fmtDuration: () => '0.1s',
    toast: value => notices.push(value), fetch() { throw Error('Unexpected network request'); },
  };
  vm.createContext(c);
  for (const file of ['04-transcript.js', '05-chat.js', '08-sessions.js']) {
    vm.runInContext(fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js', file), 'utf8'), c);
  }
  c.toolHint = (name, args) => ({name, hint: args});
  c.resetWorkerOverview = () => {};
  c.loadSessions = () => {};
  c.renderStats = () => {};
  c.restoreSessionRuntime = () => {};
  c.setSessionOpening = () => {};
  c.stage.scrollTop = 0;
  c.stage.clientHeight = 500;
  Object.defineProperty(c.stage, 'scrollHeight', {get() { return c.stream.children.reduce((sum, row) => sum + row.height, 0); }});
  if (rebuild) {
    // Previous algorithm, using the same renderer/fixtures as the fixed path.
    vm.runInContext(`loadOlderTranscript = async function () {
      var page = await j('/api/transcript?id=' + transcriptSessionID + '&limit=' + transcriptPageSize + '&before=' + transcriptBeforeSeq);
      loadedTranscriptMessages = (page.messages || []).concat(loadedTranscriptMessages);
      transcriptHasMore = !!page.has_more;
      transcriptBeforeSeq = page.before_seq || 0;
      renderLoadedTranscript(true);
    }`, c);
  }
  return {c, created, stats, notices, element};
}
function mediaMessage(seq, extra = {}) {
  return {seq, role: 'tool', name: 'show_media', content: JSON.stringify({type: 'image', path: '/portable/image-' + seq + '.png', media_type: 'image/png'}), ...extra};
}
function messages(first, count) { return Array.from({length: count}, (_, i) => mediaMessage(first + i)); }
function begin(h, initial, hasMore = true) {
  h.c.transcriptSessionID = 'history'; h.c.activeSessionID = 'history';
  h.c.loadedTranscriptMessages = initial; h.c.transcriptHasMore = hasMore;
  h.c.transcriptBeforeSeq = initial[0]?.seq || 1;
  h.c.renderLoadedTranscript(false);
}
async function older(h, page, more = false) {
  h.c.j = async () => ({messages: page, has_more: more, before_seq: page[0]?.seq || 0});
  await h.c.loadOlderTranscript();
  assert.deepEqual(h.notices, []);
}
function expand(row) { row.open = true; row.dispatch('toggle'); }
function top(h, row) { return h.c.stream.children.slice(0, h.c.stream.children.indexOf(row)).reduce((sum, node) => sum + node.height, 0) - h.c.stage.scrollTop; }

test('older pages retain expanded output, image identity, focus, chronology and scroll anchor', async () => {
  const h = harness(); begin(h, messages(61, 60));
  const row = h.c.stream.querySelector('.tool-row'), preview = row._mediaPreview, image = preview.querySelector('img');
  expand(row); const output = row._body.querySelector('pre'); image.focus();
  h.c.stage.scrollTop = 100;
  const anchor = top(h, row), oldImages = h.stats.imageSources;
  await older(h, messages(1, 60));
  assert.equal(h.c.stream.querySelectorAll('.tool-row')[60], row);
  assert.equal(row.open, true); assert.equal(row._body.querySelector('pre'), output);
  assert.equal(row._mediaPreview, preview); assert.equal(preview.querySelector('img'), image);
  assert.equal(h.c.document.activeElement, image); assert.equal(top(h, row), anchor);
  assert.equal(h.stats.imageSources - oldImages, 60);
  assert.deepEqual(Array.from(h.c.loadedTranscriptMessages, m => m.seq), Array.from({length: 120}, (_, i) => i + 1));
  assert.equal(h.c.stream.querySelector('.history-older'), null);
});

test('ten media pages construct each image once rather than rebuilding preceding pages', async t => {
  async function run(rebuild) {
    const h = harness(rebuild); begin(h, messages(541, 60));
    for (let start = 481; start >= 1; start -= 60) await older(h, messages(start, 60), start > 1);
    return {imageSources: h.stats.imageSources, elements: h.created.length, visibleImages: h.c.stream.querySelectorAll('img').length};
  }
  const before = await run(true), after = await run(false);
  assert.equal(before.imageSources, 3300); assert.equal(after.imageSources, 600);
  assert.equal(before.visibleImages, 600); assert.equal(after.visibleImages, 600);
  assert.ok(after.elements < before.elements / 5);
  t.diagnostic(JSON.stringify({pages: 10, pageSize: 60, before, after, scope: 'deterministic construction counts, no browser/network/decoding'}));
});

test('one new page does the same image work with 60 or 600 existing messages', async () => {
  for (const count of [60, 600]) {
    const h = harness(); begin(h, messages(61, count));
    const oldImages = h.stats.imageSources;
    await older(h, messages(1, 60));
    assert.equal(h.stats.imageSources - oldImages, 60);
  }
});

test('boundary tool calls recover arguments without replacing images or expanded output', async () => {
  for (const open of [false, true]) {
    const h = harness(), message = mediaMessage(2, {tool_call_id: 'boundary'}); begin(h, [message]);
    const row = h.c.stream.querySelector('.tool-row'), preview = row._mediaPreview, image = preview.querySelector('img');
    if (open) expand(row);
    const output = row._body.querySelector('pre');
    const args = '{"path":"/portable/image-2.png"}';
    await older(h, [{seq: 1, role: 'assistant', tool_calls: [{id: 'boundary', name: 'show_media', arguments: args}]}]);
    assert.equal(h.c.stream.querySelector('.tool-row'), row); assert.equal(row.open, open);
    assert.equal(row._mediaPreview, preview); assert.equal(preview.querySelector('img'), image);
    assert.equal(row._thint.textContent, args);
    if (!open) assert.equal(row._body.children.length, 0);
    expand(row);
    const blocks = row._body.querySelectorAll('pre');
    assert.equal(blocks.length, 2); assert.equal(blocks[0].textContent, JSON.stringify(JSON.parse(args), null, 2));
    assert.equal(blocks[1].textContent, message.content);
    if (open) assert.equal(blocks[1], output);
    row.open = false; row.dispatch('toggle'); expand(row);
    assert.equal(row._body.querySelectorAll('pre').length, 2);
    assert.equal(h.stats.imageSources, 1);
  }
});

test('missing boundary calls remain resolvable across more than one older page', async () => {
  const h = harness(); begin(h, [mediaMessage(5, {tool_call_id: 'far-call'})]);
  const row = h.c.stream.querySelector('.tool-row'), pager = h.c.stream.querySelector('.history-older');
  await older(h, messages(3, 2), true);
  assert.equal(h.c.stream.querySelector('.history-older'), pager); assert.equal(pager.disabled, false);
  await older(h, [{seq: 1, role: 'assistant', tool_calls: [{id: 'far-call', name: 'show_media', arguments: '{"path":"far.png"}'}]}]);
  expand(row); assert.equal(row._body.querySelectorAll('pre').length, 2);
  assert.equal(row._thint.textContent, '{"path":"far.png"}');
});

test('older task reports cannot redirect progress away from a newer worker row', async () => {
  const h = harness(); begin(h, messages(10, 1));
  const current = h.element('details'); h.c.workerRows.worker = current;
  const content = id => '<task-notification><task-id>' + id + '</task-id><agent>worker</agent><status>done</status><summary>complete</summary><result>old report</result></task-notification>';
  await older(h, [{seq: 1, role: 'tool', name: 'task', content: content('worker')}, {seq: 2, role: 'tool', name: 'task', content: content('other')}]);
  assert.equal(h.c.workerRows.worker, current); assert.ok(h.c.workerRows.other);
  assert.equal(h.c.stream.querySelectorAll('.task-row').length, 2);
});

test('incremental paging never clears separately appended rows or active run state', async () => {
  const h = harness(); begin(h, messages(2, 1));
  const live = h.element('div', 'transcript-live', 'new output'); h.c.stream.appendChild(live);
  const tools = {running: live}, order = ['running'], turn = {tok_total: 10}, workers = ['worker'];
  h.c.toolRows = tools; h.c.openToolOrder = order; h.c.lastTurn = turn; h.c.workersSeen = workers;
  h.c.transcriptLiveAppend = true;
  await older(h, messages(1, 1));
  assert.equal(h.c.stream.children.at(-1), live); assert.equal(h.c.toolRows, tools); assert.equal(h.c.openToolOrder, order);
  assert.equal(h.c.lastTurn, turn); assert.equal(h.c.workersSeen, workers); assert.equal(h.c.transcriptLiveAppend, true);
  assert.equal(h.c.stream.children[0].classList.contains('transcript-live'), false);
});

test('stale or failed requests leave current content intact and allow a retry', async () => {
  const h = harness(); begin(h, messages(2, 1));
  const row = h.c.stream.querySelector('.tool-row'), imageCount = h.stats.imageSources;
  let finish; h.c.j = () => new Promise(resolve => { finish = resolve; });
  const pending = h.c.loadOlderTranscript();
  h.c.transcriptAbortCtl = null;
  finish({messages: messages(1, 1), has_more: false}); await pending;
  assert.equal(h.c.stream.querySelector('.tool-row'), row); assert.equal(h.stats.imageSources, imageCount);
  h.c.j = async () => { throw Error('offline'); }; await h.c.loadOlderTranscript();
  const pager = h.c.stream.querySelector('.history-older');
  assert.equal(pager.disabled, false); assert.equal(h.c.stream.querySelector('.tool-row'), row);
  assert.deepEqual(h.notices, ['common.error: offline']); h.notices.length = 0;
  await older(h, messages(1, 1)); assert.equal(h.c.stream.querySelectorAll('.tool-row').length, 2);
});

test('an empty terminal page removes its pager without moving the old content anchor', async () => {
  const h = harness(); begin(h, messages(2, 1)); h.c.stage.scrollTop = 60;
  const row = h.c.stream.querySelector('.tool-row'), anchor = top(h, row);
  await older(h, []);
  assert.equal(top(h, row), anchor); assert.equal(h.c.stream.querySelector('.history-older'), null);
  assert.equal(h.c.stream.querySelector('.tool-row'), row);
});

test('repeated worker IDs select the latest row in the incoming page unless a newer page already owns it', async () => {
  for (const withNewer of [false, true]) {
    const h = harness(); begin(h, messages(10, 1));
    const current = withNewer ? h.element('details') : null;
    if (current) h.c.workerRows.repeated = current;
    const content = version => '<task-notification><task-id>repeated</task-id><agent>worker</agent><status>done</status><summary>' + version + '</summary><result>' + version + '</result></task-notification>';
    await older(h, [{seq: 1, role: 'tool', name: 'task', content: content('first')}, {seq: 2, role: 'tool', name: 'task', content: content('second')}]);
    const rows = h.c.stream.querySelectorAll('.task-row');
    assert.equal(rows.length, 2);
    assert.equal(h.c.workerRows.repeated, current || rows[1]);
  }
});

test('reused tool-call IDs bind each result to its nearest preceding call across page boundaries', async () => {
  const h = harness(); begin(h, [mediaMessage(5, {tool_call_id: 'reused'})]);
  const latest = h.c.stream.querySelector('.tool-row'); expand(latest);
  const latestOutput = latest._body.querySelector('pre');
  const call = (seq, label) => ({seq, role: 'assistant', tool_calls: [{id: 'reused', name: 'show_media', arguments: JSON.stringify({path: label})}]});
  await older(h, [call(1, 'first'), mediaMessage(2, {tool_call_id: 'reused'}), call(3, 'nearest'), mediaMessage(4, {tool_call_id: 'reused'})]);
  const rows = h.c.stream.querySelectorAll('.tool-row');
  assert.equal(rows[2], latest); assert.equal(latest._body.querySelectorAll('pre')[1], latestOutput);
  assert.deepEqual(rows.map(row => row._thint.textContent), ['{"path":"first"}', '{"path":"nearest"}', '{"path":"nearest"}']);
  rows.forEach(expand);
  assert.deepEqual(rows.map(row => JSON.parse(row._body.querySelector('pre').textContent).path), ['first', 'nearest', 'nearest']);
  assert.equal(h.stats.imageSources, 3);
});
