"use strict";

/* ═══ model palette ═══ */

var palette = $("#palette"), modelCache = [];
function togglePalette(show) {
  var want = show !== undefined ? show : palette.hidden;
  palette.hidden = !want;
  if (want) {
    toggleReasoningMenu(false);
    renderModelList($("#model-search").value.trim().toLowerCase()); // cached list: instant
    loadModels(); // then refresh quietly
    setTimeout(function () { $("#model-search").focus(); }, 30);
  }
}
$("#model-btn").addEventListener("click", function () { togglePalette(); });
document.addEventListener("click", function (e) {
  if (!palette.hidden && !palette.contains(e.target) && !$("#model-btn").contains(e.target)) togglePalette(false);
  var reasoningControl = $("#reasoning-control");
  if (!$("#reasoning-menu").hidden && !reasoningControl.contains(e.target)) toggleReasoningMenu(false);
});

// slimModels reduces /api/models entries to what the cached list needs.
function slimModels(models) {
  return (models || []).map(function (m) {
    return {
      id: m.id, provider: m.provider, hidden: !!m.hidden, active: !!m.active,
      context_length: m.context_length || 0, manual_context_length: m.manual_context_length || 0,
      reasoning: !!m.reasoning,
      vision: !!m.vision, tool_use: !!m.tool_use,
    };
  });
}

// The palette is a fast model picker, so it contains only selectable models.
// Hidden entries remain in modelCache for the active-context control and in
// Settings > Models, where users can make them visible again.
function paletteModels(models) {
  return (models || []).filter(function (m) { return !m.hidden; });
}

async function loadModels() {
  try {
    var got = await j("/api/models");
    if (got.active) {
      activeModelID = got.active;
      $("#model-name").textContent = got.active;
    }
    activeProviderID = got.provider || "";
    $("#model-prov").textContent = got.provider || "";
    // A successful response is authoritative, including an empty list. This
    // prevents paid models surviving after a key was removed or expired.
    modelCache = slimModels(got.models || []);
    saveBlobKey("supercli-model-cache", modelCache);
    renderModelList($("#model-search").value.trim().toLowerCase());
    renderReasoning(got.reasoning);
    return got;
  } catch (e) {
    if (!modelCache.length) $("#model-list").innerHTML = '<div class="side-empty">' + escHtml(e.message) + "</div>";
  }
}

function contextBudgetText(tokens) {
  if (!tokens) return "auto";
  if (tokens % 1000000 === 0) return (tokens / 1000000) + "m";
  if (tokens % 1000 === 0) return (tokens / 1000) + "k";
  return String(tokens);
}

function activeModelEntry() {
  var exact = modelCache.find(function (m) {
    return m.id === activeModelID && (!activeProviderID || m.provider === activeProviderID);
  });
  if (exact) return exact;
  return modelCache.find(function (m) { return m.active; }) || null;
}

function renderActiveContextControl() {
  var control = $("#model-context-control");
  var active = activeModelEntry();
  control.hidden = !active;
  if (!active) return;
  $("#model-context-target").textContent = (active.provider || "—") + " / " + active.id;
  $("#model-context-input").value = contextBudgetText(active.manual_context_length);
  var hint = t("model.contextHint");
  if (active.context_length) hint += " · " + t("model.contextDetected") + " " + fmtTok(active.context_length);
  $("#model-context-hint").textContent = hint;
}

function saveActiveModelContext() {
  var active = activeModelEntry();
  if (!active) return;
  var input = $("#model-context-input");
  var button = $("#model-context-save");
  var value = input.value.trim() || "auto";
  button.disabled = true;
  jpost("/api/model/context", { provider: active.provider, model: active.id, value: value })
    .then(function (result) {
      active.manual_context_length = result.automatic ? 0 : (result.tokens || 0);
      saveBlobKey("supercli-model-cache", modelCache);
      renderModelList($("#model-search").value.trim().toLowerCase());
      toast(t("model.contextSaved") + ": " + active.provider + " / " + active.id + " = " + contextBudgetText(active.manual_context_length));
    })
    .catch(function (err) { toast(err.message || t("model.contextInvalid")); })
    .finally(function () { button.disabled = false; });
}

$("#model-context-save").addEventListener("click", saveActiveModelContext);
$("#model-context-input").addEventListener("keydown", function (event) {
  if (event.key === "Enter") {
    event.preventDefault();
    saveActiveModelContext();
  }
  if (event.key === "Escape") renderActiveContextControl();
});

