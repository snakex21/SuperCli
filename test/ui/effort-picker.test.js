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
  jpost: async (url, body) => { posts.push({url, body}); }, renderStats: () => {}, modelCache: []});
 runScript(source, w);
 w.i18nEl = (tag, cls, key) => w.el(tag, cls, key);
 const box = document.getElementById('box');
 const click = n => n.dispatchEvent(new window.Event('click', {bubbles: true}));
 return {w, document, window, box, click, errors, posts};
}
test('effort picker uses model-picker styling, active row, no search/select, save and inherit', async () => {
 const h = fixture(), saved = [];
 h.w.supercliEffortPicker(h.box, {raw:'high'}, v => saved.push(v));
 const btn = h.box.querySelector('.orch-btn'), pop = h.box.querySelector('.orch-pop');
 assert.ok(pop.hidden); assert.equal(btn.textContent, 'high');
 assert.equal(h.box.querySelector('select,input'), null);
 const rows = [...pop.querySelectorAll('.prow')];
 assert.equal(rows.length, 8); assert.equal(pop.querySelectorAll('.state-dot').length, 8);
 assert.equal(pop.querySelector('.active').textContent, 'high');
 h.click(btn); assert.equal(pop.hidden, false); assert.equal(btn.getAttribute('aria-expanded'), 'true');
 h.click(rows[7]); await tick();
 assert.deepEqual(saved, ['max']); assert.equal(btn.textContent, 'max'); assert.ok(pop.hidden);
 assert.equal(pop.querySelector('.active').textContent, 'max');
 h.click(btn); h.click(rows[0]); await tick();
 assert.deepEqual(saved, ['max','']); assert.equal(btn.textContent, 'stats.effortInherit');
});
test('effort picker closes on outside click, Escape, and opening a sibling picker', () => {
 const h = fixture();
 h.w.supercliEffortPicker(h.box, {}, () => {}); h.w.supercliEffortPicker(h.box, {}, () => {});
 const btns = [...h.box.querySelectorAll('.orch-btn')], pops = [...h.box.querySelectorAll('.orch-pop')];
 h.click(btns[0]); h.click(h.document.body); assert.ok(pops[0].hidden);
 h.click(btns[0]); h.click(btns[1]); assert.ok(pops[0].hidden); assert.equal(pops[1].hidden, false);
 const esc = new h.window.Event('keydown', {bubbles:true}); Object.defineProperty(esc, 'key', {value:'Escape'});
 btns[1].dispatchEvent(esc); assert.ok(pops[1].hidden); assert.equal(btns[1].getAttribute('aria-expanded'), 'false');
});
test('failed effort save retains value and active row and permits retry', async () => {
 const h = fixture(); h.w.supercliEffortPicker(h.box, {raw:'low'}, () => Promise.reject(Error('offline')));
 const btn = h.box.querySelector('.orch-btn'), rows = h.box.querySelectorAll('.prow');
 h.click(btn); h.click(rows[5]); await tick();
 assert.equal(btn.textContent, 'low'); assert.equal(h.box.querySelector('.active').textContent, 'low');
 assert.equal(btn.disabled, false); assert.deepEqual(h.errors, ['offline']);
});
test('Modes hides effort when a purpose is off and posts purpose-specific values', async () => {
 const h = fixture();
 const knobs = [{key:'advisor_model',raw:'off'}, {key:'review_model',raw:''}, {key:'darwin',state:'off'},
  ...['advisor','review','darwin'].map(p => ({key:p+'_reasoning_effort',raw:''}))];
 h.w.appendModesSection(h.box, {knobs}); assert.equal(h.box.querySelectorAll('.effort-pick').length, 0);
 knobs[0].raw = ''; knobs[1].raw = 'review-model'; knobs[2].state = 'on';
 h.box.replaceChildren(); h.w.appendModesSection(h.box, {knobs});
 const pickers = [...h.box.querySelectorAll('.effort-pick')]; assert.equal(pickers.length, 3);
 for (let i=0; i<3; i++) { h.click(pickers[i].querySelector('.orch-btn')); h.click(pickers[i].querySelectorAll('.prow')[5]); await tick(); }
 assert.deepEqual(h.posts.map(p => p.body.key), ['advisor_reasoning_effort','review_reasoning_effort','darwin_reasoning_effort']);
 assert.ok(h.posts.every(p => p.body.value === 'high'));
});
test('new effort labels and hints exist in all 27 GUI and TUI catalogs', () => {
 const files = readDir(sourcePath(assets,'locales')).filter(f => f.endsWith('.json')); assert.equal(files.length,27);
 for (const file of files) for (const dir of [sourcePath(assets,'locales'), sourcePath(assets,'../../system/uilang/catalogs')]) {
  const c = JSON.parse(readSource(sourcePath(dir,file),'utf8'));
  for (const key of ['effort.advisor','effort.review','effort.darwin','effort.hint']) assert.ok(c[key], file+': '+key);
 }
});
