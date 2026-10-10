const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js/10-models.js'), 'utf8');

function harness() {
  function node(tag = 'div') {
    const n = {tag, children: [], events: {}, hidden: false, value: '', textContent: '', dataset: {},
      appendChild(child) { this.children.push(child); return child; },
      addEventListener(name, fn) { this.events[name] = fn; },
      setAttribute() {}, removeAttribute() {}, focus() {}, contains() { return false; },
      classList: {toggle() {}},
    };
    Object.defineProperty(n, 'innerHTML', {get() { return ''; }, set() { this.children = []; }});
    return n;
  }
  const nodes = new Map(), calls = [];
  const $ = selector => { if (!nodes.has(selector)) nodes.set(selector, node()); return nodes.get(selector); };
  $('#palette').hidden = true;
  function el(tag, cls, text) { const n = node(tag); n.className = cls || ''; n.textContent = text || ''; return n; }
  const c = {$, $$: () => [], el, i18nEl: el, t: key => key, document: {addEventListener() {}},
    activeModelID: 'model-0', activeProviderID: 'local', activeSessionID: '', projectEpoch: 0,
    sessionRuntimeReady: Promise.resolve(), sessionByID: {},
    fmtTok: String, selectedModelID: String, modelDisplayName: String, saveBlobKey() {},
    toast() {}, checkHealth() {}, setTimeout() {},
    jpost: async (url, value) => { calls.push({url, value}); return {}; },
  };
  vm.createContext(c); vm.runInContext(source, c);
  const originalLoadModels = c.loadModels;
  c.loadModels = async () => { calls.push({url: '/api/models'}); };
  c.loadReasoning = () => {};
  c.modelCache = Array.from({length: 1000}, (_, i) => ({id: 'model-' + i, provider: 'local', hidden: i === 999, context_length: 100000}));
  return {c, $, calls, originalLoadModels};
}

test('a closed picker retains model metadata without creating rows and opening restores search and selection', async () => {
  const {c, $, calls} = harness();
  c.renderModelList('');
  assert.equal($('#model-list').children.length, 0);
  assert.equal(c.modelCache.length, 1000);
  assert.equal($('#model-context-target').textContent, 'local / model-0');
  assert.equal(calls.length, 0);
  c.togglePalette(true);
  assert.equal($('#model-list').children.length, 999);
  c.renderModelList('model-998');
  assert.equal($('#model-list').children.length, 1);
  $('#model-list').children[0].events.click();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(c.palette.hidden, true);
  assert.equal($('#model-list').children.length, 0);
  assert.ok(calls.some(call => call.url === '/api/model' && call.value.model === 'model-998' && call.value.provider === 'local'));
  c.togglePalette(true);
  assert.equal($('#model-list').children.length, 999);
  c.togglePalette(false);
  c.renderModelList('');
  assert.equal($('#model-list').children.length, 0, 'a late refresh cannot rebuild hidden rows');
  assert.equal(c.modelCache.length, 1000);
});

test('default and hide actions remain available when the picker is visible', async () => {
  const {c, $, calls} = harness();
  c.togglePalette(true);
  const row = $('#model-list').children[0];
  const actions = row.children[row.children.length - 1];
  const event = {stopPropagation() {}};
  actions.children[0].events.click(event);
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(calls.some(call => call.url === '/api/model/default' && call.value.model === 'model-0'));
  assert.equal(c.palette.hidden, false);
  actions.children[1].events.click(event);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(c.modelCache[0].hidden, true);
  assert.equal($('#model-list').children.length, 998);
});

test('model selection refreshes stats immediately without waiting for the catalog', async () => {
  const {c, $, calls} = harness();
  let stats = 0;
  c.renderStats = () => { stats++; };
  c.loadModels = () => new Promise(() => {});
  c.togglePalette(true);
  c.renderModelList('model-998');
  $('#model-list').children[0].events.click();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(c.activeModelID, 'model-998');
  assert.equal(c.activeProviderID, 'local');
  assert.equal($('#model-name').textContent, 'model-998');
  assert.equal(stats, 1);
  assert.ok(calls.some(call => call.url === '/api/model'));
});

test('late catalog cannot restore an old selection or overwrite a saved manual budget', async () => {
  const {c, $, originalLoadModels} = harness();
  let finish, stats = 0;
  c.j = () => new Promise(resolve => {finish = resolve;});
  c.renderStats = () => {stats++;};
  const old = originalLoadModels();
  c.showSelectedModel('model-1', 'local');
  finish({active: 'model-0', provider: 'local', models: [{id: 'model-0', provider: 'local'}]});
  await old;
  assert.equal(c.activeModelID, 'model-1');
  assert.equal(c.modelCache.length, 1000);
  assert.equal(stats, 1);

  const beforeSave = originalLoadModels();
  c.jpost = async () => ({tokens: 64000, automatic: false});
  $('#model-context-input').value = '64k';
  c.saveActiveModelContext();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(c.activeModelEntry().manual_context_length, 64000);
  assert.equal(stats, 2);
  finish({active: 'model-1', provider: 'local', models: [{id: 'model-1', provider: 'local'}]});
  await beforeSave;
  assert.equal(c.activeModelEntry().manual_context_length, 64000);
});

test('authoritative session model refresh updates stats once and repeated identical catalogs do not', async () => {
  const {c, originalLoadModels} = harness();
  let stats = 0;
  c.renderStats = () => {stats++;};
  c.j = async () => ({active: 'session-model', provider: 'session-provider', reasoning: {},
    models: [{id: 'session-model', provider: 'session-provider', context_length: 48000}]});
  await originalLoadModels();
  assert.equal(c.activeModelID, 'session-model');
  assert.equal(c.activeProviderID, 'session-provider');
  assert.equal(stats, 1);
  await originalLoadModels();
  assert.equal(stats, 1);
});

test('catalog replacement during a manual save updates the current cache entry', async () => {
  const {c, $, originalLoadModels} = harness();
  let saved;
  c.jpost = () => new Promise(resolve => {saved = resolve;});
  $('#model-context-input').value = '64k';
  c.saveActiveModelContext();
  c.j = async () => ({active: 'model-0', provider: 'local', reasoning: {},
    models: [{id: 'model-0', provider: 'local', context_length: 128000}]});
  await originalLoadModels();
  saved({tokens: 64000, automatic: false});
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(c.activeModelEntry().manual_context_length, 64000);
  assert.equal($('#model-context-input').value, '64k');
});
