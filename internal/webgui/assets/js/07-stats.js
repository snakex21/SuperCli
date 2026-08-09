"use strict";

/* ═══ stats pane ═══ */

function statRow(label, value) {
  var row = el("div", "stat-row");
  row.appendChild(el("span", "", label));
  var val = el("span", "v", value);
  val.title = value;
  row.appendChild(val);
  return row;
}

function statNumber(value, fallback) {
  var n = Number(value);
  return Number.isFinite(n) ? n : (fallback || 0);
}
function statNullableNumber(value) {
  var n = Number(value);
  return value !== null && value !== "" && Number.isFinite(n) ? n : null;
}
function normalizeStats(raw) {
  raw = raw || {};
  var session = raw.session || {};
  var tokens = raw.tokens || {};
  var context = raw.context || {};
  var breakdown = context.breakdown || {};
  var cost = raw.cost || {};
  var telemetry = raw.telemetry || {};
  var input = statNumber(tokens.input);
  var cached = statNumber(tokens.cached_input);
  var output = statNumber(tokens.output);
  var total = Object.prototype.hasOwnProperty.call(tokens, "total")
    ? statNumber(tokens.total) : statNumber(raw.session_tokens, input + output);
  return {
    model: raw.model || session.model || activeModelID || "",
    dailyTokens: statNumber(raw.daily_tokens),
    session: {
      id: session.id || "", title: session.title || "", provider: session.provider || "",
      providerType: session.provider_type || "", model: session.model || raw.model || activeModelID || "",
      createdAt: session.created_at || "", updatedAt: session.updated_at || "",
      messages: statNumber(session.messages), userMessages: statNumber(session.user_messages),
      assistantMessages: statNumber(session.assistant_messages), toolMessages: statNumber(session.tool_messages),
      toolCalls: statNumber(session.tool_calls),
    },
    tokens: {
      input: input, evaluatedInput: Object.prototype.hasOwnProperty.call(tokens, "evaluated_input")
        ? statNumber(tokens.evaluated_input) : Math.max(0, input - cached),
      cachedInput: cached, output: output, reasoning: statNumber(tokens.reasoning), total: total,
      hasCached: !!tokens.has_cached || cached > 0, hasReasoning: !!tokens.has_reasoning || statNumber(tokens.reasoning) > 0,
    },
    context: {
      window: statNumber(context.window), estimatedUsed: statNumber(context.estimated_used),
      percent: Math.max(0, Math.min(100, statNumber(context.percent))),
      breakdown: {
        user: statNumber(breakdown.user), assistant: statNumber(breakdown.assistant),
        tools: statNumber(breakdown.tools), other: statNumber(breakdown.other),
      },
    },
    cost: {
      state: String(cost.state || "unknown").toLowerCase(), amount: statNullableNumber(cost.amount),
      currency: cost.currency || "USD", source: String(cost.source || ""), estimated: !!cost.estimated,
      partial: !!cost.partial, calls: statNumber(cost.calls), unknownCalls: statNumber(cost.unknown_calls),
      includedCalls: statNumber(cost.included_calls), inputPerMillion: statNullableNumber(cost.input_per_million),
      cachedInputPerMillion: statNullableNumber(cost.cached_input_per_million),
      outputPerMillion: statNullableNumber(cost.output_per_million),
      cacheDiscountKnown: !!cost.cache_discount_known, manual: !!cost.manual,
    },
    telemetry: {
      scope: telemetry.scope || "",
      samples: statNumber(telemetry.samples), steps: statNumber(telemetry.steps),
      durationMS: statNumber(telemetry.duration_ms), averageMS: statNumber(telemetry.average_ms),
      modelMS: statNumber(telemetry.model_ms), toolsMS: statNumber(telemetry.tools_ms),
      cliMS: statNumber(telemetry.cli_ms), persistMS: statNumber(telemetry.persist_ms),
      modelCalls: statNumber(telemetry.model_calls), helperCalls: statNumber(telemetry.helper_calls),
      auxCalls: statNumber(telemetry.aux_calls), auxMS: statNumber(telemetry.aux_ms),
      auxShare: statNumber(telemetry.aux_share),
      offTurnCalls: statNumber(telemetry.off_turn_calls), offTurnMS: statNumber(telemetry.off_turn_ms),
      failedCalls: statNumber(telemetry.failed_calls), canceledCalls: statNumber(telemetry.canceled_calls),
      toolFailures: statNumber(telemetry.tool_failures), bottleneck: telemetry.bottleneck || "",
      bottleneckShare: statNumber(telemetry.bottleneck_share),
      signals: Array.isArray(telemetry.signals) ? telemetry.signals : [],
      tools: Array.isArray(telemetry.tools) ? telemetry.tools : [],
    },
  };
}
function statsURL() {
  return "/api/stats" + (activeSessionID ? "?session=" + encodeURIComponent(activeSessionID) : "");
}
function normalizedCostState(cost) {
  if (cost && cost.partial) return "partial";
  var state = cost && cost.state || "unknown";
  return ["estimated", "manual", "subscription", "local", "free", "unknown"].indexOf(state) >= 0 ? state : "unknown";
}
function costStateLabel(cost) { return t("cost." + normalizedCostState(cost)); }
function costSourceLabel(source) {
  if (!source) return "";
  var key = "cost.source." + source;
  var label = t(key);
  return label === key ? source : label;
}
function costPrimary(cost) {
  var state = normalizedCostState(cost);
  if ((state === "estimated" || state === "manual" || state === "partial") && cost.amount !== null) {
    return (state === "manual" ? "" : "~") + fmtMoney(cost.amount, cost.currency);
  }
  if (state === "subscription") return t("cost.subscriptionValue");
  if (state === "local") return t("cost.localValue");
  if (state === "free") return t("cost.freeValue");
  if (state === "partial") return t("cost.partialValue");
  return t("cost.unknownValue");
}
function costMeta(cost) {
  var parts = [costStateLabel(cost)];
  var source = costSourceLabel(cost.source);
  if (source && ["estimated", "manual", "partial"].indexOf(normalizedCostState(cost)) >= 0) parts.push(source);
  return parts.join(" · ");
}
function costCoverage(cost) {
  var parts = [];
  if (cost.unknownCalls) parts.push(fmtCountLabel(cost.unknownCalls, "cost.unknownCalls"));
  if (cost.includedCalls) parts.push(fmtCountLabel(cost.includedCalls, "cost.includedCalls"));
  return parts.join(" · ");
}
function fmtCountLabel(value, baseKey) {
  var category = "other";
  try { category = new Intl.PluralRules(statsLocale()).select(value); } catch (e) {}
  var key = baseKey + "." + category;
  var label = t(key);
  if (label === key) label = t(baseKey + ".other");
  return fmtInteger(value) + " " + label;
}
function appendCompactContext(parent, context) {
  if (!context || context.window <= 0) return;
  var pct = Math.max(0, Math.min(100, context.percent));
  var bar = el("div", "ctx-bar");
  bar.setAttribute("role", "progressbar");
  bar.setAttribute("aria-label", t("stats.currentContext"));
  bar.setAttribute("aria-valuemin", "0");
  bar.setAttribute("aria-valuemax", "100");
  bar.setAttribute("aria-valuenow", String(pct));
  bar.setAttribute("aria-valuetext", pct + "% · " + fmtInteger(context.estimatedUsed) + " / " + fmtInteger(context.window));
  var fill = el("div");
  fill.style.width = pct + "%";
  bar.appendChild(fill);
  parent.appendChild(bar);
  parent.appendChild(el("div", "stats-note", t("stats.currentContext") + ": " + pct + "% · " +
    fmtCompactNumber(context.estimatedUsed) + " / " + fmtCompactNumber(context.window)));
}
function appendSidebarCost(parent, cost) {
  var state = normalizedCostState(cost);
  var block = el("div", "side-cost cost-state-" + state);
  block.appendChild(el("div", "side-cost-label", t("stats.totalCost")));
  block.appendChild(el("div", "side-cost-value" + (cost.amount === null ? " text" : ""), costPrimary(cost)));
  block.appendChild(el("div", "side-cost-meta", costMeta(cost)));
  var coverage = costCoverage(cost);
  if (coverage) block.appendChild(el("div", "side-cost-coverage", coverage));
  parent.appendChild(block);
}

