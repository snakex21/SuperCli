import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript, parseHTML} from './harness.js';
const assets = sourcePath(testDir, '../../internal/webgui/assets');
const read = file => readSource(sourcePath(assets, file), 'utf8');
// The coordinator owns the catalog changes; exercise the agreed translation contract.
const catalog = {...JSON.parse(read('locales/en.json')),
  'cmd.skills': 'Browse available skills', 'skills.title': 'Skills',
  'skills.search': 'Search skills', 'skills.empty': 'No skills found.',
  'skills.hint': 'Use /skills <query> to filter. Skills are loaded by the model only when needed.',
  'skills.source.project': 'Project', 'skills.source.user': 'User',
  'skills.source.builtin': 'Built-in', 'common.more': 'More',
};
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => {resolve = yes; reject = no;});
  return {promise, resolve, reject};
}
function harness() {
  const {document, window} = parseHTML(read('index.html'));
  document.hasFocus = () => true;
  Object.defineProperty(document, 'activeElement', {value: document.body, writable: true});
  window.HTMLElement.prototype.focus = function () {document.activeElement = this;};
  const $ = selector => document.querySelector(selector);
  const el = (tag, cls = '', text = '') => {
    const node = document.createElement(tag); node.className = cls; node.textContent = text;
    return node;
  };
  const requests = [], effects = [], clearedDrafts = [];
  const w = {
    document, window, Event: window.Event, AbortController, console,
    setTimeout, clearTimeout, setInterval, clearInterval,
    localStorage: {getItem: () => null}, activeWorkspacePath: 'C:/project',
    $, $$: selector => Array.from(document.querySelectorAll(selector)), el,
    t: key => {assert.ok(catalog[key], 'missing translation: ' + key); return catalog[key];},
    i18nEl: (tag, cls, key) => {const node = el(tag, cls, w.t(key)); node.setAttribute('data-i18n', key); return node;},
    escHtml: value => String(value).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'),
    superCliUI: {createComposerDraftStore: options => ({
      scope: options.getScope, clear: (...args) => clearedDrafts.push(args),
      restore: () => effects.push(['restoreDraft']),
    })},
    j: (url, options) => {
      assert.match(url, /^\/api\/skills\?/);
      assert.equal(options, undefined, 'skills must be a metadata GET');
      const pending = deferred(); requests.push({url, ...pending}); return pending.promise;
    },
    jpost: (...args) => effects.push(['jpost', ...args]),
    fetch: (...args) => effects.push(['fetch', ...args]),
  };
  createSandbox(w);
  for (const file of ['05-chat.js', '11a-panel-core.js', '11h-panel-files-about.js']) {
    runScript(read('js/' + file), w, {filename: file});
  }
  for (const name of ['sendPrompt', 'enqueuePrompt', 'interruptAndSend', 'sendAfterRun']) {
    w[name] = (...args) => {effects.push([name, ...args]); return true;};
  }
  w.clearAttachments = () => {effects.push(['clearAttachments']); w.pendingAttachments = [];};
  const event = (node, type, props = {}) => {
    const e = new window.Event(type, {bubbles: true, cancelable: true});
    Object.assign(e, props); node.dispatchEvent(e); return e;
  };
  const submit = text => {$('#prompt').value = text; return event($('#composer'), 'submit');};
  $('#composer').requestSubmit = () => event($('#composer'), 'submit');
  const page = (index, items, total = items.length, offset = 0) => {
    requests[index].resolve({items, total, offset, limit: 50});
  };
  const button = key => $('#panel-content').querySelector('button[data-i18n="' + key + '"]');
  const query = index => new URL(requests[index].url, 'http://localhost').searchParams;
  return {w, $, requests, effects, clearedDrafts, event, submit, page, button, query};
}
function skill(name, source = 'builtin') {
  return {name, description: 'Description of ' + name, category: 'coding', tags: ['local', 'safe'], risk: 'low', source};
}

