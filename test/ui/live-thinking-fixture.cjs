const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Only plain paragraphs, thinking cards and browser event delivery are modeled.
// Actual HTML parsing is covered by the optional transcript DOM experiment.
module.exports = function thinkingFixture(expanded = false) {
  const roots = new Map(), frames = new Map(), documentEvents = new Map();
  let now = 0, sequence = 0, current = null;
  const metrics = {markdownUpdates: 0, saves: 0};
  const descendants = node => node.children.flatMap(child => [child, ...descendants(child)]);
  const matches = (node, selector) => selector.startsWith('.') ? node.classList.contains(selector.slice(1)) :
    node.tag === selector.replace(/\[.*$/, '');
  function element(tag, className = '', text, nodeType = 1) {
    const node = {tag, nodeType, className, children: [], parentNode: null, dataset: {}, style: {}, events: new Map(), open: false,
      appendChild(child) {return this.insertBefore(child, null);},
      insertBefore(child, before) {
        if (child.nodeType === 11) {child.children.slice().forEach(value => this.insertBefore(value, before)); return child;}
        child.remove();
        const index = before ? this.children.indexOf(before) : this.children.length;
        assert.ok(index >= 0, 'insertion anchor belongs to parent');
        this.children.splice(index, 0, child); child.parentNode = this; return child;
      },
      remove() {if (this.parentNode) this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1); this.parentNode = null;},
      appendData(value) {this.data += value;},
      addEventListener(name, fn) {if (!this.events.has(name)) this.events.set(name, new Set()); this.events.get(name).add(fn);},
      removeEventListener(name, fn) {this.events.get(name)?.delete(fn);},
      dispatch(name, event = {}) {for (const fn of Array.from(this.events.get(name) || [])) fn(event);},
      contains(child) {return child === this || descendants(this).includes(child);},
      querySelectorAll(selector) {return descendants(this).filter(child => matches(child, selector));},
      querySelector(selector) {return this.querySelectorAll(selector)[0] || null;},
      closest(selector) {return selector.split(',').some(value => matches(this, value)) ? this : this.parentNode?.closest(selector) || null;},
    };
    node.classList = {contains: value => node.className.split(/\s+/).includes(value), add(...values) {node.className += ' ' + values.join(' ');}};
    Object.defineProperties(node, {
      childNodes: {get() {return this.children;}},
      firstChild: {get() {return this.children[0] || null;}},
      firstElementChild: {get() {return this.children.find(child => child.nodeType === 1) || null;}},
      nextSibling: {get() {return this.parentNode ? this.parentNode.children[this.parentNode.children.indexOf(this) + 1] || null : null;}},
      isConnected: {get() {return Array.from(roots.values()).some(root => root.contains(this));}},
      textContent: {get() {return this.nodeType === 3 ? this.data : this.children.map(child => child.textContent || '').join('');}, set(value) {
        this.children.slice().forEach(child => child.remove());
        if (String(value)) {const child = element('#text', '', undefined, 3); child.data = String(value); this.appendChild(child);}
      }},
    });
    if (tag === 'template') {
      node.content = element('#fragment', '', undefined, 11);
      Object.defineProperty(node, 'innerHTML', {set(html) {
        assert.match(html, /^<details class="think-block"/, 'fixture only parses thinking card markup');
        const details = element('details', 'think-block'); details.open = /\sopen(?:\s|>)/.test(html);
        details.appendChild(element('summary', '', 'Thinking'));
        details.appendChild(element('div', 'think-content'));
        node.content.appendChild(details);
      }});
    }
    if (text !== undefined) node.textContent = text;
    return node;
  }
  const $ = selector => {if (!roots.has(selector)) roots.set(selector, element('div')); return roots.get(selector);};
  const c = {$, el: element, ui: {thinkingExpanded: expanded, keybinds: {thinking: 'Shift+R'}}, window: {},
    localStorage: {getItem: () => null},
    document: {hidden: false, hasFocus: () => true, activeElement: {tagName: 'BODY'}, createElement: element,
      addEventListener(name, fn) {documentEvents.set(name, fn);},
      createDocumentFragment: () => element('#fragment', '', undefined, 11),
      createComment: () => element('#comment', '', undefined, 8),
      createTextNode(text) {const node = element('#text', '', undefined, 3); node.data = text; return node;}},
    superCliUI: {createComposerDraftStore: () => ({restore() {}})},
    t: key => key === 'reasoning.noSummary' ? 'Model used {n} reasoning tokens without a text summary.' : key,
    fmtInteger: String, fmtDuration: String,
    escHtml: value => String(value).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'), escAttr: String,
    performance: {now: () => now},
    requestAnimationFrame(fn) {frames.set(++sequence, fn); return sequence;}, cancelAnimationFrame(id) {frames.delete(id);},
  };
  c.$$ = selector => c.stream.querySelectorAll(selector);
  vm.createContext(c);
  const source = name => fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js', name), 'utf8');
  for (const name of ['03-markdown.js', '04-transcript.js', '05-chat.js', '08-sessions.js']) vm.runInContext(source(name), c);
  const keyboard = source('12-keyboard-init.js');
  vm.runInContext(keyboard.slice(0, keyboard.indexOf('/* ═══ init ═══ */')), c);
  c.smartScroll = c.addFileChanges = c.addTurnMeta = c.setRunState = c.notifyDone = c.addEventLine = () => {};
  c.saveUI = () => {metrics.saves++;};
  c.addToolCall = () => c.appendStream(element('details', 'tool-row'));
  const update = c.updateMarkdownStream;
  c.updateMarkdownStream = (state, text) => {metrics.markdownUpdates++; return update(state, text);};
  function drain() {
    for (let i = 0; frames.size; i++) {
      assert.ok(i < 64, 'animation callbacks must finish'); now += 16;
      const work = Array.from(frames.values()); frames.clear(); work.forEach(fn => fn());
    }
  }
  function toggle(card, open) {if (card.open !== open) {card.open = open; card.dispatch('toggle');} drain();}
  return {c, metrics, frames, element, drain, toggle,
    event(value) {now += 48; current = c.handleEvent(value, current); drain(); return current;},
    thought(node) {return node.querySelector('.think-content');},
    click(card) {
      c.stream.dispatch('click', {target: card.querySelector('summary'), defaultPrevented: false});
      toggle(card, !card.open); // Browser default summary action follows click propagation.
    },
    key() {
      const cards = c.stream.querySelectorAll('.think-block'), before = cards.map(card => card.open);
      documentEvents.get('keydown')({key: 'r', shiftKey: true, preventDefault() {}});
      cards.forEach((card, i) => {if (card.open !== before[i]) card.dispatch('toggle');}); drain();
    },
  };
};