var statsRenderSeq = 0;
async function renderStats() {
  var box = $("#stats-block");
  if (!box || ui.sidebarHidden) return;
  var seq = ++statsRenderSeq;
  var requestSession = activeSessionID;
  box.innerHTML = "";
  box.setAttribute("role", "region");
  box.setAttribute("aria-label", t("side.stats"));
  box.setAttribute("aria-live", "polite");
  box.setAttribute("aria-busy", "true");

  if (lastTurn) {
    box.appendChild(el("div", "stats-head", t("stats.turn")));
    box.appendChild(statRow(t("stats.model"), activeModelID || "—"));
    var ev = lastTurn.ev;
    var evalTok = (ev.tok_in || 0) - (ev.tok_cached || 0);
    if (ev.tok_cached) box.appendChild(statRow("cache / eval / gen", fmtCompactNumber(ev.tok_cached) + " / " + fmtCompactNumber(evalTok) + " / " + fmtCompactNumber(ev.tok_out)));
    else if (ev.tok_total) box.appendChild(statRow("in / gen", fmtCompactNumber(ev.tok_in) + " / " + fmtCompactNumber(ev.tok_out)));
    if (ev.cache_hit_pct) box.appendChild(statRow(t("run.cached"), ev.cache_hit_pct + "%"));
    if (ev.reasoning_tok) box.appendChild(statRow(t("run.think"), fmtCompactNumber(ev.reasoning_tok)));
    if (lastTurn.tools) box.appendChild(statRow(t("run.tools"), String(lastTurn.tools)));
  }

  var cfgPromise = j("/api/config").catch(function () { return null; });
  try {
    var stats = normalizeStats(await j(statsURL()));
    if (seq !== statsRenderSeq || requestSession !== activeSessionID) return;
    box.appendChild(el("div", "stats-head", t("stats.sessionSection")));
    if (stats.session.provider) box.appendChild(statRow(t("stats.provider"), stats.session.provider));
    box.appendChild(statRow(t("stats.model"), stats.session.model || stats.model || "—"));
    box.appendChild(statRow(t("stats.totalTokens"), fmtCompactNumber(stats.tokens.total)));
    if (stats.dailyTokens > 0) box.appendChild(statRow(t("stats.daily"), fmtCompactNumber(stats.dailyTokens)));
    appendCompactContext(box, stats.context);
    appendSidebarCost(box, stats.cost);
  } catch (e) {
    if (seq !== statsRenderSeq) return;
    box.appendChild(el("div", "side-empty", t("common.error") + ": " + e.message));
  }

  // Delegations stay visible when present, but an empty section would make the
  // narrow inspector feel like a dashboard rather than a compact HUD.
  if (workersSeen.length) {
    box.appendChild(el("div", "stats-head", t("stats.workers")));
    workersSeen.slice(-8).forEach(function (wk) {
      box.appendChild(statRow(wk.name + (wk.status ? " · " + wk.status : ""), clip(wk.summary, 40) || "—"));
    });
  }

  // Orchestrator switch — visible without digging (config.toml knob).
  var cfg = await cfgPromise;
  if (seq === statsRenderSeq && cfg) {
    var knob = null;
    (cfg.knobs || []).forEach(function (k) { if (k.key === "orchestrator") knob = k; });
    if (knob) {
      box.appendChild(el("div", "stats-head", t("stats.orch")));
      var row = el("div", "stat-row");
      row.appendChild(el("span", "", t("stats.orchDesc")));
      var seg = el("span", "seg");
      ["default", "on", "off"].forEach(function (st) {
        var label = st === "default" ? t("stats.orchAuto") : (st === "on" ? t("stats.orchOn") : t("stats.orchOff"));
        var b = el("button", st === (knob.state || "default") ? "on" : "", label);
        b.type = "button";
        b.addEventListener("click", function () {
          jpost("/api/config", { key: "orchestrator", value: st })
            .then(renderStats)
            .catch(function (e2) { toast(e2.message); });
        });
        seg.appendChild(b);
      });
      row.appendChild(seg);
      box.appendChild(row);
      // Orchestrator model — which model plays the coordinator. A
      // compact picker like the model palette: button + dropdown list.
      var knobM = null;
      (cfg.knobs || []).forEach(function (k) { if (k.key === "orchestrator_model") knobM = k; });
      if (knobM) {
        var rowM = el("div", "stat-row");
        rowM.appendChild(el("span", "", t("stats.orchModel")));
        supercliOrchPicker(rowM, knobM, function (v) {
          jpost("/api/config", { key: "orchestrator_model", value: v })
            .then(renderStats)
            .catch(function (e2) { toast(e2.message); });
        });
        box.appendChild(rowM);
      }
    }
  }
  if (seq === statsRenderSeq) box.setAttribute("aria-busy", "false");
}

