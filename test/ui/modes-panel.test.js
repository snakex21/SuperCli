import {assert, test, readSource, readDir, sourcePath, testDir, createSandbox, runScript} from './harness.js';
const assets = sourcePath(testDir, '../../internal/webgui/assets');
const read = file => readSource(sourcePath(assets, file), 'utf8');
const catalog = JSON.parse(read('locales/en.json'));
function node(tag, cls, text) {
  return { tag, className: cls || '', textContent: text || '', children: [], style: {}, events: {}, attrs: {}, value: '',
    classList: {add() {}}, appendChild(n) {this.children.push(n); return n;},
    addEventListener(type, fn) {this.events[type] = fn;}, setAttribute(key, val) {this.attrs[key] = val;},
    set innerHTML(value) {this.children = [];}, blur() {},
  };
}
function harness() {
  let gets = 0;
  const posts = [], errors = [], panel = node('div');
  const w = {sections: {}, panelContent: panel, currentSection: 'modes', el: node,
    t: key => catalog[key] || key,
    i18nEl: (tag, cls, key) => node(tag, cls, catalog[key] || key),
    settingCopy: k => [catalog['setting.' + k.key + '.label'] || k.key, k.desc || ''],
    j: async url => {assert.equal(url, '/api/config'); gets++; return {knobs: w.rows};},
    jpost: async (url, body) => {posts.push({url, body});}, toast: msg => errors.push(msg),
    supercliOrchPicker: (wrap, k, post) => {wrap.post = post;},
  };
  createSandbox(w); runScript(read('js/11b-panel-settings.js'), w);
  w.rows = Array.from(w.modeKeys, key => ({key, label: key, kind: ['context_policy'].includes(key) ? 'readonly' : ['darwin','orchestrator','darwin_auto_merge','draft_verify','stable_toolset','preflight_repo','noop_gate'].includes(key) ? 'tri' : key === 'navigator' ? 'nav' : key.endsWith('parallel') ? 'tri_auto' : 'text', value: 'off', raw: '', source: 'default', state: 'default', next_session: true}));
  return {w, panel, posts, errors, gets: () => gets};
}
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
function descendants(n) {return [n, ...n.children.flatMap(descendants)];}
function rowFor(h, key) {return h.panel.children.find(n => n.className.includes('modes-panel')).children.find(n => n.children[0].title === 'config.toml · ' + key);}

test('Modes has the TUI run modes, localized descriptions and cost hints; reloads on every open', async () => {
  const h = harness(); await h.w.sections.modes();
  assert.match(read('index.html'), /data-sec="modes" data-i18n="panel.modes"/);
  const expected = ['queue_merge','orchestrator','navigator','orchestrator_model','task_model','task_reasoning_effort','task_parallel','task_max_steps','task_max_tokens','advisor_model','advisor_reasoning_effort','review_model','review_reasoning_effort','darwin_reasoning_effort','darwin','darwin_parallel','darwin_models','darwin_judge_model','darwin_auto_merge','draft_verify','draft_verify_max_rounds','verify_commands','compact_model','context_policy','context_window','prune_protect_tokens','memory_briefing_tokens','stable_toolset','preflight_repo','fallback_models','fallback_cooldown_seconds','noop_gate'];
  assert.deepEqual(Array.from(h.w.modeKeys), expected);
  for (const key of expected) {
    const row = rowFor(h, key); assert.ok(row, key);
    assert.equal(row.children[0].children[0].textContent, catalog[['advisor_reasoning_effort','review_reasoning_effort','darwin_reasoning_effort'].includes(key) ? 'effort.hint' : 'modes.desc.' + key]);
  }
  assert.equal(rowFor(h, 'review_model').children[0].textContent, 'Check after work');
  assert.match(catalog['modes.desc.darwin'], /N×.*default off/);
  assert.match(catalog['modes.desc.review_model'], /empty\/off = disabled, zero extra cost/);
  await h.w.sections.modes(); assert.equal(h.gets(), 2);
  assert.deepEqual(h.errors, []);
});

