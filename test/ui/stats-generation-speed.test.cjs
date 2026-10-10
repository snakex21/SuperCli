'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const {parseHTML} = require('linkedom');

const assets = path.resolve(__dirname, '../../internal/webgui/assets');
const catalog = JSON.parse(fs.readFileSync(path.join(assets, 'locales/en.json'), 'utf8'));
function fixture() {
  const {document} = parseHTML('<html><body><div id="stats-block"></div></body></html>');
  const w = vm.createContext({document, window: {
    innerWidth: 1400, innerHeight: 900, matchMedia: () => ({matches: false}), addEventListener() {},
  }});
  Object.assign(w, {activeSessionID: 'first', activeModelID: 'selected-model', activeProviderID: 'selected-provider',
    lastTurn: null, workersSeen: [], detectedLanguage: () => 'en', normalizeLanguage: value => value,
    t: key => catalog[key] || key,
    setTimeout() {throw Error('speed display must not start timers');},
    setInterval() {throw Error('speed display must not start timers');},
    generationSpeedText(ev) {
      const n = ev && ev.generation_tps;
      return typeof n === 'number' && Number.isFinite(n) && n > 0 ? n.toFixed(1) + ' tok/s' : '';
    },
  });
  for (const file of ['00-helpers.js', '02-ui-settings.js']) {
    vm.runInContext(fs.readFileSync(path.join(assets, 'js', file), 'utf8'), w);
  }
  w.ui.sidebarHidden = false;
  w.i18nEl = (tag, cls, key) => w.el(tag, cls, w.t(key));
  vm.runInContext(fs.readFileSync(path.join(assets, 'js/07-stats.js'), 'utf8'), w);
  w.j = async url => url === '/api/config' ? {knobs: []} : snapshot();
  return {w, box: document.querySelector('#stats-block')};
}
function snapshot(generation) {
  return {session: {id: 'first', model: 'last-main-model'}, tokens: {input: 100, output: 130, total: 230},
    last_turn: {ev: {model: 'latest-main-model', tok_in: 100, tok_out: 30, tok_total: 130}, kind: 'response', tools: 4},
    generation_speed: generation,
  };
}
function measured() {
  return {scope: 'session', tokens_per_second: 16.25, output_tokens: 130, stream_ms: 8000, samples: 2, calls: 3};
}
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => {resolve = yes; reject = no;});
  return {promise, resolve, reject};
}

test('resumed persisted turn keeps a separate weighted session average without fabricating event speed', async () => {
  const {w, box} = fixture();
  const raw = snapshot(measured());
  const before = JSON.stringify(raw);
  w.j = async url => url === '/api/config' ? {knobs: []} : raw;
  await w.renderStats();
  const speed = box.querySelector('.stats-session-generation-speed');
  assert.ok(speed);
  assert.equal(speed.lastChild.textContent, '16.3 tok/s');
  assert.match(speed.textContent, /Session average speed/);
  assert.match(speed.title, /including helpers/);
  assert.ok(box.textContent.includes('latest-main-model'));
  assert.equal(JSON.stringify(raw), before);
  assert.equal(w.lastTurn, null, 'loading must not mutate global live telemetry');
  assert.equal(box.querySelector('.stats-live-generation-speed'), null);
  w.activeModelID = 'newly-selected';
  w.activeProviderID = 'another-provider';
  await w.renderStats();
  assert.equal(box.querySelector('.stats-session-generation-speed').lastChild.textContent, '16.3 tok/s');
  assert.doesNotMatch(speed.title, /newly-selected|another-provider/);
});

test('new sessions and unknown clocks keep the indicator with an honest dash', async () => {
  const {w, box} = fixture();
  for (const generation of [undefined, {scope: 'session', tokens_per_second: null},
    ...[0, -1, NaN, Infinity, '16.25'].map(n => ({...measured(), tokens_per_second: n})),
    {...measured(), scope: 'model'}, {...measured(), samples: 0}, {...measured(), stream_ms: 0}]) {
    w.j = async url => url === '/api/config' ? {knobs: []} : snapshot(generation);
    await w.renderStats();
    assert.equal(box.querySelector('.stats-session-generation-speed').lastChild.textContent, '—');
    assert.doesNotMatch(box.textContent, /NaN|Infinity|undefined/);
  }
});

