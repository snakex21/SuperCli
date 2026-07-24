"use strict";

sections.models = async function () {
  var got;
  try { got = await j("/api/models"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  panelContent.appendChild(sessionRuntimePreferenceGroup());
  var g = el("div", "group");
  var lbl = el("div", "g-label", t("panel.models"));
  var scan = el("button", "g-act", t("common.scan"));
  scan.addEventListener("click", async function () {
    scan.textContent = "…";
    try { await jpost("/api/provider/scan", {}); } catch (e) {}
    sections.models();
  });
  lbl.appendChild(scan);
  g.appendChild(lbl);
  // /api/models is authoritative even when it is empty. Falling back to the
  // browser cache here used to resurrect paid models after a key was removed.
  var models = slimModels(got.models || []);
  var counts = {};
  models.forEach(function (m) {
    var provider = m.provider || "—";
    if (!counts[provider]) counts[provider] = { total: 0, visible: 0 };
    counts[provider].total++;
    if (!m.hidden) counts[provider].visible++;
  });
  var providers = Object.keys(counts).sort(function (a, b) { return a.localeCompare(b); });
  if (modelProviderTab && providers.indexOf(modelProviderTab) < 0) modelProviderTab = "";

  var tabs = el("div", "model-provider-tabs");
  tabs.setAttribute("role", "tablist");
  function addProviderTab(provider, label, total, visible) {
    var b = el("button", modelProviderTab === provider ? "active" : "");
    b.type = "button";
    b.setAttribute("role", "tab");
    b.setAttribute("aria-selected", modelProviderTab === provider ? "true" : "false");
    b.appendChild(el("span", "", label));
    b.appendChild(el("small", "", visible + "/" + total));
    b.addEventListener("click", function () { modelProviderTab = provider; sections.models(); });
    tabs.appendChild(b);
  }
  var allVisible = models.filter(function (m) { return !m.hidden; }).length;
  addProviderTab("", t("model.allProviders"), models.length, allVisible);
  providers.forEach(function (provider) {
    addProviderTab(provider, provider, counts[provider].total, counts[provider].visible);
  });
  g.appendChild(tabs);

  var searchWrap = el("div", "model-settings-search");
  var searchInput = document.createElement("input");
  searchInput.type = "search";
  searchInput.className = "field-input";
  searchInput.placeholder = t("model.search");
  searchInput.setAttribute("aria-label", t("model.search"));
  searchInput.autocomplete = "off";
  searchInput.spellcheck = false;
  searchInput.value = modelSettingsSearch;
  searchWrap.appendChild(searchInput);
  g.appendChild(searchWrap);

  var providerModels = models.filter(function (m) { return !modelProviderTab || (m.provider || "—") === modelProviderTab; });
  function matchingModels() {
    var query = modelSettingsSearch.trim().toLocaleLowerCase();
    if (!query) return providerModels;
    return providerModels.filter(function (m) {
      var features = [m.id, m.provider, m.reasoning ? "think reasoning" : "", m.hidden ? "hidden" : "visible"];
      return features.join(" ").toLocaleLowerCase().indexOf(query) >= 0;
    });
  }
  var tools = el("div", "model-provider-actions");
  var matchCount = el("span", "note", "");
  tools.appendChild(matchCount);
  var bulkButtons = [];
  function bulkButton(label, hidden) {
    var b = el("button", "btn", label); b.type = "button";
    b.addEventListener("click", async function () {
      var filtered = matchingModels();
      b.disabled = true;
      try {
        await jpost("/api/model/visibility", { refs: filtered.map(function (m) { return { provider: m.provider, model: m.id }; }), hidden: hidden });
        await loadModels();
        await sections.models();
      } catch (e) { toast(e.message); b.disabled = false; }
    });
    tools.appendChild(b);
    bulkButtons.push({ button: b, hidden: hidden });
  }
  bulkButton(t("model.hideAll"), true);
  bulkButton(t("model.showAll"), false);
  g.appendChild(tools);

  var rows = el("div", "model-provider-rows");
  function renderFilteredModels() {
    var filtered = matchingModels();
    rows.innerHTML = "";
    matchCount.textContent = filtered.length + " / " + providerModels.length + " " + t("prov.models");
    bulkButtons.forEach(function (item) {
      item.button.disabled = !filtered.length || filtered.every(function (m) { return !!m.hidden === item.hidden; });
    });
    if (!filtered.length) rows.appendChild(el("div", "note model-no-matches", t("model.noMatches")));
    filtered.forEach(function (m) {
      var row = el("div", "list-row");
      row.appendChild(el("span", "state-dot " + (m.hidden ? "off" : "on")));
      var main = el("div", "lr-main");
      var title = el("div", "lr-title");
      title.innerHTML = "<code>" + escHtml(m.id) + "</code>" + (m.id === activeModelID ? ' <span style="color:var(--accent)">●</span>' : "");
      main.appendChild(title);
      var caps = [];
      if (m.context_length) caps.push("ctx " + fmtTok(m.context_length));
      if (m.reasoning) caps.push("think");
      main.appendChild(el("div", "lr-sub", (m.provider || "") + (caps.length ? " · " + caps.join(" · ") : "")));
      row.appendChild(main);
      var act = el("div", "lr-act");
      var bh = el("button", "", m.hidden ? t("model.show") : t("model.hide"));
      bh.addEventListener("click", function () {
        jpost("/api/model/toggle", { provider: m.provider, model: m.id }).then(sections.models);
      });
      act.appendChild(bh);
      var bd = el("button", "", t("model.setDefault"));
      bd.addEventListener("click", function () {
        jpost("/api/model/default", { model: m.id, provider: m.provider })
          .then(function () { toast("CLI default: " + m.id); })
          .catch(function (e) { toast(e.message); });
      });
      act.appendChild(bd);
      row.appendChild(act);
      rows.appendChild(row);
    });
  }
  searchInput.addEventListener("input", function () {
    modelSettingsSearch = searchInput.value;
    renderFilteredModels();
  });
  searchInput.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && searchInput.value) {
      searchInput.value = "";
      modelSettingsSearch = "";
      renderFilteredModels();
    }
  });
  renderFilteredModels();
  g.appendChild(rows);
  panelContent.appendChild(g);
};

