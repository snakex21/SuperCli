"use strict";

var codexAccountsRenderSeq = 0;
sections.accounts = async function () {
  var seq = ++codexAccountsRenderSeq;
  clearTimeout(codexPollTimer);
  var got;
  try { got = await j("/api/codex/accounts"); } catch (e) {
    if (seq === codexAccountsRenderSeq && currentSection === "accounts") panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  if (seq !== codexAccountsRenderSeq || currentSection !== "accounts") return;
  panelContent.innerHTML = "";
  var g = el("div", "group");
  var accountsLabel = i18nEl("div", "g-label", "acct.title");
  var reloadAccounts = i18nEl("button", "g-act", "common.refresh");
  reloadAccounts.type = "button";
  reloadAccounts.addEventListener("click", sections.accounts);
  accountsLabel.appendChild(reloadAccounts);
  g.appendChild(accountsLabel);
  (got.accounts || []).forEach(function (a) {
    var row = el("div", "list-row");
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(a.label) + "</strong> " +
      (a.login_in_progress ? '<span class="note">' + escHtml(t("acct.loggingIn")) + "</span>"
        : a.logged_in ? '<span style="color:var(--ok)">●</span>'
        : '<span class="note">' + escHtml(t("acct.loggedOut")) + "</span>");
    main.appendChild(title);
    var sub = [a.email, a.plan_type, a.last_refresh ? t("acct.usage.captured").replace("{time}", fmtWhen(a.last_refresh)) : ""].filter(Boolean).join(" · ");
    if (a.login_error) sub = t("common.error") + ": " + a.login_error;
    main.appendChild(el("div", "lr-sub", sub));
    row.appendChild(main);
    var act = el("div", "lr-act");
    if (a.logged_in) {
      var br = i18nEl("button", "", "acct.refreshTok");
      br.addEventListener("click", async function () {
        try { await jpost("/api/codex/refresh", { label: a.label }); toast(t("common.ok")); } catch (e) { toast(e.message); }
        sections.accounts();
      });
      act.appendChild(br);
      var bo = i18nEl("button", "danger", "acct.delete");
      bo.addEventListener("click", async function () {
        try { await jpost("/api/codex/logout", { label: a.label }); } catch (e) { toast(e.message); }
        sections.accounts();
      });
      act.appendChild(bo);
    } else if (!a.login_in_progress) {
      var bl = i18nEl("button", "", "acct.login");
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
  panelContent.appendChild(renderCodexUsageDashboard(got, sections.accounts));
};

/* Codex limits are account-scoped. Missing/stale windows are not zero usage. */
function codexUsagePercent(value) {
  return typeof value === "number" && Number.isFinite(value) ? Math.max(0, Math.min(100, value)) : null;
}
function codexUsagePercentText(value) {
  var percent = codexUsagePercent(value);
  if (percent === null) return "—";
  return statsFormatter("codexUsagePercent", {maximumFractionDigits: 1}).format(percent) + "%";
}
function codexUsageWindowLabel(seconds) {
  if (typeof seconds !== "number" || !Number.isFinite(seconds) || seconds <= 0) return t("acct.usage.windowUnknown");
  var key = "acct.usage.durationSeconds", n = seconds;
  if (seconds % 86400 === 0) { key = "acct.usage.durationDays"; n = seconds / 86400; }
  else if (seconds % 3600 === 0) { key = "acct.usage.durationHours"; n = seconds / 3600; }
  else if (seconds % 60 === 0) { key = "acct.usage.durationMinutes"; n = seconds / 60; }
  return t("acct.usage.period").replace("{duration}", t(key).replace("{n}", fmtInteger(n)));
}
function codexUsageAccountState(account) {
  var usage = account.usage;
  if (!usage) return "unknown";
  if (usage.availability === "available" || usage.availability === "exhausted") return usage.availability;
  if (usage.availability === "unknown") return usage.stale ? "stale" : "unknown";
  // Only the general bucket establishes account availability. A model-specific
  // bucket cannot exhaust every model; allowed=true may include paid credits.
  var limit = (usage.rate_limits || []).find(function (item) { return item.id === "codex"; });
  if (!limit) return usage.stale ? "stale" : "unknown";
  var windows = [limit.primary, limit.secondary].filter(Boolean);
  if (windows.some(function (window) { return window.stale === true; }) ||
      (!windows.length && usage.stale)) return "stale";
  if (typeof limit.allowed === "boolean") return limit.allowed ? "available" : "exhausted";
  if (limit.limit_reached === true) return "exhausted";
  var known = false, exhausted = false;
  windows.forEach(function (window) {
    var used = codexUsagePercent(window.used_percent);
    if (used !== null) { known = true; exhausted = exhausted || used === 100; }
  });
  return known ? (exhausted ? "exhausted" : "available") : "unknown";
}
function codexUsageReset(value) {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) return "—";
  var date = new Date(value * 1000);
  return isNaN(date) ? "—" : fmtDateTime(date.toISOString());
}
function renderCodexUsageDashboard(data, onUpdated) {
  data = data || {};
  var root = el("section", "usage-section codex-usage");
  var head = el("div", "usage-section-head");
  head.appendChild(i18nEl("h3", "usage-section-title", "acct.usage.title"));
  function refreshButton(label, body) {
    var button = i18nEl("button", "btn", label); button.type = "button";
    button.addEventListener("click", async function () {
      button.disabled = true;
      try {
        var updated = await jpost("/api/codex/usage", body);
        if (updated.ok === false) toast(updated.error || t("common.error"));
        if (root.isConnected) {
          if (onUpdated) await onUpdated();
          else root.replaceWith(renderCodexUsageDashboard(updated));
        }
      } catch (error) {
        if (root.isConnected) { toast(error.message); button.disabled = false; }
      }
    });
    return button;
  }
  head.appendChild(refreshButton("acct.usage.refreshAll", {all: true}));
  root.appendChild(head);
  var summary = data.usage_summary || {}, summaryLine = el("div", "codex-usage-summary");
  [["accounts", "accounts"], ["available", "available"], ["exhausted", "exhausted"], ["unknown", "unknown"], ["stale", "stale"]].forEach(function (field) {
    var count = summary[field[0]];
    summaryLine.appendChild(el("span", "codex-usage-count " + field[0], t("acct.usage." + field[1]) + ": " +
      (typeof count === "number" && Number.isFinite(count) && count >= 0 ? fmtInteger(count) : "—")));
  });
  root.appendChild(summaryLine);
  root.appendChild(i18nEl("p", "usage-caption", "acct.usage.aggregateHint"));
  var accounts = (data.accounts || []).filter(function (account) { return account.logged_in; });
  if (!accounts.length) root.appendChild(i18nEl("div", "usage-empty", "acct.usage.noAccounts"));
  accounts.forEach(function (account) {
    var state = codexUsageAccountState(account), usage = account.usage || {};
    var card = el("article", "codex-usage-account " + state);
    var title = el("div", "codex-usage-account-head");
    title.appendChild(el("strong", "", account.label || ""));
    title.appendChild(el("span", "codex-usage-state", t("acct.usage." + state)));
    title.appendChild(refreshButton("acct.usage.refresh", {label: account.label}));
    card.appendChild(title);
    var meta = [account.email, usage.plan_type || account.plan_type].filter(Boolean).join(" · ");
    if (meta) card.appendChild(el("div", "codex-usage-meta", meta));
    if (usage.captured_at) card.appendChild(el("div", "codex-usage-meta", t("acct.usage.captured").replace("{time}", fmtDateTime(usage.captured_at))));
    if (account.usage_error) card.appendChild(el("div", "codex-usage-error", account.usage_error));
    var windows = 0;
    (usage.rate_limits || []).forEach(function (limit) {
      [limit.primary, limit.secondary].forEach(function (window) {
        if (!window) return;
        windows++;
        var row = el("div", "codex-usage-window" + (window.stale ? " stale" : ""));
        var label = codexUsageWindowLabel(window.window_seconds);
        if (limit.name || limit.id) label = (limit.name || limit.id) + " · " + label;
        row.appendChild(el("div", "codex-usage-window-label", label));
        var metrics = el("div", "codex-usage-window-values");
        metrics.appendChild(el("span", "", t("acct.usage.used") + " " + codexUsagePercentText(window.used_percent)));
        metrics.appendChild(el("span", "", t("acct.usage.remaining") + " " + codexUsagePercentText(window.remaining_percent)));
        row.appendChild(metrics);
        var used = codexUsagePercent(window.used_percent);
        if (used !== null) {
          var meter = el("div", "codex-usage-meter");
          meter.setAttribute("role", "progressbar"); meter.setAttribute("aria-label", label + " · " + t("acct.usage.used"));
          meter.setAttribute("aria-valuemin", "0"); meter.setAttribute("aria-valuemax", "100"); meter.setAttribute("aria-valuenow", String(used));
          var fill = el("span", ""); fill.style.width = used + "%"; meter.appendChild(fill); row.appendChild(meter);
        }
        row.appendChild(el("div", "codex-usage-meta", t("acct.usage.reset") + ": " + codexUsageReset(window.resets_at) +
          (window.stale ? " · " + t("acct.usage.stale") : "")));
        card.appendChild(row);
      });
    });
    if (!windows) card.appendChild(i18nEl("div", "usage-caption", "acct.usage.noSnapshot"));
    if (usage.credits) {
      var credits = usage.credits, value = credits.unlimited === true ? t("acct.usage.unlimited") :
        credits.balance !== null && credits.balance !== undefined && String(credits.balance) !== "" ? String(credits.balance) :
        credits.has_credits === false ? t("acct.usage.noCredits") : t("acct.usage.unknown");
      card.appendChild(el("div", "codex-usage-meta", t("acct.usage.credits") + ": " + value));
    }
    root.appendChild(card);
  });
  return root;
}

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
  var portableLabel = i18nEl("div", "g-label", "mcp.portable");
  var openPackages = i18nEl("button", "g-act", "mcp.openPackages");
  openPackages.addEventListener("click", async function () {
    try { await jpost("/api/mcp/folder", {}); }
    catch (e) { toast(e.message); }
  });
  portableLabel.appendChild(openPackages);
  portable.appendChild(portableLabel);
  portable.appendChild(i18nEl("div", "note mcp-portable-hint", "mcp.portableHint"));
  var packages = got.packages || [];
  if (!packages.length) portable.appendChild(i18nEl("div", "note", "mcp.noPackages"));
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
  var lbl = i18nEl("div", "g-label", "mcp.servers");
  var jb = i18nEl("button", "g-act", "mcp.editJson");
  jb.addEventListener("click", function () { renderMcpJSON(got.servers || []); });
  lbl.appendChild(jb);
  g.appendChild(lbl);
  var servers = got.servers || [];
  if (!servers.length) g.appendChild(i18nEl("div", "note", "mcp.none"));
  servers.forEach(function (s) {
    var row = el("div", "list-row");
    var main = el("div", "lr-main");
    var title = el("div", "lr-title");
    title.innerHTML = "<strong>" + escHtml(s.name) + "</strong>";
    main.appendChild(title);
    main.appendChild(el("div", "lr-sub", s.command + " " + (s.args || []).join(" ")));
    row.appendChild(main);
    var act = el("div", "lr-act");
    var bx = i18nEl("button", "danger", "common.remove");
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
  ga.appendChild(i18nEl("div", "g-label", "mcp.addServer"));
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
  var submit = i18nEl("button", "btn primary fw", "common.add");
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
  var lbl = i18nEl("div", "g-label", "mcp.editJson");
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
  var save = i18nEl("button", "btn primary", "mcp.saveJson");
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
      status.textContent = t("common.ok");
      renderMcpList();
    } catch (e) { status.textContent = e.message; }
  });
  g.appendChild(save);
  g.appendChild(status);
  panelContent.appendChild(g);
}

/* ── Memory ── */
