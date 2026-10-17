import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript} from './harness.js';

const assets = sourcePath(testDir, '../../internal/webgui/assets');
const chat = readSource(sourcePath(assets, 'js/05-chat.js'), 'utf8');
const css = readSource(sourcePath(assets, 'app.css'), 'utf8');
const en = JSON.parse(readSource(sourcePath(assets, 'locales/en.json'), 'utf8'));
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
}
function descendants(node) { return node.children.flatMap(child => [child, ...descendants(child)]); }
function hasClass(node, name) { return node.className.split(/\s+/).includes(name); }

// Small DOM fixture, like the related queue tests. Native layout/inertness is
// represented by showModal; no browser, image decoding, or runtime dependency.
function harness() {
  const doc = {activeElement: null, listeners: [], dialogs: []};
  function element(tag, cls = '', text = '') {
    const n = {tag, className: cls, textContent: text, children: [], parentElement: null, attrs: {}, dataset: {}, events: {},
      hidden: false, disabled: false, value: '', maxLength: -1, scrollTop: 0,
      setAttribute(key, value) { this.attrs[key] = value; },
      addEventListener(type, fn) { (this.events[type] ||= []).push(fn); },
      appendChild(child) { child.remove(); this.children.push(child); child.parentElement = this; return child; },
      remove() {
        if (!this.parentElement) return;
        this.parentElement.children = this.parentElement.children.filter(child => child !== this);
        this.parentElement = null;
        if (this.contains(doc.activeElement)) doc.activeElement = doc.body;
      },
      contains(child) { return child === this || this.children.some(c => c.contains(child)); },
      get isConnected() { return this === doc.body || !!(this.parentElement && this.parentElement.isConnected); },
      set innerHTML(_) { for (const child of [...this.children]) child.remove(); },
      get innerHTML() { return ''; },
      focus() {
        const top = doc.dialogs.at(-1);
        if (this.isConnected && !this.disabled && (!top || top.contains(this))) doc.activeElement = this;
      },
      setSelectionRange(start, end, direction = 'none') { this.selectionStart = start; this.selectionEnd = end; this.selectionDirection = direction; },
      querySelectorAll(selector) {
        return descendants(this).filter(child => {
          if (selector === '[data-queue-edit-id]') return child.dataset.queueEditId != null;
          if (selector.startsWith('.')) return hasClass(child, selector.slice(1));
          assert.equal(selector, 'button:not([disabled]), textarea, input:not([hidden])');
          return child.tag === 'button' && !child.disabled || child.tag === 'textarea' || child.tag === 'input' && !child.hidden;
        });
      },
      getBoundingClientRect() { return {left: 100, top: 100, right: 1000, bottom: 800}; },
      showModal() { assert.equal(this.tag, 'dialog'); assert.ok(this.isConnected); this.open = true; doc.dialogs.push(this); },
      close() { this.open = false; doc.dialogs = doc.dialogs.filter(d => d !== this); fire(this, 'close'); },
      click() { if (!this.disabled) return fire(this, 'click'); },
    };
    n.classList = {
      add(...names) { n.className = [...new Set([...n.className.split(/\s+/).filter(Boolean), ...names])].join(' '); },
      remove(...names) { n.className = n.className.split(/\s+/).filter(name => !names.includes(name)).join(' '); },
      toggle(name, on) { if (on) this.add(name); else this.remove(name); },
    };
    return n;
  }
  function event(target, type, extra) {
    return {target, type, defaultPrevented: false, stopped: false, immediate: false,
      preventDefault() { this.defaultPrevented = true; }, stopPropagation() { this.stopped = true; },
      stopImmediatePropagation() { this.stopped = true; this.immediate = true; }, ...extra};
  }
  function fire(target, type, extra = {}) {
    const e = event(target, type, extra);
    const results = (target.events[type] || []).map(fn => fn(e));
    return {event: e, done: Promise.all(results)};
  }
  function key(key, shiftKey = false, extra = {}) {
    const e = event(doc.activeElement, 'keydown', {key, shiftKey, ...extra});
    for (const listener of [...doc.listeners]) {
      if (listener.capture && listener.type === 'keydown') listener.fn(e);
      if (e.immediate) break;
    }
    if (!e.stopped) {
      for (let n = e.target; n && !e.stopped; n = n.parentElement) {
        for (const fn of n.events.keydown || []) { fn(e); if (e.immediate) break; }
      }
    }
    if (!e.stopped) {
      for (const listener of [...doc.listeners]) if (!listener.capture && listener.type === 'keydown') listener.fn(e);
    }
    return e;
  }
  doc.body = element('body'); doc.activeElement = doc.body;
  doc.createElement = element;
  doc.addEventListener = (type, fn, capture = false) => doc.listeners.push({type, fn, capture});
  doc.removeEventListener = (type, fn, capture = false) => { doc.listeners = doc.listeners.filter(l => l.type !== type || l.fn !== fn || l.capture !== capture); };
  const hosts = Object.fromEntries(['prompt-queue', 'task-list', 'prompt'].map(id => {
    const n = element(id === 'prompt' ? 'textarea' : 'div'); n.id = id; doc.body.appendChild(n); return ['#' + id, n];
  }));
  const h = {doc, hosts, element, fire, key, patches: [], uploads: [], toasts: [], loads: 0, shortcuts: 0, sends: 0, serverTasks: [],
    patch: async () => ({}), state: async () => ({tasks: h.serverTasks, queue_merge: 'off'})};
  const w = {console, Promise, document: doc, activeAppDialog: null, promptEl: hosts['#prompt'], projectEpoch: 0,
    promptQueue: [], promptQueueLoadSeq: 0, promptQueueLoadedEpoch: 0, pendingAttachments: [], pauseQueue: true, streaming: true,
    queueDispatching: false, queueDispatchItem: null, pendingImmediate: null,
    $: selector => hosts[selector], $$: () => [], el: element, t: key => { assert.ok(en[key], key); return en[key]; },
    i18nEl: (tag, cls, key) => element(tag, cls, en[key]), clip: text => text,
    FormData: class { constructor() { this.files = []; } append(key, file, name) { this.files.push({key, file, name}); } },
    fetch: async (url, options) => { assert.equal(url, '/api/attachment/upload'); h.uploads.push(options.body.files); return {ok: true, json: async () => ({paths: options.body.files.map(f => f.name)})}; },
    j: async (url, options) => {
      if (options) {
        assert.equal(url, '/api/tasks'); assert.equal(options.method, 'PATCH');
        const body = JSON.parse(options.body), tasks = h.serverTasks;
        h.patches.push(body); const result = await h.patch(body);
        const item = tasks.find(q => q.id === body.id); if (item) Object.assign(item, {prompt: body.prompt, attachments: body.attachments});
        return result;
      }
      assert.equal(url, '/api/tasks?state=1'); h.loads++;
      return JSON.parse(JSON.stringify(await h.state()));
    },
    toast: message => h.toasts.push(message), updateAppBadge() {}, appendQueuedAttachmentBadge() {}, appendQueuedImageThumbnails() {},
    queueDragHandle: () => element('button'), queuePositionButton: () => element('button'), wireQueuedTaskDrag() {}, removeQueuedTask() {},
    runQueuedTask() { h.sends++; },
  };
  createSandbox(w);
  function load(from, to) { runScript(chat.slice(chat.indexOf(from), chat.indexOf(to)), w); }
  load('function attachmentName', 'function attachmentTranscriptText');
  load('function addAttachmentPaths', 'function clearAttachmentPreviewContent');
  load('function renderAttachments', 'function clearAttachments');
  // Only helpers are evaluated; the fixture never loads image pixels or URLs.
  runScript(readSource(sourcePath(assets, 'js/05a-lightbox.js'), 'utf8'), w);
  load('var queueEdit =', 'function clearQueueDropState');
  load('function renderPromptQueue()', '// Queued attachments keep');
  load('function loadPromptQueue()', 'async function loadWorkers');
  load('function isQueuedTaskTransition', 'function showQueueTransition');
  load('function renderTaskCenter()', 'var sideGoalSeq');
  doc.addEventListener('keydown', () => h.shortcuts++);
  h.w = w;
  h.setQueue = tasks => {
    w.promptQueue = tasks;
    h.serverTasks = tasks.map(q => ({...q, prompt: q.text, attachments: [...q.attachments || []]}));
    w.renderPromptQueue();
  };
  h.open = (source = 'queue', index = 0) => {
    const host = source === 'queue' ? hosts['#prompt-queue'] : hosts['#task-list'];
    const candidates = descendants(host).filter(n => n.dataset.queueEditId != null && (source !== 'edit' || n.textContent === en['common.edit']));
    const trigger = candidates[index]; assert.ok(trigger); trigger.focus(); trigger.click(); return trigger;
  };
  h.dialog = () => doc.body.children.find(n => hasClass(n, 'queue-editor'));
  h.button = key => descendants(h.dialog()).find(n => n.tag === 'button' && n.textContent === en[key]);
  h.type = value => { const input = w.queueEdit.input; input.value = value; fire(input, 'input'); };
  h.closed = () => {
    assert.equal(w.queueEdit, null); assert.equal(h.dialog(), undefined); assert.equal(w.activeAppDialog, null);
    assert.equal(doc.dialogs.length, 0); assert.equal(doc.listeners.filter(l => l.capture).length, 0);
  };
  return h;
}

