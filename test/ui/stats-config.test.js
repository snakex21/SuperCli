'use strict';
import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript, parseHTML} from './harness.js';

const assets = sourcePath(testDir, '../../internal/webgui/assets');
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
}
function config(values = {}) {
  const defaults = {orchestrator: 'default', task_model: '', task_reasoning_effort: '',
    advisor_model: 'off', advisor_reasoning_effort: '', review_model: '', review_reasoning_effort: '',
    darwin: 'default', darwin_models: '', darwin_pool_size: '', darwin_reasoning_effort: ''};
  return {knobs: Object.entries({...defaults, ...values}).map(([key, raw]) =>
    ({key, raw, state: key === 'darwin' || key === 'orchestrator' ? raw : undefined})), cost_currencies: ['USD', 'EUR']};
}
function stats() {
  return {session: {id: 'session', model: 'completed-model', provider: 'local'},
    last_turn: {ev: {model: 'completed-model', tok_total: 100, tok_in: 80, tok_out: 20, generation_tps: 10}, tools: 2},
    generation_speed: {scope: 'session', tokens_per_second: 12, samples: 1, output_tokens: 120, stream_ms: 10000},
    tokens: {input: 80, output: 20, total: 100},
    context: {window: 1000, estimated_used: 250, percent: 25, has_snapshot: true},
    cost: {state: 'estimated', amount: 0.25, currency: 'USD'}};
}
function fixture() {
  const {document, window} = parseHTML('<html><body><div id="stats-block"></div></body></html>');
  const gets = [], posts = [], errors = [];
  const w = createSandbox({document, window: {}, ui: {lang: 'en'}, projectEpoch: 0,
    activeSessionID: 'session', activeModelID: 'small', activeProviderID: 'local', lastTurn: null, workersSeen: [],
    normalizeLanguage: value => value, t: key => key,
    generationSpeedText: ev => ev.generation_tps ? ev.generation_tps + ' tok/s' : '',
    modelCache: [{id: 'small'}, {id: 'big', provider: 'cloud'}], toast: message => errors.push(message),
    setTimeout() { throw Error('stats config must not schedule a timer'); },
    setInterval() { throw Error('stats config must not poll'); },
  });
  for (const file of ['00-helpers.js', '07-stats.js'])
    runScript(readSource(sourcePath(assets, 'js', file), 'utf8'), w);
  w.toast = message => errors.push(message);
  w.i18nEl = (tag, cls, key) => { const n = w.el(tag, cls, key); n.dataset.key = key; return n; };
  w.j = url => { const request = {url, ...deferred()}; gets.push(request); return request.promise; };
  w.jpost = (url, body) => { const request = {url, body, ...deferred()}; posts.push(request); return request.promise; };
  const box = document.getElementById('stats-block');
  const click = node => { assert.ok(node, 'missing control'); node.dispatchEvent(new window.Event('click', {bubbles: true})); };
  const row = label => [...box.querySelectorAll('.stat-row')].find(n => n.firstChild.dataset.key === label);
  function pick(label, value) {
    const parent = row(label); click(parent.querySelector('.orch-btn'));
    const option = [...parent.querySelectorAll('.prow')].find(n =>
      (n.querySelector('.pid') || n).textContent === value);
    click(option); return option;
  }
  function toggle(label, index) { click(row(label).querySelectorAll('.seg button')[index]); }
  function start() {
    const offset = gets.length, done = w.renderStats();
    return {done, cfg: gets.slice(offset).find(r => r.url === '/api/config'),
      stats: gets.slice(offset).find(r => r.url.startsWith('/api/stats'))};
  }
  async function load(cfg = config()) {
    const request = start(); request.cfg.resolve(cfg); request.stats.resolve(stats()); await request.done;
  }
  return {w, document, window, box, gets, posts, errors, click, row, pick, toggle, start, load};
}
const has = (h, key) => !!h.row(key);

