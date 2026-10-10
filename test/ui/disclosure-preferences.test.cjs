const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// No browser, dependencies, image decoding or network. The counters measure DOM
// construction/source assignments, not WebView memory, network requests or FPS.
function baseHarness() {
  const nodes = new Map(), created = [], notices = [], stats = {imageSources: 0};
  let document;
  function descendants(node) { return node.children.flatMap(child => [child, ...descendants(child)]); }
  function element(tag, className = '', text, nodeType = 1) {
    const node = {tag, nodeType, className, children: [], style: {}, dataset: {}, attributes: {}, events: new Map(),
      open: false, parentNode: null, height: tag === 'button' ? 30 : 50,
      appendChild(child) { return this.insertBefore(child, null); },
      insertBefore(child, before) {
        if (child.nodeType === 11) { child.children.slice().forEach(value => this.insertBefore(value, before)); return child; }
        child.remove();
        const index = before ? this.children.indexOf(before) : this.children.length;
        assert.ok(index >= 0, 'insertBefore target must belong to parent');
        this.children.splice(index, 0, child); child.parentNode = this; return child;
      },
      remove() { if (this.parentNode) this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1); this.parentNode = null; },
      replaceChildren(...children) { this.children.slice().forEach(child => child.remove()); children.forEach(child => this.appendChild(child)); },
      setAttribute(name, value) { this.attributes[name] = value; },
      removeAttribute(name) { delete this.attributes[name]; },
      addEventListener(name, fn) { if (!this.events.has(name)) this.events.set(name, new Set()); this.events.get(name).add(fn); },
      removeEventListener(name, fn) { this.events.get(name)?.delete(fn); },
      dispatch(name) { Array.from(this.events.get(name) || []).forEach(fn => fn.call(this, {target: this})); },
      querySelector(selector) { return this.querySelectorAll(selector)[0] || null; },
      querySelectorAll(selector) {
        return descendants(this).filter(child => selector.split(',').some(value => {
          value = value.trim();
          return value.startsWith('.') ? child.classList?.contains(value.slice(1)) : child.tag === value;
        }));
      },
      focus() { document.activeElement = this; },
    };
    node.classList = {
      contains(name) { return node.className.split(/\s+/).includes(name); },
      add(...names) { node.className = Array.from(new Set(node.className.split(/\s+/).filter(Boolean).concat(names))).join(' '); },
      remove(...names) { node.className = node.className.split(/\s+/).filter(name => !names.includes(name)).join(' '); },
      toggle(name, force) { const yes = force === undefined ? !this.contains(name) : force; if (yes) this.add(name); else this.remove(name); },
    };
    Object.defineProperties(node, {
      childNodes: {get() { return this.children; }},
      firstChild: {get() { return this.children[0] || null; }},
      nextSibling: {get() { return this.parentNode ? this.parentNode.children[this.parentNode.children.indexOf(this) + 1] || null : null; }},
      isConnected: {get() { return Array.from(nodes.values()).some(root => root === this || descendants(root).includes(this)); }},
      textContent: {get() { return this.nodeType === 3 ? this.text : this.children.map(child => child.textContent).join(''); }, set(value) {
        this.replaceChildren();
        if (String(value)) { const child = element('#text', '', undefined, 3); child.text = String(value); this.appendChild(child); }
      }},
      innerHTML: {configurable:true, get() { return this.html || ''; }, set(value) { this.replaceChildren(); this.html = String(value); }},
      src: {get() { return this.source || ''; }, set(value) { this.source = value; if (tag === 'img') stats.imageSources++; }},
    });
    if (text != null) node.textContent = text;
    if (nodeType === 1) created.push(node);
    return node;
  }
  const $ = selector => { if (!nodes.has(selector)) nodes.set(selector, element('div')); return nodes.get(selector); };
  document = {hidden: false, hasFocus: () => true, createElement: element, createDocumentFragment: () => element('#fragment', '', undefined, 11)};
  const c = {$, $$: () => [], el: element, i18nEl: (tag, cls, key) => element(tag, cls, key), t: key => key,
    document, localStorage: {getItem: () => null}, window: {}, AbortController, Promise,
    superCliUI: {fileMutationTools: {}, mutationKind: () => '', createComposerDraftStore: () => ({restore() {}})},
    requestAnimationFrame: () => 1, clearInterval() {}, performance: {now: () => 100},
    prettyJSON: value => { try { return JSON.stringify(JSON.parse(value), null, 2); } catch { return value; }},
    toolHint: (name, args) => ({name, hint: args}), toolDisplayName: name => name,
    clip: (value, length) => String(value).slice(0, length), fmtDuration: () => '0.1s',
    toast: value => notices.push(value), fetch() { throw Error('Unexpected network request'); },
  };
  vm.createContext(c);
  for (const file of ['04-transcript.js', '05-chat.js', '08-sessions.js']) {
    vm.runInContext(fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js', file), 'utf8'), c);
  }
  c.toolHint = (name, args) => ({name, hint: args});
  c.resetWorkerOverview = () => {};
  c.loadSessions = () => {};
  c.renderStats = () => {};
  c.restoreSessionRuntime = () => {};
  c.setSessionOpening = () => {};
  c.stage.scrollTop = 0;
  c.stage.clientHeight = 500;
  Object.defineProperty(c.stage, 'scrollHeight', {get() { return c.stream.children.reduce((sum, row) => sum + row.height, 0); }});
  return {c, created, stats, notices, element};
}