test('queue, Tasks copy, and Tasks edit open one named body-level modal with untruncated long multiline text', async () => {
  const text = ('A very long queued message\n' + 'x'.repeat(3000) + '\n').repeat(40);
  assert.ok(text.length > 100000);
  for (const source of ['queue', 'tasks', 'edit']) {
    const h = harness(); h.setQueue([{id: 'a', text}]); h.open(source);
    const edit = h.w.queueEdit, dialog = h.dialog();
    assert.equal(dialog.tag, 'dialog'); assert.equal(dialog.open, true); assert.equal(dialog.parentElement, h.doc.body);
    assert.equal(dialog.attrs['aria-modal'], 'true');
    const title = dialog.children.find(n => n.id === dialog.attrs['aria-labelledby']);
    assert.equal(title.textContent, en['composer.editQueued']);
    assert.equal(edit.input.attrs['aria-label'], en['composer.editQueued']);
    assert.equal(edit.input.value, text); assert.ok(edit.input.rows >= 16); assert.equal(edit.input.maxLength, -1);
    assert.equal(h.doc.activeElement, edit.input);
    for (const host of Object.values(h.hosts)) assert.equal(descendants(host).some(n => hasClass(n, 'queue-editor')), false);
    h.w.editQueuedTask(h.w.promptQueue[0]);
    assert.equal(h.w.queueEdit, edit); assert.equal(h.doc.dialogs.length, 1);
    h.type(text + '\nfinal line'); await h.button('common.save').click().done;
    assert.equal(h.patches[0].prompt, text + '\nfinal line'); h.closed();
  }
});

