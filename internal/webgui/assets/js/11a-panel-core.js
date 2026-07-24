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
  $("#panel-title").textContent = t("panel." + name);
  panelContent.innerHTML = '<div class="note">' + escHtml(t("common.loading")) + "</div>";
  var rendered = (sections[name] || function () {})();
  // Async sections share one surface. If an older request finishes after the
  // user switched tabs, redraw the current tab so stale content cannot win.
  Promise.resolve(rendered).finally(function () {
    if (currentSection !== name) showSection(currentSection);
  });
}

/* ── Settings (config.toml knobs — TUI /settings parity) ── */
