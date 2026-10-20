(() => {
  'use strict';
  // Native host injection only. Do not affect embedded previews, frames or
  // navigation away from the app's document, even on the same local origin.
  const owned = new URL(__SUPERCLI_OWNED_URL__);
  if (window !== window.top || location.origin !== owned.origin || location.pathname !== owned.pathname) return;
  const mac = /Mac|iPhone|iPad|iPod/i.test(navigator.platform);
  window.addEventListener('keydown', function (event) {
    if (event.isComposing || event.keyCode === 229 || event.key === 'Process' ||
        (event.getModifierState && event.getModifierState('AltGraph'))) return;
    const key = event.key.toLowerCase();
    const primary = mac ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey;
    let protect = false;
    if (!event.altKey && !event.metaKey && (key === 'f5' ||
        (key === 'f12' && !event.ctrlKey && !event.shiftKey))) protect = true;
    if (primary) {
      if (!event.altKey) {
        protect = protect || key === 'r' || (!event.shiftKey && ['p', 's', 'u'].includes(key)) ||
          (event.shiftKey && ['i', 'j', 'c'].includes(key));
      } else if (mac && !event.shiftKey) {
        // WebKit/Chromium on macOS use Cmd+Option for source/DevTools.
        protect = protect || ['u', 'i', 'j', 'c'].includes(key);
      }
    }
    // Capture on window, before queue-editor document capture and propagation
    // barriers. Cancel only the browser default, never the app's key handlers.
    if (protect) event.preventDefault();
  }, true);
})();
