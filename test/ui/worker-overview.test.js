import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript} from './harness.js';

function harness() {
  const roots = new Map(), moves = [];
  let language = 'en';
  const catalogs = Object.fromEntries(['en', 'pl'].map(code => [code,
    JSON.parse(readSource(sourcePath(testDir, '../../internal/webgui/assets/locales/' + code + '.json'), 'utf8'))]));
  function element(tag, className = '', text = '') {
    const node = {tag, className, children: [], parentNode: null, dataset: {}, title: '', hidden: false, events: {}, scrolls: [],
      appendChild(child) { return this.insertBefore(child, null); },
      append(...children) { children.forEach(child => this.appendChild(child)); },
      insertBefore(child, next) {
        if (this === roots.get('#worker-overview-list') && child.parentNode) moves.push(child);
        if (child === next) return child;
        child.remove();
        const index = next ? this.children.indexOf(next) : this.children.length;
        assert.ok(index >= 0);
        this.children.splice(index, 0, child); child.parentNode = this;
        return child;
      },
      remove() { if (this.parentNode) this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1); this.parentNode = null; },
      replaceChildren(...children) { this.children.slice().forEach(child => child.remove()); this.append(...children); },
      addEventListener(type, callback) { (this.events[type] ||= []).push(callback); },
      click() { (this.events.click || []).forEach(callback => callback()); },
      scrollIntoView(options) { this.scrolls.push(options); },
      querySelector(selector) {
        for (const child of this.children) {
          if (selector[0] === '.' ? child.classList.contains(selector.slice(1)) : child.tag === selector) return child;
          const found = child.querySelector(selector); if (found) return found;
        }
        return null;
      }
    };
    node.classList = {
      contains(name) { return node.className.split(' ').includes(name); },
      add(...names) { node.className = [...new Set(node.className.split(' ').filter(Boolean).concat(names))].join(' '); },
      remove(...names) { node.className = node.className.split(' ').filter(name => !names.includes(name)).join(' '); }
    };
    Object.defineProperties(node, {
      isConnected: {get() { return !!this.parentNode; }},
      textContent: {get() { return this.text || this.children.map(child => child.textContent).join(''); }, set(value) { this.replaceChildren(); this.text = String(value); }}
    });
    node.textContent = text;
    return node;
  }
  const $ = selector => { if (!roots.has(selector)) roots.set(selector, element('div')); return roots.get(selector); };
  const context = {window: {}, superCliUI: {fileMutationTools: {}}, transcriptSessionID: '', activeSessionID: '', $, el: element, clip: (value, length) => String(value || '').slice(0, length), clearInterval() {}};
  context.t = key => { assert.ok(catalogs[language][key], 'missing catalog key ' + key); return catalogs[language][key]; };
  context.i18nEl = (tag, className, key) => element(tag, className, context.t(key));
  createSandbox(context);
  runScript(readSource(sourcePath(testDir, '../../internal/webgui/assets/js/04-transcript.js'), 'utf8'), context);
  context.smartScroll = () => {};
  function start(id) {
    const row = element('details');
    row._body = element('div'); row._body.hidden = true;
    row._stat = element('span'); row._tname = element('span'); row._thint = element('span'); row._taskPrompt = 'Brief ' + id;
    $('#stream').appendChild(row); context.toolRows['task-' + id] = row;
    assert.equal(context.addWorkerProgress({id, name: 'code', parent_call_id: 'task-' + id, kind: 'started'}), true);
    return row;
  }
  return {context, $, moves, start, setLanguage(code) { language = code; }};
}

