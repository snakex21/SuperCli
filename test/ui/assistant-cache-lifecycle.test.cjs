const assert = require('node:assert/strict');
const test = require('node:test');
const path = require('node:path');
let transcriptDOM;
try { transcriptDOM = require('../../scripts/transcript-dom-fixture.cjs'); }
catch (error) {
  if (error.code !== 'MODULE_NOT_FOUND' || !error.message.includes("'linkedom'")) throw error;
}
const root = path.resolve(__dirname, '../..');
// Keep the shared prototype independent of the fixture activation and its c closure.
function detailsOpenGet() { return this.hasAttribute('open'); }
function detailsOpenSet(value) { this.toggleAttribute('open', !!value); }
const openDescriptor = {configurable: true, get: detailsOpenGet, set: detailsOpenSet};
function harness() {
  const c = transcriptDOM(root);
  Object.defineProperty(c.document.defaultView.HTMLElement.prototype, 'open', openDescriptor);
  Object.assign(c, {activeSessionID: 'owned-session', runStart: Date.now(), pendingImmediate: false, pauseQueue: false,
    promptQueue: [], setRunState() {}, notifyDone() {}, addTurnMeta() {}, chatErrorText: ev => ev.err || "synthetic"});
  c.ui.thinkingExpanded = false;
  return c;
}
function domTest(name, fn) {
  test(name, {skip: !transcriptDOM && 'Optional LinkeDOM dependency required by actual DOM fixture'}, fn);
}
function normalized(node) {
  const copy = node.cloneNode(true); copy.normalize();
  function tree(n) {
    if (n.nodeType === 3) return n.data;
    return [n.nodeName, Array.from(n.attributes || []).map(a => [a.name, a.value]).sort(),
      Array.from(n.childNodes).filter(n => n.nodeType === 1 || n.nodeType === 3 && n.data).map(tree)];
  }
  return JSON.stringify(Array.from(copy.childNodes).filter(n => n.nodeType === 1 || n.nodeType === 3 && n.data).map(tree));
}
function assertCanonical(c, node) {
  const full = c.document.createElement('div'); full.innerHTML = c.renderText(node._raw);
  const renderedThoughts = node.querySelectorAll('details.think-block');
  full.querySelectorAll('details.think-block').forEach((card, i) => {card.open = renderedThoughts[i].open;});
  assert.equal(normalized(node), normalized(full));
}
function assertReleased(node) {
  for (const part of node._assistantParts) {
    assert.equal(part.complete, true);
    if (part.markdown) assert.equal(part.markdown.paragraph, null);
  }
}
function reveal(c, card, open) {card.open = open; card.dispatchEvent(new c.Event('toggle'));}

domTest('final paragraph cleanup preserves active caching, source, tail nodes and terminal/tool order', () => {
  for (const terminal of ['done', 'error', 'tool_call', 'Stop', 'EOF']) {
    const c = harness(), node = c.addAssistantMsg(), raw = '**bold** x & <y> answer';
    c.appendAssistantSource(node, raw); c.renderAssistant(node);
    const markdown = node._assistantParts[0].markdown, paragraph = node.querySelector('p');
    const tailNodes = markdown.tailNodes, cache = markdown.paragraph, html = node.innerHTML;
    assert(cache && cache.html); c.flushAssistantRender(node);
    assert.equal(markdown.paragraph, cache, 'active flush must preserve the streaming cache');
    if (terminal === 'Stop' || terminal === 'EOF') c.sealAssistantSegment(node);
    else assert.equal(c.handleEvent({type: terminal, name: 'read_lines', args: '{"path":"local.txt"}', id: 'local', err: 'synthetic'}, node), null);
    assertReleased(node); assert.equal(node._raw, raw); assert.equal(markdown.source, raw);
    assert.equal(markdown.tailNodes, tailNodes); assert.equal(node.querySelector('p'), paragraph);
    assert.equal(node.innerHTML, html); assertCanonical(c, node);
    c.flushAssistantRender(node); c.renderAssistant(node);
    assertReleased(node); assert.equal(node.querySelector('p'), paragraph); assert.equal(node.innerHTML, html);
    if (terminal === 'tool_call') {
      assert.equal(node.nextElementSibling, c.toolRows.local);
      const next = c.handleEvent({type: 'message', text: '**next** answer'}, null);
      c.flushAssistantRender(next);
      assert.notEqual(next, node); assert.equal(next.previousElementSibling, c.toolRows.local);
      assert(next._assistantParts[0].markdown.paragraph, 'the next segment remains active');
      assert.equal(node.innerHTML, html);
    }
  }
});

