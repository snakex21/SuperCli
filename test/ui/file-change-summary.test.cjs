'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');

// Only the small DOM contract used by the summary. The optional LinkeDOM
// performance fixture separately checks complete rendered HTML against the
// pre-change renderer; these tests need no application/test dependencies.
class Node {
  constructor(tag, cls = '', text = '') {
    this.tagName = tag.toUpperCase(); this.className = cls; this._text = text;
    this.children = []; this.listeners = new Map(); this.open = false;
  }
  appendChild(child) {
    if (child.tagName === '#FRAGMENT') {
      for (const node of child.children) this.children.push(node);
      child.children = [];
    } else this.children.push(child);
    return child;
  }
  addEventListener(name, handler) {
    if (!this.listeners.has(name)) this.listeners.set(name, new Set());
    this.listeners.get(name).add(handler);
  }
  removeEventListener(name, handler) { this.listeners.get(name)?.delete(handler); }
  toggle(value) {
    this.open = value;
    for (const handler of [...(this.listeners.get('toggle') || [])]) handler();
  }
  set textContent(text) { this._text = String(text); this.children = []; }
  get textContent() { return this._text + this.children.map(x => x.textContent).join(''); }
  all(cls) {
    const nodes = [];
    for (const child of this.children) {
      if (child.className.split(' ').includes(cls)) nodes.push(child);
      nodes.push(...child.all(cls));
    }
    return nodes;
  }
}
function load() {
  const stream = new Node('div');
  const c = {window: {}, $() {return new Node('div');}, t: key => key,
    document: {createElement: tag => new Node(tag), createDocumentFragment: () => new Node('#fragment')},
    el: (tag, cls, text) => new Node(tag, cls, text)};
  vm.createContext(c);
  const root = path.resolve(__dirname, '../..');
  vm.runInContext(fs.readFileSync(path.join(root, 'internal/webgui/shared-ui/runtime.js'), 'utf8'), c);
  c.superCliUI = c.window.SuperCliUI;
  vm.runInContext(fs.readFileSync(path.join(root, 'internal/webgui/assets/js/04-transcript.js'), 'utf8'), c);
  c.stream = stream;
  let scrolls = 0; c.smartScroll = () => scrolls++;
  return {c, stream, scrolls: () => scrolls};
}
const data = count => Array.from({length: count}, (_, i) => ({
  path: 'game/assets/<sprite>&-' + i + '.png',
  kind: ['created', 'modified', 'deleted'][i % 3]
}));

test('small file-change summaries stay expanded with exact normalized paths and counts', () => {
  const {c, stream, scrolls} = load();
  c.addFileChanges([{path: 'a', kind: 'created'}, {path: 'a', kind: 'created'},
    null, {path: ''}, {path: 'b', kind: 'unknown'}, {path: '<tag>&', kind: 'deleted'}]);
  const row = stream.children[0];
  assert.equal(row.open, true);
  assert.equal(row.all('file-change-title')[0].textContent, 'change.title · 3');
  assert.deepEqual(row.all('file-change-path').map(n => n.textContent), ['a', 'b', '<tag>&']);
  assert.equal(row.all('file-change-group').length, 0);
  assert.equal(scrolls(), 1);
  c.addFileChanges([]);
  assert.equal(stream.children.length, 1);
});

test('folded bulk summaries show counts and roots; opening one group shows all and only its files', () => {
  const {c, stream} = load(), changes = data(12000);
  const original = JSON.stringify(changes);
  c.addFileChanges(changes);
  const row = stream.children[0], groups = row.all('file-change-group');
  assert.equal(row.open, false);
  assert.equal(groups.length, 3);
  assert.equal(row.all('file-change-path').length, 0);
  assert.deepEqual(row.all('file-change-count').map(n => n.textContent),
    ['change.created 4000', 'change.modified 4000', 'change.deleted 4000']);
  assert.deepEqual(row.all('file-change-root').map(n => n.textContent), Array(3).fill('game/assets'));
  row.toggle(true);
  assert.equal(row.all('file-change-path').length, 0);
  groups[1].toggle(true);
  assert.deepEqual(groups[1].all('file-change-path').map(n => n.textContent),
    changes.filter(x => x.kind === 'modified').map(x => x.path.slice('game/assets/'.length)));
  assert.equal(groups[0].all('file-change-path').length, 0);
  assert.equal(groups[2].all('file-change-path').length, 0);
  const first = groups[1].all('file-change-path')[0];
  groups[1].toggle(false); groups[1].toggle(true);
  assert.equal(groups[1].all('file-change-path')[0], first);
  for (const group of groups) group.toggle(true);
  assert.equal(row.all('file-change-path').length, 12000);
  assert.equal(JSON.stringify(changes), original);
});

test('directory roots match the prior segment semantics for mixed separators and empty segments', () => {
  const {c} = load();
  function previous(group) {
    if (!group.length) return '';
    const common = String(group[0].path).replace(/\\/g, '/').split('/').slice(0, -1);
    for (let i = 1; i < group.length && common.length; i++) {
      const parts = String(group[i].path).replace(/\\/g, '/').split('/').slice(0, -1);
      let keep = 0;
      while (keep < common.length && keep < parts.length && common[keep] === parts[keep]) keep++;
      common.length = keep;
    }
    return common.join('/');
  }
  const cases = [[], ['README'], ['/a', '/b'], ['a/x', 'aExtra/y'],
    ['C:\\games\\a.png', 'C:/games/b.png'], ['a//x', 'a///y'], ['//x/a', '/x/b'],
    ['a/../x', 'a/../y'], ['日本語/żółw/a', '日本語/żółw/b'], ['a/', 'a//']];
  const segments = ['', '.', '..', 'a', 'b', 'aa', 'a-b', '日本語', 'żółw', '<tag>', 'C:'];
  let seed = 42;
  const random = n => {seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0; return seed % n;};
  for (let sample = 0; sample < 10000; sample++) {
    cases.push(Array.from({length: 1 + random(8)}, () =>
      Array.from({length: 1 + random(7)}, () => segments[random(segments.length)]).join(random(2) ? '/' : '\\')));
  }
  for (const paths of cases) {
    const group = paths.map(p => ({path: p}));
    assert.equal(c.commonFileChangeDirectory(group), previous(group), JSON.stringify(paths));
  }
});
