'use strict';
const assert = require('node:assert/strict');
const {performance} = require('node:perf_hooks');
const {load, baseline} = require('../test/ui/tool-change-stats-fixture.cjs');
if (typeof global.gc !== 'function') throw Error('Run with node --expose-gc');
const {stats} = load();
const text = ('+++ fixture.go\r\n--- fixture.go\r\n@@ context\r\n' +
  '+const fresh = "generated payload";\r\n-old generated payload\r\n context\r\n').repeat(40000);
const expected = baseline('apply_patch', text);
assert.deepEqual(JSON.parse(JSON.stringify(stats('apply_patch', text))), expected);
function measure(fn) {
  for (let n = 0; n < 3; n++) fn('apply_patch', text);
  const runs = [];
  for (let n = 0; n < 7; n++) {
    global.gc();
    const heapBefore = process.memoryUsage().heapUsed;
    const start = performance.now();
    const result = fn('apply_patch', text);
    runs.push({ms: performance.now() - start, heapAfterCallDeltaBytes: process.memoryUsage().heapUsed - heapBefore});
    assert.deepEqual(JSON.parse(JSON.stringify(result)), expected);
  }
  const median = key => runs.map(run => run[key]).sort((a,b) => a-b)[Math.floor(runs.length / 2)];
  return {medianMS: median('ms'), medianHeapAfterCallDeltaBytes: median('heapAfterCallDeltaBytes'), runs};
}
const before = measure(baseline), after = measure(stats);
console.log(JSON.stringify({runtime: process.version,
  kind: 'Pure Node summary helper; heap deltas include temporary allocations remaining before the next GC, not WebView2 RAM or FPS',
  sourceChars: text.length, lines: 240000, stats: expected, before, after}, null, 2));
