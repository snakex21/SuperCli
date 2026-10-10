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
  const {document, window: dom} = parseHTML('<html><body><div id="stage"><div id="stream"></div><div id="welcome"></div></div><div id="stats-block"></div><button id="previous">Previous</button></body></html>');
  Object.defineProperty(document, 'activeElement', {value: document.body, writable: true, configurable: true});
  dom.HTMLElement.prototype.focus = function () { document.activeElement = this; };
  Object.defineProperty(dom.HTMLSelectElement.prototype, 'value', {configurable: true,
    get() { return this._fixtureValue || ''; }, set(value) { this._fixtureValue = String(value); },
  });
  const requests = [];
  const w = vm.createContext({document, window: {SuperCliUI: {fileMutationTools: new Set()}}, AbortController,
    ui: {lang: 'en'}, activeSessionID: 'session / one', activeModelID: 'selected-model',
    activeProviderID: 'provider', lastTurn: null, workersSeen: [], sections: {},
    normalizeLanguage: value => value,
    t: key => catalog[key] || key,
    setTimeout() { throw Error('usage popup must not schedule timers'); },
    setInterval() { throw Error('usage popup must not poll'); },
  });
  for (const file of ['00-helpers.js', '04-transcript.js', '07-stats.js'])
    vm.runInContext(fs.readFileSync(path.join(assets, 'js', file), 'utf8'), w);
  w.i18nEl = (tag, cls, key) => w.el(tag, cls, w.t(key));
  w.j = async (url, opts) => {
    requests.push({url, opts});
    if (url === '/api/config') return {knobs: []};
    if (url.startsWith('/api/stats')) return {tokens: {total: 451}};
    return report();
  };
  function event(target, type, props = {}) {
    const ev = new dom.Event(type, {bubbles: true, cancelable: true});
    Object.assign(ev, props); target.dispatchEvent(ev); return ev;
  }
  function key(value, shiftKey = false) { return event(document, 'keydown', {key: value, shiftKey}); }
  return {w, document, requests, event, key};
}
function counters(extra = {}) {
  return {calls: 2, input: 120, cached_input: 20, evaluated_input: 100, output: 30, reasoning: 5, total: 150,
    has_cached: true, has_reasoning: true, cached_reported_calls: 1, reasoning_reported_calls: 1,
    context_tool_estimate: 32, context_tool_estimate_known: true,
    context_estimate_calls: 1, context_estimate_source: 'request-shape', legacy_records: 0, ...extra};
}
function report(extra = {}) {
  return {session_id: 'session / one', scope: 'session', totals: counters({calls: 3, input: 377, output: 74, total: 451}),
    rows: [
      {provider: 'api-a', model: 'model-a', ...counters(), purposes: [
        {purpose: 'main', ...counters({calls: 1, input: 100, output: 20, total: 120})},
        {purpose: 'task', ...counters({calls: 1, input: 20, output: 10, total: 30})},
      ]},
      {provider: 'api-b', model: 'model-b', ...counters({calls: 1, input: 250, output: 40, total: 290,
        cached_input: 0, reasoning: 0, has_cached: false, has_reasoning: false,
        context_tool_estimate: 999999, context_tool_estimate_known: false}),
        purposes: [{purpose: 'compact', ...counters({calls: 1, input: 250, output: 40, total: 290})}]},
    ], purposes: [], legacy_unattributed: {input: 7, output: 4, total: 11, sessions: 1}, ...extra};
}
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => {resolve = yes; reject = no;});
  return {promise, resolve, reject};
}

