'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const {parseHTML} = require('linkedom');

const assets = path.resolve(__dirname, '../../internal/webgui/assets');
const catalog = JSON.parse(fs.readFileSync(path.join(assets, 'locales/en.json'), 'utf8'));
function fixture() {
  const {document, window: dom} = parseHTML('<html><body><div id="stats-block"></div><button id="previous">Previous</button></body></html>');
  Object.defineProperty(document, 'activeElement', {value: document.body, writable: true, configurable: true});
  dom.HTMLElement.prototype.focus = function () { document.activeElement = this; };
  // linkedom supplies the browser getter but not the select.value setter.
  Object.defineProperty(dom.HTMLSelectElement.prototype, 'value', {configurable: true,
    get() { return this._fixtureValue || ''; }, set(value) { this._fixtureValue = String(value); },
  });
  const requests = [], posts = [], errors = [], counts = {stats: 0, settings: 0, usage: 0};
  const w = vm.createContext({document, window: {}, AbortController,
    ui: {lang: 'en'}, activeSessionID: 'session / one', activeModelID: 'model', sections: {},
    lastTurn: null, workersSeen: [],
    normalizeLanguage: value => value, t: key => catalog[key] || key,
    setTimeout() { throw Error('cost UI must not schedule a timer'); },
    setInterval() { throw Error('cost UI must not poll'); },
  });
  for (const name of ['00-helpers.js', '07-stats.js', '11b-panel-settings.js', '11g-panel-goal-usage.js'])
    vm.runInContext(fs.readFileSync(path.join(assets, 'js', name), 'utf8'), w);
  w.i18nEl = (tag, cls, key) => w.el(tag, cls, w.t(key));
  w.settingCopy = k => [w.t('setting.' + k.key + '.label'), w.t('setting.' + k.key + '.desc')];
  w.j = async (url, opts) => { requests.push({url, opts}); return report(); };
  w.jpost = async (url, body) => { posts.push({url, body}); return {ok: true}; };
  w.toast = message => errors.push(message);
  const realRenderStats = w.renderStats;
  const realSettings = w.sections.settings;
  w.renderStats = () => { counts.stats++; };
  w.sections.settings = () => { counts.settings++; };
  w.sections.usage = async () => { counts.usage++; };
  function event(target, type, props = {}) {
    const ev = new dom.Event(type, {bubbles: true, cancelable: true});
    Object.assign(ev, props); target.dispatchEvent(ev); return ev;
  }
  function key(value, shiftKey = false) { return event(document, 'keydown', {key: value, shiftKey}); }
  return {w, document, requests, posts, counts, errors, event, key, realRenderStats, realSettings};
}
function quote(amount, extra = {}) {
  return {state: 'estimated', amount, currency: 'PLN', source: 'official', estimated: true, ...extra};
}
function report(extra = {}) {
  return {currency: 'PLN', session_id: 'session / one', rates_source: 'NBP', pending: false,
    total: quote(0.25, {partial: true}), rows: [
      {provider: 'api-a', model: 'model-a', calls: 2, input: 120, cached_input: 20, output: 30, reasoning: 5,
        total: 150, cost: quote(0.25), legacy_calls: 1, missing_fx_calls: 0, rate_dates: ['2026-10-09', '2026-10-08']},
      {provider: 'api-b', model: 'model-b', calls: 3, input: 250, cached_input: 0, output: 40, reasoning: 0,
        total: 290, cost: quote(null, {state: 'unknown', source: 'fx_missing'}), legacy_calls: 0,
        missing_fx_calls: 3, rate_dates: ['2026-10-09']},
    ], ...extra};
}
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => {resolve = yes; reject = no;});
  return {promise, resolve, reject};
}
async function completeHandlers() {
  // Finish the promise callbacks of synthetic click/change events.
  await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
}

