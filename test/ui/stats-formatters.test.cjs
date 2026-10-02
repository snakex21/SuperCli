const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const source = fs.readFileSync(path.resolve(__dirname, '../../internal/webgui/assets/js/00-helpers.js'), 'utf8');
const languages = ['en','bg','cs','da','de','el','es','et','fi','fr','hr','hu','it','lt','lv','nb','nl','pl','pt-BR','ro','ru','sk','sl','sr-Latn','sv','tr','uk'];
function helpers(numberFactory, dateFactory) {
  const counts = { number: 0, date: 0 };
  const context = {window: {}, ui: {lang: 'en'}, normalizeLanguage: value => value,
    Intl: {
      NumberFormat: function (...args) { counts.number++; return numberFactory ? numberFactory(...args) : new Intl.NumberFormat(...args); },
      DateTimeFormat: function (...args) { counts.date++; return dateFactory ? dateFactory(...args) : new Intl.DateTimeFormat(...args); },
    }, counts};
  vm.createContext(context); vm.runInContext(source, context); return context;
}
function expectedMoney(locale, n, currency, rate) {
  const abs = Math.abs(n);
  const digits = rate ? 6 : abs > 0 && abs < 0.0001 ? 6 : abs > 0 && abs < 0.01 ? 4 : 2;
  return new Intl.NumberFormat(locale, {style: 'currency', currency: currency || 'USD',
    minimumFractionDigits: rate ? 2 : Math.min(2, digits), maximumFractionDigits: digits}).format(n);
}
test('cached formatters preserve number, currency and date output for every supported language', () => {
  const h = helpers(); const values = [-123456.7, -10000, -0.00001, 0, 0.00009, 0.001, 0.01, 42.5, 9999, 10000, 123456.7];
  for (const language of languages) {
    h.ui.lang = language;
    for (const value of values) {
      assert.equal(h.fmtInteger(value), new Intl.NumberFormat(language, {maximumFractionDigits: 0}).format(Math.round(value)), language + ' integer');
      assert.equal(h.fmtCompactNumber(value), new Intl.NumberFormat(language, {notation: 'compact', maximumFractionDigits: Math.abs(value) >= 10000 ? 1 : 0}).format(value), language + ' compact');
      for (const currency of ['USD', 'EUR', 'PLN', 'JPY', 'usd', '']) for (const rate of [false, true])
        assert.equal(h.fmtMoney(value, currency, rate), expectedMoney(language, value, currency, rate), language + ' money');
    }
    for (const iso of ['2026-10-02T09:30:00Z', '2024-02-29T23:59:00Z'])
      assert.equal(h.fmtDateTime(iso), new Intl.DateTimeFormat(language, {dateStyle: 'medium', timeStyle: 'short'}).format(new Date(iso)), language + ' date');
  }
});
test('repeated dashboard formatting constructs ICU formatters once and switches locale immediately', () => {
  const h = helpers();
  function dashboard() { return [h.fmtInteger(1234), h.fmtCompactNumber(1234), h.fmtCompactNumber(12345),
    h.fmtMoney(0.005, 'USD'), h.fmtMoney(0.00001, 'USD'), h.fmtMoney(1.2, 'USD'), h.fmtMoney(1.2, 'USD', true),
    h.fmtDateTime('2026-10-02T09:30:00Z')]; }
  const english = dashboard(); const count = {...h.counts};
  for (let i = 0; i < 1000; i++) assert.deepEqual(dashboard(), english);
  assert.deepEqual(h.counts, count);
  h.ui.lang = 'pl'; const polish = dashboard(); assert.notDeepEqual(polish, english);
  assert.equal(h.counts.number, count.number * 2); assert.equal(h.counts.date, count.date * 2);
  h.ui.lang = 'en'; assert.deepEqual(dashboard(), english);
  assert.equal(h.counts.number, count.number * 3); assert.equal(h.counts.date, count.date * 3);
});
test('arbitrary currency churn stays bounded and evicted formats remain correct', () => {
  const h = helpers();
  for (let i = 0; i < 100; i++) {
    const currency = 'X' + String.fromCharCode(65 + Math.floor(i / 26)) + String.fromCharCode(65 + i % 26);
    assert.equal(h.fmtMoney(1.23, currency), expectedMoney('en', 1.23, currency));
    assert.ok(Object.keys(h.statsFormatterCache).length <= 32);
    assert.ok(h.statsFormatterKeys.length <= 32);
  }
  assert.equal(h.fmtMoney(1.23, 'USD'), expectedMoney('en', 1.23, 'USD'));
  h.ui.lang = 'de'; h.fmtInteger(1234);
  assert.equal(Object.keys(h.statsFormatterCache).length, 1, 'old locale formatters are released');
});
test('invalid values and unsupported formatting keep the original fallback and can retry', () => {
  const h = helpers();
  assert.equal(h.fmtInteger(NaN), '0'); assert.equal(h.fmtCompactNumber(Infinity), '0');
  assert.equal(h.fmtMoney(NaN, 'USD'), '—'); assert.equal(h.fmtMoney(Infinity, 'USD'), '—');
  assert.equal(h.fmtDateTime('not a date'), '—'); assert.equal(h.fmtDateTime(''), '—');
  assert.equal(h.fmtMoney(1.23, 'invalid currency'), 'invalid currency 1.23');
  assert.equal(h.fmtMoney(0.001, 'invalid currency', true), 'invalid currency 0.001000');
  let fail = true;
  const retry = helpers((...args) => {if (fail) throw Error('temporarily unavailable'); return new Intl.NumberFormat(...args);}, () => {throw Error('unavailable');});
  assert.equal(retry.fmtInteger(1234), '1234'); assert.equal(retry.fmtCompactNumber(12345), retry.fmtTok(12345));
  assert.equal(retry.fmtMoney(1.23, 'USD'), 'USD 1.23');
  const iso = '2026-10-02T09:30:00Z'; assert.equal(retry.fmtDateTime(iso), new Date(iso).toLocaleString());
  assert.equal(Object.keys(retry.statsFormatterCache).length, 0, 'failed constructors do not poison the cache');
  fail = false; assert.equal(retry.fmtInteger(1234), new Intl.NumberFormat('en', {maximumFractionDigits: 0}).format(1234));
});
