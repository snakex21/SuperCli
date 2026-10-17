'use strict';
import {assert, test, readSource, sourcePath, testDir, runScript} from './harness.js';
const root = sourcePath(testDir, '../..');
import transcriptDOM from '../../scripts/transcript-dom-fixture.js';

for (const variant of ['not-loaded', 'real-stats-module', 'nonfunction']) {
  test('production done handler completes with optional stats hook: ' + variant, () => {
    // The fixture executes the production handleEvent body. Do not invent a
    // rememberStatsLiveSpeed stub: absence must remain an actual missing symbol.
    const c = transcriptDOM(root);
    const seen = [];
    Object.assign(c, {
      activeSessionID: 'owned-session', activeModelID: 'selected-model',
      runStart: Date.now() - 2000, runToolCount: 3, lastTurn: null,
      pendingImmediate: null, pauseQueue: false, promptQueue: [],
      addFileChanges(value) {seen.push(['changes', value]);},
      addTurnMeta(ev, elapsed) {seen.push(['meta', ev, elapsed]);},
      setRunState(state) {seen.push(['state', state]);},
      notifyDone(elapsed) {seen.push(['notify', elapsed]);},
      fmtDuration: String,
    });
    if (variant === 'real-stats-module') {
      runScript(readSource(sourcePath(root, 'internal/webgui/assets/js/07-stats.js'), 'utf8'), c);
    } else if (variant === 'nonfunction') {
      c.rememberStatsLiveSpeed = null;
    } else {
      assert.equal('rememberStatsLiveSpeed' in c, false);
    }
    const ev = {type: 'done', model: 'completed-model', tok_out: 80, generation_tps: 40, file_changes: []};
    assert.equal(c.handleEvent(ev, null), null);
    assert.equal(c.lastTurn.ev, ev);
    assert.equal(c.lastTurn.sessionID, 'owned-session');
    assert.equal(c.lastTurn.tools, 3);
    assert.deepEqual(seen.map(item => item[0]), ['changes', 'meta', 'state', 'notify']);
    assert.equal(seen[2][1], 'idle', 'stats presence cannot skip normal completion');
    if (variant === 'real-stats-module') {
      assert.equal(c.statsGenerationState.session, 'owned-session');
      assert.equal(c.statsGenerationState.live.rate, 40);
      assert.equal(c.statsGenerationState.live.model, 'completed-model');
    }
  });
}