test('clickable total opens a labeled model table, historical notes and accessible token breakdown', async () => {
  const h = fixture(), box = h.document.querySelector('#stats-block');
  h.w.appendSidebarCost(box, h.w.normalizeStats({cost: quote(0.25)}).cost);
  const opener = box.querySelector('.side-cost'); opener.focus();
  assert.equal(opener.tagName, 'BUTTON'); assert.equal(opener.getAttribute('aria-haspopup'), 'dialog');
  h.event(opener, 'click'); await completeHandlers();
  assert.equal(h.requests.length, 1);
  assert.equal(h.requests[0].url, '/api/costs?session=session%20%2F%20one');
  const dialog = h.document.querySelector('[role="dialog"]');
  assert.equal(dialog.getAttribute('aria-modal'), 'true');
  assert.equal(h.document.getElementById(dialog.getAttribute('aria-labelledby')).textContent, catalog['cost.detailsTitle']);
  const rows = dialog.querySelectorAll('tbody tr'); assert.equal(rows.length, 2);
  assert.match(rows[0].textContent, /model-a.*api-a.*150.*2/);
  const tokens = rows[0].querySelector('td');
  assert.match(tokens.title, /120.*20.*30.*5/); assert.match(tokens.getAttribute('aria-label'), /150.*120.*20.*30.*5/);
  assert.match(rows[1].textContent, /Exchange rate unavailable/);
  assert.ok(dialog.textContent.includes(catalog['cost.legacyNote'].replace('{n}', '1')));
  assert.match(dialog.textContent, /3 calls excluded/);
  assert.match(dialog.querySelector('details').textContent, /NBP.*2026-10-08, 2026-10-09/);
  const select = dialog.querySelector('select'), close = dialog.querySelector('.app-dialog-actions button:last-child');
  assert.equal(h.document.activeElement, close);
  assert.equal(h.key('Tab').defaultPrevented, true); assert.equal(h.document.activeElement, select);
  assert.equal(h.key('Tab', true).defaultPrevented, true); assert.equal(h.document.activeElement, close);
  dialog.querySelector('.cost-details-scroll').focus(); await h.w.refreshCostDetails();
  assert.equal(h.document.activeElement, select, 'refreshing focused content keeps focus in the dialog');
  h.key('Escape'); assert.equal(h.document.querySelector('[role="dialog"]'), null);
  assert.equal(h.document.activeElement, opener); assert.equal(h.w.costDetailsState, null);
});

test('historical FX details distinguish table B currency dates from the USD base and keep old payloads readable', () => {
  const h = fixture();
  const b = {provider: 'p', model: 'm', calls: 1, total: 10, cost: quote(1),
    rate_dates: ['2026-10-07','2026-10-07'], usd_rate_dates: ['2026-10-09','2026-10-09'],
    rate_sources: ['NBP table B / USD table A','NBP table B / USD table A']};
  const different = h.w.renderCostDetails(report({currency: 'THB', rows: [b]})).querySelector('.cost-details-rates');
  assert.equal(different.querySelector('p').textContent, catalog['cost.ratesNote']
    .replace('{source}', 'NBP table B / USD table A').replace('{dates}', 'THB · 2026-10-07'));
  assert.equal(different.querySelector('.cost-usd-rate-dates').textContent,
    catalog['cost.usdRatesNote'].replace('{dates}', '2026-10-09'));
  const a = {...b, rate_dates: ['2026-10-08','2026-10-09'], usd_rate_dates: ['2026-10-09','2026-10-08'], rate_sources: ['NBP table A']};
  const same = h.w.renderCostDetails(report({currency: 'EUR', rows: [a]})).querySelector('.cost-details-rates');
  assert.match(same.textContent, /NBP table A.*EUR · 2026-10-08, 2026-10-09/);
  assert.equal(same.querySelector('.cost-usd-rate-dates'), null, 'equal date sets do not add a redundant USD line');
  const old = h.w.renderCostDetails(report()).querySelector('.cost-details-rates');
  assert.equal(old.querySelector('p').textContent, catalog['cost.ratesNote']
    .replace('{source}', 'NBP').replace('{dates}', '2026-10-08, 2026-10-09'));
  assert.equal(old.querySelector('.cost-usd-rate-dates'), null);
  const untrusted = h.w.renderCostDetails(report({rows: [{...b, rate_sources: ['<img src=x onerror=bad()>']}]}));
  assert.equal(untrusted.querySelector('img'), null);
  assert.match(untrusted.querySelector('.cost-details-rates').textContent, /<img src=x onerror=bad\(\)>/);
});

