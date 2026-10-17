'use strict';
import {assert, test, readSource, readDir, sourcePath, testDir, createSandbox, runScript, parseHTML} from './harness.js';
const assets = sourcePath(testDir, '../../internal/webgui/assets');
const source = readSource(sourcePath(assets, 'js/07-stats.js'), 'utf8');
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
function fixture() {
  const {document, window} = parseHTML('<html><body><div id="box"></div></body></html>');
  const errors = [], posts = [];
  const w = createSandbox({document, Promise, t: k => k, toast: e => errors.push(e),
    el(tag, cls, text) { const n = document.createElement(tag); n.className = cls || ''; n.textContent = text || ''; return n; },
    jpost: async (url, body) => { posts.push({url, body}); }, renderStats: () => {},
    modelCache: [{id: 'small'}, {id: 'big', provider: 'cloud'}], activeProviderID: 'local'});
  runScript(source, w);
  w.renderStats = () => {}; // the script's own renderStats needs the full page
  w.i18nEl = (tag, cls, key) => { const n = w.el(tag, cls, key); n.dataset.key = key; return n; };
  const box = document.getElementById('box');
  const click = n => n.dispatchEvent(new window.Event('click', {bubbles: true}));
  return {w, box, click, errors, posts};
}
function knobs(state, models, pool) {
  return [{key: 'darwin', state}, {key: 'darwin_models', raw: models || ''},
    {key: 'darwin_pool_size', raw: pool || ''}, {key: 'darwin_reasoning_effort', raw: ''}];
}
const labels = box => [...box.querySelectorAll('.mode-row > span:first-child')].map(n => n.dataset.key);

test('Darwin model and instance rows appear only when Darwin is on, in order', () => {
  const h = fixture();
  h.w.appendModesSection(h.box, {knobs: knobs('off')});
  assert.deepEqual(labels(h.box), ['stats.darwinTool']);
  h.box.replaceChildren();
  h.w.appendModesSection(h.box, {knobs: knobs('on')});
  assert.deepEqual(labels(h.box), ['stats.darwinTool', 'stats.darwinModel', 'stats.darwinPool', 'effort.darwin']);
  const rows = [...h.box.querySelectorAll('.mode-row')];
  assert.equal(rows[1].querySelector('.orch-btn').textContent, 'stats.darwinModelDef');
  assert.equal(rows[2].title, 'stats.darwinPoolTip');
  assert.equal(rows[2].querySelector('.orch-btn').textContent, 'stats.darwinPoolAuto');
});

test('Darwin model picker saves one entry and shows a multi-model list as-is', async () => {
  const h = fixture();
  h.w.appendModesSection(h.box, {knobs: knobs('on', 'small ; cloud/big')});
  const row = h.box.querySelectorAll('.mode-row')[1];
  const btn = row.querySelector('.orch-btn');
  assert.equal(btn.textContent, 'small ; cloud/big');
  h.click(btn);
  const items = [...row.querySelectorAll('.prow')];
  assert.equal(items.filter(n => n.classList.contains('active')).length, 0);
  h.click(items.find(n => n.textContent.includes('big')));
  await tick();
  assert.deepEqual(JSON.parse(JSON.stringify(h.posts.map(p => p.body))), [{key: 'darwin_models', value: 'cloud/big'}]);
});

test('Darwin instance picker offers auto + 1..10 and saves the number', async () => {
  const h = fixture();
  h.w.appendModesSection(h.box, {knobs: knobs('on', '', '4')});
  const row = h.box.querySelectorAll('.mode-row')[2];
  const btn = row.querySelector('.orch-btn');
  assert.equal(btn.textContent, '4');
  const items = [...row.querySelectorAll('.prow')];
  assert.deepEqual(items.map(n => n.textContent), ['stats.darwinPoolAuto', '1', '2', '3', '4', '5', '6', '7', '8', '9', '10']);
  h.click(btn); h.click(items[2]); await tick();
  h.click(btn); h.click(items[0]); await tick();
  assert.deepEqual(JSON.parse(JSON.stringify(h.posts.map(p => p.body))), [{key: 'darwin_pool_size', value: '2'}, {key: 'darwin_pool_size', value: ''}]);
  assert.equal(btn.textContent, 'stats.darwinPoolAuto');
  assert.deepEqual(h.errors, []);
});

