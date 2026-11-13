"use strict";

/* ═══ control panel ═══ */

var overlay = $("#panel-overlay"), panelContent = $("#panel-content");
var currentSection = "settings";
var sections = {}; // name -> render function

function openPanel(section) {
  overlay.hidden = false;
  showSection(section || currentSection);
}
function closePanel() { overlay.hidden = true; }
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
