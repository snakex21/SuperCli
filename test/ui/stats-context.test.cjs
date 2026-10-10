'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const {parseHTML} = require('linkedom');

const assets = path.resolve(__dirname, '../../internal/webgui/assets');
const catalog = JSON.parse(fs.readFileSync(path.join(assets, 'locales/en.json'), 'utf8'));
function fixture(t) {
  const {document} = parseHTML('<html><body><div id="stats-block"></div></body></html>');
  const w = vm.createContext({document, window: {}});
  Object.assign(w, {ui: {lang: 'en'}, activeSessionID: 'first', activeModelID: 'fixture-model',
    lastTurn: null, workersSeen: [], normalizeLanguage: value => value,
    t: key => catalog[key] || key, generationSpeedText: () => '',
  });
  vm.runInContext(fs.readFileSync(path.join(assets, 'js/00-helpers.js'), 'utf8'), w);
  w.i18nEl = (tag, cls, key) => w.el(tag, cls, w.t(key));
  vm.runInContext(fs.readFileSync(path.join(assets, 'js/07-stats.js'), 'utf8'), w);
  w.j = async url => url === '/api/config' ? {knobs: []} : snapshot(50);
  return {w, box: w.document.querySelector('#stats-block')};
}
function snapshot(percent, overrides = {}) {
  return {session: {model: 'fixture-model'}, tokens: {total: 100}, context: {
    window: 32768, estimated_used: Math.round(percent * 32768 / 100), percent,
    has_snapshot: true, window_source: 'model-override', ...overrides,
  }};
}
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => {resolve = yes; reject = no;});
  return {promise, resolve, reject};
}

test('context stays visible without a window or before the first usage snapshot', t => {
  const {w, box} = fixture(t);
  for (const raw of [null, {}, snapshot(0, {window: 0}).context,
    snapshot(0, {has_snapshot: false}).context]) {
    box.replaceChildren();
    const context = raw === null ? null : w.normalizeStats({context: raw}).context;
    w.appendCompactContext(box, context);
    assert.match(box.textContent, /(?:Current context|context \(last turn\)):/);
    assert.equal(box.querySelector('[role="progressbar"]'), null, 'unknown extent must not claim a percent');
    assert.doesNotMatch(box.textContent, /NaN|undefined|Infinity/);
  }
  assert.match(box.textContent, /— \/ 32/);
  assert.equal(box.querySelector('.stats-note').title, catalog['usage.contextEmpty']);
});

test('known context renders the snapshot and preserves legacy API compatibility', t => {
  const {w, box} = fixture(t);
  const raw = snapshot(25).context;
  delete raw.has_snapshot;
  w.appendCompactContext(box, w.normalizeStats({context: raw}).context);
  const meter = box.querySelector('[role="progressbar"]');
  assert.equal(meter.getAttribute('aria-valuenow'), '25');
  assert.equal(meter.firstChild.style.width, '25%');
  assert.match(meter.getAttribute('aria-valuetext'), /25%.*8,192.*32,768/);
});

test('an unresolved backend window is marked as an estimate', t => {
  const {w, box} = fixture(t);
  w.appendCompactContext(box, w.normalizeStats(snapshot(25, {window_source: 'fallback'})).context);
  assert.match(box.querySelector('.stats-note').textContent, /\/ ~32/);
  assert.match(box.querySelector('[role="progressbar"]').getAttribute('aria-valuetext'), /\/ ~32,768/);
});

test('same-session refresh retains the visible meter until the response completes', async t => {
  const {w, box} = fixture(t);
  await w.renderStats();
  const previous = box.querySelector('[role="progressbar"]');
  const waiting = deferred();
  w.j = url => url === '/api/config' ? Promise.resolve({knobs: []}) : waiting.promise;
  const done = w.renderStats();
  assert.equal(box.querySelector('[role="progressbar"]'), previous);
  assert.equal(box.getAttribute('aria-busy'), 'true');
  waiting.resolve(snapshot(60));
  await done;
  assert.equal(box.querySelector('[role="progressbar"]').getAttribute('aria-valuenow'), '60');
  assert.equal(box.getAttribute('aria-busy'), 'false');
});