test('Darwin toggle highlights immediately, prevents duplicate saves and waits for refresh', async () => {
  const h = fixture();
  let finishSave, finishRefresh, refreshes = 0;
  h.w.jpost = (url, body) => {
    h.posts.push({url, body});
    return new Promise(resolve => { finishSave = resolve; });
  };
  h.w.renderStats = () => {
    refreshes++;
    return new Promise(resolve => { finishRefresh = resolve; });
  };
  h.w.appendModesSection(h.box, {knobs: knobs('off')});
  const seg = h.box.querySelector('.seg'), [on, off] = seg.querySelectorAll('button');
  h.click(off);
  assert.equal(h.posts.length, 0);
  h.click(on);
  assert.ok(on.classList.contains('on'));
  assert.ok(!off.classList.contains('on'));
  assert.equal(on.getAttribute('aria-pressed'), 'true');
  assert.equal(off.getAttribute('aria-pressed'), 'false');
  assert.equal(seg.getAttribute('aria-busy'), 'true');
  assert.ok(on.disabled && off.disabled);
  h.click(on); h.click(off);
  assert.equal(h.posts.length, 1);
  assert.deepEqual(JSON.parse(JSON.stringify(h.posts[0])), {url: '/api/config', body: {key: 'darwin', value: 'on'}});
  assert.equal(refreshes, 0);
  finishSave(); await tick();
  assert.equal(refreshes, 1);
  assert.ok(on.classList.contains('on'));
  assert.ok(on.disabled && off.disabled);
  finishRefresh(); await tick();
  assert.equal(seg.getAttribute('aria-busy'), 'false');
  assert.ok(!on.disabled && !off.disabled);
  h.click(on);
  assert.equal(h.posts.length, 1);
  h.click(off);
  assert.ok(off.classList.contains('on'));
  assert.equal(h.posts[1].body.value, 'default');
  finishSave(); await tick(); finishRefresh(); await tick();
  assert.deepEqual(h.errors, []);
});

test('failed Darwin toggle restores the old state and permits retry', async () => {
  const h = fixture();
  h.w.appendModesSection(h.box, {knobs: knobs('default')});
  const [on, off] = h.box.querySelectorAll('.seg button');
  const save = h.w.jpost;
  h.w.jpost = () => Promise.reject(Error('offline'));
  h.click(on);
  assert.ok(on.classList.contains('on'));
  await tick();
  assert.ok(off.classList.contains('on'));
  assert.equal(on.getAttribute('aria-pressed'), 'false');
  assert.equal(off.getAttribute('aria-pressed'), 'true');
  assert.ok(!on.disabled && !off.disabled);
  assert.deepEqual(h.errors, ['offline']);
  h.w.jpost = save;
  h.click(on); await tick();
  assert.ok(on.classList.contains('on'));
  assert.equal(h.posts.length, 1);
});

test('a failed stats refresh does not undo a saved Darwin toggle', async () => {
  const h = fixture();
  h.w.renderStats = () => Promise.reject(Error('stats unavailable'));
  h.w.appendModesSection(h.box, {knobs: knobs('on')});
  const [on, off] = h.box.querySelectorAll('.seg button');
  h.click(off); await tick();
  assert.ok(off.classList.contains('on'));
  assert.ok(!on.disabled && !off.disabled);
  assert.equal(h.posts[0].body.value, 'default');
  assert.deepEqual(h.errors, ['stats unavailable']);
});

test('Darwin model/instance copy exists in all 27 GUI and TUI catalogs', () => {
  const files = readDir(sourcePath(assets, 'locales')).filter(f => f.endsWith('.json'));
  assert.equal(files.length, 27);
  for (const file of files) {
    const gui = JSON.parse(readSource(sourcePath(assets, 'locales', file), 'utf8'));
    for (const key of ['stats.darwinModel', 'stats.darwinModelDef', 'stats.darwinPool', 'stats.darwinPoolAuto', 'stats.darwinPoolTip', 'setting.darwin_pool_size.label', 'setting.darwin_pool_size.desc'])
      assert.ok(gui[key] && gui[key].trim(), file + ': ' + key);
    const tui = JSON.parse(readSource(sourcePath(assets, '../../system/uilang/catalogs', file), 'utf8'));
    for (const key of ['tui.setting_darwin_pool_size.label', 'tui.setting_darwin_pool_size.desc', 'tui.setting_darwin_pool_size.auto'])
      assert.ok(tui[key] && tui[key].trim(), file + ': ' + key);
  }
  const pl = JSON.parse(readSource(sourcePath(assets, 'locales/pl.json'), 'utf8'));
  assert.equal(pl['stats.darwinModel'], 'Model Darwina');
  assert.equal(pl['stats.darwinModelDef'], 'jak główny model');
  assert.equal(pl['stats.darwinPool'], 'Liczba instancji');
  assert.equal(pl['stats.darwinPoolAuto'], 'auto (model wybiera, zwykle 3)');
  assert.equal(pl['stats.darwinPoolTip'], 'Ile niezależnych prób uruchamia Darwin. Każda kosztuje osobno.');
});
