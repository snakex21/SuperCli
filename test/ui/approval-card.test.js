import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript, parseHTML} from './harness.js';

const assets = sourcePath(testDir, '../../internal/webgui/assets');
const source = readSource(sourcePath(assets, 'js/05e-approval-card.js'), 'utf8');
const english = JSON.parse(readSource(sourcePath(assets, 'locales/en.json'), 'utf8'));
const polish = JSON.parse(readSource(sourcePath(assets, 'locales/pl.json'), 'utf8'));
const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };

function harness(catalog = english) {
  const {document} = parseHTML('<!doctype html><html><body><div id="chat-stream"></div></body></html>');
  const posts = [], runStates = [];
  const c = createSandbox({document, Object, JSON, Promise, String, Node: document.defaultView.Node,
    questionOverlays: {}, activeQuestionOverlay: null,
    el(tag, cls, text) { const n = document.createElement(tag); n.className = cls || ''; if (text != null) n.textContent = text; return n; },
    t: key => catalog[key] || english[key] || key,
    i18nEl(tag, cls, key) { const n = document.createElement(tag); n.className = cls || ''; n.textContent = catalog[key] || english[key] || key; return n; },
    registerLanguageRefresh(node, fn) { node._languageRefresh = fn; },
    closeQuestionOverlay() {},
    appendStream(node) { document.getElementById('chat-stream').appendChild(node); },
    smartScroll() {},
    setRunState(state, text) { runStates.push(state); },
    jpost(url, body) { posts.push({url, body: JSON.parse(JSON.stringify(body))}); return Promise.resolve({ok: true}); },
  });
  runScript(source, c);
  return {c, document, posts, runStates,
    card: () => document.querySelector('.approval-card'),
    button: cls => document.querySelector('.' + cls)};
}

const request = (extra = {}) => ({
  id: 'ask-7', question: 'fallback text', confirmation: true, allow_custom: false,
  options: [{label: 'Deny'}, {label: 'Approve'}, {label: 'Approve similar in this session'}],
  approval: {tool: 'thunderbird_mail', operation: 'copy', risk: 'write_reversible', reversible: true,
    account: 'account2', account_name: 'Local Folders', folder: 'account1://INBOX', destination: 'account2://_SuperCliTest',
    summary: 'Copy matching messages from "account1://INBOX" to "account2://_SuperCliTest"; the originals stay.', count: 2,
    samples: [{from: 'Shop <shop@example.test>', subject: '<img src=x onerror=alert(1)> Order', date: '2026-11-01'}],
    scope: 'thunderbird_mail:copy:account2:account1://inbox', approve_label: 'Approve', deny_label: 'Deny',
    similar_label: 'Approve similar in this session', grant_minutes: 30, ...extra},
});

test('approval card shows operation, account name, folder, count, samples and reversibility as text', async () => {
  const h = harness();
  h.c.showApprovalCard(request());
  await new Promise(r => setTimeout(r, 5));
  const card = h.card();
  assert.ok(card, 'card rendered');
  assert.equal(card.getAttribute('role'), 'alertdialog');
  assert.equal(card.querySelector('.approval-op').textContent, english['approval.op.copy']);
  const text = card.textContent;
  assert.match(text, /Local Folders \(account2\)/);
  assert.match(text, /account2:\/\/_SuperCliTest/);
  assert.match(text, /Shop <shop@example.test>/);
  assert.equal(card.querySelectorAll('img').length, 0, 'subjects are text, never markup');
  assert.match(text, new RegExp(english['approval.reversible.yes']));
  assert.equal(card.querySelector('.approval-row dd').textContent, 'thunderbird_mail · copy');
  assert.equal(h.document.activeElement, h.button('approval-deny'), 'the safe choice has focus first');
  assert.match(h.button('approval-similar').textContent, /30 min/);
});