test('the simple total token row opens a measured per-model breakdown with partial data and purposes', async () => {
  const h = fixture(); await h.w.renderStats();
  const opener = h.document.querySelector('.stats-usage-button'); opener.focus();
  assert.equal(opener.tagName, 'BUTTON'); assert.equal(opener.getAttribute('aria-haspopup'), 'dialog');
  assert.match(opener.getAttribute('aria-label'), /451/);
  assert.equal(h.document.querySelector('.usage-details-table'), null);
  const open = h.w.openUsageDetails; let opened;
  h.w.openUsageDetails = id => { opened = open(id); return opened; };
  h.event(opener, 'click'); await opened;
  assert.equal(h.requests.at(-1).url, '/api/usage?session=session%20%2F%20one');
  assert.equal(h.requests.filter(r => r.url.startsWith('/api/usage')).length, 1);
  const dialog = h.document.querySelector('[role="dialog"]');
  assert.equal(dialog.getAttribute('aria-modal'), 'true');
  assert.equal(h.document.getElementById(dialog.getAttribute('aria-labelledby')).textContent, catalog['usage.detailsTitle']);
  assert.match(dialog.querySelector('.cost-details-total').textContent, /451/);
  const modelRows = Array.from(dialog.querySelectorAll('.cost-details-model'), n => n.parentNode);
  assert.equal(modelRows.length, 2);
  assert.deepEqual(Array.from(modelRows[0].querySelectorAll('td'), cell => cell.textContent), ['120','20*','30','5*','2','150']);
  assert.match(modelRows[0].querySelectorAll('td')[1].title, /1 of 2 calls/);
  assert.deepEqual(Array.from(modelRows[1].querySelectorAll('td'), cell => cell.textContent), ['250','—','40','—','1','290']);
  assert.equal(modelRows[1].querySelectorAll('td')[1].title, catalog['usage.unreported']);
  assert.match(dialog.textContent, /Main work \(main\).*Worker task \(task\).*Context compaction \(compact\)/);
  assert.ok(dialog.textContent.includes(catalog['usage.partsNote']));
  assert.ok(dialog.textContent.includes(catalog['usage.partialNote']));
  assert.ok(dialog.textContent.includes(catalog['usage.unattributed'].replace('{n}', '11')));
  assert.equal(dialog.textContent.includes('999,999'), false, 'unavailable tool estimates must not be displayed');
  assert.ok(dialog.textContent.includes(catalog['usage.toolEstimate'].replace('{n}', '32')));
  const select = dialog.querySelector('select'), close = dialog.querySelector('.app-dialog-actions button:last-child');
  assert.equal(h.document.activeElement, close);
  assert.equal(h.key('Tab').defaultPrevented, true); assert.equal(h.document.activeElement, select);
  assert.equal(h.key('Tab', true).defaultPrevented, true); assert.equal(h.document.activeElement, close);
  dialog.querySelector('.cost-details-scroll').focus(); await h.w.refreshUsageDetails();
  assert.equal(h.document.activeElement, select);
  h.key('Escape'); assert.equal(h.document.querySelector('[role="dialog"]'), null);
  assert.equal(h.document.activeElement, opener); assert.equal(h.w.usageDetailsState, null);
  assert.equal(h.requests.some(r => r.opts && r.opts.method === 'POST'), false);
});

test('scope changes and close cancel requests and reject stale usage results without automatic refreshes', async () => {
  const h = fixture(), requests = [];
  h.w.j = (url, opts) => {const response = deferred(); requests.push({url, opts, ...response}); return response.promise;};
  const first = h.w.openUsageDetails(h.w.activeSessionID);
  const dialog = h.document.querySelector('[role="dialog"]'), select = dialog.querySelector('select');
  select.value = 'all'; const all = h.w.refreshUsageDetails();
  assert.equal(requests[0].opts.signal.aborted, true); assert.equal(requests[1].url, '/api/usage');
  requests[1].resolve(report({rows: [], totals: {total: 81}})); await all;
  const currentText = dialog.textContent;
  requests[0].resolve(report()); await first;
  assert.equal(dialog.textContent, currentText);
  const closed = h.w.refreshUsageDetails(); h.key('Escape');
  assert.equal(requests[2].opts.signal.aborted, true);
  requests[2].resolve(report()); await closed;
  assert.equal(dialog.isConnected, false); assert.equal(h.w.usageDetailsState, null);
  assert.equal(requests.length, 3);
});