test('modal CSS provides centered 900px desktop space, flexible textarea, bounded attachments, and mobile viewport sizing', () => {
  assert.match(css, /\.app-dialog\.queue-editor\s*\{[^}]*position: fixed; inset: 0; margin: auto;/);
  assert.match(css, /width: min\(900px, calc\(var\(--app-viewport-width, 100vw\) - 32px\)\)/);
  assert.match(css, /height: min\(800px, calc\(var\(--app-viewport-height, 100dvh\) - 32px\)\)/);
  assert.match(css, /\.queue-editor\[open\]\s*\{ display: flex; flex-direction: column;/);
  assert.match(css, /\.queue-editor \.queue-edit-input\s*\{[^}]*flex: 1;[^}]*resize: none;/);
  assert.match(css, /\.queue-editor \.queue-edit-attachments\s*\{[^}]*max-height:[^}]*overflow: auto;/);
  assert.match(css, /@media \(max-width: 540px\)\s*\{\s*\.app-dialog\.queue-editor\s*\{[^}]*100vw\) - 16px[^}]*100dvh\) - 16px/);
  assert.match(css, /\.queue-editor::backdrop\s*\{ background: rgba\(6, 6, 9, .76\); backdrop-filter: blur\(5px\);/);
});

test('queue loads and Tasks rerenders retain draft, attachment copy, selection, scroll, focus, and a single listener set', async () => {
  const h = harness(); h.setQueue([{id: 'a', text: 'server', attachments: ['original.pdf']}]); h.open('edit');
  const edit = h.w.queueEdit, input = edit.input, dialog = h.dialog();
  h.type('draft\n' + 'long text\n'.repeat(90)); input.setSelectionRange(9, 44, 'backward'); input.scrollTop = 380;
  h.serverTasks = [{id: 'a', prompt: 'another client changed this', attachments: ['remote.txt']}];
  for (let i = 0; i < 4; i++) { await h.w.loadPromptQueue(); h.w.renderTaskCenter(); }
  assert.equal(h.w.queueEdit, edit); assert.equal(h.dialog(), dialog); assert.equal(edit.input, input);
  assert.equal(edit.text, input.value); assert.ok(edit.text.startsWith('draft\n'));
  assert.deepEqual(Array.from(edit.attachments), ['original.pdf']);
  assert.deepEqual([input.selectionStart, input.selectionEnd, input.selectionDirection, input.scrollTop], [9, 44, 'backward', 380]);
  assert.equal(h.doc.activeElement, input); assert.equal(h.doc.dialogs.length, 1);
  assert.equal(h.doc.listeners.filter(l => l.capture).length, 1); assert.equal(input.events.input.length, 1);
  assert.equal(h.button('common.save').events.click.length, 1);
  await h.button('common.save').click().done;
  h.closed(); assert.equal(h.patches.length, 1);
  assert.equal(h.doc.activeElement.dataset.queueEditId, 'a'); assert.equal(h.doc.activeElement.textContent, en['common.edit']);
});