test('scope changes, modal replacement and close reject stale responses without polling', async () => {
  const h = fixture(), requests = [];
  h.w.j = (url, opts) => { const request = deferred(); requests.push({url, opts, ...request}); return request.promise; };
  const firstLoad = h.w.openCostDetails(h.w.activeSessionID);
  const first = h.document.querySelector('[role="dialog"]'), select = first.querySelector('select');
  select.value = 'all'; h.event(select, 'change');
  assert.equal(requests[0].opts.signal.aborted, true); assert.equal(requests[1].url, '/api/costs');
  requests[1].resolve(report({rows: [], total: quote(7)})); await completeHandlers();
  const currentText = first.textContent;
  requests[0].resolve(report()); await firstLoad;
  assert.equal(first.textContent, currentText, 'old session result cannot replace all history');
  const secondLoad = h.w.openCostDetails(h.w.activeSessionID);
  assert.equal(first.isConnected, false);
  const second = h.document.querySelector('[role="dialog"]');
  h.key('Escape'); assert.equal(requests[2].opts.signal.aborted, true);
  requests[2].resolve(report()); await secondLoad;
  assert.equal(second.isConnected, false); assert.equal(h.document.querySelector('[role="dialog"]'), null);
  assert.equal(requests.length, 3, 'no refresh or retry occurs automatically');
});

test('a session change during a session request closes the old result, while no session selects all history', async () => {
  const h = fixture(), response = deferred(); h.w.j = () => response.promise;
  const load = h.w.openCostDetails(h.w.activeSessionID);
  h.w.activeSessionID = 'different-session'; response.resolve(report()); await load;
  assert.equal(h.document.querySelector('[role="dialog"]'), null);
  h.w.activeSessionID = '';
  h.w.j = async (url, opts) => { h.requests.push({url, opts}); return report({rows: []}); };
  await h.w.openCostDetails('');
  const dialog = h.document.querySelector('[role="dialog"]');
  assert.equal(dialog.querySelector('select').value, 'all');
  assert.equal(dialog.querySelector('option').disabled, true);
  assert.equal(h.requests[0].url, '/api/costs');
});

test('missing rates have one manual bounded POST then a local refresh, with no automatic request', async () => {
  const h = fixture(), finished = deferred(); let posted = false;
  h.w.renderStats = () => { h.counts.stats++; finished.resolve(); };
  h.w.j = async (url, opts) => {
    h.requests.push({url, opts});
    if (url === '/api/costs/rates') { posted = true; return {ok: true}; }
    return posted ? report({rows: []}) : report();
  };
  await h.w.openCostDetails(h.w.activeSessionID);
  assert.equal(h.requests.length, 1, 'opening only reads the local report');
  const button = h.document.querySelector('.cost-details-fetch-rates');
  button.focus();
  h.event(button, 'click'); h.event(button, 'click');
  // Wait for the actual completion of POST -> report refresh -> HUD update,
  // rather than assuming a fixed number of cross-realm promise callbacks.
  await finished.promise;
  assert.deepEqual(h.requests.map(r => r.url), ['/api/costs?session=session%20%2F%20one', '/api/costs/rates', '/api/costs?session=session%20%2F%20one']);
  assert.equal(h.requests[1].opts.method, 'POST');
  assert.deepEqual(JSON.parse(h.requests[1].opts.body), {session_id: 'session / one'});
  assert.equal(h.counts.stats, 1); assert.equal(h.document.querySelector('.cost-details-fetch-rates'), null);
  assert.equal(h.document.activeElement, h.document.querySelector('.cost-details-scope select'));
});