test('a replaced dialog and a switched conversation cannot receive an old token report', async () => {
  for (const change of ['replace', 'session']) {
    const h = fixture(), response = deferred(); let first = true;
    h.w.j = (url, opts) => {
      h.requests.push({url, opts});
      if (first) {first = false; return response.promise;}
      return Promise.resolve(report({rows: [], totals: {total: 81}}));
    };
    const load = h.w.openUsageDetails(h.w.activeSessionID);
    const old = h.document.querySelector('[role="dialog"]');
    if (change === 'replace') await h.w.openUsageDetails(h.w.activeSessionID);
    else h.w.activeSessionID = 'different-session';
    response.resolve(report()); await load;
    assert.equal(old.isConnected, false);
    if (change === 'replace') assert.match(h.document.querySelector('.cost-details-total').textContent, /81/);
    else assert.equal(h.document.querySelector('[role="dialog"]'), null);
  }
});

test('usage errors are recoverable, no-session scope is history, and untrusted identities remain text', async () => {
  const h = fixture(); h.w.activeSessionID = ''; h.w.j = async () => {throw Error('database unavailable');};
  await h.w.openUsageDetails('');
  const dialog = h.document.querySelector('[role="dialog"]');
  assert.equal(dialog.querySelector('select').value, 'all'); assert.equal(dialog.querySelector('option').disabled, true);
  assert.match(dialog.textContent, /database unavailable/);
  assert.equal(dialog.querySelector('.app-dialog-actions button').disabled, false);
  h.w.j = async (url, opts) => {
    h.requests.push({url, opts});
    return report({rows: [{provider: '<script>bad()</script>', model: '<img src=x onerror=bad()>',
      ...counters(), purposes: [{purpose: '<svg onload=bad()>', ...counters()}]}]});
  };
  await h.w.refreshUsageDetails();
  assert.equal(h.requests.at(-1).url, '/api/usage');
  assert.equal(dialog.querySelector('img, script, svg'), null);
  assert.match(dialog.textContent, /<img src=x onerror=bad\(\)>/);
  assert.match(dialog.textContent, /<svg onload=bad\(\)>/);
});

test('only a known request-shape tool estimate is displayed, including a known zero', () => {
  const h = fixture();
  const body = h.w.renderUsageDetails(report({rows: [], totals: counters({context_tool_estimate: 0})}));
  assert.ok(body.textContent.includes(catalog['usage.toolEstimate'].replace('{n}', '0')));
  for (const override of [{context_tool_estimate_known: false}, {context_estimate_source: 'unknown'}]) {
    const unknown = h.w.renderUsageDetails(report({rows: [], totals: counters(override)}));
    assert.equal(unknown.textContent.includes('Estimated tool context'), false);
  }
});

test('output charts treat thinking as a subset and expose partial or unknown coverage without double counting', () => {
  const h = fixture();
  const body = h.w.renderUsageDetails(report({rows: [
    {provider: 'p', model: 'complete', ...counters({calls: 2, output: 100, reasoning: 40, reasoning_reported_calls: 2}), purposes: []},
    {provider: 'p', model: 'partial', ...counters({output: 100, reasoning: 10, reasoning_reported_calls: 1}), purposes: []},
    {provider: 'p', model: 'unknown', ...counters({output: 50, has_reasoning: false, reasoning: 0, reasoning_reported_calls: 0}), purposes: []},
    {provider: 'p', model: 'known-zero', ...counters({calls: 1, output: 20, reasoning: 0, reasoning_reported_calls: 1}), purposes: []},
    {provider: 'p', model: 'empty', ...counters({calls: 1, output: 0, reasoning: 0, reasoning_reported_calls: 1}), purposes: []},
  ]}));
  const charts = Array.from(body.querySelectorAll('.usage-output-chart'));
  assert.equal(charts.length, 5);
  assert.equal(body.querySelector('.usage-output-charts').getAttribute('aria-label'), catalog['usage.outputChart']);
  assert.ok(body.textContent.includes(catalog['usage.outputChartNote']));
  assert.equal(charts[0].querySelector('.usage-chart-thinking').style.width, '40%');
  assert.equal(charts[0].querySelector('.usage-chart-remaining').style.width, '60%');
  assert.match(charts[0].querySelector('.usage-chart-total').textContent, /100/);
  assert.match(charts[0].querySelector('[role="img"]').getAttribute('aria-label'), /Reported thinking: 40; Remaining output: 60;.*2 of 2/);
  assert.equal(charts[0].querySelector('.usage-chart-coverage'), null);
  assert.match(charts[1].querySelector('.usage-chart-coverage').textContent, /1 of 2 calls/);
  assert.match(charts[1].querySelector('.usage-chart-remaining-label').textContent, /90/);
  assert.equal(charts[2].querySelector('.usage-chart-unknown') !== null, true);
  assert.equal(charts[2].querySelector('.usage-chart-thinking').style.width, '0%');
  assert.equal(charts[2].querySelector('.usage-chart-remaining').style.width, '100%');
  assert.match(charts[2].querySelector('.usage-chart-thinking-label').textContent, /—/);
  assert.equal(charts[2].querySelector('.usage-chart-coverage').textContent, catalog['usage.unreported']);
  assert.match(charts[3].querySelector('.usage-chart-thinking-label').textContent, /: 0$/);
  assert.equal(charts[3].querySelector('.usage-chart-unknown'), null);
  assert.equal(charts[4].querySelector('.usage-chart-thinking').style.width, '0%');
  assert.equal(charts[4].querySelector('.usage-chart-remaining').style.width, '0%');
  for (const chart of charts) for (const part of chart.querySelectorAll('.usage-chart-bar span'))
    assert.equal(part.getAttribute('aria-hidden'), 'true');
  assert.match(body.querySelector('.cost-details-total').textContent, /451/, 'charts do not recalculate provider totals');
});