test('/skills browses metadata without a session, escapes text, and never reads bodies', async () => {
  const h = harness();
  h.submit('  /skills  ');
  assert.equal(h.w.activeSessionID, '');
  assert.equal(h.w.overlay.hidden, false);
  assert.equal(h.$('#panel-title').textContent, 'Skills');
  assert.equal(h.$('#skills-search'), h.w.document.activeElement);
  assert.deepEqual([...h.query(0)], [['q', ''], ['offset', '0'], ['limit', '50']]);
  assert.match(h.$('#panel-content').textContent, /Loading/);
  const item = skill('<img src=x onerror=bad()>', 'project');
  item.description = '<script>bad()</script>';
  Object.defineProperty(item, 'body', {get() {throw new Error('skill bodies must not be read');}});
  h.page(0, [item, skill('user-skill', 'user'), skill('builtin-skill')]);
  await tick();
  const list = h.$('.skills-list');
  assert.equal(list.children.length, 3);
  assert.equal(list.querySelector('img, script'), null);
  assert.match(list.textContent, /<img src=x onerror=bad\(\)>/);
  assert.match(list.textContent, /<script>bad\(\)<\/script>/);
  assert.match(list.textContent, /coding · local, safe · low/);
  for (const label of ['Project', 'User', 'Built-in']) assert.ok(list.textContent.includes(label));
  assert.match(h.$('#panel-content').textContent, /Skills are loaded by the model only when needed/);
  assert.equal(h.button('common.more').hidden, true);
  assert.deepEqual(h.effects, []);
  assert.deepEqual(h.clearedDrafts, [['new:C:/project', '  /skills  ']]);
  assert.equal(h.$('#prompt').value, '');
});

test('/skills <query> is URL encoded and local for submit and interrupt in every run/queue state', async () => {
  for (const action of ['submit', 'interrupt']) {
    for (const state of ['idle', 'running', 'finishing', 'handoff']) {
      const h = harness(), w = h.w;
      w.activeSessionID = state === 'idle' ? '' : 's1';
      w.streaming = state !== 'idle'; w.runFinishing = state === 'finishing';
      w.queueDispatching = state === 'handoff';
      const run = w.activeRun = {sessionID: 's1'};
      const attachments = w.pendingAttachments = ['C:/keep.png'];
      const queue = w.promptQueue = [{id: 'keep', text: 'queued task'}];
      const immediate = w.pendingImmediate = state === 'handoff' ? {text: 'handoff'} : null;
      const dispatchItem = w.queueDispatchItem = state === 'handoff' ? queue[0] : null;
      w.pauseQueue = true; w.abortCtl = new AbortController();
      w.runStopRevision = 7;
      const text = ' /skills  plan & review + café?# ';
      if (action === 'submit') h.submit(text);
      else {h.$('#prompt').value = text; h.event(h.$('#interrupt-btn'), 'click');}
      assert.equal(h.requests.length, 1, action + ': ' + state);
      assert.equal(h.query(0).get('q'), 'plan & review + café?#');
      assert.equal(w.streaming, state !== 'idle');
      assert.equal(w.runFinishing, state === 'finishing');
      assert.equal(w.queueDispatching, state === 'handoff');
      assert.equal(w.activeRun, run);
      assert.equal(w.pendingImmediate, immediate);
      assert.equal(w.queueDispatchItem, dispatchItem);
      assert.equal(w.pendingAttachments, attachments);
      assert.equal(w.promptQueue, queue);
      assert.equal(w.pauseQueue, true);
      assert.equal(w.runStopRevision, 7);
      assert.equal(w.abortCtl.signal.aborted, false);
      assert.deepEqual(h.effects, [], action + ': ' + state);
      h.page(0, []); await tick();
    }
  }
});

test('slash suggestion/help is localized; click and Tab complete, Enter opens skills locally', async () => {
  const h = harness(), prompt = h.$('#prompt');
  prompt.value = '/'; h.event(prompt, 'input');
  const suggestions = h.$('#slash-suggestions');
  assert.equal(suggestions.hidden, false);
  assert.match(suggestions.textContent, /\/skills — Browse available skills/);
  assert.ok(suggestions.querySelector('[data-i18n="cmd.skills"]'));
  h.event(suggestions.querySelector('button'), 'click');
  assert.equal(prompt.value, '/skills ');
  assert.equal(suggestions.hidden, true);
  prompt.value = '/sk'; h.event(prompt, 'input');
  assert.equal(h.event(prompt, 'keydown', {key: 'Tab'}).defaultPrevented, true);
  assert.equal(prompt.value, '/skills ');
  prompt.value += 'filesystem';
  h.event(prompt, 'keydown', {key: 'Enter', shiftKey: false});
  assert.equal(h.query(0).get('q'), 'filesystem');
  assert.equal(suggestions.hidden, true);
  assert.deepEqual(h.effects, []);
  h.page(0, []); await tick();
});

test('normal messages and similarly named slash text retain the existing send/queue paths', async () => {
  for (const text of ['hello', '/skills-extra']) {
    for (const streaming of [false, true]) {
      const h = harness(); h.w.streaming = streaming;
      h.submit(text); await tick();
      assert.equal(h.requests.length, 0);
      assert.deepEqual(h.effects.map(call => call[0]), streaming ? ['enqueuePrompt'] : ['clearAttachments', 'sendPrompt']);
      assert.equal(h.effects.at(-1)[1], text);
    }
  }
});