/* ── Providers ── */

var runtimeRenderSeq = 0;
sections.runtime = async function () {
  var seq = ++runtimeRenderSeq;
  panelContent.innerHTML = "";
  var intro = el("div", "runtime-intro");
  var introCopy = el("div");
  introCopy.appendChild(el("div", "runtime-title", t("runtime.title")));
  introCopy.appendChild(el("div", "note", t("runtime.hint")));
  intro.appendChild(introCopy);
  var refreshAll = el("button", "btn", t("common.refresh")); refreshAll.type = "button";
  refreshAll.addEventListener("click", function () { sections.runtime(); });
  intro.appendChild(refreshAll); panelContent.appendChild(intro);

  var got;
  try { got = await j("/api/providers"); } catch (e) {
    panelContent.appendChild(el("div", "note", e.message));
    return;
  }
  if (seq !== runtimeRenderSeq || currentSection !== "runtime") return;
  var providers = got.providers || [];
  if (!providers.length) {
    panelContent.appendChild(el("div", "runtime-empty", t("runtime.empty")));
    return;
  }
  var list = el("div", "runtime-list"); panelContent.appendChild(list);
  providers.forEach(function (p) {
    var row = el("section", "runtime-row checking");
    var head = el("div", "runtime-head");
    var identity = el("div", "runtime-identity");
    identity.appendChild(el("span", "runtime-dot"));
    identity.appendChild(el("strong", "", p.Name));
    identity.appendChild(el("span", "runtime-kind", p.Type || ""));
    head.appendChild(identity);
    var state = el("span", "runtime-state", t("runtime.checking") + "…"); head.appendChild(state);
    row.appendChild(head);
    var endpoint = el("div", "runtime-endpoint", p.BaseURL || "—"); row.appendChild(endpoint);
    var facts = el("div", "runtime-facts"); row.appendChild(facts);
    var details = document.createElement("details"); details.className = "runtime-details";
    var summary = document.createElement("summary"); summary.textContent = t("runtime.details");
    details.appendChild(summary); details.appendChild(el("div", "runtime-detail-body", t("common.loading")));
    row.appendChild(details);
    var actions = el("div", "runtime-actions");
    var refresh = el("button", "", t("common.refresh")); refresh.type = "button";
    var toggle = el("button", "", p.Disabled ? t("runtime.enable") : t("runtime.disable")); toggle.type = "button";
    actions.appendChild(refresh); actions.appendChild(toggle); row.appendChild(actions); list.appendChild(row);

    async function update() {
      row.className = "runtime-row checking";
      state.textContent = t("runtime.checking") + "…";
      refresh.disabled = true;
      try {
        var d = await j("/api/provider/diagnostics?name=" + encodeURIComponent(p.Name));
        if (seq !== runtimeRenderSeq || currentSection !== "runtime") return;
        renderRuntimeDiagnostic(row, facts, details, endpoint, state, d);
        toggle.textContent = d.disabled ? t("runtime.enable") : t("runtime.disable");
        toggle.dataset.disabled = d.disabled ? "1" : "0";
        toggle.dataset.active = d.active ? "1" : "0";
      } catch (e) {
        row.className = "runtime-row offline";
        state.textContent = t("runtime.offline");
        facts.innerHTML = "";
        facts.appendChild(runtimeFact(t("common.error"), e.message, ""));
      } finally { refresh.disabled = false; }
    }
    refresh.addEventListener("click", update);
    toggle.addEventListener("click", async function () {
      if (toggle.dataset.active === "1" && toggle.dataset.disabled !== "1") {
        toast(t("runtime.switchFirst")); return;
      }
      toggle.disabled = true;
      try {
        await j("/api/providers", {method:"PUT", headers:{"Content-Type":"application/json"}, body:JSON.stringify({name:p.Name, disabled:toggle.dataset.disabled !== "1"})});
        await loadModels(); await update();
      } catch (e) { toast(e.message); }
      finally { toggle.disabled = false; }
    });
    update();
  });
};