test('configuration renders while stats is deferred, then keeps last-turn/session/workers/config order', async () => {
  const h = fixture(); h.w.workersSeen = [{name: 'worker', summary: 'done'}];
  const request = h.start();
  assert.equal(h.box.querySelector('.stats-config').textContent, '', 'pending config must not invent confirmed defaults');
  request.cfg.resolve(config()); await tick();
  assert.ok(has(h, 'stats.darwinTool'));
  assert.ok(has(h, 'stats.orchDesc'));
  assert.equal(h.box.querySelector('.stats-last-turn-generation-speed'), null);
  assert.equal(h.box.getAttribute('aria-busy'), 'true');
  const focused = h.row('stats.darwinTool').querySelectorAll('button')[1]; focused.focus();
  request.stats.resolve(stats()); await request.done;
  assert.equal(h.document.activeElement, focused, 'stats completion must not detach usable config controls');
  assert.deepEqual([...h.box.querySelectorAll('.stats-head')].map(n => n.dataset.key),
    ['stats.turn', 'stats.sessionSection', 'stats.workers', 'stats.orch', 'stats.modes']);
  assert.ok(h.box.querySelector('.stats-last-turn-generation-speed'));
  assert.ok(h.box.querySelector('.stats-session-generation-speed'));
  assert.ok(h.box.querySelector('.side-cost'));
  assert.equal(h.box.querySelector('[role="progressbar"]').getAttribute('aria-valuenow'), '25');
  assert.equal(h.box.getAttribute('aria-busy'), 'false');
});

test('failed stats does not hide usable modes or make a successful save depend on another GET', async () => {
  const h = fixture(), request = h.start();
  request.stats.reject(Error('stats offline')); request.cfg.resolve(config()); await request.done;
  assert.match(h.box.textContent, /stats offline/);
  assert.ok(has(h, 'stats.darwinTool'));
  h.toggle('stats.darwinTool', 0);
  assert.equal(has(h, 'stats.darwinModel'), false);
  h.posts[0].resolve(config({darwin: 'on'})); await tick();
  assert.ok(has(h, 'stats.darwinModel'));
  assert.equal(h.gets.length, 2);
  assert.deepEqual(h.errors, []);
});

test('each sidebar config callback uses the full POST snapshot only and retains the stats DOM', async () => {
  const cases = [
    ['orchestrator', 'on', 'stats.orchDesc', null, 1],
    ['task_model', 'cloud/big', 'stats.taskModel', 'big'],
    ['task_reasoning_effort', 'high', 'stats.taskEffort', 'high'],
    ['advisor_model', 'cloud/big', 'stats.advModel', 'big'],
    ['advisor_reasoning_effort', 'high', 'effort.advisor', 'high'],
    ['review_model', 'cloud/big', 'stats.reviewModel', 'big'],
    ['review_reasoning_effort', 'high', 'effort.review', 'high'],
    ['darwin', 'default', 'stats.darwinTool', null, 1],
    ['darwin_models', 'cloud/big', 'stats.darwinModel', 'big'],
    ['darwin_pool_size', '7', 'stats.darwinPool', '7'],
    ['darwin_reasoning_effort', 'high', 'effort.darwin', 'high'],
  ];
  for (const [key, value, label, pick, toggle] of cases) {
    const h = fixture(), initial = {darwin: 'on', advisor_model: 'small', review_model: 'small'};
    await h.load(config(initial));
    const meter = h.box.querySelector('[role="progressbar"]'), speed = h.box.querySelector('.stats-session-generation-speed');
    const cost = h.box.querySelector('.side-cost'), lastTurn = h.box.querySelector('.stats-last-turn-generation-speed');
    if (pick) h.pick(label, pick); else h.toggle(label, toggle);
    await tick();
    assert.equal(h.posts.length, 1, key);
    assert.deepEqual(h.posts[0].body, {key, value});
    h.posts[0].resolve(config({...initial, [key]: value})); await tick();
    if (pick) assert.equal(h.row(label).querySelector('.orch-btn').textContent, value, key + ' confirmed value');
    assert.equal(h.gets.length, 2, key + ' must not fetch stats/config after saving');
    assert.equal(h.box.querySelector('[role="progressbar"]'), meter);
    assert.equal(h.box.querySelector('.stats-session-generation-speed'), speed);
    assert.equal(h.box.querySelector('.side-cost'), cost);
    assert.equal(h.box.querySelector('.stats-last-turn-generation-speed'), lastTurn);
    assert.deepEqual(h.errors, []);
  }
});

