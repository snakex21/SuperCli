const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

// Exercise real event dispatch, assistant parts and Markdown DOM placement.
// This small DOM implements only the element operations used by plain streaming
// paragraphs and thinking cards; it is not a layout/FPS/browser measurement.
function harness() {
  const roots = new Map(), frames = new Map();
  let now = 0, sequence = 0;
  function descendants(node) { return node.children.flatMap(child => [child, ...descendants(child)]); }
  function element(tag, className = '', text, nodeType = 1) {
    const node = {tag, nodeType, className, children: [], parentNode: null, dataset: {}, style: {}, events: new Map(), open: false,
      appendChild(child) { return this.insertBefore(child, null); },
      insertBefore(child, before) {
        if (child.nodeType === 11) { child.children.slice().forEach(value => this.insertBefore(value, before)); return child; }
        child.remove();
        const index = before ? this.children.indexOf(before) : this.children.length;
        assert.ok(index >= 0, 'insertion anchor belongs to parent');
        this.children.splice(index, 0, child); child.parentNode = this; return child;
      },
      remove() { if (this.parentNode) this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1); this.parentNode = null; },
      appendData(value) { this.data += value; },
      addEventListener(name, fn) { this.events.set(name, fn); },
      removeEventListener(name) { this.events.delete(name); },
      querySelector(selector) { return descendants(this).find(child => selector.startsWith('.') ? child.classList.contains(selector.slice(1)) : child.tag === selector) || null; },
    };
    node.classList = {contains: value => node.className.split(/\s+/).includes(value), add(...values) {node.className += ' ' + values.join(' ');}};
    Object.defineProperties(node, {
      childNodes: {get() {return this.children;}},
      firstChild: {get() {return this.children[0] || null;}},
      firstElementChild: {get() {return this.children.find(child => child.nodeType === 1) || null;}},
      nextSibling: {get() {return this.parentNode ? this.parentNode.children[this.parentNode.children.indexOf(this) + 1] || null : null;}},
      isConnected: {get() {return Array.from(roots.values()).some(root => root === this || descendants(root).includes(this));}},
      textContent: {get() {return this.nodeType === 3 ? this.data : this.children.map(child => child.textContent || '').join('');}, set(value) {
        this.children.slice().forEach(child => child.remove());
        if (String(value)) {const child = element('#text', '', undefined, 3); child.data = String(value); this.appendChild(child);}
      }},
    });
    if (tag === 'template') {
      node.content = element('#fragment', '', undefined, 11);
      Object.defineProperty(node, 'innerHTML', {set(html) {
        assert.match(html, /^<details class="think-block"/);
        const details = element('details', 'think-block'); details.open = /\sopen(?:\s|>)/.test(html);
        details.appendChild(element('summary', '', 'Thinking'));
        details.appendChild(element('div', 'think-content'));
        node.content.appendChild(details);
      }});
    }
    if (text !== undefined) node.textContent = text;
    return node;
  }
  const $ = selector => { if (!roots.has(selector)) roots.set(selector, element('div')); return roots.get(selector); };
  const c = {$, $$: () => [], el: element, ui: {thinkingExpanded: null}, window: {}, localStorage: {getItem: () => null},
    document: {hidden: false, hasFocus: () => true, createElement: element,
      createDocumentFragment: () => element('#fragment', '', undefined, 11),
      createComment: () => element('#comment', '', undefined, 8),
      createTextNode(text) {const node = element('#text', '', undefined, 3); node.data = text; return node;}},
    superCliUI: {createComposerDraftStore: () => ({restore() {}})},
    t: key => key === 'reasoning.noSummary' ? 'Model used {n} reasoning tokens without a text summary.' : key,
    fmtInteger: String, fmtDuration: String,
    escHtml: value => String(value).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;"),
    escAttr: String, performance: {now: () => now},
    requestAnimationFrame(fn) {frames.set(++sequence, fn); return sequence;}, cancelAnimationFrame(id) {frames.delete(id);},
  };
  vm.createContext(c);
  for (const name of ['03-markdown.js', '04-transcript.js', '05-chat.js']) {
    vm.runInContext(fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js', name), 'utf8'), c);
  }
  c.smartScroll = () => {}; c.addFileChanges = () => {}; c.addTurnMeta = () => {}; c.setRunState = () => {}; c.notifyDone = () => {};
  c.addToolCall = () => c.appendStream(element('details', 'tool-row'));
  let current = null;
  return {c, frames, stream: c.stream, event(value) {current = c.handleEvent(value, current); return current;},
    advance() {now += 280;},
    flushFrame() {now += 16; const work = Array.from(frames.values()); frames.clear(); work.forEach(fn => fn());},
    cards(node) {return node.children.filter(child => child.nodeType === 1);},
  };
}