function preferenceHarness(settings = {}) {
  const h = baseHarness(), c = h.c, posts = [], timers = new Map(), events = new Map();
  let nextTimer = 0;
  const matches = (node, selector) => selector.split(',').some(value => {
    value = value.trim();
    if (value.startsWith('.')) return node.classList?.contains(value.slice(1));
    if (value === 'details[data-think-id]') return node.tag === 'details' && node.attributes['data-think-id'];
    return node.tag === value;
  });
  const originalElement = h.element;
  function element(tag, cls, text) {
    const node = originalElement(tag, cls, text);
    node.closest = selector => {
      for (let current = node; current; current = current.parentNode) if (matches(current, selector)) return current;
      return null;
    };
    node.contains = target => target === node || descendants(node).includes(target);
    node.querySelectorAll = selector => descendants(node).filter(child => matches(child, selector));
    if (tag === 'template') {
      node.content = originalElement('#fragment');
      Object.defineProperty(node.content,'firstElementChild',{get(){return node.content.children[0]||null;}});
      Object.defineProperty(node, 'innerHTML', {set(html) {
        const thought = element('details','think-block');
        thought.open = /\sopen(?:\s|>)/.test(html);
        thought.setAttribute('data-think-id','think-1');
        thought.appendChild(element('summary'));
        thought.appendChild(element('div','think-content'));
        node.content.replaceChildren(thought);
      }});
    }
    return node;
  }
  function descendants(node) { return node.children.flatMap(child => [child, ...descendants(child)]); }
  // Existing roots were created before these generic DOM methods were attached.
  for (const node of [c.stream,c.stage]) {
    node.contains = target => target === node || descendants(node).includes(target);
    node.querySelectorAll = selector => descendants(node).filter(child => matches(child, selector));
  }
  c.document.createElement = c.el = element;
  c.i18nEl = (tag,cls,key) => element(tag,cls,key);
  c.document.activeElement = {tagName:'BODY'};
  c.document.documentElement = element('html');
  c.document.addEventListener = (name, listener) => events.set(name,listener);
  c.window.addEventListener = () => {};
  c.window.matchMedia = () => ({matches:false,addEventListener(){}});
  c.detectedLanguage = () => 'en'; c.normalizeLanguage = value => value;
  c.loadLanguage = async () => {}; c.escHtml = c.escAttr = String;
  c.setTimeout = fn => { const id=++nextTimer; timers.set(id,fn); return id; };
  c.clearTimeout = id => timers.delete(id); c.setInterval = () => 1;
  c.localStorage = {getItem:() => null, setItem(){throw Error('Portable preferences must not write browser storage');}};
  c.j = async url => {assert.equal(url,'/api/settings'); return {settings};};
  c.jpost = async (url,patch) => {posts.push({url,patch});};
  for (const file of ['02-ui-settings.js','03-markdown.js']) vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js',file),'utf8'),c);
  c.applyUI = () => {};
  c.markdownStream = target => ({target});
  c.updateMarkdownStream = (state,text) => {state.target.textContent=text;};
  const keys = fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js/12-keyboard-init.js'),'utf8');
  vm.runInContext(keys.slice(0,keys.indexOf('/* ═══ init ═══ */')),c);
  c.$$ = selector => c.stream.querySelectorAll(selector);
  function activate(target, extra = {}) {
    const event = {target,defaultPrevented:false,preventDefault(){this.defaultPrevented=true;},...extra};
    Array.from(c.stream.events.get('click') || []).forEach(fn => fn(event));
    // Native summary activation happens after click propagation, then emits an
    // asynchronous toggle. This models the intended value before that action.
    const summary = target.closest('summary'), row=summary?.parentNode;
    if (row && !event.defaultPrevented && !target.closest('a,button,input,select,textarea,label')) {row.open=!row.open;row.dispatch('toggle');}
  }
  function key(value) {events.get('keydown')({key:value,shiftKey:true,preventDefault(){}});}
  function flush() {const callbacks=Array.from(timers.values());timers.clear();callbacks.forEach(fn=>fn());}
  return {...h,c,element,posts,activate,key,flush,timers};
}
function summaryCard(h, cls = 'tool-row') {
  const row=h.element('details',cls), summary=h.element('summary');
  summary.appendChild(h.element('span')); row.appendChild(summary); h.c.appendStream(row); return row;
}

