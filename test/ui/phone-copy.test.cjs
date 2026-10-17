const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const assets = path.resolve(__dirname, '../../internal/webgui/assets');
const source = fs.readFileSync(path.join(assets, 'js/06-phone.js'), 'utf8');
const url = 'https://phone-fixture.trycloudflare.com/phone#full-pairing-token_123';

function fixture(options = {}) {
  const nodes = new Map(), notices = [], copied = [], textareas = [];
  let state = 'off', currentURL = url, interval, selected;
  const document = {
    hidden: false,
    activeElement: null,
    getElementById(id) {
      if (!nodes.has(id)) nodes.set(id, {
        hidden: id === 'phone-panel', disabled: id === 'phone-copy', value: '', listeners: {},
        classList: {toggle() {}}, setAttribute() {}, replaceChildren() {this.innerHTML = '';},
        addEventListener(type, fn) {this.listeners[type] = fn;},
        focus() {document.activeElement = this;},
        click() {return this.listeners.click();},
      });
      return nodes.get(id);
    },
    body: {children: [], appendChild(node) {this.children.push(node);}},
    createElement(tag) {
      assert.equal(tag, 'textarea');
      const node = {
        style: {}, focus() {document.activeElement = this;},
        select() {selected = this;},
        remove() {document.body.children.splice(document.body.children.indexOf(this), 1);},
      };
      textareas.push(node);
      return node;
    },
    execCommand(command) {
      assert.equal(command, 'copy');
      assert.equal(selected.readOnly, true);
      assert.match(selected.style.cssText, /position:fixed;left:-9999px/);
      if (options.execThrows) throw new Error('Denied');
      if (options.execFails) return false;
      copied.push(selected.value);
      return true;
    },
  };
  const navigator = {};
  if (options.clipboard !== 'missing') navigator.clipboard = {
    writeText(text) {
      if (options.clipboard === 'throws') throw new Error('Denied');
      if (options.clipboard === 'rejects') return Promise.reject(new Error('Denied'));
      copied.push(text);
      return Promise.resolve();
    },
  };
  vm.runInNewContext(source, {
    document, navigator, location: {hostname: 'localhost'},
    window: {addEventListener() {}},
    setInterval(fn) {interval = fn; return 1;}, clearInterval() {},
    t: key => key, toast: text => notices.push(text),
    j: async () => ({state, expires_at: '2026-10-17T12:00:00Z'}),
    jpost: async endpoint => {
      if (endpoint === '/api/phone/stop') {state = 'off'; return {};}
      assert.equal(endpoint, '/api/phone/pair');
      return {url: currentURL, qr_svg: '<svg>server QR</svg>'};
    },
  });
  const node = id => document.getElementById(id);
  return {
    node, document, copied, notices, textareas,
    async open() {
      // Finish the initial off-state refresh, then open the actual dialog.
      await Promise.resolve();
      state = 'on';
      await node('open-phone').click();
    },
    async refresh() {await interval();},
    async rotate() {
      currentURL = url + '-rotated';
      node('phone-new-code').click();
      // The DOM click listener deliberately does not return pair()'s promise.
      // Drain its microtasks before asserting the newly rendered URL.
      await new Promise(setImmediate);
    },
  };
}

test('phone button copies the complete QR pairing URL and gives localized feedback', async () => {
  const h = fixture();
  assert.equal(h.node('phone-copy').disabled, true);
  await h.node('phone-copy').click();
  assert.deepEqual(h.copied, []);
  await h.open();
  assert.equal(h.node('phone-qr').innerHTML, '<svg>server QR</svg>');
  assert.equal(h.node('phone-link').value, url);
  assert.equal(h.node('phone-copy').disabled, false);
  await h.node('phone-copy').click();
  assert.deepEqual(h.copied, [url]);
  assert.deepEqual(h.notices, ['phone.copied']);
  assert.equal(h.textareas.length, 0);
  await h.rotate();
  await h.node('phone-copy').click();
  assert.equal(h.copied[1], url + '-rotated');
  await h.node('phone-disconnect').click();
  assert.equal(h.node('phone-copy').disabled, true);
  assert.equal(h.node('phone-link').value, '');
  await h.node('phone-copy').click();
  assert.equal(h.copied.length, 2);
});

for (const clipboard of ['missing', 'rejects', 'throws']) {
  test(`phone copy uses a hidden textarea when clipboard ${clipboard}`, async () => {
    const h = fixture({clipboard});
    await h.open();
    h.node('phone-copy').focus();
    await h.node('phone-copy').click();
    assert.deepEqual(h.copied, [url]);
    assert.deepEqual(h.notices, ['phone.copied']);
    assert.equal(h.textareas.length, 1);
    assert.deepEqual(h.document.body.children, []);
    assert.equal(h.document.activeElement, h.node('phone-copy'));
  });
}

for (const failure of ['execFails', 'execThrows']) {
  test(`phone copy reports ${failure} without false success and cleans up`, async () => {
    const h = fixture({clipboard: 'missing', [failure]: true});
    await h.open();
    h.node('phone-copy').focus();
    await h.node('phone-copy').click();
    assert.deepEqual(h.copied, []);
    assert.deepEqual(h.notices, ['phone.copyFailed']);
    assert.deepEqual(h.document.body.children, []);
    assert.equal(h.document.activeElement, h.node('phone-copy'));
  });
}

test('copy link is beside the QR, and every GUI catalog has its strings', () => {
  const html = fs.readFileSync(path.join(assets, 'index.html'), 'utf8');
  assert.match(html, /class="phone-actions phone-pairing"><div id="phone-qr"><\/div><button id="phone-copy" type="button" data-i18n="phone.copyLink" disabled>Copy link<\/button>/);
  const locales = path.join(assets, 'locales');
  const english = JSON.parse(fs.readFileSync(path.join(locales, 'en.json'), 'utf8'));
  const expected = {
    'en.json': {
      'phone.copyLink': 'Copy link',
      'phone.copied': 'Link copied',
      'phone.copyFailed': 'Could not copy the link. Copy it manually from the field.',
    },
    'pl.json': {
      'phone.copyLink': 'Kopiuj link',
      'phone.copied': 'Link skopiowany',
      'phone.copyFailed': 'Nie udało się skopiować linku. Skopiuj go ręcznie z pola.',
    },
  };
  const placeholders = text => (text.match(/\{[\w]+\}/g) || []).sort();
  for (const name of fs.readdirSync(locales).filter(name => name.endsWith('.json'))) {
    const catalog = JSON.parse(fs.readFileSync(path.join(locales, name), 'utf8'));
    for (const key of ['phone.copyLink', 'phone.copied', 'phone.copyFailed']) {
      assert.ok(Object.hasOwn(catalog, key), `${name}: ${key}`);
      assert.equal(typeof catalog[key], 'string', `${name}: ${key}`);
      assert.ok(catalog[key].trim(), `${name}: ${key}`);
      assert.deepEqual(placeholders(catalog[key]), placeholders(english[key]), `${name}: ${key} placeholders`);
      if (expected[name]) assert.equal(catalog[key], expected[name][key], `${name}: ${key}`);
    }
  }
});
