export async function run(name, options) {
  let result;
  if (name === 'tool-change-stats') {
    result = await (await import('./tool-change-stats.js')).default();
  } else if (name === 'transcript-dom') {
    result = await (await import('./transcript-dom.js')).default(options);
  } else {
    if (globalThis.sessionRuntimeReady) await sessionRuntimeReady;
    const check = (condition, message) => { if (!condition) throw new Error(message); };
    check(typeof addAssistantMsg === 'function' && typeof renderAssistant === 'function', 'Production test page did not initialize');
    check(Array.isArray(globalThis.__uiBenchErrors), 'Use an isolated fixture page with __uiBenchErrors error recording');
    if (name === 'transcript-performance') {
      const expanded = ui.thinkingExpanded;
      ui.thinkingExpanded = true; // Historical parser default, not a product change.
      try { result = (await import('./transcript-performance-checks.js')).default(options); }
      finally { ui.thinkingExpanded = expanded; }
    } else if (name === 'transcript-native') {
      // The original pasteable native fixture remains a classic browser script.
      // A local URL is used rather than a model request or a running GUI process.
      const source = await fetch(new URL('../transcript-native-fixture.js', import.meta.url));
      check(source.ok, 'Native transcript fixture source unavailable');
      (0, eval)(await source.text());
      const history = supercliTranscriptFixture.history();
      check(history.elements > 0 && history.messages === 20, 'Native history fixture lost messages/DOM: ' + JSON.stringify(history));
      check(history.retainedMessageWrappers === 0, 'Successful native history render retained raw page wrappers');
      const stream = await supercliTranscriptFixture.stream();
      check(stream.sourceChars > 0 && stream.chunks > 0 && stream.renders > 0 && stream.elements > 0, 'Native stream fixture did not render source');
      for (const key of ['elapsedMs', 'rendererMs', 'domCommitP50Ms', 'domCommitP95Ms', 'domCommitMaxMs']) {
        check(Number.isFinite(stream[key]) && stream[key] >= 0, 'Missing native stream metric: ' + key);
      }
      check(stream.domCommitP50Ms <= stream.domCommitP95Ms && stream.domCommitP95Ms <= stream.domCommitMaxMs, 'Native latency quantiles are inconsistent');
      supercliTranscriptFixture.clear();
      check(document.querySelector('#stream').childNodes.length === 0, 'Native clear retained transcript DOM');
      result = {kind: 'Synthetic native browser DOM-commit timings; not real FPS or WebView2 RAM', history, stream};
    } else throw new Error('Unknown benchmark case: ' + name);
    check(__uiBenchErrors.length === 0, 'Browser errors: ' + JSON.stringify(__uiBenchErrors));
  }
  return {runtime: navigator.userAgent, ...result};
}
