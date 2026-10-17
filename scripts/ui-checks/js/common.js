export function check(condition, name, detail) {
  if (!condition) throw Error(name + (detail === undefined ? '' : ': ' + JSON.stringify(detail)));
  window.__uiCheckResults.push(name);
}
export function equal(actual, expected, name) {
  check(Object.is(actual, expected), name, {actual, expected});
}
export function deepEqual(actual, expected, name) {
  check(JSON.stringify(actual) === JSON.stringify(expected), name, {actual, expected});
}
export async function waitFor(predicate, name, timeout = 10000) {
  const started = performance.now();
  for (;;) {
    if (predicate()) return;
    if (performance.now() - started > timeout) throw Error('Timed out: ' + name);
    await new Promise(resolve => setTimeout(resolve, 10));
  }
}
export async function ready() {
  await window.__uiCheckInit;
  await sessionRuntimeReady;
}
export async function frame() {
  // A background headless target can suspend rAF despite disabled throttling.
  // Geometry reads force native layout; the timer bounds this rendering barrier
  // without activating a tab/window or changing application animation APIs.
  await Promise.race([
    new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))),
    new Promise(resolve => setTimeout(resolve, 100))
  ]);
  document.documentElement.getBoundingClientRect();
}
export function visible(node) {
  if (!node) return false;
  const rect = node.getBoundingClientRect();
  return rect.width > 0 && rect.height > 0 && getComputedStyle(node).visibility !== 'hidden';
}
export function noErrors() { deepEqual(window.__uiCheckErrors, [], 'browser JavaScript errors'); }
export async function fixtureState() { const response = await fetch('/__ui_checks/state'); if (!response.ok) throw Error('fixture state'); return response.json(); }
export function deferred() { let resolve; const promise = new Promise(yes => { resolve = yes; }); return {promise, resolve}; }
export function submit(text) {
  document.querySelector('#prompt').value = text;
  document.querySelector('#composer').dispatchEvent(new Event('submit', {bubbles:true,cancelable:true}));
}
