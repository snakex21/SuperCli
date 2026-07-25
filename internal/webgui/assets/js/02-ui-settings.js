"use strict";

/* ═══ UI settings (server-persisted blob) ═══ */

var ui = {
  theme: "dark", lang: /^pl(?:-|_|$)/i.test((navigator.languages && navigator.languages[0]) || navigator.language || "") ? "pl" : "en", uiFont: "system", codeFont: "system", uiScale: "auto",
  notifySound: false, notifyDesktop: false, appBadge: true, sidebarHidden: true, rememberSessionRuntime: true,
  keybinds: { panel: "Ctrl+,", sidebar: "Ctrl+B", focus: "/", thinking: "Shift+T", tools: "Shift+E" },
};
var uiBlob = {}; // last blob seen from the server (read-only mirror)
var sidebarCompactMQ = window.matchMedia("(max-width: 1180px)");

// Real, commonly-installed font choices. Every option must visibly
// change rendering on a stock Windows box — a knob that does nothing
// breaks the "it just works" contract.
var UI_FONTS = {
  system: '-apple-system, "Segoe UI", system-ui, sans-serif',
  segoe: '"Segoe UI", system-ui, sans-serif',
  arial: 'Arial, Helvetica, sans-serif',
  tahoma: 'Tahoma, Geneva, sans-serif',
  georgia: 'Georgia, "Times New Roman", serif',
  aptos: 'Aptos, Calibri, "Segoe UI", sans-serif',
  verdana: 'Verdana, Geneva, sans-serif',
  trebuchet: '"Trebuchet MS", Arial, sans-serif',
};
var CODE_FONTS = {
  system: 'ui-monospace, "Cascadia Mono", Consolas, monospace',
  cascadia: '"Cascadia Mono", "Cascadia Code", Consolas, monospace',
  consolas: 'Consolas, "Lucida Console", monospace',
  courier: '"Courier New", Courier, monospace',
  lucida: '"Lucida Console", Consolas, monospace',
  jetbrains: '"JetBrains Mono", "Cascadia Mono", Consolas, monospace',
  fira: '"Fira Code", "Cascadia Mono", Consolas, monospace',
};

var pushTimer = null;
// saveUI persists ONLY the keys this client owns (ui.*). The server
// merges them into the existing blob, so server-side keys (last model,
// model cache) can never be wiped by a stale browser state.
function saveUI() {
  var patch = {};
  Object.keys(ui).forEach(function (k) { patch["ui." + k] = ui[k]; });
  clearTimeout(pushTimer);
  pushTimer = setTimeout(function () {
    jpost("/api/settings", patch).catch(function () {});
  }, 300);
  try { localStorage.setItem("supercli-ui", JSON.stringify(ui)); } catch (e) {}
}
function saveBlobKey(key, value) {
  var patch = {};
  patch[key] = value;
  jpost("/api/settings", patch).catch(function () {});
}
function resolvedUIScale() {
  var scale = ({compact:.9, normal:1, large:1.1, xlarge:1.25, huge:1.4})[ui.uiScale];
  if (ui.uiScale === "auto" || !scale) {
    scale = 1;
    if (window.innerWidth >= 3000 && window.innerHeight >= 1500) scale = 1.2;
    else if (window.innerWidth >= 2300 && window.innerHeight >= 1200) scale = 1.12;
    else if (window.innerWidth >= 1800 && window.innerHeight >= 950) scale = 1.06;
  }
  return scale;
}
// A window that has not been laid out yet (a desktop window still being
// created, a hidden or minimized one) reports a 0-sized viewport.
var viewportMeasurable = function () { return window.innerWidth > 0 && window.innerHeight > 0; };
// Poll with a timer, not requestAnimationFrame: an unrendered window (the
// very case we are waiting out) never runs animation frames.
var viewportRetryTimer = 0;
var viewportRetryDelay = 0;
function scheduleViewportRemeasure() {
  if (viewportRetryTimer) return;
  viewportRetryDelay = Math.min(1000, (viewportRetryDelay || 25) * 2);
  viewportRetryTimer = setTimeout(function () {
    viewportRetryTimer = 0;
    applyViewportScale();
  }, viewportRetryDelay);
}
function applyViewportScale() {
  var root = document.documentElement;
  root.dataset.uiScale = ui.uiScale || "auto";
  // Pinning the root box to a 0-sized measurement freezes the whole app at
  // 1px: .stage ends up 0px tall, so the transcript can never be scrolled,
  // and `body { overflow: hidden }` leaves no page scrollbar to fall back
  // on. Stay on the stylesheet's `html, body { height: 100% }` until the
  // window can actually be measured — `resize` is not guaranteed to fire
  // for the first real layout.
  if (!viewportMeasurable()) {
    root.style.removeProperty("zoom");
    root.style.removeProperty("width");
    root.style.removeProperty("height");
    root.style.setProperty("--app-viewport-width", "100vw");
    root.style.setProperty("--app-viewport-height", "100vh");
    root.style.setProperty("--ui-scale", 1);
    scheduleViewportRemeasure();
    return;
  }
  viewportRetryDelay = 0;
  var scale = resolvedUIScale();
  var width = Math.max(1, window.innerWidth / scale);
  var height = Math.max(1, window.innerHeight / scale);
  root.style.zoom = scale;
  root.style.width = width + "px";
  root.style.height = height + "px";
  root.style.setProperty("--app-viewport-width", width + "px");
  root.style.setProperty("--app-viewport-height", height + "px");
  root.style.setProperty("--ui-scale", scale);
  root.classList.toggle("ui-scale-compact", width <= 1180);
  root.classList.toggle("ui-scale-narrow", width <= 900);
  root.classList.toggle("ui-scale-mobile", width <= 600);
}
function applyUI() {
  document.documentElement.dataset.theme = ui.theme || "dark";
  document.documentElement.style.setProperty("--sans", UI_FONTS[ui.uiFont] || UI_FONTS.system);
  document.documentElement.style.setProperty("--mono", CODE_FONTS[ui.codeFont] || CODE_FONTS.system);
  applyViewportScale();
  $("#shell").classList.toggle("sidebar-hidden", !!ui.sidebarHidden);
  applyI18n();
  if (typeof promptQueue !== "undefined") renderPromptQueue();
}
window.addEventListener("resize", function () {
  applyViewportScale();
});
// Second signal: some embedders (desktop window, restored-from-minimized)
// resize the page without delivering a window `resize` event.
if (window.visualViewport) {
  window.visualViewport.addEventListener("resize", function () {
    applyViewportScale();
  });
}
async function loadUI() {
  try { Object.assign(ui, JSON.parse(localStorage.getItem("supercli-ui") || "{}")); } catch (e) {}
  applyUI();
  try {
    var got = await j("/api/settings");
    if (got && got.settings) {
      uiBlob = got.settings;
      Object.keys(ui).forEach(function (k) {
        if (uiBlob["ui." + k] !== undefined) ui[k] = uiBlob["ui." + k];
      });
      if (Array.isArray(uiBlob["supercli-model-cache"])) modelCache = uiBlob["supercli-model-cache"];
      if (Array.isArray(uiBlob[sentAttachmentStorageKey])) sentAttachmentIndex = uiBlob[sentAttachmentStorageKey];
    }
  } catch (e) {}
  // A persisted desktop-open inspector must not cover the conversation when
  // the app is reopened on a smaller monitor or in a narrow window.
  if (sidebarCompactMQ.matches || document.documentElement.classList.contains("ui-scale-compact")) ui.sidebarHidden = true;
  applyUI();
}