test('portable disclosure preferences normalize and reload without writes or changing unset thought defaults', async () => {
  for (const settings of [{},{'ui.toolsExpanded':true,'ui.thinkingExpanded':false},{'ui.toolsExpanded':'yes','ui.thinkingExpanded':'false'}]) {
    const h=preferenceHarness(settings); await h.c.loadUI();
    assert.equal(h.c.ui.toolsExpanded,settings['ui.toolsExpanded']===true);
    assert.equal(h.c.ui.thinkingExpanded,typeof settings['ui.thinkingExpanded']==='boolean'?settings['ui.thinkingExpanded']:null);
    assert.equal(h.posts.length,0);assert.equal(h.timers.size,0);
    if (h.c.ui.thinkingExpanded===null) {assert.equal(h.c.thinkingDisclosureOpen(false),true);assert.equal(h.c.thinkingDisclosureOpen(true),false);}
  }
  const h=preferenceHarness();await h.c.loadUI();const row=summaryCard(h);h.activate(row.children[0]);h.flush();
  assert.equal(h.posts.length,1);assert.equal(h.posts[0].url,'/api/settings');
  const next=preferenceHarness(h.posts[0].patch);await next.c.loadUI();assert.equal(next.c.ui.toolsExpanded,true);assert.equal(next.posts.length,0);
});

test('only outer user summary activation persists; nested controls and programmatic toggles do not', async () => {
  const h=preferenceHarness();await h.c.loadUI();const row=summaryCard(h), summary=row.children[0];
  row.open=true;row.dispatch('toggle');assert.equal(h.timers.size,0);
  h.activate(summary.children[0]);h.flush();assert.equal(row.open,false);assert.equal(h.posts.length,1);assert.equal(h.posts[0].patch['ui.toolsExpanded'],false);
  h.activate(summary,{detail:0});h.flush();assert.equal(row.open,true);assert.equal(h.posts.length,2);assert.equal(h.posts[1].patch['ui.toolsExpanded'],true);
  const button=h.element('button');summary.appendChild(button);h.activate(button);
  const nested=h.element('details'), nestedSummary=h.element('summary');nested.appendChild(nestedSummary);row.appendChild(nested);h.activate(nestedSummary);
  h.activate(summary,{defaultPrevented:true});assert.equal(h.timers.size,0);assert.equal(h.posts.length,2);
  const thought=summaryCard(h,'think-block');h.activate(thought.children[0]);h.flush();assert.equal(h.posts[2].patch['ui.thinkingExpanded'],true);
});