test('one active rate generation shares a single completion wait across HUD and popup refreshes', async () => {
  const h = fixture(), ready = deferred(), hudLoads = [], popupLoads = [];
  let pending = true;
  h.w.j = (url, opts) => {
    h.requests.push({url, opts});
    if (url === '/api/costs/ready') return ready.promise;
    if (url === '/api/config') return Promise.resolve({knobs: []});
    if (url.startsWith('/api/stats')) return Promise.resolve({cost: quote(pending ? null : 0.25, {
      source: pending ? 'fx_missing' : 'official', rates_pending: pending, rates_generation: 42,
    })});
    if (url.startsWith('/api/costs')) return Promise.resolve(report({pending, rates_generation: 42}));
    throw Error('unexpected request: ' + url);
  };
  h.w.renderStats = () => {
    h.counts.stats++;
    const load = h.realRenderStats(); hudLoads.push(load); return load;
  };
  const refresh = h.w.refreshCostDetails;
  h.w.refreshCostDetails = () => { const load = refresh(); popupLoads.push(load); return load; };
  await h.w.renderStats();
  assert.ok(h.document.querySelector('.side-cost').textContent.includes(catalog['cost.pending']));
  await h.w.openCostDetails(h.w.activeSessionID);
  assert.ok(h.document.querySelector('[role="dialog"]').textContent.includes(catalog['cost.pending']));
  await Promise.all([h.w.renderStats(), h.w.refreshCostDetails()]);
  await Promise.all([h.w.renderStats(), h.w.refreshCostDetails()]);
  const shared = h.w.waitCostRatesReady(42);
  assert.equal(shared, h.w.waitCostRatesReady(42));
  assert.equal(h.requests.filter(r => r.url === '/api/costs/ready').length, 1);
  assert.equal(h.counts.stats, 3);
  pending = false; ready.resolve({ok: true});
  assert.equal(await shared, true);
  // The completion callback dispatches both refreshes synchronously; await
  // their actual promises rather than checking progress or using timers.
  await Promise.all([...hudLoads, ...popupLoads]);
  assert.equal(h.counts.stats, 4, 'completion refreshes the latest HUD exactly once');
  assert.equal(h.requests.filter(r => r.url.startsWith('/api/costs?')).length, 4,
    'completion refreshes the open popup exactly once');
  assert.equal(h.document.querySelector('.side-cost').textContent.includes(catalog['cost.pending']), false);
  assert.equal(h.document.querySelector('[role="dialog"]').textContent.includes(catalog['cost.pending']), false);
  await Promise.all([h.w.renderStats(), h.w.refreshCostDetails()]);
  assert.equal(h.requests.filter(r => r.url === '/api/costs/ready').length, 1, 'idle reports do not wait');
  assert.equal(h.requests.some(r => r.opts && r.opts.method === 'POST'), false);
  assert.equal(h.posts.length, 0);
});