test('Review and Advisor effort options wait for a successful snapshot; failed model/pool/effort saves retain confirmed values', async () => {
  const h = fixture(); await h.load();
  for (const [label, key, effort] of [['stats.reviewModel', 'review_model', 'effort.review'], ['stats.advModel', 'advisor_model', 'effort.advisor']]) {
    h.pick(label, 'big');
    assert.equal(has(h, effort), false);
    h.posts.at(-1).resolve(config({review_model: 'cloud/big', [key]: 'cloud/big'})); await tick();
    assert.ok(has(h, effort));
  }
  const initial = {darwin: 'on', advisor_model: 'small', review_model: 'small', darwin_pool_size: '4', darwin_reasoning_effort: 'low'};
  for (const [label, choice, expected] of [['stats.reviewModel', 'big', 'small'], ['stats.darwinPool', '7', '4'], ['effort.darwin', 'high', 'low']]) {
    const f = fixture(); await f.load(config(initial));
    f.pick(label, choice); await tick();
    f.posts[0].reject(Error('offline')); await tick();
    assert.equal(f.row(label).querySelector('.orch-btn').textContent, expected);
    assert.deepEqual(f.errors, ['offline']);
    assert.equal(f.gets.length, 2);
  }
});

test('older config GET cannot overwrite a saved snapshot, including a GET started during a save', async () => {
  for (const saveFirst of [false, true]) {
    const h = fixture(); await h.load();
    if (saveFirst) h.toggle('stats.darwinTool', 0);
    const refresh = h.start();
    if (!saveFirst) h.toggle('stats.darwinTool', 0);
    h.posts[0].resolve(config({darwin: 'on'})); await tick();
    assert.ok(has(h, 'stats.darwinModel'));
    refresh.cfg.resolve(config()); refresh.stats.resolve(stats()); await refresh.done;
    assert.ok(has(h, 'stats.darwinModel'), 'old GET must not remove saved fields');
    assert.equal(h.box.querySelectorAll('.stats-config').length, 1);
    assert.deepEqual(h.errors, []);
  }
});

test('rapid same-key model saves are blocked while pending, failed saves can retry, and old detached controls cannot save', async () => {
  const h = fixture(); await h.load();
  const oldChoice = h.pick('stats.reviewModel', 'big');
  h.click(oldChoice); h.click(oldChoice);
  assert.equal(h.posts.length, 1);
  h.posts[0].reject(Error('offline')); await tick();
  h.click(oldChoice); assert.equal(h.posts.length, 2);
  h.posts[1].resolve(config({review_model: 'cloud/big'})); await tick();
  assert.ok(has(h, 'effort.review'));
  assert.equal(oldChoice.isConnected, false);
  h.click(oldChoice);
  assert.equal(h.posts.length, 2);
  assert.deepEqual(h.errors, ['offline']);
});

