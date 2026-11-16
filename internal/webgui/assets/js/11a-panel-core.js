"use strict";

/* ═══ control panel ═══ */

var overlay = $("#panel-overlay"), panelContent = $("#panel-content");
var currentSection = "settings";
var sections = {}; // name -> render function

/* The panel is a modal dialog (role=dialog aria-modal in index.html): opening
   moves focus into it, Tab cycles inside it, and closing hands focus back to
   whatever opened it. */
var panelDialog = overlay.querySelector(".panel"), panelOpener = null;
function openPanel(section) {
  var opening = overlay.hidden;
  if (opening) {
    var active = document.activeElement;
    panelOpener = active && active !== document.body && !overlay.contains(active) ? active : null;
  }
  overlay.hidden = false;
  showSection(section || currentSection);
  if (opening) {
    var start = $("#panel-nav button.active") || panelDialog;
    start.focus({ preventScroll: true });
  }
}
function closePanel() {
  if (overlay.hidden) return;
  var hadFocus = overlay.contains(document.activeElement) || document.activeElement === document.body;
  overlay.hidden = true;
  var opener = panelOpener;
  panelOpener = null;
  if (!hadFocus) return;
  if (opener && opener.isConnected && opener.getClientRects().length && !opener.disabled) opener.focus({ preventScroll: true });
  else if (typeof promptEl !== "undefined" && promptEl) promptEl.focus({ preventScroll: true });
}
function panelFocusables() {
  return Array.prototype.filter.call(panelDialog.querySelectorAll(
    'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])'
  ), function (node) { return node.getClientRects().length > 0; });
}
overlay.addEventListener("keydown", function (e) {
  if (e.key !== "Tab" || e.defaultPrevented) return;
  var items = panelFocusables();
  if (!items.length) { e.preventDefault(); panelDialog.focus(); return; }
  var first = items[0], last = items[items.length - 1], active = document.activeElement;
  if (e.shiftKey && (active === first || active === panelDialog)) { e.preventDefault(); last.focus(); }
  else if (!e.shiftKey && active === last) { e.preventDefault(); first.focus(); }
});
// Escape in a text field inside the panel cancels that edit only (TUI parity:
// the first Esc cancels the edit, the next one closes the panel). Field
// handlers that revert their value run first; focus then stays in the dialog.
function panelTextField(node) {
  if (!node || !overlay.contains(node)) return false;
  if (node.isContentEditable || node.tagName === "TEXTAREA") return true;
  return node.tagName === "INPUT" && !/^(checkbox|radio|button|submit|reset|range|color|file|image)$/i.test(node.type);
}
$("#open-panel").addEventListener("click", function () { openPanel(); });
$("#panel-close").addEventListener("click", closePanel);
overlay.addEventListener("click", function (e) { if (e.target === overlay) closePanel(); });
$("#panel-nav").addEventListener("click", function (e) {
  var b = e.target.closest("button[data-sec]");
  if (b) showSection(b.dataset.sec);
});
function showSection(name) {
  currentSection = name;
  $$("#panel-nav button[data-sec]").forEach(function (b) {
    b.classList.toggle("active", b.dataset.sec === name);
  });
  $("#panel-title").textContent = t(name === "skills" ? "skills.title" : "panel." + name);
  panelContent.innerHTML = '<div class="note">' + escHtml(t("common.loading")) + "</div>";
  var rendered = (sections[name] || function () {})();
  // Async sections share one surface. If an older request finishes after the
  // user switched tabs, redraw the current tab so stale content cannot win.
  Promise.resolve(rendered).finally(function () {
    if (currentSection !== name) showSection(currentSection);
  });
}

/* ── Live data refresh ──
   Server "data_changed" sync frames (agent tools, other windows), a focus
   return, and a light poll for writes made by another process sharing the
   data folder (TUI) all funnel here. Refreshes are debounced per kind, only
   touch views that are open, skip identical data and preserve typed input,
   focus and scroll. */
var liveDataTimers = Object.create(null);
var liveDataDebounceMS = 250, liveDataPollMS = 5000;
function sideGoalTabOpen() {
  var tab = $("#side-tab-goal");
  return !!(tab && tab.classList.contains("active"));
}
function liveViewOpen(kind) {
  if (kind === "goal") return (!overlay.hidden && currentSection === "goal") || sideGoalTabOpen();
  if (kind === "memory") return !overlay.hidden && currentSection === "memory";
  return false;
}
function panelLiveRefresh(kind) {
  if (!kind) return;
  clearTimeout(liveDataTimers[kind]);
  liveDataTimers[kind] = setTimeout(function () {
    delete liveDataTimers[kind];
    if (document.hidden || !liveViewOpen(kind)) return;
    if (kind === "goal") {
      if (!overlay.hidden && currentSection === "goal" && sections.goal) sections.goal({ live: true });
      if (sideGoalTabOpen() && typeof loadSideGoal === "function") loadSideGoal(true);
    } else if (kind === "memory" && sections.memory) {
      sections.memory({ live: true });
    }
  }, liveDataDebounceMS);
}
function liveFieldKeys(host) {
  var seen = Object.create(null);
  return Array.prototype.map.call(host.querySelectorAll("input, textarea"), function (field) {
    var base = field.tagName + "|" + (field.name || field.placeholder || field.getAttribute("aria-label") || "");
    seen[base] = (seen[base] || 0) + 1;
    return { field: field, key: base + "|" + seen[base] };
  });
}
// Re-render panelContent without losing what the user typed, focus or scroll.
function preservePanelState(render) {
  var host = panelContent, scroll = host.scrollTop, saved = Object.create(null), focusKey = "", selection = null;
  liveFieldKeys(host).forEach(function (item) {
    var f = item.field;
    if (f.type !== "checkbox" && f.type !== "radio" && f.value !== f.defaultValue) saved[item.key] = f.value;
    if (document.activeElement === f) {
      focusKey = item.key;
      try { selection = [f.selectionStart, f.selectionEnd]; } catch (e) { selection = null; }
    }
  });
  render();
  liveFieldKeys(host).forEach(function (item) {
    if (item.key in saved) item.field.value = saved[item.key];
    if (item.key === focusKey) {
      item.field.focus({ preventScroll: true });
      if (selection) try { item.field.setSelectionRange(selection[0], selection[1]); } catch (e) {}
    }
  });
  host.scrollTop = scroll;
}
(function liveDataPoll() {
  setTimeout(function () {
    if (!document.hidden && liveViewOpen("goal")) panelLiveRefresh("goal");
    liveDataPoll();
  }, liveDataPollMS);
})();
window.addEventListener("focus", function () { panelLiveRefresh("goal"); panelLiveRefresh("memory"); });
document.addEventListener("visibilitychange", function () {
  if (!document.hidden) { panelLiveRefresh("goal"); panelLiveRefresh("memory"); }
});

/* ── Settings (config.toml knobs — TUI /settings parity) ── */
