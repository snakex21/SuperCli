"use strict";

/* ═══ keyboard ═══ */

function comboOf(e) {
  var parts = [];
  if (e.ctrlKey) parts.push("Ctrl");
  if (e.altKey) parts.push("Alt");
  if (e.shiftKey) parts.push("Shift");
  var k = e.key.length === 1 ? e.key.toUpperCase() : e.key;
  if (k === " ") k = "Space";
  parts.push(k);
  return parts.join("+");
}
document.addEventListener("keydown", function (e) {
  if (e.key === "Escape") {
    if (!overlay.hidden) {
      if (panelTextField(e.target)) {
        e.preventDefault();
        if (document.activeElement === e.target) e.target.blur();
        panelDialog.focus({ preventScroll: true });
        return;
      }
      closePanel();
      return;
    }
    if (!$("#reasoning-menu").hidden) { toggleReasoningMenu(false); $("#reasoning-btn").focus(); return; }
    if (!palette.hidden) { togglePalette(false); return; }
    if (sitePreviewController) { closeSitePreview(); return; }
    return;
  }
  var combo = comboOf(e);
  var typing = /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName);
  if (combo === ui.keybinds.panel) { e.preventDefault(); overlay.hidden ? openPanel() : closePanel(); return; }
  if (combo === ui.keybinds.sidebar) { e.preventDefault(); toggleSidebar(); return; }
  if (typing) return;
  if (combo === ui.keybinds.focus) { e.preventDefault(); promptEl.focus(); return; }
  if (combo === ui.keybinds.thinking) {
    e.preventDefault();
    var blocks = $$(".think-block");
    var anyOpen = blocks.some(function (b) { return b.open; });
    saveDisclosurePreference("thinking", !anyOpen);
    blocks.forEach(function (b) { b.open = !anyOpen; });
    return;
  }
  if (combo === ui.keybinds.tools) {
    e.preventDefault();
    var rows = $$(".tool-row");
    var anyOpenT = rows.some(function (r) { return r.open; });
    saveDisclosurePreference("tools", !anyOpenT);
    rows.forEach(function (r) { r.open = !anyOpenT; });
  }
});

/* ═══ init ═══ */

(async function init() {
  var settingsSnapshot = await loadUI();
  applyI18n();
  // The conversation list does not wait for the health check or the draft
  // store; only reopening the last conversation needs the drafts loaded.
  var healthReady = checkHealth();
  var sessionsReady = loadSessions();
  await composerDraftStore.load(settingsSnapshot);
  sessionsReady.then(resumeLastSessionOnStart);
  var h = await healthReady;
  if (h) {
    if (selectedModelID(h.model)) loadReasoning();
    loadModels();
  }
  loadProjects();
	loadPromptQueue();
	loadWorkers();
  renderStats();
  if (typeof initMemoryIndicator === "function") initMemoryIndicator();
  if (typeof startSubscriptionUsageAutoRefresh === "function") startSubscriptionUsageAutoRefresh();
  setInterval(checkHealth, 30000);
	// The open Tasks tab refreshes every 2.5 s while a worker runs, else
	// every 15 s; never while the page is hidden.
	var workersTick = 0;
	setInterval(function () {
	  var tasksTab = $("#side-tabs button[data-tab='tasks']");
	  workersTick++;
	  if (document.hidden || !tasksTab || !tasksTab.classList.contains("active")) return;
	  if ((typeof workersListRunning === "undefined" || workersListRunning) || workersTick % 6 === 0) loadWorkers();
	}, 2500);
  promptEl.focus();
})();
