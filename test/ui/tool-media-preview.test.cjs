const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

function descendants(node) { return node.children.flatMap(child => [child, ...descendants(child)]); }
function element(tag, className = '', text) {
  const node = {tag, nodeType: 1, className, children: [], style: {}, dataset: {}, attributes: {}, events: new Map(),
    open: false, scrollHeight: 1000, scrollTop: 0, clientHeight: 500,
    appendChild(child) {
      if (child.nodeType === 11) child.children.splice(0).forEach(value => this.appendChild(value));
      else { this.children.push(child); child.parentNode = this; }
      return child;
    },
    replaceChildren(...children) { this.children.forEach(child => child.parentNode = null); this.children = []; children.forEach(child => this.appendChild(child)); },
    remove() { if (this.parentNode) this.parentNode.children = this.parentNode.children.filter(child => child !== this); this.parentNode = null; },
    setAttribute(name, value) { this.attributes[name] = value; },
    removeAttribute(name) { delete this.attributes[name]; if (name === 'src') this.src = ''; },
    addEventListener(name, fn) { if (!this.events.has(name)) this.events.set(name, new Set()); this.events.get(name).add(fn); },
    removeEventListener(name, fn) { this.events.get(name)?.delete(fn); },
    dispatch(name) { Array.from(this.events.get(name) || []).forEach(fn => fn.call(this, {target: this})); },
    querySelectorAll(selector) {
      const selectors = selector.split(',').map(value => value.trim());
      return descendants(this).filter(child => selectors.some(value => value.startsWith('.') ?
        String(child.className || '').split(' ').includes(value.slice(1)) : child.tag === value));
    },
    querySelector(selector) { return this.querySelectorAll(selector)[0] || null; },
    focus() {}, pause() { this.pauses = (this.pauses || 0) + 1; }, load() { this.loads = (this.loads || 0) + 1; },
    showModal() { this.open = true; },
    close() { this.open = false; this.dispatch('close'); },
  };
  node.classList = {
    add(...names) { node.className += ' ' + names.join(' '); },
    remove(...names) { node.className = node.className.split(' ').filter(value => !names.includes(value)).join(' '); },
    toggle() {},
  };
  Object.defineProperty(node, 'textContent', {get() { return this.nodeType === 3 ? this.text : this.children.map(child => child.textContent).join(''); },
    set(value) { this.children = String(value) ? [{nodeType: 3, text: String(value), textContent: String(value), children: []}] : []; }});
  Object.defineProperty(node, 'innerHTML', {get() { return ''; }, set() { this.children = []; }});
  if (text != null) node.textContent = text;
  return node;
}
function harness() {
  const nodes = new Map(), created = [];
  const $ = selector => { if (!nodes.has(selector)) nodes.set(selector, element('div')); return nodes.get(selector); };
  const el = (tag, cls, text) => { const node = element(tag, cls, text); created.push(node); return node; };
  const c = {$, $$: () => [], el, i18nEl: (tag, cls, key) => el(tag, cls, key), t: key => key,
    document: {hidden: false, hasFocus: () => true, createElement: el, createDocumentFragment() { const node = element('#fragment'); node.nodeType = 11; return node; }},
    localStorage: {getItem: () => null}, window: {}, AbortController, Promise,
    superCliUI: {fileMutationTools: {}, mutationKind: () => '', createComposerDraftStore: () => ({restore() {}, clear() {}, scope() { return ''; }})},
    requestAnimationFrame: () => 1, clearInterval() {}, performance: {now: () => 100},
    prettyJSON: value => { try { return JSON.stringify(JSON.parse(value), null, 2); } catch { return value; }},
    toolHint: name => ({name, hint: 'media file'}), toolDisplayName: name => name,
    clip: value => String(value), fmtDuration: () => '0.1s', toast: value => { throw Error(value); },
    fetch: () => { throw Error('No network request is allowed in this fixture'); },
  };
  vm.createContext(c);
  for (const file of ['04-transcript.js', '05-chat.js', '08-sessions.js']) {
    vm.runInContext(fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js', file), 'utf8'), c);
  }
  return {c, $, created};
}
function liveResult(h, name, raw, err = '') {
  const row = element('details'); row._toolName = name; row._t0 = 0; row._clock = 1;
  row._body = element('div'); row._stat = element('span'); row._tname = element('span');
  row.appendChild(row._body);
  h.c.toolRows = {'media-1': row}; h.c.openToolOrder = ['media-1'];
  h.c.addToolResult('media-1', raw, err);
  return row;
}
function expand(row) { row.open = true; row.dispatch('toggle'); }
function mediaResult(type, file, extra = {}) {
  return JSON.stringify({type, source: 'screen', path: file, media_type: {image: 'image/png', video: 'video/mp4', audio: 'audio/mpeg'}[type], bytes: 10, attached: false, ...extra});
}
function buttons(node) { return node.querySelectorAll('.sent-attachment-preview'); }

test('successful screenshots and images preview lazily in live output without changing raw text', () => {
  for (const [name, source] of [['send_screenshot', 'screen'], ['send_screenshot', 'clipboard'], ['show_media', 'screen']]) {
    const h = harness(), file = 'C:\\portable project\\.supercli\\snapshots\\zażółć 😀.png';
    const raw = mediaResult('image', file, {source});
    const row = liveResult(h, name, raw);
    assert.equal(row._body.children.length, 0);
    assert.equal(h.created.filter(node => node.tag === 'img').length, 0);
    expand(row);
    const cards = buttons(row._body);
    assert.equal(cards.length, 1);
    const img = cards[0].children[0];
    assert.equal(img.src, '/api/attachment/preview?path=' + encodeURIComponent(file));
    assert.equal(img.loading, 'lazy');
    assert.equal(img.alt, 'zażółć 😀.png');
    assert.equal(row._body.querySelectorAll('pre')[0].textContent, raw);
    row.open = false; row.dispatch('toggle'); expand(row);
    assert.equal(buttons(row._body)[0], cards[0]);
    cards[0].dispatch('click');
    assert.equal(h.$('#attachment-preview-dialog').open, true);
    assert.equal(h.$('#attachment-preview-content').querySelector('img').src, img.src);
    img.dispatch('error');
    assert.equal(buttons(row._body).length, 0, 'an unavailable image removes its preview card');
    assert.equal(row._body.querySelectorAll('pre')[0].textContent, raw);
  }
});

test('persisted tool results share lazy media previews across Unix, Windows and UNC paths', () => {
  const cases = [
    ['send_screenshot', 'image', '/portable/.supercli/snapshots/screen.png'],
    ['show_media', 'image', 'C:/portable/picture.JPEG'],
    ['show_media', 'video', '/portable/clip.mp4'],
    ['show_media', 'video', 'C:\\portable\\clip.webm'],
    ['show_media', 'audio', '/portable/sound.mp3'],
    ['show_media', 'audio', 'C:/portable/sound.wav'],
    ['show_media', 'audio', '\\\\server\\portable\\sound.ogg'],
  ];
  for (const [name, type, file] of cases) {
    const h = harness(), raw = mediaResult(type, file);
    const row = h.c.buildHistoryFragment([{seq: 2, role: 'tool', tool_call_id: 'old-media', name, content: raw}]).children[0];
    const body = row.children[1];
    assert.equal(body.children.length, 0);
    assert.equal(h.created.filter(node => ['img', 'video', 'audio'].includes(node.tag)).length, 0);
    expand(row);
    assert.equal(buttons(body).length, 1);
    assert.equal(body.querySelector('pre').textContent, raw);
    if (type === 'image') assert.equal(body.querySelector('img').loading, 'lazy');
    else {
      assert.equal(h.created.filter(node => node.tag === type).length, 0, 'expanding creates a button only');
      buttons(body)[0].dispatch('click');
      const media = h.$('#attachment-preview-content').querySelector(type);
      assert.ok(media);
      assert.equal(media.src, '/api/attachment/preview?path=' + encodeURIComponent(file));
      assert.equal(media.preload, 'none');
      assert.equal(media.controls, true);
      assert.notEqual(media.autoplay, true);
      assert.equal(media.attributes.autoplay, undefined);
    }
  }
});

test('live audio and video keep their original path and create no player until clicked', () => {
  for (const [type, ext] of [['video', '.mp4'], ['audio', '.mp3']]) {
    const h = harness(), file = '/portable/sample' + ext, raw = mediaResult(type, file);
    const row = liveResult(h, 'show_media', raw);
    expand(row);
    assert.equal(row._body.querySelector(type), null);
    assert.equal(buttons(row._body)[0].textContent, 'sample' + ext);
    assert.equal(row._body.querySelector('pre').textContent, raw);
    buttons(row._body)[0].dispatch('click');
    assert.equal(h.$('#attachment-preview-content').querySelector(type).preload, 'none');
  }
});

test('unrecognized, malformed and error results never preview and keep diagnostics', () => {
  const good = mediaResult('image', '/portable/good.png');
  const bad = [
    ['other_tool', good, ''],
    ['send_screenshot', good, good],
    ['show_media', 'not JSON', ''],
    ['show_media', mediaResult('image', '/portable/image.png', {media_type: 'text/html'}), ''],
    ['show_media', mediaResult('image', '/portable/image.png', {media_type: 'audio/mpeg'}), ''],
    ['show_media', mediaResult('audio', '/portable/sound.mp3', {media_type: 'video/mp4'}), ''],
    ['show_media', 'null', ''],
    ['show_media', JSON.stringify({type: 'image'}), ''],
    ['show_media', mediaResult('image', 'relative.png'), ''],
    ['show_media', mediaResult('image', 'https://example.com/image.png'), ''],
    ['show_media', mediaResult('image', '/portable/image.svg', {media_type: 'image/svg+xml'}), ''],
    ['show_media', mediaResult('video', '/portable/image.png', {media_type: 'image/png'}), ''],
    ['send_screenshot', mediaResult('video', '/portable/clip.mp4'), ''],
    ['show_media', mediaResult('image', '/portable/image.png', {save_error: 'permission denied'}), ''],
    ['show_media', mediaResult('image', '/portable/image.png', {error: 'missing file'}), ''],
    ['show_media', mediaResult('image', '/portable/\u0000image.png'), ''],
  ];
  for (const [name, raw, err] of bad) {
    const h = harness(), row = liveResult(h, name, raw, err);
    expand(row);
    assert.equal(buttons(row._body).length, 0);
    assert.equal(row._body.querySelector('pre').textContent, err || raw);
    const old = h.c.buildHistoryFragment([{seq: 2, role: 'tool', name, content: err || raw}]).children[0];
    // Persisted error role payloads remain text, even without a separate error flag.
    if (!err) { expand(old); assert.equal(buttons(old).length, 0); }
  }
});

test('closing, Escape close events and switching previews stop playback and release source', () => {
  for (const finish of ['button', 'escape', 'switch']) {
    const h = harness();
    h.c.openAttachmentPreview('/portable/sound.ogg');
    const content = h.$('#attachment-preview-content'), media = content.querySelector('audio');
    if (finish === 'button') h.$('#attachment-preview-close').dispatch('click');
    else if (finish === 'escape') h.$('#attachment-preview-dialog').close();
    else h.c.openAttachmentPreview('/portable/next.png');
    assert.equal(media.pauses, 1);
    assert.equal(media.loads, 1);
    assert.equal(media.src, '');
    assert.equal(media.parentNode, null);
    assert.equal(content.querySelector('audio'), null);
    if (finish === 'switch') assert.ok(content.querySelector('img'));
    else assert.equal(content.children.length, 0);
  }
});

test('existing image and PDF previews and composer attachment names keep their behavior', () => {
  const h = harness();
  h.c.openAttachmentPreview('/portable/doc.pdf');
  const frame = h.$('#attachment-preview-content').querySelector('iframe');
  assert.equal(frame.src, '/api/attachment/preview?path=%2Fportable%2Fdoc.pdf#view=FitH');
  h.c.openAttachmentPreview('/portable/photo.webp');
  assert.equal(h.$('#attachment-preview-content').querySelector('img').src, '/api/attachment/preview?path=%2Fportable%2Fphoto.webp');
  h.c.closeAttachmentPreview();
  assert.equal(h.$('#attachment-preview-content').children.length, 0);
  const paperclip = String.fromCodePoint(0x1F4CE);
  assert.equal(h.c.userMessageDisplayText('text\n\n' + paperclip + ' photo.webp, doc.pdf', ['/portable/photo.webp', '/portable/doc.pdf']),
    'text\n\n' + paperclip + ' doc.pdf');
});

test('tool MIME descriptors preview valid content even with no or mismatched file extension', () => {
  const cases = [
    ['image', 'image/png', '/portable/output.bin'],
    ['image', 'image/jpeg', 'C:/portable/output'],
    ['image', 'image/gif', '/portable/picture.pdf'],
    ['image', 'image/webp', '/portable/picture.wav'],
    ['video', 'video/mp4', '/portable/output.bin'],
    ['video', 'video/webm', '/portable/movie.png'],
    ['audio', 'audio/mpeg', '/portable/output.bin'],
    ['audio', 'audio/wave', '/portable/output'],
    ['audio', 'audio/wav', '/portable/sound.mp4'],
    ['audio', 'audio/x-wav', '/portable/output'],
    ['audio', 'audio/ogg', '/portable/output'],
    ['audio', 'application/ogg', '/portable/output'],
  ];
  for (const [type, mime, file] of cases) {
    for (const history of [false, true]) {
      const h = harness(), raw = mediaResult(type, file, {media_type: mime});
      const row = history ? h.c.buildHistoryFragment([{seq: 2, role: 'tool', name: 'show_media', content: raw}]).children[0] :
        liveResult(h, 'show_media', raw);
      const body = history ? row.children[1] : row._body;
      expand(row);
      assert.equal(buttons(body).length, 1, type + ' ' + mime + ' ' + file);
      assert.equal(body.querySelector('pre').textContent, raw);
      buttons(body)[0].dispatch('click');
      const content = h.$('#attachment-preview-content');
      assert.ok(content.querySelector(type === 'image' ? 'img' : type));
      assert.equal(content.querySelector('iframe'), null, 'tool MIME takes priority over a PDF-looking name');
      if (type !== 'image') assert.equal(content.querySelector(type).preload, 'none');
    }
  }
});

test('a tool MIME override does not change ordinary user attachment inference for the same path', () => {
  const h = harness(), file = '/portable/output.bin', raw = mediaResult('image', file);
  const row = liveResult(h, 'show_media', raw);
  expand(row); buttons(row._body)[0].dispatch('click');
  assert.ok(h.$('#attachment-preview-content').querySelector('img'));
  h.c.closeAttachmentPreview();
  assert.equal(h.c.previewableAttachment(file), false);
  assert.throws(() => h.c.openAttachmentPreview(file), /attachment.previewUnavailable/);
  const host = element('div');
  h.c.renderSentAttachments(host, [file]);
  assert.equal(host.children.length, 0);
});

test('portable screenshot identifiers keep the same lazy live and history URL after moving the app', () => {
  const previewPath = 'snapshot:screen-2398173.png';
  const stableURL = '/api/attachment/preview?path=' + encodeURIComponent(previewPath);
  for (const file of ['C:/original/app/.supercli/snapshots/screen-2398173.png', 'D:/moved/app/.supercli/snapshots/screen-2398173.png']) {
    for (const history of [false, true]) {
      const h = harness(), raw = mediaResult('image', file, {preview_path: previewPath});
      const row = history ? h.c.buildHistoryFragment([{seq: 2, role: 'tool', name: 'send_screenshot', content: raw}]).children[0] :
        liveResult(h, 'send_screenshot', raw);
      const body = history ? row.children[1] : row._body;
      assert.equal(body.children.length, 0);
      assert.equal(h.created.filter(node => node.tag === 'img').length, 0);
      expand(row);
      const card = buttons(body)[0], image = card.children[0];
      assert.equal(image.src, stableURL);
      assert.equal(image.alt, 'screen-2398173.png');
      assert.equal(card.title, 'attachment.preview: screen-2398173.png');
      assert.equal(body.querySelector('pre').textContent, raw);
      card.dispatch('click');
      assert.equal(h.$('#attachment-preview-content').querySelector('img').src, stableURL);
      assert.equal(h.$('#attachment-preview-title').textContent, 'screen-2398173.png');
    }
  }
});

test('only strict snapshot basenames from send_screenshot override the original preview path', () => {
  const file = '/portable/.supercli/snapshots/screen.png';
  const absoluteURL = '/api/attachment/preview?path=' + encodeURIComponent(file);
  for (const previewPath of [
    'snapshot:../screen.png', 'snapshot:folder/screen.png', 'snapshot:folder\\screen.png',
    'snapshot:screen.png:other', 'snapshot:', 'snapshot:screen name.png',
    'snapshot:screen.png\n', 'SNAPSHOT:screen.png', 'https://example.com/image.png', 17, null,
  ]) {
    const h = harness(), raw = mediaResult('image', file, {preview_path: previewPath});
    const row = liveResult(h, 'send_screenshot', raw);
    expand(row);
    const card = buttons(row._body)[0];
    assert.equal(card.children[0].src, absoluteURL);
    card.dispatch('click');
    assert.equal(h.$('#attachment-preview-content').querySelector('img').src, absoluteURL);
  }
  const h = harness();
  const row = liveResult(h, 'show_media', mediaResult('image', file, {preview_path: 'snapshot:screen.png'}));
  expand(row);
  assert.equal(buttons(row._body)[0].children[0].src, absoluteURL);
  const invalid = liveResult(h, 'send_screenshot', mediaResult('image', 'relative.png', {preview_path: 'snapshot:screen.png'}));
  expand(invalid);
  assert.equal(buttons(invalid._body).length, 0, 'a portable token cannot bypass the absolute path requirement');
});
