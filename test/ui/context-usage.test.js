'use strict';
import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript, parseHTML} from './harness.js';
const assets = sourcePath(testDir, '../../internal/webgui/assets');
const read = name => readSource(sourcePath(assets, name), 'utf8');

test('context usage remains optional, bounded and explicit about measurement scope', () => {
  const {document} = parseHTML('<html><body></body></html>');
  for (const lang of ['en', 'pl']) {
    const catalog = JSON.parse(read('locales/' + lang + '.json'));
    const w = createSandbox({document, window: {}, activeModelID: '', sections: {},
      t: key => catalog[key] || key, registerLanguageRefresh() {},
      setTimeout() {throw Error('unexpected timer');}, setInterval() {throw Error('unexpected poll');},
    });
    runScript(read('js/00-helpers.js'), w);
    runScript(read('js/07-stats.js'), w);
    runScript(read('js/11g-panel-goal-usage.js'), w);
    assert.equal(w.normalizeStats({}).telemetry.contextUsage, null);
    const raw = {measured_calls: 3, logical_calls: 2, traced_calls: 3, text_bytes: 100, known_bytes: 80,
      sources: {human: {added_items: 1, added_bytes: 8, exposures: 3, text_bytes: 24, calls: 3}, unknown: {text_bytes: 20}, PRIVATE_SECRET: {text_bytes: 999}},
      reasons: {user_input: 1, tool_results: 1, transport_retry: 1, PRIVATE_SECRET: 200}, schema_bytes: 44, tool_definition_exposures: 6};
    const context = w.normalizeStats({telemetry: {context_usage: raw}}).telemetry.contextUsage;
    const panel = w.renderContextUsageSources(context);
    assert.equal(panel.tagName.toLowerCase(), 'details');
    assert.ok(panel.textContent.includes(catalog['telemetry.contextScope']));
    assert.ok(panel.textContent.includes('80%'));
    assert.ok(panel.textContent.includes('2 / 3'));
    assert.ok(!panel.textContent.includes('PRIVATE_SECRET'));
    assert.equal(panel.querySelectorAll('script').length, 0);
    assert.equal(w.renderContextUsageSources(null), null);
  }
});
