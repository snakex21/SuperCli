"use strict";

sections.accounts = async function () {
  clearTimeout(codexPollTimer);
  var got;
  try { got = await j("/api/codex/accounts"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  var g = el("div", "group");
  g.appendChild(el("div", "g-label", t("acct.title")));
  var anyInProgress = (got.pending_logins || []).length > 0;
  (got.accounts || []).forEach(function (a) {
    if (a.login_in_progress) anyInProgress = true;
    var row = el("div", "list-row");
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(a.label) + "</strong> " +
      (a.login_in_progress ? '<span class="note">' + escHtml(t("acct.loggingIn")) + "</span>"
        : a.logged_in ? '<span style="color:var(--ok)">●</span>'
        : '<span class="note">' + escHtml(t("acct.loggedOut")) + "</span>");
    main.appendChild(title);
    var sub = [a.email, a.plan_type, a.last_refresh ? "refreshed " + fmtWhen(a.last_refresh) : ""].filter(Boolean).join(" · ");
    if (a.login_error) sub = "login error: " + a.login_error;
    main.appendChild(el("div", "lr-sub", sub));
    if (a.limits) {
      main.appendChild(el("div", "lr-sub", "limits: " + JSON.stringify(a.limits)));
    }
    row.appendChild(main);
    var act = el("div", "lr-act");
    if (a.logged_in) {
      var br = el("button", "", t("acct.refreshTok"));
      br.addEventListener("click", async function () {
        try { await jpost("/api/codex/refresh", { label: a.label }); toast("OK"); } catch (e) { toast(e.message); }
        sections.accounts();
      });
      act.appendChild(br);
      var bo = el("button", "danger", t("acct.logout"));
      bo.addEventListener("click", async function () {
        try { await jpost("/api/codex/logout", { label: a.label }); } catch (e) { toast(e.message); }
        sections.accounts();
      });
      act.appendChild(bo);
    } else if (!a.login_in_progress) {
      var bl = el("button", "", t("acct.login"));
      bl.addEventListener("click", async function () {
        try { await jpost("/api/codex/login", { label: a.label }); } catch (e) { toast(e.message); }
        sections.accounts();
      });
      act.appendChild(bl);
    }
    row.appendChild(act);
    g.appendChild(row);
  });
  panelContent.appendChild(g);
  if (anyInProgress && currentSection === "accounts" && !overlay.hidden) {
    codexPollTimer = setTimeout(sections.accounts, 2000);
  }
};

/* ── MCP ── */

sections.mcp = function () { renderMcpList(); };

async function renderMcpList() {
  var got;
  try { got = await j("/api/mcp/servers"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  var portable = el("div", "group");
  var portableLabel = el("div", "g-label", t("mcp.portable"));
  var openPackages = el("button", "g-act", t("mcp.openPackages"));
  openPackages.addEventListener("click", async function () {
    try { await jpost("/api/mcp/folder", {}); }
    catch (e) { toast(e.message); }
  });
  portableLabel.appendChild(openPackages);
  portable.appendChild(portableLabel);
  portable.appendChild(el("div", "note mcp-portable-hint", t("mcp.portableHint")));
  var packages = got.packages || [];
  if (!packages.length) portable.appendChild(el("div", "note", t("mcp.noPackages")));
  packages.forEach(function (p) {
    var row = el("div", "list-row mcp-package-row");
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(p.name || p.id) + "</strong>" +
      (p.version ? " <small>" + escHtml(p.version) + "</small>" : "");
    main.appendChild(title);
    if (p.description) main.appendChild(el("div", "lr-desc", p.description));
    main.appendChild(el("div", "lr-sub", p.manifest || "manifest.toml"));
    if (p.error) main.appendChild(el("div", "mcp-package-error", p.error));
    row.appendChild(main);
    var state = p.running ? t("mcp.running") : (p.available ? t("mcp.ready") : (p.enabled ? t("mcp.unavailable") : t("mcp.disabled")));
    var badge = el("span", "mcp-state " + (p.running ? "running" : (p.available ? "ready" : "error")), state);
    badge.title = p.running ? ((p.tools || 0) + " tools") : (p.available ? t("mcp.lazy") : (p.error || ""));
    row.appendChild(badge);
    portable.appendChild(row);
  });
  panelContent.appendChild(portable);

  var g = el("div", "group");
  var lbl = el("div", "g-label", t("mcp.servers"));
  var jb = el("button", "g-act", t("mcp.editJson"));
  jb.addEventListener("click", function () { renderMcpJSON(got.servers || []); });
  lbl.appendChild(jb);
  g.appendChild(lbl);
  var servers = got.servers || [];
  if (!servers.length) g.appendChild(el("div", "note", t("mcp.none")));
  servers.forEach(function (s) {
    var row = el("div", "list-row");
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(s.name) + "</strong>";
    main.appendChild(title);
    main.appendChild(el("div", "lr-sub", s.command + " " + (s.args || []).join(" ")));
    row.appendChild(main);
    var act = el("div", "lr-act");
    var bx = el("button", "danger", t("common.remove"));
    bx.addEventListener("click", async function () {
      try { await jpost("/api/mcp/remove", { name: s.name }); } catch (e) { toast(e.message); }
      renderMcpList();
    });
    act.appendChild(bx);
    row.appendChild(act);
    g.appendChild(row);
  });
  panelContent.appendChild(g);

  var ga = el("div", "group");
  ga.appendChild(el("div", "g-label", t("mcp.addServer")));
  var form = el("form", "form-grid");
  form.autocomplete = "off";
  function fld(labelText, ph, full, tag) {
    var lab = el("label", full ? "fw" : "");
    lab.appendChild(el("span", "", labelText));
    var inp = document.createElement(tag || "input");
    inp.className = "field-input";
    inp.placeholder = ph || "";
    if (tag === "textarea") { inp.rows = 3; inp.style.fontFamily = "var(--mono)"; }
    lab.appendChild(inp);
    form.appendChild(lab);
    return inp;
  }
  var nameI = fld(t("prov.name"), "context7");
  var cmdI = fld(t("mcp.command"), "npx");
  var argsI = fld(t("mcp.args"), "-y, @upstash/context7-mcp", true);
  var envI = fld(t("mcp.env"), "KEY=value", true, "textarea");
  var submit = el("button", "btn primary fw", t("common.add"));
  submit.type = "submit";
  form.appendChild(submit);
  form.addEventListener("submit", async function (e) {
    e.preventDefault();
    var env = {};
    envI.value.split("\n").forEach(function (line) {
      var eq = line.indexOf("=");
      if (eq > 0) env[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
    });
    try {
      await jpost("/api/mcp/add", {
        name: nameI.value.trim(),
        command: cmdI.value.trim(),
        args: argsI.value.split(",").map(function (s) { return s.trim(); }).filter(Boolean),
        env: env,
      });
      renderMcpList();
    } catch (err) { toast(err.message); }
  });
  ga.appendChild(form);
  panelContent.appendChild(ga);
}

function renderMcpJSON(servers) {
  panelContent.innerHTML = "";
  var g = el("div", "group");
  var lbl = el("div", "g-label", t("mcp.editJson"));
  var back = el("button", "g-act", "‹ " + t("mcp.backToList"));
  back.addEventListener("click", renderMcpList);
  lbl.appendChild(back);
  g.appendChild(lbl);
  var obj = {};
  servers.forEach(function (s) { obj[s.name] = { command: s.command, args: s.args || [], env: s.env || {} }; });
  var ta = el("textarea", "editor-area");
  ta.spellcheck = false;
  ta.value = JSON.stringify(obj, null, 2);
  g.appendChild(ta);
  var save = el("button", "btn primary", t("mcp.saveJson"));
  save.style.marginTop = "8px";
  var status = el("span", "note");
  status.style.marginLeft = "10px";
  save.addEventListener("click", async function () {
    var parsed;
    try { parsed = JSON.parse(ta.value); } catch (e) { status.textContent = "JSON: " + e.message; return; }
    try {
      // Remove servers not present anymore, then upsert the rest.
      var names = Object.keys(parsed);
      for (var i = 0; i < servers.length; i++) {
        if (names.indexOf(servers[i].name) < 0) await jpost("/api/mcp/remove", { name: servers[i].name });
      }
      for (var n = 0; n < names.length; n++) {
        var sc = parsed[names[n]] || {};
        await jpost("/api/mcp/add", { name: names[n], command: sc.command || "", args: sc.args || [], env: sc.env || {} });
      }
      status.textContent = "OK";
      renderMcpList();
    } catch (e) { status.textContent = e.message; }
  });
  g.appendChild(save);
  g.appendChild(status);
  panelContent.appendChild(g);
}

/* ── Memory ── */