test('rate completion rejects stale views and never retries a failed generation automatically', async t => {
  for (const change of ['close', 'session', 'model', 'provider']) {
    await t.test(change + ' before completion prevents stale refresh', async () => {
      const h = fixture(), ready = deferred();
      h.w.activeProviderID = 'provider-one';
      h.w.j = (url, opts) => {
        h.requests.push({url, opts});
        if (url === '/api/costs/ready') return ready.promise;
        if (url === '/api/config') return Promise.resolve({knobs: []});
        if (url.startsWith('/api/stats')) return Promise.resolve({cost: quote(null, {
          source: 'fx_missing', rates_pending: true, rates_generation: 42,
        })});
        return Promise.resolve(report({pending: true, rates_generation: 42}));
      };
      if (change !== 'close') {
        h.w.renderStats = () => { h.counts.stats++; return h.realRenderStats(); };
        await h.w.renderStats();
      }
      await h.w.openCostDetails(h.w.activeSessionID);
      const dialog = h.document.querySelector('[role="dialog"]'), text = dialog.textContent;
      const requestsBefore = h.requests.length, statsBefore = h.counts.stats;
      const wait = h.w.waitCostRatesReady(42);
      if (change === 'close') h.key('Escape');
      if (change === 'session') h.w.activeSessionID = 'different-session';
      if (change === 'model') h.w.activeModelID = 'different-model';
      if (change === 'provider') h.w.activeProviderID = 'different-provider';
      ready.resolve({ok: true}); await wait;
      assert.equal(h.requests.length, requestsBefore);
      assert.equal(h.counts.stats, statsBefore);
      if (change === 'close' || change === 'session') assert.equal(dialog.isConnected, false);
      else assert.equal(dialog.textContent, text);
    });
  }
  await t.test('the latest scope alone receives the completion refresh', async () => {
    const h = fixture(), ready = deferred(), loads = []; let pending = true;
    h.w.j = (url, opts) => {
      h.requests.push({url, opts});
      return url === '/api/costs/ready' ? ready.promise : Promise.resolve(report({pending, rates_generation: 42}));
    };
    const refresh = h.w.refreshCostDetails;
    h.w.refreshCostDetails = () => { const load = refresh(); loads.push(load); return load; };
    await h.w.openCostDetails(h.w.activeSessionID);
    h.document.querySelector('.cost-details-scope select').value = 'all';
    await h.w.refreshCostDetails();
    const wait = h.w.waitCostRatesReady(42);
    pending = false; ready.resolve({ok: true}); await wait; await Promise.all(loads);
    assert.deepEqual(h.requests.map(r => r.url), [
      '/api/costs?session=session%20%2F%20one', '/api/costs/ready', '/api/costs', '/api/costs',
    ]);
    assert.equal(h.counts.stats, 1);
  });
  for (const legacy of [false, true]) {
    await t.test((legacy ? 'legacy pending state' : 'generation ID') + ' suppresses retry after cancellation', async () => {
      const h = fixture(), first = deferred(), next = deferred(), loads = [];
      let generation = legacy ? undefined : 42, pending = true, nextCycle = false;
      h.w.j = (url, opts) => {
        h.requests.push({url, opts});
        if (url === '/api/costs/ready') return nextCycle ? next.promise : first.promise;
        return Promise.resolve(report({pending, rates_generation: generation}));
      };
      const refresh = h.w.refreshCostDetails;
      h.w.refreshCostDetails = () => { const load = refresh(); loads.push(load); return load; };
      await h.w.openCostDetails(h.w.activeSessionID);
      const wait = h.w.waitCostRatesReady(generation);
      first.reject(Object.assign(Error('wait canceled'), {name: 'AbortError'}));
      assert.equal(await wait, false);
      if (legacy) {
        h.w.observeCostRatesPending(null, null, 'hud', () => true);
        assert.equal(await h.w.waitCostRatesReady(undefined), false, 'missing metadata cannot invent a new idle cycle');
      }
      await h.w.refreshCostDetails(); await h.w.refreshCostDetails();
      assert.equal(h.requests.filter(r => r.url === '/api/costs/ready').length, 1);
      assert.equal(h.counts.stats, 0, 'a failed wait cannot automatically refresh or retry');
      if (legacy) { pending = false; await h.w.refreshCostDetails(); }
      else generation = 43;
      pending = true; nextCycle = true; await h.w.refreshCostDetails();
      assert.equal(h.requests.filter(r => r.url === '/api/costs/ready').length, 2, 'a new cycle can wait once');
      const nextWait = h.w.waitCostRatesReady(generation);
      pending = false; next.resolve({ok: true}); await nextWait; await Promise.all(loads);
      assert.equal(h.counts.stats, 1);
      assert.equal(h.requests.some(r => r.opts && r.opts.method === 'POST'), false);
    });
  }
});