function assertOrder(h, node, answer, thought) {
  const cards = h.cards(node);
  assert.equal(cards[0].className, 'think-block', 'thinking card is the first visible child');
  assert.equal(cards[0].querySelector('.think-content').textContent, thought);
  assert.equal(cards.slice(1).map(child => child.textContent).join(''), answer, 'complete answer follows the thought');
  assert.deepEqual(Array.from(node._assistantParts, part => part.kind), ['thinking', 'markdown']);
}

test('usage-only summary arriving after the answer appears above it without replacing answer nodes', () => {
  const h = harness(), answer = 'Completed answer for the user.';
  const node = h.event({type: 'message', text: answer}), paragraph = h.cards(node).find(child => child.tag === 'p'), text = paragraph.firstChild;
  h.advance();
  h.event({type: 'reasoning', reasoning_tok: 151});
  assertOrder(h, node, answer, 'Model used 151 reasoning tokens without a text summary.');
  assert.equal(h.cards(node).find(child => child.tag === 'p'), paragraph); assert.equal(paragraph.firstChild, text);
  assert.equal(h.event({type: 'done'}), null);
  h.c.flushAssistantRender(node); h.c.sealAssistantSegment(node);
  assertOrder(h, node, answer, 'Model used 151 reasoning tokens without a text summary.');
  assert.equal(h.cards(node).find(child => child.tag === 'p'), paragraph); assert.equal(paragraph.firstChild, text);
  assert.equal(node._reasoningOpen, false); assert.equal(node._sealed, true); assert.equal(h.frames.size, 0);
  assert.equal(node._pacedParts, null); assert.equal(node._displayParts, null); assert.equal(node._partsCache, null);
});

test('delayed native fragments stay in one leading thought and preserve folded state and exact final prose', () => {
  const h = harness(), node = h.event({type: 'reasoning', text: 'First thought.'});
  h.event({type: 'message', text: 'Answer.'});
  const thought = h.cards(node)[0], paragraph = h.cards(node).find(child => child.tag === 'p'); thought.open = false;
  h.advance(); h.event({type: 'reasoning', text: ' Delayed thought.'});
  assertOrder(h, node, 'Answer.', 'First thought. Delayed thought.');
  assert.equal(h.cards(node)[0], thought); assert.equal(thought.open, false); assert.equal(h.cards(node).find(child => child.tag === 'p'), paragraph);
  h.advance(); h.event({type: 'message', text: ' More prose received.'});
  assert.ok(h.frames.size > 0, 'completion exercises pending paced answer text');
  h.event({type: 'done'});
  assertOrder(h, node, 'Answer. More prose received.', 'First thought. Delayed thought.');
  assert.equal(h.cards(node)[0], thought); assert.equal(h.cards(node).find(child => child.tag === 'p'), paragraph); assert.equal(thought.open, false);
  assert.equal(h.frames.size, 0); assert.equal(node._raw, '<thinking>First thought. Delayed thought.</thinking>\nAnswer. More prose received.');
});

test('late reasoning before EOF/error and replacement recovery uses the current answer, never a stale boundary', () => {
  for (const terminal of ['EOF', 'error']) {
    const h = harness(), node = h.event({type: 'message', text: 'Original answer.'});
    h.event({type: 'reasoning', text: 'Original thought.'});
    node._raw = 'Recovered answer.'; h.c.flushAssistantRender(node);
    const recovered = h.cards(node).find(child => child.tag === 'p');
    h.event({type: 'reasoning', text: 'Recovered thought.'});
    if (terminal === 'EOF') h.c.sealAssistantSegment(node);
    else {h.c.addEventLine = () => {}; h.event({type: 'error', err: 'fixture'});}
    assertOrder(h, node, 'Recovered answer.', 'Recovered thought.');
    assert.equal(h.cards(node).find(child => child.tag === 'p'), recovered); assert.equal(h.frames.size, 0);
    assert.ok(!node.textContent.includes('Original'));
  }
});

test('native reasoning preserves segment boundaries and empty count notifications do not create rows', () => {
  const h = harness(); assert.equal(h.event({type: 'reasoning', reasoning_tok: 0}), null); assert.equal(h.stream.children.length, 0);
  const old = h.event({type: 'message', text: 'Prior segment.'}); h.event({type: 'tool_call', name: 'read_lines', id: 'one'});
  const next = h.event({type: 'reasoning', text: 'Next thought.'}); h.event({type: 'message', text: 'Next answer.'}); h.event({type: 'done'});
  assert.notEqual(next, old); assert.equal(old.textContent, 'Prior segment.'); assert.equal(old._sealed, true);
  assertOrder(h, next, 'Next answer.', 'Next thought.'); assert.equal(h.frames.size, 0);
});
