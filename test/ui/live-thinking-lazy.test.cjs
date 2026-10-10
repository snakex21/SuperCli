const assert = require('node:assert/strict');
const test = require('node:test');
const fixture = require('./live-thinking-fixture.cjs');

test('folded live reasoning stays unmaterialized through streaming and final flush, then reveals complete source', () => {
  for (const terminal of ['done', 'error', 'tool_call', 'EOF']) {
    const h = fixture(), node = h.event({type: 'reasoning', text: 'First żółć 😀.'});
    h.event({type: 'reasoning', text: ' Received tail.'});
    const card = node.querySelector('.think-block'), body = h.thought(node);
    assert.equal(card.open, false); assert.equal(body.childNodes.length, 0); assert.equal(h.metrics.markdownUpdates, 0);
    h.event({type: 'message', text: 'Complete answer.'});
    const paragraph = node.children.find(child => child.tag === 'p');
    const beforeTerminal = h.metrics.markdownUpdates;
    if (terminal === 'EOF') h.c.sealAssistantSegment(node);
    else h.event({type: terminal, name: 'read_lines', id: 'synthetic', err: 'synthetic error'});
    h.c.flushAssistantRender(node); h.c.sealAssistantSegment(node); h.c.flushAssistantRender(node);
    assert.equal(h.frames.size, 0); assert.equal(body.childNodes.length, 0);
    assert.equal(h.metrics.markdownUpdates, beforeTerminal + 1, 'finalization renders only answer once');
    assert.equal(node._raw, '<thinking>First żółć 😀. Received tail.</thinking>\nComplete answer.');
    h.toggle(card, true);
    assert.equal(body.textContent, 'First żółć 😀. Received tail.');
    assert.equal(node.children.find(child => child.tag === 'p'), paragraph); assert.equal(paragraph.textContent, 'Complete answer.');
  }
});

test('repeated fold and reopen pauses an existing thinking DOM and catches up exactly once', () => {
  const h = fixture(true), node = h.event({type: 'reasoning', text: 'Already visible.'});
  const card = node.querySelector('.think-block'), body = h.thought(node), paragraph = body.firstElementChild;
  for (const tail of [' Second.', ' Third.']) {
    h.toggle(card, false); const count = h.metrics.markdownUpdates, before = body.textContent;
    h.event({type: 'reasoning', text: tail}); h.c.flushAssistantRender(node);
    assert.equal(h.metrics.markdownUpdates, count); assert.equal(body.textContent, before); assert.equal(body.firstElementChild, paragraph);
    h.toggle(card, true);
    assert.equal(h.metrics.markdownUpdates, count + 1); assert.equal(body.textContent, node._raw.slice('<thinking>'.length).trim());
  }
});

test('summary click and actual keyboard shortcut reveal the latest text and preserve preference', () => {
  const h = fixture(), node = h.event({type: 'reasoning', text: 'Click reveals this.'}), card = node.querySelector('.think-block');
  h.click(card); assert.equal(h.thought(node).textContent, 'Click reveals this.'); assert.equal(h.c.ui.thinkingExpanded, true);
  h.key(); assert.equal(card.open, false); assert.equal(h.c.ui.thinkingExpanded, false);
  const count = h.metrics.markdownUpdates; h.event({type: 'reasoning', text: ' Keyboard reveals tail.'});
  assert.equal(h.metrics.markdownUpdates, count);
  h.key(); assert.equal(card.open, true); assert.equal(h.c.ui.thinkingExpanded, true); assert.equal(h.metrics.saves, 3);
  assert.equal(h.thought(node).textContent, 'Click reveals this. Keyboard reveals tail.');
});