test('failed rate fetch preserves data and allows retry; scope changes cancel its result', async () => {
  const h = fixture(), pending = deferred();
  h.w.j = async (url, opts) => {
    h.requests.push({url, opts});
    if (url === '/api/costs/rates') throw Error('offline');
    return report();
  };
  await h.w.openCostDetails(h.w.activeSessionID);
  let button = h.document.querySelector('.cost-details-fetch-rates');
  h.event(button, 'click'); await completeHandlers();
  assert.equal(button.disabled, false); assert.match(h.document.querySelector('[role="dialog"]').textContent, /offline/);
  assert.equal(h.document.querySelectorAll('tbody tr').length, 2);
  h.w.j = async (url, opts) => {
    h.requests.push({url, opts});
    return url === '/api/costs/rates' ? pending.promise : report({rows: []});
  };
  h.event(button, 'click');
  const post = h.requests.at(-1), scope = h.document.querySelector('select');
  scope.value = 'all'; h.event(scope, 'change'); await completeHandlers();
  assert.equal(post.opts.signal.aborted, true);
  const text = h.document.querySelector('[role="dialog"]').textContent;
  pending.resolve({ok: true}); await completeHandlers();
  assert.equal(h.document.querySelector('[role="dialog"]').textContent, text);
  assert.equal(h.counts.stats, 0);
});

test('read failure is recoverable and model/provider text never becomes markup', async () => {
  const h = fixture(); h.w.j = async () => { throw Error('local database unavailable'); };
  await h.w.openCostDetails(h.w.activeSessionID);
  const dialog = h.document.querySelector('[role="dialog"]');
  assert.match(dialog.textContent, /local database unavailable/);
  assert.equal(dialog.querySelector('.app-dialog-actions button').disabled, false);
  const row = {...report().rows[0], model: '<img src=x onerror=alert(1)>', provider: '<script>bad()</script>'};
  h.w.j = async () => report({rows: [row]}); await h.w.refreshCostDetails();
  assert.equal(dialog.querySelector('img'), null); assert.equal(dialog.querySelector('script'), null);
  assert.match(dialog.textContent, /<img src=x onerror=alert\(1\)>/);
});

test('legacy cost aggregates stay outside model calls and cannot offer a fictitious daily FX retry', () => {
  const h = fixture();
  const legacy = {provider: '', model: '', calls: 0, input: 200, output: 40, total: 240, legacy_calls: 1,
    missing_fx_calls: 1, rate_dates: [], cost: quote(null, {state: 'unknown', source: 'fx_missing'})};
  const unavailable = h.w.renderCostDetails(report({rows: [], legacy_unattributed: legacy}), () => {});
  const section = unavailable.querySelector('.cost-details-legacy');
  assert.equal(section.querySelector('h3').textContent, catalog['cost.legacyAggregate']);
  assert.ok(section.textContent.includes(catalog['cost.legacyAggregateNote']));
  assert.match(section.textContent, /240.*Exchange rate unavailable/);
  assert.equal(unavailable.querySelector('table'), null, 'no fictitious model or API-call row');
  assert.equal(unavailable.querySelector('.cost-details-fetch-rates'), null, 'a multi-day aggregate has no recoverable daily FX rate');
  const usd = h.w.renderCostDetails(report({currency: 'USD', rows: [], legacy_unattributed: {
    ...legacy, missing_fx_calls: 0, cost: quote(1, {currency: 'USD'}),
  }}));
  assert.match(usd.querySelector('.cost-details-legacy').textContent, /240.*~\$1\.00/);
  const mixed = h.w.renderCostDetails(report({legacy_unattributed: legacy}), () => {});
  assert.equal(mixed.querySelectorAll('.cost-details-model').length, 2);
  assert.ok(mixed.querySelector('.cost-details-fetch-rates'), 'real missing per-call rates retain their manual fetch');
  assert.match(mixed.textContent, /3 calls excluded/);
  assert.equal(h.w.renderCostDetails(report()).querySelector('.cost-details-legacy'), null, 'older API responses retain their existing view');
});