test('Modes edits use the shared endpoint and stay in Modes; text reset and tri-state work', async () => {
  const h = harness(); await h.w.sections.modes();
  const darwin = rowFor(h, 'darwin');
  darwin.children.find(n => n.className === 'seg').children[1].events.click();
  await tick();
  assert.equal(h.posts[0].url, '/api/config');
  assert.equal(h.posts[0].body.key, 'darwin'); assert.equal(h.posts[0].body.value, 'on');
  assert.equal(h.w.currentSection, 'modes');
  const review = rowFor(h, 'review_model').children.find(n => n.tag === 'input');
  assert.equal(review.attrs['aria-label'], 'Check after work');
  review.value = ' local/checker '; review.events.blur(); await tick();
  assert.equal(h.posts[1].body.key, 'review_model'); assert.equal(h.posts[1].body.value, 'local/checker');
  const manual = {key:'review_model', kind:'text', raw:'local/checker', value:'local/checker', source:'manual'};
  const reset = h.w.knobRow(manual, [], 'modes').children.find(n => n.tag === 'input');
  reset.value = ''; reset.events.blur(); await tick();
  assert.equal(h.posts[2].body.value, '');
});

test('Modes avoids stale asynchronous content after navigation and reports load errors', async () => {
  const h = harness(); let resolve;
  h.w.j = () => new Promise(r => {resolve = r;});
  const pending = h.w.sections.modes(); h.w.currentSection = 'providers';
  resolve({knobs: h.w.rows}); await pending;
  assert.equal(h.panel.children.length, 1);
  h.w.currentSection = 'modes'; h.w.j = async () => {throw Error('offline');};
  await h.w.sections.modes(); assert.equal(h.panel.children.at(-1).textContent, 'offline');
});

test('Every catalog contains mode copy with matching placeholders and phone styles wrap controls', () => {
  const keys = Object.keys(catalog).filter(k => k === 'panel.modes' || k.startsWith('modes.'));
  const files = readDir(sourcePath(assets, 'locales')).filter(n => n.endsWith('.json'));
  assert.equal(files.length, 27);
  assert.equal(catalog['panel.modes'], 'Modes');
  assert.equal(catalog['modes.review'], 'Check after work');
  const placeholders = text => (text.match(/\{[\w]+\}/g) || []).sort();
  for (const file of files) {
    const locale = JSON.parse(read('locales/' + file));
    for (const key of keys) {
      assert.ok(Object.hasOwn(locale, key), file + ': ' + key);
      assert.equal(typeof locale[key], 'string', file + ': ' + key);
      assert.ok(locale[key].trim(), file + ': ' + key);
      assert.deepEqual(placeholders(locale[key]), placeholders(catalog[key]), file + ': ' + key + ' placeholders');
    }
    if (file === 'pl.json') {assert.equal(locale['panel.modes'], 'Tryby'); assert.equal(locale['modes.review'], 'Sprawdzanie po pracy');}
  }
  const css = read('phone.css');
  assert.match(css, /\.modes-panel \.knob-row\s*\{[^}]*flex-wrap:wrap/);
  assert.match(css, /\.modes-panel \.k-name[^}]*overflow-wrap:anywhere/);
  assert.match(css, /\.modes-panel \.seg[^}]*max-width:100%/);
});

test('Post-work verdict notice is appended to the GUI transcript', () => {
  const source = read('js/05-chat.js');
  const noticeCase = source.slice(source.indexOf('case "notice":'), source.indexOf('case "worker_progress":'));
  const lines = [], w = {lines, addEventLine: (text, _, tag) => lines.push({text, tag}), noticeTag: () => 'review'};
  createSandbox(w);
  runScript('function receive(ev) { var current = "assistant"; switch(ev.type) {' + noticeCase + '} }', w);
  for (const text of ['review (checker): accepted', 'review (checker): revise — round limit reached (1), finishing']) {
    w.receive({type:'notice', text}); assert.equal(lines.at(-1).text, text);
  }
});