test('simultaneous distinct-knob saves are sequenced and render a cumulative confirmed snapshot without clobbering either knob', async () => {
  const h = fixture(); await h.load();
  h.toggle('stats.darwinTool', 0); h.pick('stats.reviewModel', 'big');
  assert.equal(h.posts.length, 1, 'second POST waits for the first authoritative snapshot');
  assert.equal(has(h, 'effort.review'), false);
  h.posts[0].resolve(config({darwin: 'on'})); await tick();
  assert.equal(h.posts.length, 2);
  assert.deepEqual(h.posts[1].body, {key: 'review_model', value: 'cloud/big'});
  h.posts[1].resolve(config({darwin: 'on', review_model: 'cloud/big'})); await tick();
  assert.ok(has(h, 'stats.darwinModel'));
  assert.ok(has(h, 'effort.review'));
  assert.equal(h.row('stats.darwinTool').querySelector('.on').textContent, 'set.state.on');
  assert.equal(h.row('stats.reviewModel').querySelector('.orch-btn').textContent, 'cloud/big');
  assert.equal(h.gets.length, 2);
  assert.deepEqual(h.errors, []);
});

test('a failed queued save neither discards another knob success nor blocks a later save', async () => {
  for (const failFirst of [false, true]) {
    const h = fixture(); await h.load();
    h.toggle('stats.darwinTool', 0); h.pick('stats.reviewModel', 'big');
    if (failFirst) h.posts[0].reject(Error('first failed'));
    else h.posts[0].resolve(config({darwin: 'on'}));
    await tick();
    assert.equal(h.posts.length, 2);
    if (failFirst) h.posts[1].resolve(config({review_model: 'cloud/big'}));
    else h.posts[1].reject(Error('second failed'));
    await tick();
    assert.equal(has(h, 'stats.darwinModel'), !failFirst);
    assert.equal(has(h, 'effort.review'), failFirst);
    assert.deepEqual(h.errors, [failFirst ? 'first failed' : 'second failed']);
    h.toggle('stats.orchDesc', 1);
    assert.equal(h.posts.length, 3);
    h.posts[2].resolve(config({orchestrator: 'on', darwin: failFirst ? 'default' : 'on', review_model: failFirst ? 'cloud/big' : ''})); await tick();
    assert.equal(h.gets.length, 2);
  }
});

const changes = {
  session: h => { h.w.activeSessionID = 'other'; },
  model: h => { h.w.activeModelID = 'other'; },
  provider: h => { h.w.activeProviderID = 'other'; },
  project: h => { h.w.projectEpoch++; },
  language: h => { h.w.ui.lang = 'pl'; },
  hidden: h => { h.w.ui.sidebarHidden = true; },
  detached: h => { h.box.remove(); },
};
test('selection/project/language/hidden/detached guards reject stale GET, POST success/failure, and clicks before another renderer runs', async () => {
  for (const [name, change] of Object.entries(changes)) {
    const h = fixture(); await h.load();
    const request = h.start(); change(h);
    request.cfg.resolve(config({darwin: 'on'})); request.stats.resolve(stats()); await request.done;
    assert.equal(has(h, 'stats.darwinModel'), false, name + ' stale GET');
    h.toggle('stats.darwinTool', 0);
    assert.equal(h.posts.length, 0, name + ' stale click');
    for (const fail of [false, true]) {
      const f = fixture(); await f.load();
      f.toggle('stats.darwinTool', 0); change(f);
      if (fail) f.posts[0].reject(Error('stale failure')); else f.posts[0].resolve(config({darwin: 'on'}));
      await tick();
      assert.equal(has(f, 'stats.darwinModel'), false, name + ' stale POST');
      assert.deepEqual(f.errors, [], name + ' stale error');
    }
  }
});

test('new renderer rejects old GETs and detached controls, while same-session pending stats retains its meter', async () => {
  const h = fixture(); await h.load();
  const meter = h.box.querySelector('[role="progressbar"]');
  const first = h.start(), second = h.start();
  assert.equal(h.box.querySelector('[role="progressbar"]'), meter);
  second.cfg.resolve(config({darwin: 'on'})); second.stats.resolve(stats()); await second.done;
  first.cfg.resolve(config()); first.stats.resolve(stats()); await first.done;
  assert.ok(has(h, 'stats.darwinModel'));
  const oldButton = h.row('stats.darwinTool').querySelectorAll('button')[1];
  h.w.ui.lang = 'pl'; const next = h.start();
  h.click(oldButton); assert.equal(h.posts.length, 0);
  next.cfg.resolve(config()); next.stats.resolve(stats()); await next.done;
  assert.equal(has(h, 'stats.darwinModel'), false);
});