test('currency selection writes one global setting and refreshes open details and sums', async () => {
  const h = fixture(); await h.w.openCostDetails(h.w.activeSessionID);
  const codes = ['USD','PLN','EUR','AUD','CNY','KRW'];
  const row = h.w.knobRow({key: 'cost_currency', kind: 'currency', raw: 'PLN', value: 'PLN', source: 'manual'}, codes);
  h.document.body.appendChild(row); const select = row.querySelector('select');
  assert.equal(select.value, 'PLN'); assert.equal(select.getAttribute('aria-label'), catalog['setting.cost_currency.label']);
  assert.deepEqual(Array.from(select.querySelectorAll('option'), option => option.value), codes);
  select.value = 'EUR'; h.event(select, 'change'); await completeHandlers();
  assert.equal(h.posts.length, 1); assert.equal(h.posts[0].url, '/api/config');
  assert.deepEqual(JSON.parse(JSON.stringify(h.posts[0].body)), {key: 'cost_currency', value: 'EUR'});
  assert.equal(h.counts.settings, 1); assert.equal(h.counts.stats, 1); assert.equal(h.requests.length, 2);
});

test('settings currency choices come from the API with localized names and a selected-value fallback', async () => {
  const h = fixture();
  h.w.panelContent = h.w.el('div'); h.document.body.appendChild(h.w.panelContent);
  h.w.applyGenerationSpeedVisibility = () => {};
  h.w.j = async url => {
    assert.equal(url, '/api/config');
    return {cost_currencies: ['USD','PLN','AUD','CNY','KRW','AUD','bad',''], knobs: [
      {key: 'cost_currency', kind: 'currency', raw: 'PLN', value: 'PLN', source: 'manual'},
    ]};
  };
  let constructed = 0;
  h.w.Intl = {DisplayNames: class {
    constructor(languages, options) {
      constructed++; assert.deepEqual(Array.from(languages), ['en']); assert.equal(options.type, 'currency');
    }
    of(code) { return 'Name ' + code; }
  }};
  await h.realSettings();
  const options = Array.from(h.w.panelContent.querySelectorAll('.k-currency option'));
  assert.deepEqual(options.map(option => option.value), ['USD','PLN','AUD','CNY','KRW']);
  assert.equal(options[2].textContent, 'AUD · Name AUD');
  assert.equal(constructed, 1, 'one display-name formatter serves the complete list');
  h.w.Intl = {};
  const row = h.w.knobRow({key: 'cost_currency', kind: 'currency', raw: 'NOK', source: 'manual'});
  assert.deepEqual(Array.from(row.querySelectorAll('option'), option => option.value), ['NOK']);
  assert.equal(row.querySelector('select').value, 'NOK');
  assert.equal(row.querySelector('option').textContent, 'NOK');
});

test('manual price display and saved rate remain USD when the cost total is another currency', async () => {
  const h = fixture();
  const stats = h.w.normalizeStats({session: {provider: 'api', model: 'model'}, cost: quote(4, {
    input_per_million: 1, cached_input_per_million: 0.1, output_per_million: 5, pricing_currency: 'USD',
  })});
  const details = h.w.renderPriceDetails(stats); h.document.body.appendChild(details);
  const text = details.querySelector('.price-rates').textContent;
  assert.match(text, /\$1\.00.*\$0\.10.*\$5\.00/); assert.doesNotMatch(text, /PLN/);
  const form = details.querySelector('form'); h.event(form, 'submit'); await completeHandlers();
  assert.deepEqual(JSON.parse(JSON.stringify(h.posts[0].body)), {
    provider: 'api', model: 'model', input_per_million: 1, cached_input_per_million: 0.1, output_per_million: 5,
  });
});