test('Tab wraps enabled modal controls, keyboard shortcuts do not reach chat, and Escape cancels and restores focus', () => {
  const h = harness(); h.setQueue([{id: 'a', text: 'before', attachments: ['keep.pdf']}]); const trigger = h.open();
  const input = h.w.queueEdit.input, save = h.button('common.save');
  input.focus(); assert.equal(h.key('Tab', true).defaultPrevented, true); assert.equal(h.doc.activeElement, save);
  assert.equal(h.key('Tab').defaultPrevented, true); assert.equal(h.doc.activeElement, input);
  h.key('Enter', false, {ctrlKey: true}); assert.equal(h.shortcuts, 0); assert.equal(h.patches.length, 0);
  h.hosts['#prompt'].focus(); assert.equal(h.doc.activeElement, input, 'showModal keeps background inert');
  h.type('discard'); const e = h.key('Escape');
  assert.equal(e.defaultPrevented, true); assert.equal(e.immediate, true); h.closed();
  assert.equal(h.doc.activeElement, trigger); assert.equal(h.shortcuts, 0); assert.equal(h.sends, 0);
  assert.equal(h.w.streaming, true); assert.equal(h.w.pauseQueue, true); assert.equal(h.w.queueDispatching, false);
  assert.equal(h.w.promptQueue[0].text, 'before');
});

test('existing app dialog cancellation cleans up the native queue modal and keyboard listener', () => {
  const h = harness(); h.setQueue([{id: 'a', text: 'before'}]); const trigger = h.open();
  h.w.activeAppDialog(null); h.closed(); assert.equal(h.doc.activeElement, trigger); assert.equal(h.patches.length, 0);
});

test('a nested native attachment preview keeps its own Escape and focus', () => {
  const h = harness(); h.setQueue([{id: 'a', text: 'before'}]); h.open(); const edit = h.w.queueEdit;
  const preview = h.element('dialog'); h.doc.body.appendChild(preview); preview.showModal(); preview.focus();
  const e = h.key('Escape'); assert.equal(e.defaultPrevented, false); assert.equal(h.w.queueEdit, edit);
  preview.close(); preview.remove(); edit.input.focus(); h.key('Escape'); h.closed();
});

for (const mode of ['cancel', 'escape', 'native cancel', 'backdrop', 'close']) {
  test(mode + ' discards text and attachment edits without a PATCH; backdrop ignores panel padding', async () => {
    const h = harness(), original = {id: 'a', text: 'before', attachments: ['original.pdf']}; h.setQueue([original]); const trigger = h.open();
    h.type('discard this'); const dialog = h.dialog();
    descendants(dialog).find(n => hasClass(n, 'attachment-remove')).click();
    const picker = descendants(dialog).find(n => n.type === 'file'); picker.files = [{name: 'discard.txt'}]; h.fire(picker, 'change'); await tick();
    if (mode === 'cancel') h.button('common.cancel').click();
    else if (mode === 'escape') h.key('Escape');
    else if (mode === 'native cancel') assert.equal(h.fire(dialog, 'cancel').event.defaultPrevented, true);
    else if (mode === 'close') dialog.close();
    else {
      h.fire(dialog, 'mousedown', {clientX: 120, clientY: 120}); assert.equal(h.dialog(), dialog);
      h.fire(dialog, 'mousedown', {clientX: 10, clientY: 10});
    }
    h.closed(); assert.equal(h.doc.activeElement, trigger); assert.equal(h.patches.length, 0);
    assert.equal(original.text, 'before'); assert.deepEqual(original.attachments, ['original.pdf']); assert.equal(h.sends, 0);
  });
}

