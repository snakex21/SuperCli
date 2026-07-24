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
  var lbl = el("div", "g-label", t("panel.files"));
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
  var save = el("button", "btn primary", t("files.save"));
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

sections.about = async function () {
  panelContent.innerHTML = "";
  var g = el("div", "group");
  g.appendChild(el("div", "g-label", "SuperCli"));
  g.appendChild(el("div", "note", t("about.desc")));
  panelContent.appendChild(g);

  var gi = el("div", "group");
  var h = await checkHealth();
  [[t("about.workspace"), h ? h.home : "—"], [t("about.model"), h ? h.model : "—"]].forEach(function (pair) {
    var row = el("div", "toggle-row");
    row.appendChild(el("span", "", pair[0]));
    var v = el("span", "note", pair[1]);
    v.style.fontFamily = "var(--mono)";
    row.appendChild(v);
    gi.appendChild(row);
  });
  panelContent.appendChild(gi);

  var gd = el("div", "group");
  var doctorHead = el("div", "g-label", ui.lang === "pl" ? "Diagnostyka" : "Doctor");
  var refreshDoctor = el("button", "btn small", ui.lang === "pl" ? "sprawdź ponownie" : "run again");
  doctorHead.appendChild(refreshDoctor);
  gd.appendChild(doctorHead);
  var doctorBody = el("div", "doctor-report");
  gd.appendChild(doctorBody);
  async function runDoctor() {
    doctorBody.innerHTML = "";
    doctorBody.appendChild(el("div", "note", ui.lang === "pl" ? "Sprawdzanie…" : "Checking…"));
    try {
      var report = await j("/api/doctor"); doctorBody.innerHTML = "";
      var s = report.summary || {};
      doctorBody.appendChild(el("div", "note", (s.ok || 0) + " ok · " + (s.warn || 0) + " warn · " + (s.fail || 0) + " fail · " + (s.skip || 0) + " skip"));
      (report.checks || []).forEach(function (check) {
        var row = el("div", "toggle-row doctor-row " + String(check.Status || check.status || ""));
        var name = check.Name || check.name || "check";
        var detail = check.Detail || check.detail || "";
        row.appendChild(el("span", "", name));
        var value = el("span", "note", detail); value.title = detail;
        row.appendChild(value); doctorBody.appendChild(row);
      });
    } catch (e) { doctorBody.innerHTML = ""; doctorBody.appendChild(el("div", "note", e.message)); }
  }
  refreshDoctor.addEventListener("click", runDoctor);
  panelContent.appendChild(gd);
  runDoctor();

  var gk = el("div", "group");
  gk.appendChild(el("div", "g-label", t("about.shortcuts")));
  gk.appendChild(el("div", "note", t("about.rebindHint")));
  var fixed = [["Enter", t("kb.send")], ["Shift+Enter", t("kb.newline")], ["Esc", t("kb.close")]];
  fixed.forEach(function (pair) {
    var row = el("div", "toggle-row");
    row.appendChild(el("span", "", pair[1]));
    var k = el("kbd", "", pair[0]);
    k.style.cursor = "default";
    row.appendChild(k);
    gk.appendChild(row);
  });
  [["focus", t("kb.focus")], ["panel", t("kb.panel")], ["sidebar", t("kb.sidebar")], ["thinking", t("kb.thinking")], ["tools", t("kb.tools")]].forEach(function (pair) {
    var row = el("div", "toggle-row");
    row.appendChild(el("span", "", pair[1]));
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
