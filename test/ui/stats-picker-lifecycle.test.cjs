const assert = require('node:assert/strict');
const cp = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const sourcePath = path.resolve(__dirname, '../../internal/webgui/assets/js/07-stats.js');
function fixture(source) {
  const listeners = new Map(), counts = {contains: 0, detachedContains: 0};
  let language = 'en';
  function matches(node, selector) {
    return selector[0] === '.' ? node.className.split(' ').includes(selector.slice(1)) : node.tag === selector;
  }
  function element(tag, className = '', text = '') {
    const node = {tag, className, text: String(text), children: [], parentNode: null, hidden: false, value: '', events: new Map(), attributes: {},
      appendChild(child) { child.remove(); child.parentNode = this; this.children.push(child); return child; },
      remove() { if (this.parentNode) this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1); this.parentNode = null; },
      replaceChildren(...children) { this.children.slice().forEach(child => child.remove()); children.forEach(child => this.appendChild(child)); },
      setAttribute(name, value) { this.attributes[name] = String(value); },
      addEventListener(type, fn) { if (!this.events.has(type)) this.events.set(type, new Set()); this.events.get(type).add(fn); },
      contains(target) { counts.contains++; if (!this.isConnected) counts.detachedContains++; for (let n = target; n; n = n.parentNode) if (n === this) return true; return false; },
      querySelectorAll(selector) { const found = []; for (const child of this.children) { if (matches(child, selector)) found.push(child); found.push(...child.querySelectorAll(selector)); } return found; },
      querySelector(selector) { return this.querySelectorAll(selector)[0] || null; },
      focus() { if (!this.isConnected) return; for (let n = this; n; n = n.parentNode) if (n.hidden) return; document.activeElement = this; },
    };
    Object.defineProperties(node, {
      isConnected: {get() { for (let n = this; n; n = n.parentNode) if (n === body) return true; return false; }},
      textContent: {get() { return this.text + this.children.map(child => child.textContent).join(''); }},
      innerHTML: {set(value) { if (value !== '') throw Error('fixture only clears HTML'); this.replaceChildren(); }},
    });
    return node;
  }
  const body = element('body'), document = {body, activeElement: body,
    addEventListener(type, fn) { if (!listeners.has(type)) listeners.set(type, new Set()); listeners.get(type).add(fn); },
    getElementsByClassName(name) { return body.querySelectorAll('.' + name); },
  };
  const context = {document, window: {}, modelCache: [
    {id: 'model-a', provider: 'main'}, {id: 'model-b', provider: 'other'}, {id: 'hidden', hidden: true},
  ], activeProviderID: 'main', t: key => language + ':' + key, el: element,
    i18nEl: (tag, className, key) => element(tag, className, language + ':' + key)};
  vm.createContext(context); vm.runInContext(source, context);
  function emit(target, type = 'click') {
    const event = {target, type};
    for (let node = target; node; node = node.parentNode) for (const fn of Array.from(node.events.get(type) || [])) fn.call(node, event);
    for (const fn of Array.from(listeners.get(type) || [])) fn(event);
  }
  function host() { return body.appendChild(element('section', 'arbitrary-host')); }
  function picker(container, raw, onSave) { context.supercliOrchPicker(container, {raw}, onSave); return container.children.at(-1); }
  return {body, document, context, counts, listeners, element, emit, host, picker, setLanguage(value) { language = value; }};
}

test('picker outside-click registration is lazy, shared and keeps first-use listener ordering', () => {
  const h = fixture(fs.readFileSync(sourcePath, 'utf8'));
  assert.equal(h.listeners.get('click')?.size || 0, 0, 'source loading must not install a click listener');
  const states = [], host = h.host();
  let popup;
  h.document.addEventListener('click', () => states.push(['before', popup.hidden]));
  const first = h.picker(host, '', () => {}); popup = first.querySelector('.orch-pop');
  h.document.addEventListener('click', () => states.push(['after', popup.hidden]));
  h.picker(h.host(), '', () => {});
  assert.equal(h.listeners.get('click').size, 3, 'two unrelated listeners and one picker listener');
  h.emit(first.querySelector('.orch-btn')); states.length = 0;
  h.emit(h.body);
  assert.deepEqual(states, [['before', false], ['after', true]]);
});