test('session switch clears the old meter and ignores stale success or failure', async t => {
  for (const fail of [false, true]) {
    const {w, box} = fixture(t);
    await w.renderStats();
    const old = deferred(), current = deferred();
    w.j = url => url === '/api/config' ? Promise.resolve({knobs: []})
      : url.includes('session=second') ? current.promise : old.promise;
    const firstDone = w.renderStats();
    w.activeSessionID = 'second';
    const secondDone = w.renderStats();
    assert.equal(box.querySelector('[role="progressbar"]'), null);
    assert.match(box.textContent, /Current context: — \/ —/, 'new conversation shows its own pending placeholder');
    current.resolve(snapshot(10));
    await secondDone;
    if (fail) old.reject(Error('old request failed')); else old.resolve(snapshot(90));
    await firstDone;
    assert.equal(box.querySelector('[role="progressbar"]').getAttribute('aria-valuenow'), '10');
    assert.doesNotMatch(box.textContent, /old request failed/);
    assert.equal(box.getAttribute('aria-busy'), 'false');
  }
});

test('failed stats response keeps a visible context placeholder', async t => {
  const {w, box} = fixture(t);
  w.j = async url => {if (url === '/api/config') return {knobs: []}; throw Error('unavailable');};
  await w.renderStats();
  assert.match(box.textContent, /Current context: — \/ —/);
  assert.match(box.textContent, /unavailable/);
  assert.equal(box.getAttribute('aria-busy'), 'false');
});

test('selected model and manual limit are distinct from the last request snapshot', async t => {
  const {w, box} = fixture(t);
  w.activeModelID = 'selected-model';
  w.activeProviderID = 'selected-provider';
  w.lastTurn = {ev: {model: 'completed-model', tok_total: 100}, tools: 0};
  for (const active of [
    {model: 'selected-model', provider: 'selected-provider', window: 65536, window_source: 'catalog'},
    {model: 'fixture-model', provider: 'other-provider', window: 32768, window_source: 'model-override'},
    {model: 'fixture-model', provider: '', window: 8192, window_source: 'model-override'},
    {model: 'selected-model', provider: 'selected-provider', window: 16384, window_source: 'fallback'},
  ]) {
    w.j = async url => url === '/api/config' ? {knobs: []} : {...snapshot(25), active_context: active};
    await w.renderStats();
    const meter = box.querySelector('[role="progressbar"]');
    assert.equal(meter.getAttribute('aria-valuenow'), '25');
    assert.match(meter.getAttribute('aria-valuetext'), /8,192 \/ 32,768/);
    assert.match(box.textContent, /context \(last turn\): 25%/);
    const selected = box.querySelector('.stats-active-context');
    assert.ok(selected);
    assert.ok(selected.textContent.includes(active.model));
    assert.ok(selected.title.includes(w.fmtInteger(active.window)));
    assert.equal(box.querySelector('.stat-row .v').textContent, 'completed-model', 'last turn must retain its own model even if new usage arrives');
    if (active.window_source === 'fallback') assert.match(selected.textContent, /~16/);
  }
  w.j = async url => url === '/api/config' ? {knobs: []} : {...snapshot(25),
    active_context: {model: 'fixture-model', provider: '', window: 32768}};
  await w.renderStats();
  assert.equal(box.querySelector('.stats-active-context'), null, 'matching request/selection needs no duplicate budget line');
});

test('model or provider changes reject pending old stats and configuration', async t => {
  for (const change of ['model', 'provider']) {
    const {w, box} = fixture(t);
    w.activeProviderID = 'first-provider';
    await w.renderStats();
    assert.ok(box.querySelector('[role="progressbar"]'), box.textContent);
    const oldStats = deferred(), oldConfig = deferred();
    w.j = url => url === '/api/config' ? oldConfig.promise : oldStats.promise;
    const oldDone = w.renderStats();
    if (change === 'model') w.activeModelID = 'second-model';
    else w.activeProviderID = 'second-provider';
    // The selection guard also protects the gap before a refresh is started.
    oldStats.resolve(snapshot(90));
    oldConfig.resolve({knobs: [{key: 'orchestrator', state: 'off'}]});
    await oldDone;
    assert.equal(box.querySelector('[role="progressbar"]').getAttribute('aria-valuenow'), '50');
    const current = deferred();
    w.j = url => url === '/api/config' ? Promise.resolve({knobs: []}) : current.promise;
    const done = w.renderStats();
    assert.ok(box.querySelector('.stats-active-context').textContent.includes(w.activeModelID));
    current.resolve({...snapshot(20), active_context: {model: w.activeModelID, provider: w.activeProviderID,
      window: 64000, window_source: 'model-override'}});
    await done;
    assert.equal(box.querySelector('[role="progressbar"]').getAttribute('aria-valuenow'), '20');
    assert.ok(box.querySelector('.stats-active-context').title.includes(w.activeProviderID));
    assert.equal(box.getAttribute('aria-busy'), 'false');
  }
});