test('a single model tool-context estimate is shown once, with totals retained for multiple models or empty rows', () => {
  const h = fixture();
  const data = counters({calls: 1124, context_estimate_calls: 1124, context_tool_estimate: 5000});
  const row = {provider: 'p', model: 'm', ...data, purposes: []};
  const text = catalog['usage.toolEstimate'].replace('{n}', '5,000');
  const one = h.w.renderUsageDetails(report({totals: data, rows: [row]}));
  assert.equal(one.textContent.split(text).length - 1, 1);
  const many = h.w.renderUsageDetails(report({totals: data, rows: [row, {...row, model: 'second'}]}));
  assert.equal(many.textContent.split(text).length - 1, 3);
});

test('timing charts use matched phase measurements, show coverage and preserve purpose times separately', () => {
  const h = fixture();
  const timing = {has_timing: true, timing_reported_calls: 2, duration_ms: 9000,
    ttft_ms: 1000, ttft_reported_calls: 1, stream_ms: 3000, stream_output_tokens: 30, stream_reported_calls: 1};
  const body = h.w.renderUsageDetails(report({rows: [{provider: 'p', model: 'm', ...counters({calls: 3, output: 900}), ...timing,
    purposes: [{purpose: 'compact', ...counters({calls: 1, output: 30}), ...timing, duration_ms: 4000, timing_reported_calls: 1}],
  }]}));
  assert.ok(body.textContent.includes(catalog['usage.timingNote']));
  const modelTiming = body.querySelector('.usage-purpose-row > td > .usage-timing');
  assert.match(modelTiming.querySelector('.usage-timing-total').textContent, /9\.0s/);
  assert.match(modelTiming.textContent, /2 of 3 calls/);
  assert.match(modelTiming.textContent, /1 of 3 calls/);
  const bar = modelTiming.querySelector('.usage-time-bar');
  assert.equal(bar.querySelector('.usage-chart-thinking').style.width, '25%');
  assert.equal(bar.querySelector('.usage-chart-remaining').style.width, '75%');
  assert.match(bar.getAttribute('aria-label'), /Waiting for first output: 1\.0s; Response stream: 3\.0s;.*1 of 3/);
  assert.match(modelTiming.querySelector('.usage-stream-speed').textContent, /10\.0 tok\/s/,
    'speed uses only output from calls matched to stream timing, not all model output');
  const purposeTiming = body.querySelector('.usage-purpose-table .usage-timing-compact');
  assert.match(purposeTiming.querySelector('.usage-timing-total').textContent, /4\.0s/);
  assert.equal(purposeTiming.querySelector('.usage-time-bar'), null, 'purpose details keep a compact numeric breakdown');
  assert.match(purposeTiming.textContent, /1\.0s.*3\.0s.*10\.0 tok\/s/);
});