test('worker overview keeps numeric order and moves existing buttons only when their position changes', () => {
  const h = harness();
  ['worker-10', 'worker-2', 'worker-1'].forEach(h.start);
  const buttons = h.$('#worker-overview-list').children.slice();
  assert.deepEqual(buttons.map(button => button.title.split(' · ')[0]), ['worker-1', 'worker-2', 'worker-10']);
  assert.equal(h.moves.length, 2, 'only the two out-of-order insertions need relocation');
  h.moves.length = 0;
  for (let step = 0; step < 20; step++) {
    for (const id of ['worker-1', 'worker-2', 'worker-10']) {
      const base = {id, name: 'code', parent_call_id: 'task-' + id, call_id: 'call-' + step, tool: 'read_lines'};
      h.context.addWorkerProgress({...base, kind: 'tool_call', args: JSON.stringify({path: 'source.go', from: 1, to: 10})});
      h.context.addWorkerProgress({...base, kind: 'tool_result', output: 'source', err: step === 19 ? 'fixture error' : ''});
    }
  }
  assert.equal(h.moves.length, 0, 'unchanged order must not move or re-append focused buttons');
  h.$('#worker-overview-list').children.forEach((button, index) => assert.equal(button, buttons[index]));
  assert.equal(h.$('#worker-overview-summary').textContent, h.context.workerOverviewSummary({running: 3}));
});

test('worker overview refreshes status and language, opens the current row and releases buttons on reset', () => {
  const h = harness(), first = h.start('worker-1'), second = h.start('worker-2');
  const buttons = h.$('#worker-overview-list').children.slice(); h.moves.length = 0;
  h.context.addWorkerProgress({id: 'worker-1', name: 'code', parent_call_id: 'task-worker-1', kind: 'finished', status: 'done'});
  h.setLanguage('pl');
  h.context.updateWorkerOverview('worker-1', 'code', 'done', 'Wynik', first);
  h.context.updateWorkerOverview('worker-2', 'code', 'running', undefined, second);
  // The line names the worker (its kind is in the tooltip) and its task.
  assert.equal(buttons[0].querySelector('.worker-overview-name').textContent, 'Worker 1');
  assert.equal(buttons[0].querySelector('.worker-overview-activity').textContent, 'Brief worker-1');
  assert.equal(buttons[0].querySelector('.worker-overview-status').textContent, h.context.t('task.done'));
  assert.equal(buttons[0].dataset.state, 'done');
  assert.equal(h.$('#worker-overview-summary').textContent, h.context.workerOverviewSummary({running: 1, done: 1}));
  const list = h.$('#worker-overview-list').children;
  assert.equal(list[0], buttons[1], 'running workers stay ahead of finished ones');
  assert.equal(list[1], buttons[0]);
  assert.equal(h.moves.length, 1, 'only the finished worker is relocated');
  buttons[0].click(); assert.equal(first.open, true); assert.equal(first._body.hidden, false); assert.equal(first.scrolls.length, 1);
  // A new invocation must be bound by its started event, not by assigning a
  // different worker's row directly to the overview.
  const continuation = h.start('worker-1');
  buttons[0].click(); assert.equal(continuation.open, true); assert.equal(continuation._body.hidden, false); assert.equal(continuation.scrolls.length, 1);
  continuation.remove(); buttons[0].click(); assert.equal(continuation.scrolls.length, 1, 'detached rows must not be opened or scrolled');
  h.context.resetWorkerOverview();
  assert.equal(h.$('#worker-overview-list').children.length, 0); assert.equal(h.$('#worker-overview').hidden, true);
  assert.equal(Object.keys(h.context.workerOverview).length, 0);
  h.start('worker-1'); assert.notEqual(h.$('#worker-overview-list').children[0], buttons[0]);
});

