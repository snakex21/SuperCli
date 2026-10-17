// Installed before application scripts, in the disposable test origin only.
window.__uiCheckResults = [];
window.__uiCheckMetrics = {};
window.__uiCheckErrors = [];
window.addEventListener('error', event => {
  if (event.error || event.message) window.__uiCheckErrors.push(event.error?.stack || event.message);
});
window.addEventListener('unhandledrejection', event => {
  window.__uiCheckErrors.push(event.reason?.stack || String(event.reason));
});
