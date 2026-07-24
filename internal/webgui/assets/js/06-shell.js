"use strict";

/* ═══ notifications ═══ */

function notifyDone(elapsed) {
  if (!appFocused || document.hidden) { unreadDone++; updateAppBadge(); }
  if (ui.notifySound) {
    try {
      var ctx = new (window.AudioContext || window.webkitAudioContext)();
      var osc = ctx.createOscillator(), gain = ctx.createGain();
      osc.connect(gain); gain.connect(ctx.destination);
      osc.frequency.value = 660; gain.gain.value = 0.04;
      osc.start(); osc.stop(ctx.currentTime + 0.12);
    } catch (e) {}
  }
  if (ui.notifyDesktop && (!appFocused || document.hidden) && window.Notification && Notification.permission === "granted") {
    try { new Notification("SuperCli", { body: t("run.done") + " · " + fmtDuration(elapsed) }); } catch (e) {}
  }
}
window.addEventListener("blur", function () { appFocused = false; });
window.addEventListener("focus", function () { appFocused = true; unreadDone = 0; updateAppBadge(); });
document.addEventListener("visibilitychange", function () {
  appFocused = !document.hidden && document.hasFocus();
  if (appFocused) { unreadDone = 0; updateAppBadge(); }
});

/* ═══ health / status ═══ */

var activeModelID = "", activeProviderID = "";
var activeWorkspacePath = "";
function workspaceDisplayName(path) {
  var clean = String(path || "").replace(/[\\/]+$/, "");
  return clean.split(/[\\/]/).pop() || clean;
}
$("#open-project-folder").addEventListener("click", function () {
  if (activeWorkspacePath) openWorkspaceFolder(activeWorkspacePath);
});
async function checkHealth() {
  var dot = $("#status-dot");
  try {
    var h = await j("/api/health");
    dot.className = "status-dot ok" + (streaming ? " busy" : "");
    dot.title = t("status.connected") + " · " + (h.model || "");
    activeWorkspacePath = h.home || "";
    $("#workspace").textContent = workspaceDisplayName(activeWorkspacePath);
    $("#workspace").title = activeWorkspacePath;
    $("#open-project-folder").disabled = !activeWorkspacePath;
    if (h.model) {
      var modelChanged = h.model !== activeModelID;
      activeModelID = h.model;
      $("#model-name").textContent = h.model;
      if (modelChanged) loadReasoning();
    }
    return h;
  } catch (e) {
    dot.className = "status-dot err";
    dot.title = t("status.offline");
    $("#open-project-folder").disabled = !activeWorkspacePath;
    return null;
  }
}

/* ═══ side panel: tabs ═══ */

function activateSideTab(button) {
  if (!button) return;
  $$("#side-tabs button[data-tab]").forEach(function (tab) {
    var selected = tab === button;
    tab.classList.toggle("active", selected);
    tab.setAttribute("aria-selected", selected ? "true" : "false");
    tab.tabIndex = selected ? 0 : -1;
  });
  $$(".side-pane").forEach(function (pane) {
    var selected = pane.id === "tab-" + button.dataset.tab;
    pane.classList.toggle("active", selected);
    pane.hidden = !selected;
  });
}
(function initSideTabs() {
  var tabs = $("#side-tabs");
  tabs.setAttribute("role", "tablist");
  $$("#side-tabs button[data-tab]").forEach(function (button) {
    var pane = $("#tab-" + button.dataset.tab);
    button.id = "side-tab-" + button.dataset.tab;
    button.setAttribute("role", "tab");
    button.setAttribute("aria-controls", pane.id);
    pane.setAttribute("role", "tabpanel");
    pane.setAttribute("aria-labelledby", button.id);
  });
  activateSideTab($("#side-tabs button.active") || $("#side-tabs button[data-tab]"));
})();
$("#side-tabs").addEventListener("click", function (e) {
  var b = e.target.closest("button[data-tab]");
  if (!b) return;
  activateSideTab(b);
  if (b.dataset.tab === "stats") renderStats();
  if (b.dataset.tab === "sessions") loadSessions();
  if (b.dataset.tab === "projects") loadProjects();
	if (b.dataset.tab === "tasks") { loadPromptQueue(); loadWorkers(); }
  if (b.dataset.tab === "goal") loadSideGoal();
});
$("#side-tabs").addEventListener("keydown", function (e) {
  if (["ArrowLeft", "ArrowRight", "Home", "End"].indexOf(e.key) < 0) return;
  var tabs = $$("#side-tabs button[data-tab]");
  var index = tabs.indexOf(document.activeElement);
  if (index < 0) return;
  e.preventDefault();
  if (e.key === "Home") index = 0;
  else if (e.key === "End") index = tabs.length - 1;
  else index = (index + (e.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length;
  tabs[index].focus();
  tabs[index].click();
});
function toggleSidebar() {
  ui.sidebarHidden = !ui.sidebarHidden;
  applyUI();
  saveUI();
  if (!ui.sidebarHidden) renderStats();
}
function closeSidebar() {
  if (ui.sidebarHidden) return;
  ui.sidebarHidden = true;
  applyUI();
  saveUI();
}
$("#toggle-sidebar").addEventListener("click", toggleSidebar);
$("#close-sidebar").addEventListener("click", closeSidebar);
$("#sidebar-backdrop").addEventListener("click", closeSidebar);
sidebarCompactMQ.addEventListener("change", function (event) {
  if (event.matches) closeSidebar();
});