test('bulk disclosure changes save once and ignore ensuing per-card toggle events', async () => {
  const h=preferenceHarness();await h.c.loadUI();const rows=Array.from({length:20},()=>summaryCard(h));
  h.key('e');rows.forEach(row=>row.dispatch('toggle'));h.flush();
  assert.ok(rows.every(row=>row.open));assert.equal(h.posts.length,1);assert.equal(h.posts[0].patch['ui.toolsExpanded'],true);
  h.key('e');rows.forEach(row=>row.dispatch('toggle'));h.flush();assert.ok(rows.every(row=>!row.open));assert.equal(h.posts.length,2);
  const thoughts=Array.from({length:3},()=>summaryCard(h,'think-block'));h.key('t');thoughts.forEach(row=>row.dispatch('toggle'));h.flush();
  assert.ok(thoughts.every(row=>row.open));assert.equal(h.posts.length,3);assert.equal(h.posts[2].patch['ui.thinkingExpanded'],true);
});

test('expanded preference applies to future live/history rows while retaining exact input and releasing consumed args', async () => {
  const h=preferenceHarness({'ui.toolsExpanded':true});await h.c.loadUI();
  h.c.addToolCall('custom_tool','{"value":"source"}','live');const live=h.c.toolRows.live;
  assert.equal(live.open,true);assert.equal(live._body.hidden,false);assert.equal(live._toolArgs,null);
  assert.equal(live._body.querySelector('pre').textContent,'{\n  "value": "source"\n}');
  const token='session:fixture_123/'+'c'.repeat(64)+'.png';h.c.appendNativeToolImages(live,[token]);assert.equal(live._body.querySelectorAll('img').length,1);
  const history=h.c.buildHistoryFragment([{seq:1,role:'tool',name:'custom_tool',content:'raw'}]).children[0];
  assert.equal(history.open,true);assert.equal(history._body.querySelector('pre').textContent,'raw');assert.equal(h.timers.size,0);assert.equal(h.posts.length,0);
});

test('thought preference covers inline and native segments, restored lazy materialization, and within-block choice', async () => {
  for (const expanded of [null,false,true]) {
    const h=preferenceHarness({'ui.thinkingExpanded':expanded});await h.c.loadUI();
    const inline=h.c.renderThinkBlock('thought');assert.equal(/class="think-block" open/.test(inline),expanded!==false);
    const live=h.c.assistantPart({kind:'thinking',text:'live thought'},false);
    assert.equal(live.node.open,expanded===null?true:expanded);assert.equal(live.node.querySelector('.think-content').textContent,expanded===false?'':'live thought');
    if (!live.node.open) {live.node.open=true;live.node.dispatch('toggle');}
    assert.equal(live.node.querySelector('.think-content').textContent,'live thought');
    const old=h.c.assistantPart({kind:'thinking',text:'saved thought'},true);
    assert.equal(old.node.open,expanded===true);assert.equal(old.node.querySelector('.think-content').textContent,expanded===true?'saved thought':'');
    if (!old.node.open) {old.node.open=true;old.node.dispatch('toggle');assert.equal(old.node.querySelector('.think-content').textContent,'saved thought');}
    const node=h.element('div');node._raw='<thinking>same block</thinking>answer';h.c.renderAssistant(node);
    const thought=node._assistantParts[0].node;thought.open=false;h.c.ui.thinkingExpanded=true;node._raw+=" more";h.c.renderAssistant(node);
    assert.equal(node._assistantParts[0].node,thought);assert.equal(thought.open,false);
    const native=h.element('div');native._raw='';native._displayParts=[{kind:'thinking',text:'native'},{kind:'markdown',text:'answer'}];h.c.renderAssistant(native);
    assert.equal(native._assistantParts[0].node.open,true);assert.equal(h.posts.length,0);assert.equal(h.timers.size,0);
  }
});