// supercliOrchPicker renders the orchestrator-model selector: a compact
// button showing the current value plus a dropdown list of known models,
// mirroring the model palette rows (.prow). Clicking a row calls onSave
// with the "provider/model" ref (or "" for the main model).
function supercliOrchPicker(container, knobM, onSave) {
  var wrap = el("span", "orch-pick");
  var btn = el("button", "orch-btn", knobM.raw || t("stats.orchModelDef"));
  btn.type = "button";
  var pop = el("div", "orch-pop");
  pop.hidden = true;
  function fill() {
    var cur = knobM.raw || "";
    pop.innerHTML = "";
    var phead = el("div", "phead");
    var search = el("input");
    search.placeholder = t("model.search");
    search.setAttribute("autocomplete", "off");
    phead.appendChild(search);
    var list = el("div", "plist");
    function renderList(filter) {
      list.innerHTML = "";
      var base = el("div", "prow" + (cur === "" ? " active" : ""));
      base.appendChild(el("span", "state-dot on"));
      base.appendChild(el("span", "pid", t("stats.orchModelDef")));
      base.addEventListener("click", function () {
        pop.hidden = true;
        if (cur !== "") onSave("");
      });
      list.appendChild(base);
      var shown = 0;
      (modelCache || []).forEach(function (m) {
        if (m.hidden) return;
        if (filter && (m.id + " " + (m.provider || "")).toLowerCase().indexOf(filter) < 0) return;
        shown++;
        var ref = (m.provider && m.provider !== activeProviderID) ? m.provider + "/" + m.id : m.id;
        var rowEl = el("div", "prow" + (ref === cur ? " active" : ""));
        rowEl.appendChild(el("span", "state-dot on"));
        rowEl.appendChild(el("span", "pid", m.id));
        if (m.provider && m.provider !== activeProviderID) rowEl.appendChild(el("span", "pprov", m.provider));
        rowEl.addEventListener("click", function () {
          pop.hidden = true;
          if (ref !== cur) onSave(ref);
        });
        list.appendChild(rowEl);
      });
      if (!shown) list.appendChild(el("div", "side-empty", "—"));
    }
    search.addEventListener("input", function () {
      renderList(this.value.trim().toLowerCase());
    });
    renderList("");
    pop.appendChild(phead);
    pop.appendChild(list);
    search.focus();
  }
  btn.addEventListener("click", function () {
    if (pop.hidden) fill();
    pop.hidden = !pop.hidden;
  });
  document.addEventListener("click", function (e) {
    if (!wrap.contains(e.target)) pop.hidden = true;
  });
  wrap.appendChild(btn);
  wrap.appendChild(pop);
  container.appendChild(wrap);
}

