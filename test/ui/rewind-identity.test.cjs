'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');

function harness() {
  const roots = new Map(), gets = [], posts = [];
  function element(tag, className = '', text = '') {
    return {tag, className, textContent: text, children: [], events: {}, style: {}, isConnected: true, value: '',
      classList: {add() {}},
      appendChild(child) {this.children.push(child); return child;},
      setAttribute() {}, addEventListener(name, fn) {this.events[name] = fn;},
      querySelector(selector) {return this.children.find(child => child.className === selector.slice(1)) || null;},
      remove() {}, focus() {}, dispatchEvent() {}, setSelectionRange() {},
    };
  }
  const $ = selector => {if (!roots.has(selector)) roots.set(selector, element('div')); return roots.get(selector);};
  const c = {$, el: element, i18nEl: (tag, cls, key) => element(tag, cls, key), t: key => key,
    window: {}, superCliUI: {fileMutationTools: {}}, document: {createElement: element, createDocumentFragment: () => element('#fragment'), body: element('body')},
    requestAnimationFrame: () => 1, cancelAnimationFrame() {},
    renderSentAttachments() {}, userMessageDisplayText: text => text,
    sentAttachmentsFor: () => [], fmtInteger: String, activeSessionID: 'original', transcriptSessionID: 'original', streaming: false,
    j: async url => {gets.push(url); return {available: true, checkpoints: 1, files: ['synthetic.txt']};},
    jpost: async (url, body) => {posts.push({url, body}); return {};},
    forgetSentAttachments() {}, toast() {}, promptEl: element('textarea'), sessionByID: {}, Event: class {},
  };
  vm.createContext(c);
  for (const file of ['04-transcript.js', '08-sessions.js']) {
    vm.runInContext(fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js', file), 'utf8'), c);
  }
  c.loadSessions = async () => {};
  c.resumeSession = async () => true;
  return {c, gets, posts, element};
}

test('restored cards keep exact ID and SID when identical text and active session change', () => {
  const h = harness(), c = h.c;
  const history = c.buildHistoryFragment([
    {role: 'user', seq: 3, content: 'same prompt', message_id: '9007199254740993'},
    {role: 'user', seq: 5, content: 'same prompt', message_id: '9007199254740994'},
    {role: 'user', seq: 7, content: 'legacy without physical ID'},
  ]);
  const opened = [];
  c.showRewindDialog = (...args) => opened.push(args);
  c.activeSessionID = 'replacement';
  history.children[0].querySelector('.msg-rewind').events.click({preventDefault() {}, stopPropagation() {}});
  history.children[1].querySelector('.msg-rewind').events.click({preventDefault() {}, stopPropagation() {}});
  assert.deepEqual(opened.map(args => [args[0], args[1], args[4]]), [
    ['original', 3, '9007199254740993'], ['original', 5, '9007199254740994'],
  ]);
  assert.equal(history.children[2].querySelector('.msg-rewind'), null);
});

test('preview and final request carry the same decimal ID without numeric conversion', async () => {
  const h = harness(), c = h.c, id = '9007199254740993';
  await c.showRewindDialog('original', 3, 'same prompt', null, id);
  assert.equal(h.gets[0], '/api/checkpoint/rewind?session=original&from_seq=3&message_id=' + id);
  const panel = c.document.body.children[0].children[0];
  const confirm = panel.children[panel.children.length-1].children[1];
  c.activeSessionID = 'replacement';
  await confirm.events.click();
  assert.equal(h.posts.length, 1);
  assert.equal(h.posts[0].url, '/api/session/rewind');
  assert.equal(h.posts[0].body.session_id, 'original');
  assert.equal(h.posts[0].body.selected_seq, 3);
  assert.equal(h.posts[0].body.selected_message_id, id);
  assert.equal(h.posts[0].body.rewind_files, true);
});

test('live rewind requires the successful current receipt and does not query by text', async () => {
  const h = harness(), c = h.c, node = h.element('div');
  c.bindLiveMessageReceipt(node, {type: 'session_activity', session_id: 'original'});
  assert.equal(await c.addLatestMessageRewind(node, 'same prompt', 9), 0);
  assert.equal(h.gets.length, 0);
  assert.equal(node.querySelector('.msg-rewind'), null);
  c.bindLiveMessageReceipt(node, {type: 'message', session_id: 'original', user_seq: 3, user_message_id: '99'});
  assert.equal(await c.addLatestMessageRewind(node, 'same prompt'), 0);
  c.bindLiveMessageReceipt(node, {type: 'session_activity', session_id: 'original', user_seq: 3, user_message_id: '9007199254740993'});
  c.activeSessionID = 'replacement';
  const opened = [];
  c.showRewindDialog = (...args) => opened.push(args);
  assert.equal(await c.addLatestMessageRewind(node, 'same prompt'), 3);
  node.querySelector('.msg-rewind').events.click({preventDefault() {}, stopPropagation() {}});
  assert.equal(opened[0][0], 'original');
  assert.equal(opened[0][4], '9007199254740993');
  assert.equal(h.gets.length, 0);
  assert.equal(await c.rewindSession('original', 3, 'same prompt', '', false, null), false);
  assert.equal(h.posts.length, 0);
});