test('project replacement drops stale POST snapshots and does not send an old queued save to the new project', async () => {
  const h = fixture(); await h.load();
  h.toggle('stats.darwinTool', 0); h.pick('stats.reviewModel', 'big');
  const oldControl = h.row('stats.darwinTool').querySelectorAll('button')[1];
  h.w.projectEpoch++;
  const next = h.start();
  next.cfg.resolve(config({advisor_model: 'small'})); next.stats.resolve(stats()); await next.done;
  h.posts[0].resolve(config({darwin: 'on'})); await tick();
  assert.equal(h.posts.length, 1, 'queued old-project POST must not start');
  assert.equal(has(h, 'stats.darwinModel'), false);
  assert.equal(has(h, 'effort.review'), false);
  assert.ok(has(h, 'effort.advisor'));
  h.click(oldControl); assert.equal(h.posts.length, 1);
  assert.deepEqual(h.errors, []);
});

function installProjects(h) {
  const button = h.document.createElement('button'); button.id = 'add-project'; h.document.body.appendChild(button);
  Object.assign(h.w, {streaming: false, checkHealth: async () => {}, newSession: () => {}, loadPromptQueue: () => {}});
  runScript(readSource(sourcePath(assets, 'js/09-projects.js'), 'utf8'), h.w);
  h.w.loadProjects = async () => {};
}

test('project initiation cancels an old queued config action before the delayed project acknowledgement', async () => {
  for (const action of ['use', 'add', 'relocate']) {
    const h = fixture(); installProjects(h); await h.load();
    let serverWorkspace = 'C:/synthetic/initial';
    const post = h.w.jpost;
    h.w.jpost = (url, body) => {
      if (url === '/api/projects') serverWorkspace = body.target; // apply before returning the held acknowledgement
      return post(url, body);
    };
    h.toggle('stats.darwinTool', 0); h.pick('stats.reviewModel', 'big');
    const projectDone = h.w.projectAction(action, 'C:/synthetic/next');
    const projectRequest = h.posts.find(p => p.url === '/api/projects');
    assert.ok(projectRequest, 'the project request has reached the synthetic server');
    assert.equal(serverWorkspace, 'C:/synthetic/next');
    // The synthetic server has switched its workspace but is holding the HTTP
    // acknowledgement. Config itself is global, not a per-project file.
    assert.equal(h.w.projectEpoch, 0, 'the existing epoch has not advanced before acknowledgement');
    h.posts[0].resolve(config({darwin: 'on'})); await tick();
    const emittedConfigPosts = h.posts.filter(p => p.url === '/api/config').length;
    projectRequest.resolve({}); await projectDone;
    assert.equal(emittedConfigPosts, 1, action + ': do not emit the queued old-UI Review action after switching began');
    assert.deepEqual(h.errors, action === 'relocate' ? ['project.changed'] : []);
  }
});

test('a failed project transition still cancels previously queued actions without blocking a new user retry', async () => {
  const h = fixture(); installProjects(h); await h.load();
  h.toggle('stats.darwinTool', 0); h.pick('stats.reviewModel', 'big');
  const projectDone = h.w.projectAction('use', 'C:/synthetic/next');
  h.posts[1].reject(Error('project failed')); await projectDone;
  assert.equal(h.w.projectEpoch, 0);
  h.posts[0].resolve(config({darwin: 'on'})); await tick();
  assert.equal(h.posts.length, 2, 'a completed failed transition must not revive the old queued action');
  assert.equal(has(h, 'effort.review'), false);
  h.pick('stats.reviewModel', 'big');
  assert.equal(h.posts.length, 3, 'a new action after the failed transition is allowed');
  h.posts[2].resolve(config({darwin: 'on', review_model: 'cloud/big'})); await tick();
  assert.ok(has(h, 'effort.review'));
  assert.deepEqual(h.errors, ['project failed']);
});