test('old or incomplete timing data never manufactures call duration, stream phases or speed', () => {
  const h = fixture();
  const row = {provider: 'p', model: 'm', ...counters(), purposes: []};
  const unknown = h.w.renderUsageDetails(report({rows: [{...row, ttft_ms: 1500, ttft_reported_calls: 1}]}));
  assert.equal(unknown.querySelector('.usage-timing'), null);
  assert.equal(unknown.textContent.includes(catalog['usage.timingNote']), false);
  const duration = h.w.renderUsageDetails(report({rows: [{...row, has_timing: true, timing_reported_calls: 1, duration_ms: 1500}]}));
  assert.match(duration.querySelector('.usage-timing-total').textContent, /1\.5s/);
  assert.equal(duration.querySelector('.usage-time-bar'), null);
  assert.equal(duration.querySelector('.usage-stream-speed'), null);
  const zero = h.w.renderUsageDetails(report({rows: [{...row, has_timing: true, timing_reported_calls: 2, duration_ms: 0,
    ttft_ms: 0, ttft_reported_calls: 2, stream_ms: 0, stream_reported_calls: 2, stream_output_tokens: 30}]}));
  assert.match(zero.querySelector('.usage-timing-total').textContent, /0\.0s/);
  assert.equal(zero.querySelector('.usage-stream-speed'), null);
  assert.equal(zero.querySelector('.usage-time-bar .usage-chart-thinking').style.width, '0%');
  assert.equal(zero.querySelector('.usage-time-bar .usage-chart-remaining').style.width, '0%');
});

test('legacy aggregates explain incomplete attribution without inventing model calls or a provider row', () => {
  const h = fixture();
  const mixed = h.w.renderUsageDetails(report({totals: counters({calls: 3, total: 451, legacy_records: 1})}));
  assert.equal(mixed.querySelector('.usage-legacy-note').textContent, catalog['usage.legacyNote']);
  assert.equal(mixed.querySelectorAll('.cost-details-model').length, 2);
  const modelCalls = Array.from(mixed.querySelectorAll('.cost-details-model'), label => label.parentNode.querySelectorAll('td')[4].textContent);
  assert.deepEqual(modelCalls, ['2', '1'], 'the historical aggregate does not become another measured call');
  const legacyOnly = h.w.renderUsageDetails(report({rows: [], totals: counters({calls: 0, total: 11, legacy_records: 1})}));
  assert.equal(legacyOnly.querySelector('.usage-legacy-note').textContent, catalog['usage.legacyNote']);
  assert.match(legacyOnly.querySelector('.cost-details-total').textContent, /11/);
  assert.equal(legacyOnly.querySelector('.cost-details-model'), null);
  assert.equal(h.w.usagePurposeLabel('legacy'), catalog['usage.purpose.legacy'] + ' (legacy)');
  const current = h.w.renderUsageDetails(report({totals: counters(), legacy_unattributed: {total: 0}}));
  assert.equal(current.querySelector('.usage-legacy-note'), null);
});

test('included legacy usage and historical gaps have separate notes without changing the recorded total', () => {
  const h = fixture();
  const data = report({totals: counters({calls: 3, input: 370, output: 107, total: 477, legacy_records: 1}),
    rows: [
      {provider: 'p', model: 'a', ...counters({calls: 2, input: 150, output: 55, total: 205}), purposes: []},
      {provider: 'q', model: 'b', ...counters({calls: 1, input: 20, output: 12, total: 32}), purposes: []},
    ], legacy_unattributed: {input: 200, output: 40, total: 240, sessions: 1},
    legacy_gap: {input: 30, output: 23, total: 53, sessions: 1},
  });
  const body = h.w.renderUsageDetails(data);
  assert.match(body.querySelector('.cost-details-total').textContent, /477/);
  const modelTotals = Array.from(body.querySelectorAll('.cost-details-model'), label => label.parentNode.querySelectorAll('td')[5].textContent);
  assert.deepEqual(modelTotals, ['205', '32']);
  assert.ok(body.textContent.includes(catalog['usage.unattributed'].replace('{n}', '240')));
  assert.equal(body.querySelector('.usage-gap-note').textContent, catalog['usage.legacyGap'].replace('{n}', '53'));
  assert.equal(body.querySelector('.cost-details-total').textContent.includes('530'), false);
  assert.equal(h.w.renderUsageDetails(report()).querySelector('.usage-gap-note'), null, 'older server responses remain compatible');
  assert.equal(h.w.renderUsageDetails(report({legacy_gap: {total: 0}})).querySelector('.usage-gap-note'), null);
});