test('approve posts the trusted approve label and leaves a receipt', async () => {
  const h = harness();
  h.c.showApprovalCard(request());
  h.button('approval-approve').click();
  await flush();
  assert.deepEqual(h.posts, [{url: '/api/question/answer', body: {id: 'ask-7', selected: ['Approve'], custom: '', cancelled: false}}]);
  assert.ok(h.card().classList.contains('approved'));
  assert.equal(h.document.querySelector('.approval-actions'), null, 'buttons are gone after answering');
  assert.equal(h.document.querySelector('.approval-receipt').textContent, english['approval.receipt.approved']);
  assert.equal(h.c.questionOverlays['ask-7'], undefined);
});

test('approve similar posts the session label', async () => {
  const h = harness();
  h.c.showApprovalCard(request());
  h.button('approval-similar').click();
  await flush();
  assert.deepEqual(h.posts[0].body.selected, ['Approve similar in this session']);
  assert.equal(h.document.querySelector('.approval-receipt').textContent, english['approval.receipt.similar']);
});

test('deny button and Escape cancel the request without approving', async () => {
  const h = harness();
  h.c.showApprovalCard(request());
  h.button('approval-deny').click();
  await flush();
  assert.deepEqual(h.posts[0].body, {id: 'ask-7', selected: [], custom: '', cancelled: true});
  assert.ok(h.card().classList.contains('denied'));

  const k = harness();
  k.c.showApprovalCard(request());
  k.card().dispatchEvent(new k.document.defaultView.KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
  await flush();
  assert.equal(k.posts.length, 1);
  assert.equal(k.posts[0].body.cancelled, true);
});

test('irreversible operations offer no session grant and are marked', async () => {
  const h = harness();
  h.c.showApprovalCard(request({operation: 'delete_folder', risk: 'destructive', reversible: false, similar_label: '', count: 5}));
  assert.equal(h.button('approval-similar'), null);
  assert.ok(h.card().classList.contains('irreversible'));
  assert.match(h.card().textContent, new RegExp(english['approval.reversible.no']));
});

test('Polish catalog localizes the card', async () => {
  const h = harness(polish);
  h.c.showApprovalCard(request({operation: 'create_folder'}));
  assert.equal(h.card().querySelector('.approval-op').textContent, 'Utworzyć folder poczty?');
  assert.equal(h.button('approval-approve').textContent, 'Zatwierdź');
  assert.equal(h.button('approval-deny').textContent, 'Odrzuć');
  assert.match(h.button('approval-similar').textContent, /Zatwierdź podobne w tej sesji/);
});

test('summary_key re-localizes host_power and headless cards; argument text stays literal', async () => {
  const h = harness(polish);
  h.c.showApprovalCard(request({tool: 'host_power', operation: 'arm_shutdown', risk: 'external_side_effect', reversible: false,
    similar_label: '', count: -1, account: '', account_name: '', folder: '', destination: '', samples: [],
    summary: 'English fallback', summary_key: 'hostPower.confirm', summary_args: {goal: 'Ship {seconds} release'}}));
  const op = h.card().querySelector('.approval-op').textContent;
  assert.equal(op, polish['hostPower.confirm'].replace('{goal}', 'Ship {seconds} release'));
  assert.equal(h.card().querySelector('.approval-detail').hidden, true);

  const k = harness(polish);
  k.c.showApprovalCard(request({tool: 'headless_control', operation: 'type', risk: 'external_side_effect', reversible: false,
    similar_label: '', count: -1, samples: [], summary: 'English fallback', summary_key: 'approval.summary.headless_action',
    summary_args: {target: 'lab', action: 'type'}, summary_detail: '{"text":"<b>hi</b>"}'}));
  assert.equal(k.card().querySelector('.approval-op').textContent, polish['approval.summary.headless_action'].replace('{action}', 'type').replace('{target}', 'lab'));
  const detail = k.card().querySelector('.approval-detail');
  assert.equal(detail.hidden, false);
  assert.equal(detail.textContent, '{"text":"<b>hi</b>"}');
  assert.equal(detail.querySelectorAll('b').length, 0, 'detail is text, never markup');

  const u = harness();
  u.c.showApprovalCard(request({operation: 'unknown_op', summary: 'Server summary', summary_key: 'approval.summary.missing'}));
  assert.equal(u.card().querySelector('.approval-op').textContent, 'Server summary');
});