test('Save is single-flight, freezes text/removals/uploads, and restores the rerendered trigger after reload', async () => {
  const h = harness(), done = deferred(); h.setQueue([{id: 'a', text: 'before', attachments: ['keep.pdf']}]); const trigger = h.open('tasks');
  h.type('after\nline two'); h.patch = () => done.promise;
  h.button('common.save').focus();
  const saving = h.button('common.save').click().done, edit = h.w.queueEdit;
  assert.equal(h.doc.activeElement, edit.input, 'disabling the focused Save control keeps focus inside the editor');
  assert.equal(edit.saving, true); assert.equal(edit.input.readOnly, true); assert.equal(h.button('common.save').disabled, true);
  assert.equal(h.button('composer.attach').disabled, true);
  assert.equal(descendants(h.dialog()).find(n => hasClass(n, 'attachment-remove')).disabled, true);
  await h.w.saveQueuedTaskEdit(); assert.equal(h.patches.length, 1);
  h.fire(h.dialog(), 'drop', {dataTransfer: {files: [{name: 'too-late.txt'}]}}); assert.equal(h.uploads.length, 0);
  assert.equal(h.w.streaming, true); assert.equal(h.w.queueDispatching, false); assert.equal(h.w.pauseQueue, true);
  done.resolve({}); await saving; h.closed();
  assert.equal(h.patches[0].prompt, 'after\nline two'); assert.deepEqual(h.patches[0].attachments, ['keep.pdf']);
  assert.equal(trigger.isConnected, false); assert.notEqual(h.doc.activeElement, trigger);
  assert.equal(h.doc.activeElement.dataset.queueEditId, 'a'); assert.ok(hasClass(h.doc.activeElement, 'queue-copy-edit'));
});

test('a save error keeps the same draft, attachments, caret and focus for retry, with a live error message', async () => {
  const h = harness(); h.setQueue([{id: 'a', text: 'before', attachments: ['keep.pdf']}]); h.open(); h.type('retry\nthis');
  const edit = h.w.queueEdit; edit.input.setSelectionRange(3, 5); edit.input.scrollTop = 60;
  h.patch = async () => { throw Error('queue temporarily unavailable'); };
  await h.button('common.save').click().done;
  assert.equal(h.w.queueEdit, edit); assert.equal(edit.text, 'retry\nthis'); assert.deepEqual(Array.from(edit.attachments), ['keep.pdf']);
  assert.equal(edit.error.attrs['aria-live'], 'polite'); assert.equal(edit.error.textContent, 'queue temporarily unavailable');
  assert.equal(edit.saving, false); assert.equal(edit.input.readOnly, false); assert.equal(h.button('common.save').disabled, false);
  assert.deepEqual([edit.input.selectionStart, edit.input.selectionEnd, edit.input.scrollTop], [3, 5, 60]);
  assert.equal(h.doc.activeElement, edit.input); assert.equal(h.loads, 1);
  h.patch = async () => ({}); await h.button('common.save').click().done; h.closed(); assert.equal(h.patches.length, 2);
});

test('blank text with attachments is valid; a completely empty message stays open without writing', async () => {
  const h = harness(); h.setQueue([{id: 'a', text: 'before', attachments: ['only.pdf']}]); h.open(); h.type('');
  await h.button('common.save').click().done; h.closed(); assert.deepEqual(h.patches[0], {id: 'a', prompt: '', attachments: ['only.pdf']});
  h.setQueue([{id: 'b', text: 'before'}]); h.open(); h.type(' \n '); await h.button('common.save').click().done;
  assert.equal(h.patches.length, 1); assert.equal(h.w.queueEdit.error.textContent, en['dialog.required']);
  assert.equal(h.doc.activeElement, h.w.queueEdit.input);
});