test('several workers stopped by a limit or a lost connection get one Resume all button', () => {
  const h = harness(), sent = [];
  h.context.sendPrompt = text => { sent.push(text); };
  const wrap = h.context.el('div');
  wrap.appendChild(h.$('#worker-overview'));
  const rows = Object.fromEntries(['worker-3', 'worker-1', 'worker-2', 'worker-4', 'worker-5'].map(id => [id, h.start(id)]));
  const stop = (id, status) => {
    h.context.addWorkerResumeAction(rows[id], id);
    h.context.updateWorkerOverview(id, 'code', status, '', rows[id]);
  };
  stop('worker-1', 'failed');
  const button = () => wrap.children.find(child => child.className === 'worker-resume-all');
  assert.ok(!button() || button().hidden, 'one waiting worker keeps only its card button');
  stop('worker-2', 'interrupted');
  stop('worker-3', 'failed');
  stop('worker-4', 'stopped'); // stopped by the user: not part of "resume all"
  // Failed without a card button (no saved conversation shown): still listed,
  // so the count matches the panel and the coordinator's list.
  h.context.updateWorkerOverview('worker-5', 'code', 'failed', '', rows['worker-5']);
  h.context.renderWorkerOverview();
  assert.equal(button().hidden, false);
  assert.equal(button().textContent, 'Resume all (4)');
  button().click();
  assert.equal(sent.length, 1);
  assert.match(sent[0], /^Resume worker-1, worker-2, worker-3, worker-5 with send_message/);
  for (const id of ['worker-1', 'worker-2', 'worker-3']) assert.equal(rows[id]._resumeButton.disabled, true, id + ' card button');
  assert.ok(!rows['worker-4']._resumeButton.disabled, 'the user-stopped worker keeps its own button');
  assert.equal(button().hidden, true);
  button().click();
  assert.equal(sent.length, 1, 'a second click resumes nothing more');
});

test('declined resume reasons read as short notes in the UI language', () => {
  const h = harness();
  const key = reason => h.context.refusedKey('send_message', reason);
  assert.equal(key('refused: worker-4 was stopped on request, so a general resume does not include it. Resume the others'), 'task.blockedStoppedByYou');
  assert.equal(key('refused: worker-5 failed on its task (tests fail), so a general resume does not repeat it.'), 'task.blockedTaskFailed');
  assert.equal(key('refused: the user said not to resume workers now. Do not resume worker-1.'), 'task.blockedSaidNo');
  assert.equal(key('refused: the user asked to resume only the workers they named, not worker-2.'), 'task.blockedNotNamed');
  assert.equal(key('refused: to resume worker-1 the user must ask for it explicitly (ask to resume'), 'task.blockedNeedsWord');
});

test('a resumed run on another model shows one Model: old → new line on its card', () => {
  const h = harness(), c = h.context;
  const card = (callID) => {
    const row = c.el('details');
    row._body = c.el('div'); row._stat = c.el('span'); row._tname = c.el('span'); row._thint = c.el('span');
    row._activity = c.el('div', 'task-activity'); row._taskPrompt = 'Brief worker-5';
    Object.defineProperty(row._thint, 'childNodes', {get() { return this.children; }});
    h.$('#stream').appendChild(row); c.toolRows[callID] = row;
    return row;
  };
  const first = card('task-1');
  c.addWorkerProgress({id: 'worker-5', name: 'code', parent_call_id: 'task-1', kind: 'started', run: 1, model: 'gpt-a', reasoning_effort: 'high'});
  assert.equal(first._activity.children.length, 0, 'the first run has nothing to compare with');
  c.addWorkerProgress({id: 'worker-5', name: 'code', parent_call_id: 'task-1', kind: 'finished', run: 1, status: 'failed', model: 'gpt-a', reasoning_effort: 'high'});
  const resumed = card('send-1');
  const start2 = {id: 'worker-5', name: 'code', parent_call_id: 'send-1', kind: 'started', run: 2, model: 'gpt-b', reasoning_effort: 'low'};
  c.addWorkerProgress(start2);
  c.addWorkerProgress({...start2}); // a replayed start of the same run
  const notes = resumed._activity.children.filter(child => child.className.includes('model-change'));
  assert.equal(notes.length, 1);
  assert.match(notes[0].textContent, /^Model: gpt-a.* → gpt-b/);
  assert.match(resumed._modelLabel.textContent, /^gpt-b/, 'the card shows the model of the current run');
  const third = card('send-2');
  c.addWorkerProgress({...start2, parent_call_id: 'send-2', run: 3});
  assert.equal(third._activity.children.length, 0, 'same model and reasoning: no line');
});