test('resumed stats show durable last response or model-call metadata without inventing a generation speed', async () => {
  const h = fixture();
  for (const kind of ['response', 'model_call']) {
    const saved = {kind, ev: {model: 'restored-model', tok_in: 100, tok_cached: 20, tok_out: 40,
      tok_total: 140, reasoning_tok: 10, has_cached: true, has_reasoning: true}, tools: 3, elapsed: 1200};
    h.w.j = async url => url === '/api/config' ? {knobs: []} : {tokens: {total: 451}, last_turn: saved};
    await h.w.renderStats();
    const box = h.document.querySelector('#stats-block');
    assert.equal(box.querySelector('.stats-head').textContent, catalog[kind === 'model_call' ? 'usage.lastModelCall' : 'stats.turn']);
    assert.equal(box.querySelector('.stat-row .v').textContent, 'restored-model');
    assert.match(box.textContent, /20 \/ 80 \/ 40/);
    assert.ok(box.textContent.includes(catalog['run.think'] + '10'));
    assert.ok(box.textContent.includes(catalog['run.tools'] + '3'));
    assert.equal(!!box.querySelector('.stats-last-turn-generation-speed'), false, 'saved turn metadata cannot invent its own stream measurement');
    assert.equal(box.querySelector('.stats-session-generation-speed .v').textContent, '—', 'session average remains visible when measurements are unknown');
    assert.equal(h.w.lastTurn, null, 'restoring saved metadata does not mutate live turn state');
  }
  h.w.j = async url => url === '/api/config' ? {knobs: []} : {tokens: {total: 451},
    last_turn: {kind: 'response', ev: {model: '', tok_in: 7, tok_out: 3, tok_total: 10}, tools: 0}};
  await h.w.renderStats();
  assert.equal(h.document.querySelector('#stats-block .stat-row .v').textContent, '—', 'an unknown historical identity cannot use the selected model');
  h.w.lastTurn = {ev: {model: 'live-model', tok_in: 1, tok_out: 2, tok_total: 3, generation_tps: 20}, tools: 1};
  h.w.j = async url => url === '/api/config' ? {knobs: []} : {tokens: {total: 451}};
  await h.w.renderStats();
  const box = h.document.querySelector('#stats-block');
  assert.equal(box.querySelector('.stat-row .v').textContent, 'live-model', 'older servers retain the existing live fallback');
  assert.equal(box.querySelector('.generation-speed .v').textContent, '20.0 tok/s');
});

test('token details copy is translated with matching placeholders in all shipped catalogs', () => {
  const keys = ['usage.detailsTitle','usage.openDetails','usage.partsNote','usage.toolEstimate','usage.purposes',
    'usage.unreported','usage.purpose.main','usage.purpose.task','usage.purpose.compact','usage.coverage',
    'usage.partialNote','usage.unattributed','usage.lastModelCall','usage.legacyNote','usage.purpose.legacy','usage.legacyGap',
    'usage.outputChart','usage.outputChartNote','usage.reportedThinking','usage.remainingOutput','usage.modelTime',
    'usage.firstOutputWait','usage.responseStream','usage.streamSpeed','usage.timingNote'];
  for (const file of fs.readdirSync(path.join(assets, 'locales'))) {
    const values = JSON.parse(fs.readFileSync(path.join(assets, 'locales', file), 'utf8'));
    for (const key of keys) {
      assert.equal(typeof values[key], 'string', file + ' ' + key); assert.ok(values[key].trim());
      assert.deepEqual((values[key].match(/\{[a-z_]+\}/g) || []).sort(), (catalog[key].match(/\{[a-z_]+\}/g) || []).sort(), file + ' ' + key);
    }
  }
});