test('editing and removing current prices never reloads immutable historical prices into the form', async () => {
  const h = fixture();
  const historical = quote(4, {state: 'manual', manual: true, source: 'manual', estimated: false,
    input_per_million: 1, cached_input_per_million: 0.1, output_per_million: 5, pricing_currency: 'USD'});
  const current = quote(0, {state: 'manual', manual: true, source: 'manual', estimated: false, currency: 'USD',
    input_per_million: 2, cached_input_per_million: 0.2, output_per_million: 10, pricing_currency: 'USD'});
  const stats = h.w.normalizeStats({session: {provider: 'api', model: 'model'}, cost: historical, pricing: current});
  assert.equal(stats.cost.inputPerMillion, 1); assert.equal(stats.pricing.inputPerMillion, 2);
  const details = h.w.renderPriceDetails(stats); h.document.body.appendChild(details);
  assert.match(details.querySelector('.price-rates').textContent, /\$2\.00.*\$0\.20.*\$10\.00/);
  h.event(details.querySelector('form'), 'submit'); await completeHandlers();
  assert.deepEqual(JSON.parse(JSON.stringify(h.posts[0].body)), {
    provider: 'api', model: 'model', input_per_million: 2, cached_input_per_million: 0.2, output_per_million: 10,
  });
  h.w.appConfirm = async () => true;
  const remove = details.querySelector('.price-actions .danger'); assert.ok(remove);
  h.event(remove, 'click'); await completeHandlers();
  assert.deepEqual(JSON.parse(JSON.stringify(h.posts[1].body)), {provider: 'api', model: 'model', remove: true});
  const refreshed = h.w.normalizeStats({session: {provider: 'api', model: 'model'}, cost: historical,
    pricing: quote(0, {currency: 'USD', manual: false, input_per_million: 2.5,
      cached_input_per_million: 0.25, output_per_million: 12, pricing_currency: 'USD'})});
  const afterRemove = h.w.renderPriceDetails(refreshed);
  assert.equal(afterRemove.querySelector('.price-actions .danger'), null, 'historical manual cost cannot retain the current remove action');
  assert.match(afterRemove.querySelector('.price-rates').textContent, /\$2\.50.*\$0\.25.*\$12\.00/);
  assert.equal(refreshed.cost.amount, 4); assert.equal(refreshed.cost.inputPerMillion, 1);
  const olderServer = h.w.normalizeStats({cost: historical});
  assert.equal(olderServer.pricing, null);
  assert.match(h.w.renderPriceDetails(olderServer).querySelector('.price-rates').textContent, /\$1\.00/);
});

test('new copy is translated with matching placeholders in every shipped locale', () => {
  const keys = ['cost.detailsTitle','cost.openDetails','cost.scope','cost.scope.session','cost.scope.all',
    'cost.apiCalls','cost.callsHint','cost.empty','cost.legacyNote','cost.missingFxNote','cost.ratesTitle',
    'cost.ratesNote','cost.pending','cost.source.fx_missing','cost.fxMissingValue','cost.fetchRates',
    'setting.cost_currency.label','setting.cost_currency.desc','cost.legacyAggregate','cost.legacyAggregateNote','cost.usdRatesNote'];
  for (const file of fs.readdirSync(path.join(assets, 'locales'))) {
    const values = JSON.parse(fs.readFileSync(path.join(assets, 'locales', file), 'utf8'));
    for (const key of keys) {
      assert.equal(typeof values[key], 'string', file + ' ' + key); assert.ok(values[key].trim());
      assert.deepEqual((values[key].match(/\{[a-z_]+\}/g) || []).sort(), (catalog[key].match(/\{[a-z_]+\}/g) || []).sort(), file + ' ' + key);
    }
  }
});
