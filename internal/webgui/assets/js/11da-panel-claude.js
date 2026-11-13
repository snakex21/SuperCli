"use strict";

function claudeStatusIcon(connected) {
  var icon = el("span", "claude-auth-icon" + (connected ? " connected" : ""));
  icon.setAttribute("aria-hidden", "true");
  icon.innerHTML = connected
    ? '<svg viewBox="0 0 24 24" fill="none"><path d="m6 12 4 4 8-9" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>'
    : '<svg viewBox="0 0 24 24" fill="none"><path d="M12 3v18M3 12h18M5.6 5.6l12.8 12.8M5.6 18.4 18.4 5.6" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>';
  return icon;
}

async function renderClaudeLogin(templates) {
  panelContent.innerHTML = "";
  var root = el("section", "group claude-auth");
  var head = el("div", "claude-auth-head");
  var back = el("button", "g-act", "‹ " + t("common.back"));
  back.type = "button";
  head.appendChild(back);
  head.appendChild(el("span", "claude-auth-brand", "SuperCli / Claude"));
  root.appendChild(head);
  var content = el("div", "claude-auth-content");
  root.appendChild(content);
  panelContent.appendChild(root);
  var controller = null, data = {logged_in: false};
  function active() { return root.isConnected; }
  back.addEventListener("click", function () {
    if (controller) controller.abort();
    renderProvidersList();
  });
  function draw(error, waiting) {
    if (!active()) return;
    content.innerHTML = "";
    content.appendChild(claudeStatusIcon(!!data.logged_in && !error));
    content.appendChild(el("div", "claude-auth-eyebrow", "Claude"));
    content.appendChild(el("h2", "claude-auth-title", t(data.logged_in ? "claude.auth.title" : "claude.login")));
    content.appendChild(el("p", "claude-auth-copy", t(data.logged_in ? "claude.auth.body" : "claude.hint")));
    var status = el("div", "claude-auth-status" + (error ? " error" : ""));
    status.setAttribute("role", error ? "alert" : "status");
    status.setAttribute("aria-live", "polite");
    status.textContent = error || (waiting ? t(waiting === "loading" ? "common.loading" : "claude.waiting") : "");
    content.appendChild(status);
    var actions = el("div", "claude-auth-actions");
    if (data.logged_in && !waiting) {
      var models = Array.isArray(data.models) ? data.models : [];
      var selected = document.createElement("select");
      selected.className = "field-select";
      selected.setAttribute("aria-label", t("stats.model"));
      models.forEach(function (name) {
        var option = el("option", "", name); option.value = name;
        option.selected = name === data.active_model; selected.appendChild(option);
      });
      if (models.length) {
        var modelLabel = el("label", "claude-auth-model");
        modelLabel.appendChild(el("span", "", t("stats.model")));
        modelLabel.appendChild(selected); content.appendChild(modelLabel);
      }
      var chat = el("button", "claude-auth-primary", t("claude.auth.chat") + " →");
      chat.type = "button";
      chat.addEventListener("click", async function () {
        chat.disabled = true;
        try {
          if (models.length && data.provider) {
            await jpost("/api/model", {provider: data.provider, model: selected.value});
            await loadModels();
            if (typeof renderStats === "function") await renderStats();
            closePanel();
            var prompt = document.getElementById("prompt"); if (prompt) prompt.focus();
          } else openPanel("models");
        } catch (e) { if (active()) { draw(e.message); } }
      });
      actions.appendChild(chat);
      var logout = el("button", "claude-auth-secondary", t("claude.logout"));
      logout.type = "button";
      logout.addEventListener("click", async function () {
        logout.disabled = true;
        try {
          await jpost("/api/claude/logout", {});
          data = {logged_in: false}; draw();
          if (typeof renderStats === "function") await renderStats();
        } catch (e) { draw(e.message); }
      });
      actions.appendChild(logout);
      content.appendChild(actions);
      if (typeof renderClaudeUsageDashboard === "function") content.appendChild(renderClaudeUsageDashboard(data));
    } else {
      var login = el("button", "claude-auth-primary", t("claude.login") + " ↗");
      login.type = "button"; login.disabled = !!waiting;
      login.addEventListener("click", async function () {
        draw(null, true);
        controller = new AbortController();
        try {
          var language = typeof ui !== "undefined" ? ui.lang : "en";
          var result = await j("/api/claude/login?lang=" + encodeURIComponent(language), {
            method: "POST", signal: controller.signal,
            headers: {"Content-Type": "application/json"}, body: "{}"
          });
          if (!active()) return;
          data = result;
          draw(result.setup_error || result.scan_error);
          toast(t("claude.auth.title"));
          await loadModels();
          if (typeof renderStats === "function") await renderStats();
        } catch (e) { if (active()) draw(e.message); }
        finally { controller = null; }
      });
      actions.appendChild(login);
      content.appendChild(actions);
    }
  }
  draw(null, "loading");
  try { data = await j("/api/claude/account"); if (active()) draw(); }
  catch (e) { draw(e.message); }
}

function renderClaudeUsageDashboard(data, onUpdated) {
  data = data || {};
  var root = el("section", "usage-section claude-usage");
  var head = el("div", "usage-section-head");
  head.appendChild(el("h3", "usage-section-title", t("claude.usage.title")));
  var refresh = el("button", "btn", t("subscription.refresh"));
  refresh.type = "button"; refresh.disabled = !data.logged_in;
  refresh.addEventListener("click", async function () {
    refresh.disabled = true;
    try {
      var updated = await jpost("/api/claude/usage", {});
      if (root.isConnected) {
        if (onUpdated) await onUpdated();
        else root.replaceWith(renderClaudeUsageDashboard(updated));
        if (typeof renderStats === "function") await renderStats();
      }
    } catch (e) { if (root.isConnected) { toast(e.message); refresh.disabled = false; } }
  });
  head.appendChild(refresh); root.appendChild(head);
  if (!data.logged_in) {
    root.appendChild(el("p", "usage-caption", t("claude.disconnected")));
    var connect = el("button", "btn", t("claude.login")); connect.type = "button";
    connect.addEventListener("click", function () { renderClaudeLogin([]); });
    root.appendChild(connect); return root;
  }
  var usage = data.usage || {};
  if (usage.captured_at) root.appendChild(el("p", "usage-caption", t("acct.usage.captured").replace("{time}", fmtDateTime(usage.captured_at))));
  if (data.usage_error) root.appendChild(el("p", "codex-usage-error", data.usage_error));
  if (!(usage.windows || []).length) root.appendChild(el("p", "usage-caption", t("acct.usage.noSnapshot")));
  else root.appendChild(renderSubscriptionSummary([{provider: "Claude", windows: usage.windows}]));
  if (usage.extra_usage && usage.extra_usage.enabled) {
    root.appendChild(el("p", "usage-caption", t("claude.usage.extra") + ": " + subscriptionPercentText(usage.extra_usage.used_percent)));
  }
  return root;
}
