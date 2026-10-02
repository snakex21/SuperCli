const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

function fixture(options = {}) {
  const copied = [], notices = [], actions = [], body = {children: [], appendChild(n) {this.children.push(n); n.parentNode = this;}};
  const active = {focus(settings) {actions.push(['focus', settings.preventScroll]);}};
  const ranges = [{cloneRange() {return {saved: true};}}];
  const selection = {rangeCount: 1, getRangeAt: n => ranges[n], removeAllRanges() {actions.push('clear ranges');}, addRange(range) {actions.push(['restore range', range.saved]);}};
  const document = {body, activeElement: active, createElement(tag) {
    const n = {tag, style: {}, select() {actions.push(['select', this.value]);}, remove() {body.children = body.children.filter(x => x !== this); this.parentNode = null;}};
    return n;
  }, execCommand(name) {actions.push(name); return options.fallbackSuccess !== false;}};
  const escape = s => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  const c = {document, window: {getSelection: () => selection}, t: key => key,
    escHtml: escape, escAttr: escape, toast: text => notices.push(text)};
  if (options.clipboard !== false) c.navigator = {clipboard: {async writeText(text) {copied.push(text); if (options.reject) throw Error('Denied'); if (options.wait) await options.wait;}}};
  vm.createContext(c);
  vm.runInContext(fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js/03-markdown.js'), 'utf8'), c);
  function button(text) {
    const code = {textContent: text}, pre = {querySelector: selector => selector === 'code' ? code : null};
    return {code, disabled: false, closest: selector => selector === 'pre' ? pre : null};
  }
  return {c, copied, notices, actions, body, button};
}

test('copy reads only the clicked code block, preserving indentation, newlines, Unicode and HTML-looking text', async () => {
  const h = fixture(), text = '  const value = "żółć 😀 <unsafe>&";\n\treturn value;\n';
  await h.c.copyCodeBlock(h.button(text));
  await h.c.copyCodeBlock(h.button('second block'));
  assert.deepEqual(h.copied, [text, 'second block']);
  assert.deepEqual(h.notices, ['code.copied', 'code.copied']);
  assert.equal(h.actions.length, 0, 'modern clipboard needs no hidden textarea or selection changes');
});

test('a pending copy captures the current text and cannot submit twice while more code streams in', async () => {
  let finish; const wait = new Promise(resolve => finish = resolve);
  const h = fixture({wait}), button = h.button('first\n  second');
  const pending = h.c.copyCodeBlock(button);
  assert.equal(button.disabled, true);
  button.code.textContent += '\nmore received later';
  await h.c.copyCodeBlock(button);
  assert.deepEqual(h.copied, ['first\n  second']);
  finish(); await pending;
  assert.equal(button.disabled, false);
  assert.deepEqual(h.notices, ['code.copied']);
});

test('missing or rejected Clipboard API falls back locally and restores focus and selection', async () => {
  for (const options of [{clipboard: false}, {reject: true}]) {
    const h = fixture(options), button = h.button('  <sample>\nżółć');
    await h.c.copyCodeBlock(button);
    assert.ok(h.actions.some(action => Array.isArray(action) && action[0] === 'select' && action[1] === button.code.textContent));
    assert.ok(h.actions.includes('copy'));
    assert.ok(h.actions.some(action => Array.isArray(action) && action[0] === 'focus' && action[1] === true));
    assert.ok(h.actions.some(action => Array.isArray(action) && action[0] === 'restore range' && action[1] === true));
    assert.equal(h.body.children.length, 0);
    assert.equal(button.disabled, false);
    assert.deepEqual(h.notices, ['code.copied']);
  }
});

test('failure of both clipboard paths reports failure and releases temporary elements', async () => {
  const h = fixture({reject: true, fallbackSuccess: false}), button = h.button('sample');
  await h.c.copyCodeBlock(button);
  assert.deepEqual(h.notices, ['code.copyFailed']);
  assert.equal(button.disabled, false);
  assert.equal(h.body.children.length, 0);
});

test('only fenced blocks get a translated keyboard-accessible button and code stays escaped', () => {
  const h = fixture();
  const html = h.c.renderMarkdownish('before\n\n'+String.fromCharCode(96).repeat(3)+'html\n<img src=x onerror=bad()>\n'+String.fromCharCode(96).repeat(3));
  assert.match(html, /class="code-copy" type="button"/);
  assert.match(html, /data-i18n="code.copy"/);
  assert.match(html, /aria-label="code.copy"/);
  assert.match(html, /<code>&lt;img src=x onerror=bad\(\)&gt;<\/code>/);
  assert.doesNotMatch(html, /onclick=/);
  assert.doesNotMatch(h.c.renderMarkdownish('inline '+String.fromCharCode(96)+'code'+String.fromCharCode(96)), /code-copy/);
});