function renderModelList(filter) {
  var list = $("#model-list");
  list.innerHTML = "";
  paletteModels(modelCache).forEach(function (m) {
    if (filter && (m.id + " " + m.provider).toLowerCase().indexOf(filter) < 0) return;
    var isActive = m.id === activeModelID && (!activeProviderID || m.provider === activeProviderID);
    var row = el("div", "prow" + (isActive ? " active" : ""));
    row.appendChild(el("span", "state-dot on"));
    row.appendChild(el("span", "pid", m.id));
    row.appendChild(el("span", "pprov", m.provider || ""));
    if (m.context_length) row.appendChild(el("span", "pbadge", fmtTok(m.context_length)));
    if (m.manual_context_length) {
      var manualBadge = el("span", "pbadge manual-context", "ctx " + contextBudgetText(m.manual_context_length));
      manualBadge.title = t("model.contextBudget");
      row.appendChild(manualBadge);
    }
    if (m.reasoning) row.appendChild(el("span", "pbadge", "think"));
    var act = el("span", "pact");
    var bd = el("button", "", t("model.setDefault"));
    bd.title = "Set as CLI default (config.toml)";
    bd.addEventListener("click", function (e) {
      e.stopPropagation();
      jpost("/api/model/default", { model: m.id, provider: m.provider })
        .then(function () { toast("CLI default: " + m.id); })
        .catch(function (err) { toast(err.message); });
    });
    act.appendChild(bd);
    var bh = el("button", "", t("model.hide"));
    bh.addEventListener("click", function (e) {
      e.stopPropagation();
      jpost("/api/model/toggle", { provider: m.provider, model: m.id }).then(function () {
        m.hidden = !m.hidden;
        saveBlobKey("supercli-model-cache", modelCache);
        renderModelList(filter);
      });
    });
    act.appendChild(bh);
    row.appendChild(act);
    row.addEventListener("click", function () {
      jpost("/api/model", { model: m.id, provider: m.provider })
        .then(function () { togglePalette(false); loadReasoning(); loadModels(); checkHealth(); })
        .catch(function (err) { toast(err.message); });
    });
    list.appendChild(row);
  });
  if (!list.children.length) list.innerHTML = '<div class="side-empty">—</div>';
  renderActiveContextControl();
}
$("#model-search").addEventListener("input", function () {
  renderModelList(this.value.trim().toLowerCase());
});
$("#model-scan").addEventListener("click", async function () {
  this.textContent = "…";
  try { await jpost("/api/provider/scan", {}); } catch (e) {}
  this.textContent = t("common.scan");
  loadModels();
});
function renderReasoning(r) {
  var control = $("#reasoning-control");
  var btn = $("#reasoning-btn");
  var configured = (r && r.configured) || "";
  // The control is a stable part of the model cluster. Do not hide it while
  // model discovery is pending (or when a provider omitted capability
  // metadata); the backend negotiates/omits unsupported parameters safely.
  control.hidden = false;

  $("#reasoning-level").textContent = configured || t("model.auto");
  btn.classList.toggle("active", !!configured);
  btn.title = t("model.reasoning") + ": " + (configured || t("model.default")) +
    (r && r.adjusted ? " (effective: " + r.effective + ")" : "");

  var options = $("#reasoning-options");
  options.innerHTML = "";
  [{ value: "", label: t("model.default") }].concat((r && r.levels ? r.levels : []).map(function (lv) {
    return { value: lv, label: lv };
  })).forEach(function (item) {
    var option = el("button", "reasoning-option" + (item.value === configured ? " selected" : ""));
    option.type = "button";
    option.dataset.level = item.value;
    option.setAttribute("role", "menuitemradio");
    option.setAttribute("aria-checked", item.value === configured ? "true" : "false");
    option.tabIndex = item.value === configured ? 0 : -1;
    option.appendChild(el("span", "reasoning-check", "◆"));
    option.appendChild(el("span", "", item.label));
    options.appendChild(option);
  });
}

async function loadReasoning() {
  try {
    renderReasoning(await j("/api/reasoning"));
  } catch (e) {
    // Keep the always-visible default control; health status already reports
    // connectivity and a transient request failure must not move the toolbar.
  }
}

function toggleReasoningMenu(show) {
  var menu = $("#reasoning-menu");
  var want = show !== undefined ? show : menu.hidden;
  menu.hidden = !want;
  $("#reasoning-btn").setAttribute("aria-expanded", want ? "true" : "false");
  if (want) {
    togglePalette(false);
    setTimeout(function () {
      var selected = $("#reasoning-options .selected") || $("#reasoning-options .reasoning-option");
      if (selected) selected.focus();
    }, 0);
  }
}

var reasoningSaving = false;
$("#reasoning-btn").addEventListener("click", function () {
  if (!reasoningSaving) toggleReasoningMenu();
});
$("#reasoning-options").addEventListener("click", async function (e) {
  var option = e.target.closest("button[data-level]");
  if (!option || reasoningSaving) return;
  var btn = $("#reasoning-btn");
  reasoningSaving = true;
  toggleReasoningMenu(false);
  btn.setAttribute("aria-busy", "true");
  btn.focus();
  try {
    var got = await jpost("/api/reasoning", { level: option.dataset.level || "default" });
    renderReasoning(got);
  } catch (err) {
    toast(err.message);
    await loadModels();
  } finally {
    reasoningSaving = false;
    btn.removeAttribute("aria-busy");
  }
});
$("#reasoning-menu").addEventListener("keydown", function (e) {
  var options = $$("#reasoning-options .reasoning-option");
  if (!options.length) return;
  var index = options.indexOf(document.activeElement);
  if (e.key === "Escape") {
    e.preventDefault();
    e.stopPropagation();
    toggleReasoningMenu(false);
    $("#reasoning-btn").focus();
    return;
  }
  if (e.key === "Home" || e.key === "End" || e.key === "ArrowDown" || e.key === "ArrowUp") {
    e.preventDefault();
    if (e.key === "Home") index = 0;
    else if (e.key === "End") index = options.length - 1;
    else if (e.key === "ArrowDown") index = (index + 1) % options.length;
    else index = (index <= 0 ? options.length : index) - 1;
    options[index].focus();
  }
});
