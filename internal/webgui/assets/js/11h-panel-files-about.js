"use strict";

sections.files = function () { renderFiles(filesCwd); };

async function renderFiles(dir) {
  var got;
  try { got = await j("/api/files" + (dir ? "?dir=" + encodeURIComponent(dir) : "")); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  filesCwd = got.dir;
  panelContent.innerHTML = "";
  var g = el("div", "group");
  var lbl = i18nEl("div", "g-label", "panel.files");
  var up = el("button", "g-act", "↑ " + t("files.up"));
  up.addEventListener("click", function () {
    var parent = filesCwd.replace(/[\\/][^\\/]+$/, "");
    renderFiles(parent);
  });
  lbl.appendChild(up);
  g.appendChild(lbl);
  g.appendChild(el("div", "file-path", got.dir));
  (got.files || []).sort(function (a, b) {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
    return a.name.localeCompare(b.name);
  }).forEach(function (f) {
    var b = el("button", "file-row");
    b.type = "button";
    b.appendChild(el("span", "fi", f.is_dir ? "▸" : "·"));
    b.appendChild(el("span", "", f.name));
    if (!f.is_dir) b.appendChild(el("span", "fs", fmtSize(f.size)));
    b.addEventListener("click", function () {
      var sep = filesCwd.indexOf("\\") >= 0 ? "\\" : "/";
      var p = filesCwd + sep + f.name;
      if (f.is_dir) renderFiles(p);
      else openEditor(p);
    });
    g.appendChild(b);
  });
  panelContent.appendChild(g);
}

async function openEditor(path) {
  var got;
  try { got = await j("/api/file/read?path=" + encodeURIComponent(path)); } catch (e) {
    toast(e.message);
    return;
  }
  panelContent.innerHTML = "";
  var g = el("div", "group");
  var lbl = el("div", "g-label", path.split(/[\\/]/).pop());
  var back = el("button", "g-act", "‹ " + t("common.back"));
  back.addEventListener("click", function () { renderFiles(filesCwd); });
  lbl.appendChild(back);
  g.appendChild(lbl);
  g.appendChild(el("div", "file-path", got.path));
  var ta = el("textarea", "editor-area");
  ta.spellcheck = false;
  ta.value = got.content;
  g.appendChild(ta);
  var save = i18nEl("button", "btn primary", "files.save");
  save.style.marginTop = "8px";
  var status = el("span", "note");
  status.style.marginLeft = "10px";
  save.addEventListener("click", async function () {
    try {
      await jpost("/api/file/write", { path: got.path, content: ta.value });
      status.textContent = t("files.saved");
    } catch (e) { status.textContent = e.message; }
  });
  g.appendChild(save);
  g.appendChild(status);
  panelContent.appendChild(g);
}

/* ── About + keybinds ── */

// Update checks happen only after a click. The last result lives in memory.
var updateState = null;
var updateActionPending = "";
function createUpdatePanel(currentVersion) {
  var group = el("div", "group update-panel");
  group.appendChild(i18nEl("div", "g-label", "update.title"));
  group.appendChild(i18nEl("div", "note", "update.hint"));
  var currentRow = el("div", "toggle-row");
  currentRow.appendChild(i18nEl("span", "", "update.currentVersion"));
  var current = el("span", "note update-version", currentVersion || "—");
  currentRow.appendChild(current);
  group.appendChild(currentRow);
  var latestRow = el("div", "toggle-row");
  latestRow.appendChild(i18nEl("span", "", "update.latestVersion"));
  var latest = el("span", "note update-version", "—");
  latestRow.appendChild(latest);
  group.appendChild(latestRow);
  var status = el("div", "note update-status");
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  group.appendChild(status);
  var actions = el("div", "update-actions");
  var check = i18nEl("button", "btn", "update.check");
  var download = i18nEl("button", "btn", "update.download");
  var install = i18nEl("button", "btn primary", "update.install");
  var release = i18nEl("a", "btn update-release", "update.release");
  release.target = "_blank";
  release.rel = "noopener noreferrer";
  [check, download, install].forEach(function (button) { button.type = "button"; });
  actions.appendChild(check); actions.appendChild(download); actions.appendChild(install); actions.appendChild(release);
  group.appendChild(actions);
  var failureCode = "", failureDetail = "";
  function render() {
    var state = updateState || {};
    current.textContent = state.current_version || currentVersion || "—";
    latest.textContent = state.latest_version || "—";
    var key = updateActionPending ? "update." + ({check:"checking", download:"downloading", install:"installing"})[updateActionPending] :
      "update." + (state.status || "unchecked");
    status.textContent = t(failureCode || key).replace("{version}", state.latest_version || "");
    status.title = failureDetail;
    check.disabled = !!updateActionPending;
    download.disabled = !!updateActionPending || !state.available || state.supported === false || state.status === "downloaded" || state.status === "installed";
    install.disabled = !!updateActionPending || state.status !== "downloaded" || state.supported === false;
    release.hidden = true;
    if (state.url) {
      try {
        var url = new URL(state.url);
        if (url.protocol === "https:") { release.href = url.href; release.hidden = false; }
      } catch (error) {}
    }
  }
  async function run(action) {
    if (updateActionPending) return;
    updateActionPending = action;
    failureCode = ""; failureDetail = "";
    render();
    var failureKey = "update." + action + "Failed";
    var failure = null;
    try {
      var response = await fetch("/api/update", action === "check" ? {cache:"no-store"} : {
        method:"POST", headers:{"Content-Type":"application/json"}, body:JSON.stringify({action:action}), cache:"no-store",
      });
      var result = await response.json();
      if (!response.ok) {
        failureKey = I18N.en[result.code] ? result.code : failureKey;
        throw new Error(result.error || "HTTP " + response.status);
      }
      updateState = result;
    } catch (error) { failure = error; }
    finally {
      updateActionPending = "";
      if (failure) { failureCode = failureKey; failureDetail = failure.message; }
      render();
    }
  }
  check.addEventListener("click", function () { run("check"); });
  download.addEventListener("click", function () { run("download"); });
  install.addEventListener("click", function () { run("install"); });
  registerLanguageRefresh(group, render);
  render();
  return group;
}



// Names are stable report identifiers, separate from raw runtime details.
// Explicit ID fields take precedence when the backend supplies them.
function doctorCheckLabel(check) {
  var name = String(check.Name || check.name || "");
  var identity = String(check.ID || check.id || name).toLowerCase().replace(/[_-]/g, " ");
  var key = ({binary:"doctor.binary", runtime:"doctor.runtime", home:"about.workspace", "data dir":"doctor.dataDir",
    "storage db":"doctor.storageDB", "sessions db":"doctor.sessionsDB", "memory db":"doctor.memoryDB",
    sessions:"side.sessions", provider:"stats.provider", "provider config":"doctor.providerConfig", tools:"context.tools"})[identity];
  if (key) return t(key);
  var command = identity.match(/^(git|rg) command$/);
  if (command) return t("doctor.command").replace("{name}", command[1]);
  var server = identity.match(/^(ollama|lmstudio) server$/);
  if (server) return t("doctor.server").replace("{name}", server[1] === "ollama" ? "Ollama" : "LM Studio");
  if (identity.indexOf("provider ") === 0) return t("stats.provider") + " · " + name.slice("provider ".length);
  return name;
}
function createDoctorRow(check) {
  var row = el("div", "toggle-row doctor-row " + String(check.Status || check.status || ""));
  var label = el("span", "", doctorCheckLabel(check));
  registerLanguageRefresh(label, function () { label.textContent = doctorCheckLabel(check); });
  row.appendChild(label);
  var detail = check.Detail || check.detail || "";
  var value = el("span", "note", detail);
  value.title = detail;
  row.appendChild(value);
  return row;
}

sections.about = async function () {
  panelContent.innerHTML = "";
  var g = el("div", "group");
  g.appendChild(el("div", "g-label", "SuperCli"));
  g.appendChild(i18nEl("div", "note", "about.desc"));
  panelContent.appendChild(g);

  var gi = el("div", "group");
  var h = await checkHealth();
  panelContent.appendChild(createUpdatePanel(h && (h.version || h.current_version)));
  [["about.workspace", h ? h.home : "—"], ["about.model", h ? h.model : "—"]].forEach(function (pair) {
    var row = el("div", "toggle-row");
    row.appendChild(i18nEl("span", "", pair[0]));
    var v = el("span", "note", pair[0] === "about.model" ? modelDisplayName(pair[1]) : pair[1]);
    if (pair[0] === "about.model") registerLanguageRefresh(v, function () { v.textContent = modelDisplayName(pair[1]); });
    v.style.fontFamily = "var(--mono)";
    row.appendChild(v);
    gi.appendChild(row);
  });
  panelContent.appendChild(gi);

  var gd = el("div", "group");
  var doctorHead = i18nEl("div", "g-label", "about.doctor");
  var refreshDoctor = i18nEl("button", "btn small", "about.doctorAgain");
  doctorHead.appendChild(refreshDoctor);
  gd.appendChild(doctorHead);
  var doctorBody = el("div", "doctor-report");
  gd.appendChild(doctorBody);
  async function runDoctor() {
    doctorBody.innerHTML = "";
    doctorBody.appendChild(i18nEl("div", "note", "about.checking"));
    try {
      var report = await j("/api/doctor"); doctorBody.innerHTML = "";
      var s = report.summary || {};
      var summary = el("div", "note");
      function refreshSummary() { summary.textContent = (s.ok || 0) + " " + t("common.ok") + " · " + (s.warn || 0) + " " + t("common.warn") + " · " + (s.fail || 0) + " " + t("common.fail") + " · " + (s.skip || 0) + " " + t("common.skip"); }
      refreshSummary();
      doctorBody.appendChild(summary);
      registerLanguageRefresh(summary, refreshSummary);
      (report.checks || []).forEach(function (check) { doctorBody.appendChild(createDoctorRow(check)); });
    } catch (e) { doctorBody.innerHTML = ""; doctorBody.appendChild(el("div", "note", e.message)); }
  }
  refreshDoctor.addEventListener("click", runDoctor);
  panelContent.appendChild(gd);
  runDoctor();

  var gk = el("div", "group");
  gk.appendChild(i18nEl("div", "g-label", "about.shortcuts"));
  gk.appendChild(i18nEl("div", "note", "about.rebindHint"));
  var fixed = [["Enter", "kb.send"], ["Shift+Enter", "kb.newline"], ["Esc", "kb.close"]];
  fixed.forEach(function (pair) {
    var row = el("div", "toggle-row");
    row.appendChild(i18nEl("span", "", pair[1]));
    var k = el("kbd", "", pair[0]);
    k.style.cursor = "default";
    row.appendChild(k);
    gk.appendChild(row);
  });
  [["focus", "kb.focus"], ["panel", "kb.panel"], ["sidebar", "kb.sidebar"], ["thinking", "kb.thinking"], ["tools", "kb.tools"]].forEach(function (pair) {
    var row = el("div", "toggle-row");
    row.appendChild(i18nEl("span", "", pair[1]));
    var k = el("kbd", "", ui.keybinds[pair[0]] || "—");
    k.title = t("about.rebindHint");
    k.addEventListener("click", function () {
      k.classList.add("recording");
      k.textContent = "…";
      function capture(e) {
        e.preventDefault();
        e.stopPropagation();
        if (e.key === "Shift" || e.key === "Control" || e.key === "Alt" || e.key === "Meta") return;
        var combo = comboOf(e);
        ui.keybinds[pair[0]] = combo;
        saveUI();
        k.classList.remove("recording");
        k.textContent = combo;
        document.removeEventListener("keydown", capture, true);
      }
      document.addEventListener("keydown", capture, true);
    });
    row.appendChild(k);
    gk.appendChild(row);
  });
  panelContent.appendChild(gk);
};
