import {assert} from '../../test/ui/harness.js';
import {load, baseline} from '../../test/ui/tool-change-stats-fixture.js';

export default async function toolChangeStats() {
  assert.equal(typeof gc, 'function', 'Browser must expose actual GC');
  assert.ok(performance.memory && performance.memory.usedJSHeapSize > 0, 'Precise JS heap metrics unavailable');
  const {stats} = load();
  const text = ('+++ fixture.go\r\n--- fixture.go\r\n@@ context\r\n' +
    '+const fresh = "generated payload";\r\n-old generated payload\r\n context\r\n').repeat(40000);
  const expected = baseline('apply_patch', text);
  assert.deepEqual(JSON.parse(JSON.stringify(stats('apply_patch', text))), expected);
  async function collect() {
    // Weak references are kept alive for a job; give actual GC its own task.
    await new Promise(resolve => setTimeout(resolve, 0)); gc();
  }
  async function measure(fn) {
    for (let n = 0; n < 3; n++) fn('apply_patch', text);
    const runs = [];
    for (let n = 0; n < 7; n++) {
      await collect();
      const heapBefore = performance.memory.usedJSHeapSize;
      const start = performance.now();
      const result = fn('apply_patch', text);
      const ms = performance.now() - start;
      runs.push({ms, heapAfterCallDeltaBytes: performance.memory.usedJSHeapSize - heapBefore});
      assert.deepEqual(JSON.parse(JSON.stringify(result)), expected);
    }
    const median = key => runs.map(run => run[key]).sort((a,b) => a-b)[Math.floor(runs.length / 2)];
    return {medianMS: median('ms'), medianHeapAfterCallDeltaBytes: median('heapAfterCallDeltaBytes'), runs};
  }
  const before = await measure(baseline), after = await measure(stats);
  return {kind: 'Pure summary helper in an isolated native browser; JS heap deltas include temporary allocations before the next GC, not WebView2 RAM or FPS',
    sourceChars: text.length, lines: 240000, stats: expected, before, after};
}