test('picker, drop, and mixed text/file paste edit attachments without replacing the textarea or composer attachments', async () => {
  const h = harness(); h.setQueue([{id: 'a', text: 'before', attachments: ['keep.pdf']}]); h.open(); h.type('typed\ntext');
  const edit = h.w.queueEdit; edit.input.setSelectionRange(4, 4); edit.input.scrollTop = 30;
  const picker = descendants(h.dialog()).find(n => n.type === 'file'); picker.files = [{name: 'picker.txt'}]; h.fire(picker, 'change'); await tick();
  h.fire(h.dialog(), 'dragover', {dataTransfer: {types: ['Files']}}); assert.ok(hasClass(h.dialog(), 'drop-target'));
  const drop = h.fire(h.dialog(), 'drop', {dataTransfer: {files: [{name: 'drop.txt'}]}}).event; await tick();
  assert.equal(drop.defaultPrevented, true); assert.equal(drop.stopped, true); assert.equal(hasClass(h.dialog(), 'drop-target'), false);
  const paste = h.fire(h.dialog(), 'paste', {clipboardData: {items: [{kind: 'file', getAsFile: () => ({name: 'paste.txt'})}], getData: () => 'also text'}}).event; await tick();
  assert.equal(paste.defaultPrevented, false); assert.equal(paste.stopped, true);
  assert.deepEqual(Array.from(edit.attachments), ['keep.pdf', 'picker.txt', 'drop.txt', 'paste.txt']);
  assert.equal(h.uploads.length, 3); assert.deepEqual(h.w.pendingAttachments, []);
  assert.equal(h.w.queueEdit.input, edit.input); assert.equal(edit.input.value, 'typed\ntext'); assert.equal(edit.input.selectionStart, 4); assert.equal(edit.input.scrollTop, 30);
  await h.button('common.save').click().done; assert.deepEqual(h.patches[0].attachments, ['keep.pdf', 'picker.txt', 'drop.txt', 'paste.txt']); h.closed();
});

test('upload pending through refresh blocks Save; upload failure preserves the same text and attachment draft', async () => {
  const h = harness(), done = deferred(); h.setQueue([{id: 'a', text: 'before', attachments: ['keep.pdf']}]); h.open(); h.type('draft');
  const edit = h.w.queueEdit; h.w.fetch = () => done.promise;
  h.fire(h.dialog(), 'paste', {clipboardData: {files: [{name: 'new.txt'}], getData: () => ''}});
  assert.equal(edit.uploading, true); assert.equal(h.button('common.save').disabled, true);
  await h.w.loadPromptQueue(); assert.equal(h.w.queueEdit, edit); await h.w.saveQueuedTaskEdit(); assert.equal(h.patches.length, 0);
  done.resolve({ok: false, text: async () => 'upload rejected'}); await tick();
  assert.equal(edit.uploading, false); assert.equal(edit.text, 'draft'); assert.deepEqual(Array.from(edit.attachments), ['keep.pdf']);
  assert.match(edit.error.textContent, /upload rejected/); assert.equal(h.button('common.save').disabled, false);
});

for (const race of ['cancel', 'project switch', 'remove', 'dispatch']) {
  test('late upload after ' + race + ' cannot revive the popup or change a different draft', async () => {
    const h = harness(), done = deferred(); h.setQueue([{id: 'a', text: 'before', attachments: ['keep.pdf']}]); h.open();
    h.w.fetch = () => done.promise;
    h.fire(h.dialog(), 'drop', {dataTransfer: {files: [{name: 'late.txt'}]}});
    if (race === 'cancel') h.button('common.cancel').click();
    else if (race === 'project switch') h.w.projectEpoch++;
    else if (race === 'remove') { h.w.promptQueue = []; h.w.renderTaskCenter(); }
    else { h.w.queueDispatchItem = h.w.promptQueue[0]; h.w.renderPromptQueue(); }
    if (race === 'cancel') { h.setQueue([{id: 'b', text: 'other', attachments: ['other.pdf']}]); h.open(); h.type('new draft'); }
    done.resolve({ok: true, json: async () => ({paths: ['late.txt']})}); await tick();
    assert.equal(h.toasts.length, 0); assert.equal(h.patches.length, 0);
    if (race === 'cancel') { assert.equal(h.w.queueEdit.text, 'new draft'); assert.deepEqual(Array.from(h.w.queueEdit.attachments), ['other.pdf']); h.key('Escape'); }
    h.closed();
  });
}