domTest('completed source continuation and replacement recover the descriptor without duplicate paragraphs', () => {
  for (const raw of ['plain "quotes" & <angle>', '**bold** "quotes" & <angle>', 'First.\n\n**tail** x & <y>']) {
    const c = harness(), node = c.addAssistantMsg();
    c.appendAssistantSource(node, raw); c.sealAssistantSegment(node);
    const paragraphs = Array.from(node.querySelectorAll('p')), last = paragraphs.at(-1);
    const inline = last.querySelector('strong'), previousText = node._raw;
    c.appendAssistantSource(node, ' continued & <value>'); c.renderAssistant(node);
    assert.equal(node._raw, previousText + ' continued & <value>'); assertReleased(node); assertCanonical(c, node);
    assert.equal(node.querySelectorAll('p').length, paragraphs.length);
    assert.equal(Array.from(node.querySelectorAll('p')).at(-1), last);
    if (inline) assert.equal(last.querySelector('strong'), inline);
    node._raw = 'Recovered\n\n- one\n- two\n\n**new tail**'; c.renderAssistant(node);
    assertReleased(node); assertCanonical(c, node);
    node._raw = '\x60\x60\x60js\nconst x = "<&>";\n\x60\x60\x60\n\nplain tail'; c.renderAssistant(node);
    assertReleased(node); assertCanonical(c, node);
  }
});

domTest('resumed history and folded reasoning reveal complete source without retaining another paragraph cache', () => {
  const c = harness(), raw = '<thinking>**hidden** x & <y></thinking>\n**visible** answer';
  const fragment = c.buildHistoryFragment([{role: 'assistant', content: raw, seq: 1}], null, false);
  c.stream.appendChild(fragment);
  const node = c.stream.querySelector('.msg-assistant'), card = node.querySelector('details.think-block');
  const thought = node._assistantParts[0], visible = node._assistantParts[1], answer = visible.markdown.tailNodes[0];
  assert.equal(node._raw, raw); assert.equal(node._history, true); assertReleased(node);
  assert.equal(card.open, false); assert.equal(thought.markdown, null);
  reveal(c, card, true); assertReleased(node); assertCanonical(c, node);
  const reasoningParagraph = card.querySelector('.think-content p'), html = node.innerHTML;
  for (let i = 0; i < 3; i++) {reveal(c, card, false); reveal(c, card, true);}
  assertReleased(node); assert.equal(node.innerHTML, html); assert.equal(visible.markdown.tailNodes[0], answer);
  assert.equal(card.querySelector('.think-content p'), reasoningParagraph);
  reveal(c, card, false);
  node._raw = '<thinking>**hidden** x & <y> appended</thinking>\n**visible** answer';
  c.renderAssistant(node); assert.equal(card.open, false); assertReleased(node);
  reveal(c, card, true); assertReleased(node); assertCanonical(c, node);
  assert.equal(card.querySelector('.think-content p'), reasoningParagraph);
  assert.equal(visible.markdown.tailNodes[0], answer);
  assert.equal(card.querySelectorAll('.think-content p').length, 1);
});

domTest('late native reasoning and renderer failure preserve canonical recovery after completion', () => {
  const c = harness(), node = c.addAssistantMsg(); c.ui.thinkingExpanded = true;
  c.appendAssistantReasoning(node, '**native** x & <y>'); c.closeAssistantReasoning(node);
  c.appendAssistantSource(node, '**answer** complete'); c.flushAssistantRender(node);
  const answer = node._assistantParts[1].markdown.tailNodes[0];
  c.handleEvent({type: 'done'}, node); assertReleased(node);
  c.appendAssistantReasoning(node, ' late'); c.flushAssistantRender(node);
  assertReleased(node); assertCanonical(c, node);
  assert.equal(node._assistantParts[1].markdown.tailNodes[0], answer);
  assert.equal(node.querySelectorAll('.think-content p').length, 1);
  const update = c.updateMarkdownStream;
  c.updateMarkdownStream = () => {throw new Error('synthetic renderer failure');};
  node._raw = '**recovery source** & <y>'; c.renderAssistant(node);
  assert.equal(node.textContent, node._raw); assert.equal(node._assistantParts, null);
  c.updateMarkdownStream = update; c.sealAssistantSegment(node); c.renderAssistant(node);
  assertReleased(node); assertCanonical(c, node); assert.equal(node._raw, '**recovery source** & <y>');
});