test('skills pagination uses the API offset and limit, appends metadata, and stops at total', async () => {
  const h = harness(); h.submit('/skills coding');
  h.page(0, Array.from({length: 50}, (_, i) => skill('skill-' + i)), 52);
  await tick();
  assert.equal(h.$('.skills-list').children.length, 50);
  assert.equal(h.button('common.more').hidden, false);
  h.event(h.button('common.more'), 'click');
  assert.equal(h.button('common.more').disabled, true);
  assert.deepEqual([...h.query(1)], [['q', 'coding'], ['offset', '50'], ['limit', '50']]);
  h.page(1, [skill('skill-50'), skill('skill-51')], 52, 50);
  await tick();
  assert.equal(h.$('.skills-list').children.length, 52);
  assert.match(h.$('#panel-content').textContent, /52 \/ 52/);
  assert.equal(h.button('common.more').hidden, true);
  assert.deepEqual(h.effects, []);
});

test('changing the search resets pagination and rejects older success/error responses', async () => {
  for (const staleError of [false, true]) {
    const h = harness(); h.submit('/skills old');
    h.page(0, [skill('old-page')], 2); await tick();
    h.event(h.button('common.more'), 'click');
    const search = h.$('#skills-search'); search.value = 'new & query'; h.event(search, 'input');
    assert.equal(h.$('.skills-list').children.length, 0);
    assert.equal(h.query(2).get('q'), 'new & query');
    assert.equal(h.query(2).get('offset'), '0');
    h.page(2, [skill('current')]); await tick();
    if (staleError) h.requests[1].reject(new Error('stale failure'));
    else h.page(1, [skill('stale-page')], 2, 1);
    await tick();
    assert.equal(h.$('.skills-list').children.length, 1);
    assert.match(h.$('.skills-list').textContent, /current/);
    assert.doesNotMatch(h.$('#panel-content').textContent, /stale/);
    assert.equal(h.button('common.refresh').hidden, true);
  }
});

test('closed skills panels ignore responses and reopening creates a fresh request and owner', async () => {
  const h = harness(); h.submit('/skills closed');
  const oldRoot = h.$('.skills-panel'), before = oldRoot.innerHTML;
  h.w.closePanel(); h.page(0, [skill('must-not-render')]); await tick();
  assert.equal(oldRoot.innerHTML, before);
  assert.equal(h.w.overlay.hidden, true);
  h.submit('/skills stale'); h.w.closePanel(); h.submit('/skills current');
  assert.notEqual(h.$('.skills-panel'), oldRoot);
  h.page(2, [skill('current-result')]); await tick();
  h.requests[1].reject(new Error('closed failure')); await tick();
  assert.match(h.$('.skills-list').textContent, /current-result/);
  assert.doesNotMatch(h.$('#panel-content').textContent, /closed failure/);
  assert.equal(h.$('#panel-nav').querySelectorAll('[data-sec="skills"]').length, 1);
});

test('switching panel sections prevents pending skills responses from replacing that section', async () => {
  const h = harness(); h.submit('/skills slow'); await tick();
  h.w.sections.settings = () => {h.w.panelContent.textContent = 'Settings content';};
  h.w.showSection('settings');
  h.page(0, [skill('stale')]); await tick();
  assert.equal(h.w.currentSection, 'settings');
  assert.equal(h.w.panelContent.textContent, 'Settings content');
  assert.equal(h.$('#panel-title').textContent, 'Settings');
});

test('metadata errors offer refresh; retry preserves the page and empty results give a useful hint', async () => {
  const h = harness(); h.submit('/skills missing');
  h.requests[0].reject(new Error('<metadata unavailable>')); await tick();
  assert.match(h.$('.skills-panel [role="status"]').textContent, /error: <metadata unavailable>/);
  assert.equal(h.button('common.refresh').hidden, false);
  assert.equal(h.button('common.more').hidden, true);
  h.event(h.button('common.refresh'), 'click');
  assert.equal(h.query(1).get('offset'), '0');
  h.page(1, []); await tick();
  assert.match(h.$('#panel-content').textContent, /No skills found/);
  assert.match(h.$('#panel-content').textContent, /Use \/skills <query> to filter/);
  assert.equal(h.button('common.refresh').hidden, true);
  const search = h.$('#skills-search'); search.value = ''; h.event(search, 'input');
  h.page(2, [skill('first')], 2); await tick();
  h.event(h.button('common.more'), 'click');
  h.requests[3].reject(new Error('page failed')); await tick();
  h.event(h.button('common.refresh'), 'click');
  assert.equal(h.query(4).get('q'), '');
  assert.equal(h.query(4).get('offset'), '1');
  h.page(4, [skill('second')], 2, 1); await tick();
  assert.equal(h.$('.skills-list').children.length, 2);
  assert.equal(h.button('common.more').hidden, true);
});