test('a confirmed save from a replaced same-project group reconciles the current model/session/language group', async () => {
  for (const change of [h => { h.w.activeModelID = 'other'; }, h => { h.w.activeSessionID = 'other'; }, h => { h.w.ui.lang = 'pl'; }]) {
    const h = fixture(); await h.load();
    h.toggle('stats.darwinTool', 0);
    change(h); const current = h.start();
    current.cfg.resolve(config()); current.stats.resolve(stats()); await current.done;
    assert.equal(has(h, 'stats.darwinModel'), false, 'the current GET preceded the successful mutation');
    h.posts[0].resolve(config({darwin: 'on'})); await tick();
    const reconciliation = h.gets.slice(4);
    assert.equal(reconciliation.length, 1, 'a discarded successful POST must reconcile the visible same-project group');
    assert.equal(reconciliation[0].url, '/api/config', 'reconcile config only, not stats');
    reconciliation[0].resolve(config({darwin: 'on'})); await tick();
    assert.ok(has(h, 'stats.darwinModel'));
    assert.deepEqual(h.errors, []);
  }
});

test('old-group success waits for a newer group save success or failure before reconciling and never applies the old snapshot', async () => {
  for (const failCurrent of [false, true]) {
    const h = fixture(); await h.load(); h.toggle('stats.darwinTool', 0);
    h.w.ui.lang = 'pl'; const current = h.start();
    current.cfg.resolve(config()); current.stats.resolve(stats()); await current.done;
    const meter = h.box.querySelector('[role="progressbar"]');
    h.pick('stats.reviewModel', 'big');
    h.posts[0].resolve(config({darwin: 'on'})); await tick();
    assert.equal(h.gets.length, 4, 'wait until the current group mutation settles');
    assert.equal(has(h, 'stats.darwinModel'), false, 'do not copy an old-group full snapshot');
    if (failCurrent) h.posts[1].reject(Error('current save failed'));
    else h.posts[1].resolve(config({review_model: 'cloud/big'})); // snapshot captured before the old mutation committed
    await tick();
    assert.equal(h.gets.length, 5);
    assert.equal(h.gets[4].url, '/api/config');
    h.gets[4].resolve(config({darwin: 'on', review_model: failCurrent ? '' : 'cloud/big'})); await tick();
    assert.ok(has(h, 'stats.darwinModel'));
    assert.equal(has(h, 'effort.review'), !failCurrent);
    assert.equal(h.box.querySelector('[role="progressbar"]'), meter);
    assert.deepEqual(h.errors, failCurrent ? ['current save failed'] : []);
  }
});

test('a newer save invalidates an in-flight reconciliation read without extra stats or config requests', async () => {
  const h = fixture(); await h.load(); h.toggle('stats.darwinTool', 0);
  h.w.activeModelID = 'other'; const current = h.start();
  current.cfg.resolve(config()); current.stats.resolve(stats()); await current.done;
  h.posts[0].resolve(config({darwin: 'on'})); await tick();
  const reconciliation = h.gets[4]; assert.equal(reconciliation.url, '/api/config');
  h.pick('stats.reviewModel', 'big');
  h.posts[1].resolve(config({darwin: 'on', review_model: 'cloud/big'})); await tick();
  reconciliation.resolve(config({darwin: 'on'})); await tick();
  assert.ok(has(h, 'effort.review'), 'an older reconciliation read must not remove the newly confirmed Review effort');
  assert.ok(has(h, 'stats.darwinModel'));
  assert.equal(h.gets.length, 5);
  assert.deepEqual(h.errors, []);
});