test('project change closes the modal even when the new queue load fails and IDs happen to match', async () => {
  const h = harness(); h.setQueue([{id: 'same', text: 'old project'}]); h.open(); h.type('old draft');
  h.w.projectEpoch++; h.w.promptQueue = [{id: 'same', text: 'new project'}]; h.state = async () => { throw Error('offline'); };
  await h.w.loadPromptQueue(); h.closed(); await h.w.saveQueuedTaskEdit();
  assert.equal(h.patches.length, 0); assert.equal(h.w.promptQueue[0].text, 'new project'); assert.equal(h.doc.activeElement, h.hosts['#prompt']);
});

for (const race of ['project switch', 'remove', 'dispatch']) {
  test('pending Save then ' + race + ' closes cleanly without reloading or overwriting another queue', async () => {
    const h = harness(), done = deferred(); h.setQueue([{id: 'a', text: 'old'}]); h.open(); h.type('old draft'); h.patch = () => done.promise;
    const saving = h.button('common.save').click().done;
    if (race === 'project switch') { h.w.projectEpoch++; h.w.promptQueue = [{id: 'a', text: 'new project'}]; }
    else if (race === 'remove') h.w.promptQueue = [{id: 'b', text: 'other item'}];
    else h.w.pendingImmediate = h.w.promptQueue[0];
    h.w.renderTaskCenter(); h.closed();
    done.resolve({}); await saving; h.closed(); assert.equal(h.loads, 0); assert.equal(h.patches.length, 1);
    if (race === 'project switch') assert.equal(h.w.promptQueue[0].text, 'new project');
    if (race === 'remove') assert.equal(h.w.promptQueue[0].text, 'other item');
  });
}

for (const failure of [false, true]) {
  test('late ' + (failure ? 'failed' : 'successful') + ' Save and detached handlers never close or save the next editor', async () => {
    const h = harness(), done = deferred(); h.setQueue([{id: 'a', text: 'first'}, {id: 'b', text: 'second'}]); h.open();
    const oldSave = h.button('common.save'), oldCancel = h.button('common.cancel'); h.patch = () => done.promise;
    const saving = oldSave.click().done; h.button('common.cancel').click(); h.open('queue', 1); h.type('second draft');
    const newer = h.w.queueEdit; oldSave.disabled = false; oldSave.click(); oldCancel.click();
    if (failure) done.reject(Error('old error')); else done.resolve({}); await saving;
    assert.equal(h.w.queueEdit, newer); assert.equal(newer.text, 'second draft'); assert.equal(h.doc.activeElement, newer.input);
    assert.equal(h.loads, 0); assert.equal(h.patches.length, 1); assert.equal(h.toasts.length, 0); assert.equal(h.doc.dialogs.length, 1);
    h.key('Escape'); h.closed();
  });
}

test('a claimed-item save error refreshes durable state and closes instead of leaving an orphan editor', async () => {
  const h = harness(); h.setQueue([{id: 'a', text: 'claimed'}]); h.open();
  h.patch = async () => { h.serverTasks = []; throw Error('task is being dispatched'); };
  await h.button('common.save').click().done; h.closed();
  assert.deepEqual(Array.from(h.w.promptQueue), []); assert.equal(h.patches.length, 1); assert.equal(h.loads, 1);
  assert.equal(h.doc.activeElement, h.hosts['#prompt']);
});

test('project switch during the post-save queue reload ignores the old response and closes the modal', async () => {
  const h = harness(), done = deferred(); h.setQueue([{id: 'a', text: 'old'}]); h.open(); h.state = () => done.promise;
  const saving = h.button('common.save').click().done; await tick(); assert.equal(h.loads, 1);
  h.w.projectEpoch++; h.w.promptQueue = [{id: 'b', text: 'new project'}];
  done.resolve({tasks: [{id: 'a', prompt: 'old response'}]}); await saving; h.closed();
  assert.equal(h.w.promptQueue[0].id, 'b'); assert.equal(h.w.promptQueue[0].text, 'new project');
});