test('recovery replacement updates unopened state and discards its reveal listener when its part is removed', () => {
  const h = fixture(), node = h.event({type: 'reasoning', text: 'Old thought.'}), old = node.querySelector('.think-block');
  node._raw = '<thinking>Recovered żółć 😀.</thinking>\nRecovered answer.'; h.c.flushAssistantRender(node);
  assert.equal(h.thought(node).childNodes.length, 0); h.toggle(old, true);
  assert.equal(h.thought(node).textContent, 'Recovered żółć 😀.');
  h.toggle(old, false); node._raw = 'Replacement answer.'; h.c.flushAssistantRender(node);
  assert.equal(node.querySelector('.think-block'), null); assert.equal(old.events.get('toggle').size, 0);
  const count = h.metrics.markdownUpdates; old.open = true; old.dispatch('toggle');
  assert.equal(h.metrics.markdownUpdates, count); assert.equal(node.textContent, 'Replacement answer.');
});

test('a failed deferred reveal retains complete source and can retry on the next opening', () => {
  const h = fixture(), node = h.event({type: 'reasoning', text: 'Recoverable complete thought.'});
  h.event({type: 'message', text: 'Preserved answer.'}); h.event({type: 'done'});
  const raw = node._raw, card = node.querySelector('.think-block'), update = h.c.updateMarkdownStream;
  let failed = false;
  h.c.updateMarkdownStream = (state, text) => {
    if (!failed) {failed = true; throw Error('synthetic reveal failure');}
    return update(state, text);
  };
  assert.throws(() => h.toggle(card, true), /synthetic reveal failure/);
  assert.equal(node._raw, raw); assert.equal(card.events.get('toggle').size, 1);
  h.toggle(card, false); h.toggle(card, true);
  assert.equal(h.thought(node).textContent, 'Recoverable complete thought.');
  assert.equal(node._raw, raw); assert.equal(node.children.find(child => child.tag === 'p').textContent, 'Preserved answer.');
});

test('late and usage-only reasoning remains above the original answer and is complete after reveal', () => {
  const h = fixture(), node = h.event({type: 'message', text: 'Original answer.'}), paragraph = node.firstElementChild;
  h.event({type: 'reasoning', text: 'Late thought.'}); h.event({type: 'reasoning', reasoning_tok: 151}); h.event({type: 'done'});
  assert.equal(node.firstElementChild.className, 'think-block'); assert.equal(h.thought(node).childNodes.length, 0);
  assert.ok(node.children.includes(paragraph)); assert.equal(paragraph.textContent, 'Original answer.');
  h.toggle(node.firstElementChild, true);
  assert.equal(h.thought(node).textContent, 'Late thought.Model used 151 reasoning tokens without a text summary.');
  assert.equal(node.children.filter(child => child.tag === 'p')[0], paragraph);
});

test('history restoration honors default and explicit disclosure preferences, including refold and reopen', () => {
  for (const expanded of [null, false, true]) {
    const h = fixture(expanded), raw = '<thinking>Saved thought.</thinking>\nSaved answer.';
    h.c.stream.appendChild(h.c.buildHistoryFragment([{role: 'assistant', content: raw, seq: 1}], null, false));
    const node = h.c.stream.querySelector('.msg-assistant'), card = node.querySelector('.think-block'), body = h.thought(node);
    assert.equal(card.open, expanded === true); assert.equal(body.childNodes.length === 0, expanded !== true);
    h.toggle(card, true); assert.equal(body.textContent, 'Saved thought.');
    h.toggle(card, false); const count = h.metrics.markdownUpdates, before = body.textContent;
    h.c.renderAssistant(node); assert.equal(body.textContent, before); assert.equal(h.metrics.markdownUpdates, count + 1, 'only answer is updated');
    h.toggle(card, true); assert.equal(body.textContent, 'Saved thought.'); assert.equal(node._raw, raw);
  }
  const defaultLive = fixture(null), node = defaultLive.event({type: 'reasoning', text: 'Default visible reasoning.'});
  assert.equal(node.querySelector('.think-block').open, true); assert.equal(defaultLive.thought(node).textContent, 'Default visible reasoning.');
});