test('a failed newer save preserves reconciliation of an earlier confirmed mutation', async () => {
  const h = fixture(); await h.load(); h.toggle('stats.darwinTool', 0);
  h.w.activeModelID = 'other'; const current = h.start();
  current.cfg.resolve(config()); current.stats.resolve(stats()); await current.done;
  const meter = h.box.querySelector('[role="progressbar"]');
  h.posts[0].resolve(config({darwin: 'on'})); await tick();
  const staleRead = h.gets[4]; assert.equal(staleRead.url, '/api/config');
  h.pick('stats.reviewModel', 'big');
  h.posts[1].reject(Error('review save failed')); await tick();
  assert.equal(h.gets.length, 6, 'the invalidated reconciliation is still required after the newer save fails');
  assert.equal(h.gets[5].url, '/api/config');
  staleRead.resolve(config({darwin: 'on', review_model: 'cloud/big'})); await tick();
  assert.equal(has(h, 'stats.darwinModel'), false, 'the invalidated read must not paint');
  assert.equal(has(h, 'effort.review'), false);
  h.gets[5].resolve(config({darwin: 'on'})); await tick();
  assert.ok(has(h, 'stats.darwinModel'), 'the earlier successful Darwin save must be reflected');
  assert.equal(has(h, 'effort.review'), false, 'the failed Review save must not be displayed as confirmed');
  assert.equal(h.box.querySelector('[role="progressbar"]'), meter);
  assert.equal(h.posts.length, 2, 'no automatic write retry');
  assert.deepEqual(h.errors, ['review save failed']);
});

test('reconciliation invalidates the replaced group GET and handles read failure separately from POST success', async () => {
  for (const failRead of [false, true]) {
    const h = fixture(); await h.load(); h.toggle('stats.darwinTool', 0);
    h.w.activeSessionID = 'other'; const current = h.start();
    h.posts[0].resolve(config({darwin: 'on'})); await tick();
    const reconciliation = h.gets[4]; assert.equal(reconciliation.url, '/api/config');
    if (failRead) reconciliation.reject(Error('config read failed'));
    else reconciliation.resolve(config({darwin: 'on'}));
    await tick();
    current.cfg.resolve(config()); current.stats.resolve(stats()); await current.done;
    assert.equal(has(h, 'stats.darwinModel'), !failRead, 'the pre-mutation GET is not authoritative after successful saving');
    assert.equal(h.posts.length, 1, 'read failure must not retry or roll back the saved mutation');
    assert.equal(h.gets.length, 5, 'no automatic retry or stats refresh');
    assert.deepEqual(h.errors, failRead ? ['config read failed'] : []);
  }
});

test('a failed old-group POST does not reconcile or interfere with a newer confirmed save', async () => {
  const h = fixture(); await h.load(); h.toggle('stats.darwinTool', 0);
  h.w.ui.lang = 'pl'; const current = h.start();
  current.cfg.resolve(config()); current.stats.resolve(stats()); await current.done;
  h.pick('stats.reviewModel', 'big');
  h.posts[1].resolve(config({review_model: 'cloud/big'})); await tick();
  h.posts[0].reject(Error('old save failed')); await tick();
  assert.ok(has(h, 'effort.review'));
  assert.equal(has(h, 'stats.darwinModel'), false);
  assert.equal(h.gets.length, 4);
  assert.deepEqual(h.errors, []);
});

test('detached filtered model options cannot submit even when the picker row is still current', async () => {
  const h = fixture(); await h.load();
  const option = h.pick('stats.reviewModel', 'big');
  h.posts[0].reject(Error('offline')); await tick();
  const row = h.row('stats.reviewModel'); h.click(row.querySelector('.orch-btn'));
  assert.equal(option.isConnected, false);
  h.click(option); assert.equal(h.posts.length, 1);
});