test('a good same-session live rate survives a newer unmeasured reply without being called a session average', async () => {
  const {w, box} = fixture();
  w.lastTurn = {sessionID: 'first', ev: {model: 'measured-model', generation_tps: 42.5}};
  w.rememberStatsLiveSpeed(w.lastTurn, 'first'); // done handler also runs while the pane is hidden
  w.lastTurn = {sessionID: 'first', ev: {model: 'new-unmeasured-model', tok_total: 100}};
  await w.renderStats();
  const average = box.querySelector('.stats-session-generation-speed');
  const live = box.querySelector('.stats-live-generation-speed');
  assert.equal(average.lastChild.textContent, '—');
  assert.match(live.textContent, /Last measured response.*42\.5 tok\/s/);
  assert.match(live.title, /measured-model/);
  assert.doesNotMatch(live.title, /new-unmeasured-model|selected-model/);
  assert.equal(w.lastTurn.ev.generation_tps, undefined, 'never copy an old rate into a newer event');
  w.j = async url => url === '/api/config' ? {knobs: []} : snapshot(measured());
  await w.renderStats();
  assert.equal(box.querySelector('.stats-live-generation-speed'), null, 'durable average takes precedence');
});

test('pending and failed same-session refresh preserve speed, session switch rejects stale results', async () => {
  const {w, box} = fixture();
  w.j = async url => url === '/api/config' ? {knobs: []} : snapshot(measured());
  await w.renderStats();
  const previous = box.querySelector('.stats-session-generation-speed');
  const failed = deferred();
  w.j = url => url === '/api/config' ? Promise.resolve({knobs: []}) : failed.promise;
  const failing = w.renderStats();
  assert.equal(box.querySelector('.stats-session-generation-speed'), previous);
  failed.reject(Error('temporary unavailable'));
  await failing;
  assert.equal(box.querySelector('.stats-session-generation-speed').lastChild.textContent, '16.3 tok/s');
  const old = deferred(), current = deferred();
  w.j = url => url === '/api/config' ? Promise.resolve({knobs: []})
    : url.includes('session=second') ? current.promise : old.promise;
  const oldDone = w.renderStats();
  w.activeSessionID = 'second';
  w.lastTurn = {sessionID: 'first', ev: {model: 'old', generation_tps: 99}};
  const currentDone = w.renderStats();
  assert.equal(box.querySelector('.stats-session-generation-speed').lastChild.textContent, '—');
  current.resolve({...snapshot(), session: {id: 'second'}, last_turn: null});
  await currentDone;
  assert.equal(box.querySelector('.stats-live-generation-speed'), null);
  assert.doesNotMatch(box.textContent, /99\.0 tok\/s/, 'bound live telemetry must not cross conversations');
  old.resolve(snapshot(measured()));
  await oldDone;
  assert.equal(box.querySelector('.stats-session-generation-speed').lastChild.textContent, '—');
});

test('shared user setting hides and reveals the metric without deleting its measurement', async () => {
  const {w, box} = fixture();
  w.j = async url => url === '/api/config' ? {knobs: []} : snapshot(measured());
  await w.renderStats();
  const speed = box.querySelector('.stats-session-generation-speed');
  assert.ok(speed.classList.contains('generation-speed'));
  w.ui.showGenerationSpeed = false;
  w.applyGenerationSpeedVisibility();
  assert.ok(w.document.documentElement.classList.contains('generation-speed-hidden'));
  w.ui.showGenerationSpeed = true;
  w.applyGenerationSpeedVisibility();
  assert.ok(!w.document.documentElement.classList.contains('generation-speed-hidden'));
  assert.equal(box.querySelector('.stats-session-generation-speed'), speed);
  assert.equal(speed.lastChild.textContent, '16.3 tok/s');
});
