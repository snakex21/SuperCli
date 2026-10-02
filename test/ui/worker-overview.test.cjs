const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');

function harness() {
  const roots = new Map(), moves = [];
  let language = 'en';
  const catalogs = Object.fromEntries(['en', 'pl'].map(code => [code,
    JSON.parse(fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/locales/' + code + '.json'), 'utf8'))]));
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
  const context = {window: {}, superCliUI: {}, $, el: element, clip: (value, length) => String(value || '').slice(0, length), clearInterval() {}};
  context.t = key => { assert.ok(catalogs[language][key], 'missing catalog key ' + key); return catalogs[language][key]; };
  context.i18nEl = (tag, className, key) => element(tag, className, context.t(key));
  vm.createContext(context);
  vm.runInContext(fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js/04-transcript.js'), 'utf8'), context);
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
  assert.equal(h.$('#worker-overview-summary').textContent, h.context.t('task.workers') + ' · ' + h.context.t('task.active') + ': 3 / 3');
});

test('worker overview refreshes status and language, opens the current row and releases buttons on reset', () => {
  const h = harness(), first = h.start('worker-1'), second = h.start('worker-2');
  const buttons = h.$('#worker-overview-list').children.slice(); h.moves.length = 0;
  h.context.addWorkerProgress({id: 'worker-1', name: 'code', parent_call_id: 'task-worker-1', kind: 'finished', status: 'done'});
  h.setLanguage('pl');
  h.context.updateWorkerOverview('worker-1', 'code', 'done', 'Wynik', first);
  h.context.updateWorkerOverview('worker-2', 'code', 'running', undefined, second);
  assert.equal(buttons[0].querySelector('.worker-overview-name').textContent, 'Worker 1 · code');
  assert.equal(buttons[0].querySelector('.worker-overview-activity').textContent, 'Wynik');
  assert.equal(buttons[0].querySelector('.worker-overview-status').textContent, h.context.t('task.done'));
  assert.equal(buttons[0].dataset.state, 'done');
  assert.equal(h.$('#worker-overview-summary').textContent, h.context.t('task.workers') + ' · ' + h.context.t('task.active') + ': 1 / 2');
  assert.equal(h.moves.length, 0);
  buttons[0].click(); assert.equal(first.open, true); assert.equal(first._body.hidden, false); assert.equal(first.scrolls.length, 1);
  h.context.updateWorkerOverview('worker-1', 'code', 'running', 'Nowa tura', second);
  buttons[0].click(); assert.equal(second.open, true); assert.equal(second._body.hidden, false); assert.equal(second.scrolls.length, 1);
  second.remove(); buttons[0].click(); assert.equal(second.scrolls.length, 1, 'detached rows must not be opened or scrolled');
  h.context.resetWorkerOverview();
  assert.equal(h.$('#worker-overview-list').children.length, 0); assert.equal(h.$('#worker-overview').hidden, true);
  assert.equal(Object.keys(h.context.workerOverview).length, 0);
  h.start('worker-1'); assert.notEqual(h.$('#worker-overview-list').children[0], buttons[0]);
});
