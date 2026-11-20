'use strict';
import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript, parseHTML} from './harness.js';

const assets = sourcePath(testDir, '../../internal/webgui/assets');
const catalog = JSON.parse(readSource(sourcePath(assets, 'locales/en.json'), 'utf8'));
function fixture() {
  const {document} = parseHTML('<html><body><div id="stats-block"></div><div id="panel"></div></body></html>');
  const w = createSandbox({document, window: {}, AbortController,
    ui: {lang: 'en'}, activeSessionID: 'one', activeModelID: 'model', sections: {},
    lastTurn: null, workersSeen: [], normalizeLanguage: value => value, t: key => catalog[key] || key,
    setTimeout() { return 0; }, clearTimeout() {}, setInterval() { return 0; },
  });
  for (const name of ['00-helpers.js', '07-stats.js', '07a-subscription-usage.js', '11da-panel-claude.js', '11e-panel-accounts-mcp.js'])
    runScript(readSource(sourcePath(assets, 'js', name), 'utf8'), w);
  w.i18nEl = (tag, cls, key) => w.el(tag, cls, w.t(key));
  w.requestSubscriptionUsageRefresh = () => Promise.resolve(false);
  return {w, document};
}
const future = seconds => new Date(Date.now() + seconds * 1000).toISOString();
// The ChatGPT backend's placeholder secondary window: 0 %, no duration, no reset.
const placeholder = {id: 'secondary', window_seconds: 0, used_percent: 0, remaining_percent: 100, resets_at: null, stale: false};
function chatgpt(used) {
  return {provider: 'ChatGPT', provider_type: 'codex', account: 'default', windows: [
    {id: 'primary', window_seconds: 604800, used_percent: used, remaining_percent: 100 - used, resets_at: future(86400), stale: false},
    placeholder,
  ]};
}

test('subscription bars update in place: same nodes, new width and text', () => {
  const {w, document} = fixture(), host = document.querySelector('#panel');
  const summary = w.renderSubscriptionSummary([chatgpt(37)], false);
  host.appendChild(summary);
  const fill = summary.querySelector('.subscription-meter > span'), line = summary.querySelector('.subscription-window-line strong');
  assert.equal(fill.style.width, '63%');
  const same = w.updateSubscriptionSummary(summary, [chatgpt(52)], false);
  assert.equal(same, summary);
  assert.equal(summary.querySelector('.subscription-meter > span'), fill, 'the bar node is kept');
  assert.ok(fill.isConnected);
  assert.equal(fill.style.width, '48%');
  assert.equal(summary.querySelector('.subscription-window-line strong'), line);
  assert.match(line.textContent, /48/);
  assert.equal(summary.querySelector('.subscription-meter').getAttribute('aria-valuenow'), '48');
});

test('placeholder ChatGPT window is hidden; a real one without duration is labelled', () => {
  const {w} = fixture();
  const summary = w.renderSubscriptionSummary([chatgpt(37)], false);
  assert.equal(summary.querySelectorAll('.subscription-window').length, 1);
  assert.doesNotMatch(summary.textContent, new RegExp(catalog['acct.usage.windowUnknown']));
  const extra = {...placeholder, used_percent: 12, remaining_percent: 88};
  const labelled = w.renderSubscriptionSummary([{...chatgpt(37), windows: [chatgpt(37).windows[0], extra]}], false);
  assert.equal(labelled.querySelectorAll('.subscription-window').length, 2);
  assert.match(labelled.textContent, new RegExp(catalog['acct.usage.windowAdditional']));
  const dashboard = w.renderCodexUsageDashboard({accounts: [{label: 'default', logged_in: true, usage: {rate_limits: [{id: 'codex',
    primary: {window_seconds: 604800, used_percent: 37, remaining_percent: 63, resets_at: 1900000000},
    secondary: {window_seconds: null, used_percent: 0, remaining_percent: 100, resets_at: null}}]}}], usage_summary: {}});
  assert.equal(dashboard.querySelectorAll('.codex-usage-window').length, 1);
  assert.doesNotMatch(dashboard.textContent, new RegExp(catalog['acct.usage.windowUnknown']));
});

test('sidebar statistics refresh patches the subscription block instead of rebuilding it', async () => {
  const {w, document} = fixture();
  let used = 37;
  w.j = url => {
    if (url === '/api/config') return Promise.resolve({knobs: []});
    if (url.startsWith('/api/stats')) return Promise.resolve({cost: {state: 'subscription'}, subscription_usage: [chatgpt(used)]});
    throw Error('unexpected request: ' + url);
  };
  await w.renderStats();
  const block = document.querySelector('.side-cost'), fill = block.querySelector('.subscription-meter > span');
  assert.ok(fill); assert.equal(fill.style.width, '63%');
  let opened = '';
  w.openPanel = section => { opened = section; };
  used = 80;
  await w.renderStats();
  assert.equal(document.querySelector('.side-cost'), block, 'the block stays connected');
  assert.equal(block.querySelector('.subscription-meter > span'), fill, 'the bar stays connected');
  assert.equal(fill.style.width, '20%');
  assert.ok(block.querySelector('.subscription-meter').classList.contains('low') === false);
  assert.equal(document.querySelectorAll('.side-cost').length, 1);
  block.click();
  assert.equal(opened, 'usage');
});

test('dashboard refresh patches the shown Codex section in place', async () => {
  const {w, document} = fixture(), host = document.querySelector('#panel');
  const data = used => ({accounts: [{label: 'default', logged_in: true, usage: {rate_limits: [{id: 'codex',
    primary: {window_seconds: 604800, used_percent: used, remaining_percent: 100 - used, resets_at: 1900000000}}]}}],
    usage_summary: {accounts: 1, available: 1, exhausted: 0, unknown: 0, stale: 0}});
  const root = w.renderCodexUsageDashboard(data(10));
  host.appendChild(root);
  const fill = root.querySelector('.codex-usage-meter > span');
  w.jpost = async () => data(70);
  w.toast = () => {};
  root.querySelector('.usage-section-head button').click();
  for (let i = 0; i < 6; i++) await Promise.resolve();
  assert.equal(host.firstChild, root);
  assert.equal(root.querySelector('.codex-usage-meter > span'), fill);
  assert.equal(fill.style.width, '30%');
});