function runtimeFact(label, value, source) {
  var fact = el("div", "runtime-fact");
  fact.appendChild(el("span", "runtime-fact-label", label));
  var right = el("span", "runtime-fact-right");
  right.appendChild(el("strong", "", value || "—"));
  if (source) right.appendChild(el("small", "", source));
  fact.appendChild(right);
  return fact;
}

function renderRuntimeDiagnostic(row, facts, details, endpoint, state, d) {
  row.className = "runtime-row " + (d.status || "offline");
  state.textContent = t("runtime." + (d.status || "offline")) + (d.active ? " · " + t("runtime.active") : "");
  endpoint.textContent = d.endpoint || "—";
  var kind = row.querySelector(".runtime-kind");
  kind.textContent = (d.server || d.type || "") + (d.scope ? " · " + d.scope.toUpperCase() : "");
  facts.innerHTML = "";
  if (d.status === "offline") {
    facts.appendChild(runtimeFact(t("common.error"), d.error || t("runtime.offline"), ""));
  } else if (d.status !== "disabled") {
    facts.appendChild(runtimeFact(t("runtime.latency"), d.latency_ms + " ms", t("runtime.measured")));
    facts.appendChild(runtimeFact(t("runtime.model"), d.selected_model || "—", t("runtime.reported")));
    facts.appendChild(runtimeFact(t("runtime.models"), String((d.models || []).length), t("runtime.reported")));
    if (d.context_window) facts.appendChild(runtimeFact(t("runtime.context"), fmtTok(d.context_window), t("runtime.reported")));
  }
  if (d.last_call) {
    var c = d.last_call;
    if (c.ttft_ms) facts.appendChild(runtimeFact(t("runtime.ttft"), fmtDuration(c.ttft_ms), t("runtime.lastRun")));
    if (c.tokens_per_second) facts.appendChild(runtimeFact(t("runtime.speed"), c.tokens_per_second.toFixed(1) + " tok/s", t("runtime.lastRun")));
    if (c.duration_ms) facts.appendChild(runtimeFact(t("runtime.duration"), fmtDuration(c.duration_ms), t("runtime.lastRun")));
  }
  var body = details.querySelector(".runtime-detail-body"); body.innerHTML = "";
  if ((d.models || []).length) {
    var models = el("div", "runtime-models");
    d.models.forEach(function (model) { models.appendChild(el("code", "", model)); });
    body.appendChild(models);
  }
  var limits = el("div", "runtime-limits");
  limits.appendChild(el("div", "runtime-limit-title", t("runtime.limits")));
  limits.appendChild(el("span", "", t("runtime.hardware")));
  limits.appendChild(el("span", "", t("runtime.backendQueue")));
  body.appendChild(limits);
}
