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
    if (!overlay.hidden) { closePanel(); return; }
    if (!$("#reasoning-menu").hidden) { toggleReasoningMenu(false); $("#reasoning-btn").focus(); return; }
    if (!palette.hidden) { togglePalette(false); return; }
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
    blocks.forEach(function (b) { b.open = !anyOpen; });
    return;
  }
  if (combo === ui.keybinds.tools) {
    e.preventDefault();
    var rows = $$(".tool-row");
    var anyOpenT = rows.some(function (r) { return r.open; });
    rows.forEach(function (r) { r.open = !anyOpenT; });
  }
});

/* ═══ init ═══ */

(async function init() {
  await loadUI();
  applyI18n();
  var h = await checkHealth();
  await composerDraftStore.load();
  if (h) {
    if (h.model) loadReasoning();
    loadModels();
  }
  loadSessions();
  loadProjects();
	loadPromptQueue();
	loadWorkers();
  renderStats();
  setInterval(checkHealth, 30000);
	setInterval(function () {
	  var tasksTab = $("#side-tabs button[data-tab='tasks']");
	  if (tasksTab && tasksTab.classList.contains("active")) loadWorkers();
	}, 2500);
  promptEl.focus();
})();
