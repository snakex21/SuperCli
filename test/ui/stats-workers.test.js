'use strict';
import {assert, test, readSource, readDir, sourcePath, testDir, createSandbox, runScript, parseHTML} from './harness.js';
const assets = sourcePath(testDir, '../../internal/webgui/assets');
const read = name => readSource(sourcePath(assets, name), 'utf8');
const catalog = JSON.parse(read('locales/en.json'));

function fixture() {
  const {document, window} = parseHTML('<html><body><div id="stats-block"></div></body></html>');
  Object.defineProperty(window.HTMLSelectElement.prototype, 'value', {configurable: true,
    get() { return this._value || ''; }, set(value) { this._value = value; }});
  const posts = [], errors = [];
  const knobs = [
    {key: 'orchestrator', state: 'default'},
    {key: 'orchestrator_model', raw: 'old/coordinator'},
    {key: 'task_model', raw: ''},
    {key: 'task_reasoning_effort', raw: ''},
  ];
  const w = createSandbox({document, window: {}, ui: {lang: 'en'},
    activeSessionID: 'session', activeModelID: 'gpt-5', lastTurn: null, workersSeen: [],
    t: key => catalog[key] || key, normalizeLanguage: value => value,
    setTimeout() {throw Error('unexpected timer');}, setInterval() {throw Error('unexpected poll');},
  });
  runScript(read('js/00-helpers.js'), w);
  runScript(read('js/07-stats.js'), w);
  w.i18nEl = (tag, cls, key) => w.el(tag, cls, w.t(key));
  w.toast = err => errors.push(err);
  w.j = async url => {
    if (url === '/api/config') return {knobs};
    if (url.startsWith('/api/stats')) return {};
    throw Error('unexpected URL: ' + url);
  };
  w.jpost = async (url, body) => {
    posts.push({url, body}); knobs.find(k => k.key === body.key).raw = body.value;
    return {knobs: JSON.parse(JSON.stringify(knobs)), cost_currencies: ['USD']};
  };
  w.modelCache = [{id: 'worker-model', provider: 'local'}];
  w.activeProviderID = 'main';
  return {w, document, posts, errors, knobs, event(node, type) {node.dispatchEvent(new window.Event(type));}};
}
const tick = () => new Promise(resolve => setTimeout(resolve, 0));