test('generic connected pickers preserve inside/outside clicks, filtering and save semantics', () => {
  const h = fixture(fs.readFileSync(sourcePath, 'utf8')), saves = [];
  const first = h.picker(h.host(), '', value => saves.push(value));
  const second = h.picker(h.host(), 'other/model-b', value => saves.push(value));
  const button = first.querySelector('.orch-btn'), popup = first.querySelector('.orch-pop');
  h.emit(button);
  assert.equal(popup.hidden, false);
  assert.equal(first.querySelectorAll('.prow').length, 3, 'default and two visible models');
  h.emit(first.querySelector('.prow'));
  assert.deepEqual(saves, [], 'choosing the current default is not a change');
  h.emit(button);
  h.emit(second.querySelector('.orch-btn'));
  assert.equal(popup.hidden, true, 'opening another picker closes the first');
  const secondPopup = second.querySelector('.orch-pop'), search = second.querySelector('input');
  h.emit(search); assert.equal(secondPopup.hidden, false);
  search.value = '  MODEL-B  '; h.emit(search, 'input');
  const rows = second.querySelectorAll('.prow');
  assert.equal(rows.length, 2, 'filter retains the default row and matching model');
  h.emit(rows[1]); assert.deepEqual(saves, [], 'active provider/model ref is not saved again');
  h.emit(second.querySelector('.orch-btn'));
  h.emit(second.querySelector('.prow')); assert.deepEqual(saves, ['']);
  h.emit(button);
  const other = first.querySelectorAll('.prow')[2];
  assert.equal(other.textContent, 'model-bother');
  h.emit(other); assert.deepEqual(saves, ['', 'other/model-b']);
  h.setLanguage('pl'); h.emit(button);
  assert.equal(first.querySelector('.orch-btn'), button, 'reopening preserves button identity');
  assert.equal(first.querySelector('input').placeholder, 'pl:model.search');
  assert.equal(first.querySelector('.prow').textContent, 'pl:stats.orchModelDef');
  const focused = h.document.activeElement;
  h.emit(h.body);
  assert.equal(popup.hidden, true);
  assert.equal(h.document.activeElement, focused, 'outside close must not move focus');
});

async function retentionCheck(makeFixture, currentSourcePath) {
  const h = makeFixture(fs.readFileSync(currentSourcePath, 'utf8')), host = h.host();
  function rebuild() {
    const refs = [];
    for (let i = 0; i < 100; i++) {
      host.replaceChildren();
      const picker = h.picker(host, '', () => {});
      refs.push(new WeakRef(picker));
      h.emit(picker.querySelector('.orch-btn'));
      const search = picker.querySelector('input');
      search.value = 'model'; h.emit(search, 'input');
    }
    return refs;
  }
  const refs = rebuild();
  await new Promise(resolve => setImmediate(() => { global.gc(); setImmediate(() => { global.gc(); resolve(); }); }));
  assert.equal(refs[0].deref(), undefined, 'first detached materialized picker is still retained');
  assert.equal(refs.filter(ref => ref.deref()).length, 1, 'only the current picker remains reachable');
  assert.equal(h.listeners.get('click').size, 1, 'rebuilds must not accumulate document callbacks');
  h.counts.contains = 0; h.emit(h.body);
  assert.equal(h.counts.contains, 1); assert.equal(h.counts.detachedContains, 0);
  assert.equal(host.children[0].querySelector('.orch-pop').hidden, true);
}

test('rebuilding materialized lists releases the first and subsequent detached pickers', async () => {
  const script = "const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'); const fixture=" + fixture.toString() + "; (" + retentionCheck.toString() + ")(fixture,process.argv[1]).catch(error=>{console.error(error);process.exitCode=1;});";
  await new Promise((resolve, reject) => cp.execFile(process.execPath, ['--expose-gc', '-e', script, sourcePath], {windowsHide: true}, (error, stdout, stderr) => {
    if (error) reject(new Error(stderr || stdout || error.message)); else resolve();
  }));
});


test('opening focuses the visible search so keyboard filtering is immediately available', () => {
  const h = fixture(fs.readFileSync(sourcePath, 'utf8')), saves = [];
  const picker = h.picker(h.host(), '', value => saves.push(value));
  const button = picker.querySelector('.orch-btn'), popup = picker.querySelector('.orch-pop');
  assert.equal(h.document.activeElement, h.body);
  h.emit(button);
  const search = picker.querySelector('input');
  assert.equal(popup.hidden, false);
  assert.equal(h.document.activeElement, search, 'opening must focus the visible input');
  search.value = 'model-b'; h.emit(h.document.activeElement, 'input');
  assert.equal(picker.querySelectorAll('.prow').length, 2);
  h.emit(button);
  assert.equal(popup.hidden, true);
  assert.equal(picker.querySelector('input'), search, 'closing does not rebuild the list');
  h.emit(button);
  const reopened = picker.querySelector('input');
  assert.notEqual(reopened, search);
  assert.equal(h.document.activeElement, reopened, 'reopening focuses the fresh visible input');
  assert.equal(picker.querySelector('.orch-btn'), button);
  assert.deepEqual(saves, []);
});