test('stats orchestrator exposes delegation, worker model and all worker effort levels', async () => {
  const h = fixture(); await h.w.renderStats();
  const rows = [...h.document.querySelectorAll('.stat-row')];
  for (const key of ['stats.orchDesc', 'stats.taskModel', 'stats.taskEffort']) {
    const row = rows.find(n => n.firstChild.textContent === catalog[key]);
    assert.ok(row, key); assert.ok(row.title);
  }
  assert.ok(!h.document.body.textContent.includes('old/coordinator'));
  assert.ok(!read('js/07-stats.js').includes('"orchestrator_model"'));
  assert.equal(h.document.querySelector('select'), null);
  const effort = h.document.querySelector('.effort-pick');
  const choicesEffort = [...effort.querySelectorAll('.prow')];
  assert.deepEqual(choicesEffort.map(n => n.textContent), [catalog['stats.effortInherit'], 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']);
  assert.equal(effort.querySelector('.orch-btn').textContent, catalog['stats.effortInherit']);
  h.event(effort.querySelector('.orch-btn'), 'click');
  h.event(choicesEffort[5], 'click'); await tick();
  assert.deepEqual(JSON.parse(JSON.stringify(h.posts.at(-1))), {url: '/api/config', body: {key: 'task_reasoning_effort', value: 'high'}});
  assert.equal(h.document.querySelector('.effort-pick .orch-btn').textContent, 'high');
  const model = [...h.document.querySelectorAll('.stat-row')].find(n => n.firstChild.textContent === catalog['stats.taskModel']);
  assert.ok(model.textContent.includes(catalog['stats.orchModelDef']));
  h.event(model.querySelector('button'), 'click');
  const choices = [...model.querySelectorAll('.prow')];
  assert.equal(choices.length, 2); // inherit + known model; never an off entry
  h.event(choices[1], 'click'); await tick();
  assert.deepEqual(JSON.parse(JSON.stringify(h.posts.at(-1))), {url: '/api/config', body: {key: 'task_model', value: 'local/worker-model'}});
  assert.deepEqual(h.errors, []);
});

test('orchestrator Auto/Always/Never responds before saving and rolls back failed saves', async () => {
  const h = fixture(); await h.w.renderStats();
  function controls() {
    const row = [...h.document.querySelectorAll('.stat-row')].find(n => n.firstChild.textContent === catalog['stats.orchDesc']);
    const seg = row.querySelector('.seg');
    return {seg, buttons: [...seg.querySelectorAll('button')]};
  }
  let {seg, buttons} = controls();
  let finishSave;
  h.w.jpost = (url, body) => {
    h.posts.push({url, body});
    return new Promise(resolve => { finishSave = resolve; });
  };
  h.w.renderStats = () => {};
  h.event(buttons[0], 'click');
  assert.equal(h.posts.length, 0);
  for (const [index, value] of [[1, 'on'], [2, 'off'], [0, 'default']]) {
    const count = h.posts.length;
    h.event(buttons[index], 'click');
    assert.ok(buttons[index].classList.contains('on'));
    assert.equal(buttons[index].getAttribute('aria-pressed'), 'true');
    assert.equal(seg.querySelectorAll('.on').length, 1);
    assert.ok(buttons.every(b => b.disabled));
    assert.equal(seg.getAttribute('aria-busy'), 'true');
    h.event(buttons[(index + 1) % buttons.length], 'click');
    assert.equal(h.posts.length, count + 1);
    assert.deepEqual(JSON.parse(JSON.stringify(h.posts.at(-1))), {url: '/api/config', body: {key: 'orchestrator', value}});
    h.knobs.find(k => k.key === 'orchestrator').state = value;
    finishSave({knobs: JSON.parse(JSON.stringify(h.knobs)), cost_currencies: ['USD']}); await tick();
    ({seg, buttons} = controls());
    assert.ok(buttons[index].classList.contains('on'));
    assert.ok(buttons.every(b => !b.disabled));
    assert.equal(seg.getAttribute('aria-busy'), 'false');
  }
  h.w.jpost = () => Promise.reject(Error('offline'));
  h.event(buttons[1], 'click');
  assert.ok(buttons[1].classList.contains('on'));
  await tick();
  assert.ok(buttons[0].classList.contains('on'));
  assert.equal(buttons[0].getAttribute('aria-pressed'), 'true');
  assert.ok(buttons.every(b => !b.disabled));
  assert.deepEqual(h.errors, ['offline']);
});

test('worker stats and TUI copy is translated in all 27 languages', () => {
  const locales = readDir(sourcePath(assets, 'locales')).filter(f => f.endsWith('.json'));
  assert.equal(locales.length, 27);
  for (const file of locales) {
    const gui = JSON.parse(read('locales/' + file));
    const tui = JSON.parse(readSource(sourcePath(assets, '../../system/uilang/catalogs', file), 'utf8'));
    for (const key of ['stats.taskModel', 'stats.taskEffort', 'stats.effortInherit', 'stats.orchTooltip', 'stats.taskModelTooltip', 'stats.taskEffortTooltip', 'setting.task_reasoning_effort.label', 'setting.task_reasoning_effort.desc', 'modes.desc.task_reasoning_effort']) assert.ok(gui[key], file + ': ' + key);
    for (const part of ['label', 'desc', 'inherit']) assert.ok(tui['tui.setting_task_reasoning_effort.' + part], file + ': ' + part);
    assert.equal(gui['stats.orchModel'], undefined);
    if (file !== 'en.json') assert.notEqual(gui['stats.taskEffortTooltip'], catalog['stats.taskEffortTooltip']);
  }
});
