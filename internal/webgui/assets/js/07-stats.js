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
  var activeContext = raw.active_context || null;
  var breakdown = context.breakdown || {};
  var cost = raw.cost || {};
  var telemetry = raw.telemetry || {};
  var generation = raw.generation_speed || {};
  var generationRate = generation.tokens_per_second;
  var generationKnown = generation.scope === "session" && typeof generationRate === "number" &&
    Number.isFinite(generationRate) && generationRate > 0 && statNumber(generation.samples) > 0 &&
    statNumber(generation.stream_ms) > 0 && statNumber(generation.output_tokens) > 0;
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
      hasSnapshot: Object.prototype.hasOwnProperty.call(context, "has_snapshot")
        ? !!context.has_snapshot : statNumber(context.estimated_used) > 0,
      windowSource: String(context.window_source || ""),
      breakdown: {
        user: statNumber(breakdown.user), assistant: statNumber(breakdown.assistant),
        tools: statNumber(breakdown.tools), other: statNumber(breakdown.other),
      },
    },
    activeContext: activeContext ? {
      model: activeContext.model || "", provider: activeContext.provider || "",
      window: statNumber(activeContext.window), windowSource: String(activeContext.window_source || ""),
    } : null,
    cost: {
      state: String(cost.state || "unknown").toLowerCase(), amount: statNullableNumber(cost.amount),
      currency: cost.currency || "USD", source: String(cost.source || ""), estimated: !!cost.estimated,
      pricingCurrency: cost.pricing_currency || "USD",
      partial: !!cost.partial, calls: statNumber(cost.calls), unknownCalls: statNumber(cost.unknown_calls),
      includedCalls: statNumber(cost.included_calls), inputPerMillion: statNullableNumber(cost.input_per_million),
      cachedInputPerMillion: statNullableNumber(cost.cached_input_per_million),
      outputPerMillion: statNullableNumber(cost.output_per_million),
      cacheDiscountKnown: !!cost.cache_discount_known, manual: !!cost.manual,
      ratesPending: !!cost.rates_pending,
      ratesPendingKnown: Object.prototype.hasOwnProperty.call(cost, "rates_pending"),
      ratesGeneration: Object.prototype.hasOwnProperty.call(cost, "rates_generation") ? cost.rates_generation : null,
    },
    pricing: raw.pricing ? normalizeStats({cost: raw.pricing}).cost : null,
    lastTurn: raw.last_turn && raw.last_turn.ev ? raw.last_turn : null,
    generationSpeed: {
      scope: "session", rate: generationKnown ? generationRate : null,
      samples: statNumber(generation.samples), calls: statNumber(generation.calls),
      streamMS: statNumber(generation.stream_ms), outputTokens: statNumber(generation.output_tokens),
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
  if (cost.source === "fx_missing") return t("cost.fxMissingValue");
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
  context = context || {};
  var label = context.hasSnapshot ? t("stats.ctx") : t("stats.currentContext");
  var hasWindow = context.window > 0;
  var fallbackWindow = String(context.windowSource || "").indexOf("fallback") === 0;
  var limit = hasWindow ? (fallbackWindow ? "~" : "") + fmtCompactNumber(context.window) : "—";
  if (!hasWindow || context.hasSnapshot === false || context.estimatedUsed == null) {
    var used = context.hasSnapshot === false || context.estimatedUsed == null
      ? "—" : fmtCompactNumber(context.estimatedUsed);
    var note = el("div", "stats-note", label + ": " + used + " / " +
      limit);
    if (context.hasSnapshot === false || context.estimatedUsed == null) note.title = t("usage.contextEmpty");
    parent.appendChild(note);
    return;
  }
  var pct = Math.max(0, Math.min(100, context.percent));
  var bar = el("div", "ctx-bar");
  bar.setAttribute("role", "progressbar");
  bar.setAttribute("aria-label", label);
  bar.setAttribute("aria-valuemin", "0");
  bar.setAttribute("aria-valuemax", "100");
  bar.setAttribute("aria-valuenow", String(pct));
  bar.setAttribute("aria-valuetext", pct + "% · " + fmtInteger(context.estimatedUsed) + " / " +
    (fallbackWindow ? "~" : "") + fmtInteger(context.window));
  var fill = el("div");
  fill.style.width = pct + "%";
  bar.appendChild(fill);
  parent.appendChild(bar);
  parent.appendChild(el("div", "stats-note", label + ": " + pct + "% · " +
    fmtCompactNumber(context.estimatedUsed) + " / " + limit));
}
function appendActiveContext(parent, active, context, session) {
  if (!active) return;
  // The previous request keeps its own denominator. Only a distinct selected
  // model/profile/budget needs a second line; it has no invented usage percent.
  if (context && (context.hasSnapshot === false || (session &&
    active.model === session.model && active.provider === session.provider && active.window === context.window))) return;
  var limit = active.window > 0 ?
    (active.windowSource.indexOf("fallback") === 0 ? "~" : "") + fmtCompactNumber(active.window) : "—";
  var note = el("div", "stats-note stats-active-context", t("stats.contextWindow") + ": " +
    (active.model || "—") + " · " + limit);
  note.title = (active.provider ? active.provider + " / " : "") + (active.model || "—") +
    " · " + (active.window > 0 ? fmtInteger(active.window) : "—");
  parent.appendChild(note);
}
function appendSidebarCost(parent, cost) {
  var state = normalizedCostState(cost);
  var sessionID = activeSessionID;
  var block = el("button", "side-cost cost-state-" + state);
  block.type = "button";
  block.title = t("cost.openDetails");
  block.setAttribute("aria-label", t("cost.openDetails") + ": " + costPrimary(cost));
  block.setAttribute("aria-haspopup", "dialog");
  block.appendChild(i18nEl("span", "side-cost-label", "stats.totalCost"));
  block.appendChild(el("span", "side-cost-value" + (cost.amount === null ? " text" : ""), costPrimary(cost)));
  block.appendChild(el("span", "side-cost-meta", costMeta(cost)));
  var coverage = costCoverage(cost);
  if (coverage) block.appendChild(el("span", "side-cost-coverage", coverage));
  if (cost.ratesPending) block.appendChild(i18nEl("span", "side-cost-meta", "cost.pending"));
  block.addEventListener("click", function () { openCostDetails(sessionID); });
  parent.appendChild(block);
}

function appendSidebarUsage(parent, tokens) {
  var sessionID = activeSessionID;
  var row = el("button", "stat-row stat-action stats-usage-button");
  row.type = "button";
  row.title = t("usage.openDetails");
  row.setAttribute("aria-label", t("usage.openDetails") + ": " + fmtInteger(tokens.total));
  row.setAttribute("aria-haspopup", "dialog");
  row.appendChild(i18nEl("span", "", "stats.totalTokens"));
  row.appendChild(el("span", "v", fmtCompactNumber(tokens.total)));
  row.addEventListener("click", function () { openUsageDetails(sessionID); });
  parent.appendChild(row);
}
function usagePurposeLabel(purpose) {
  var key = "usage.purpose." + purpose, translated = t(key);
  return translated === key ? purpose || "—" : translated + " (" + purpose + ")";
}
function usageCoverage(reported, calls) {
  return t("usage.coverage").replace("{n}", fmtInteger(reported)).replace("{calls}", fmtInteger(calls));
}
function appendUsageCells(tr, row) {
  var calls = statNumber(row.calls);
  ["input", "cached_input", "output", "reasoning", "calls", "total"].forEach(function (key) {
    var subset = key === "cached_input" || key === "reasoning";
    var known = !subset || !!row[key === "cached_input" ? "has_cached" : "has_reasoning"];
    var reported = statNumber(row[key === "cached_input" ? "cached_reported_calls" : "reasoning_reported_calls"]);
    var partial = subset && known && reported < calls;
    var td = el("td", "cost-details-number", known ? fmtInteger(row[key]) + (partial ? "*" : "") : "—");
    if (subset) {
      td.title = known ? usageCoverage(reported, calls) : t("usage.unreported");
      td.setAttribute("aria-label", td.textContent + "; " + td.title);
    }
    tr.appendChild(td);
  });
}
function appendUsageToolEstimate(parent, row) {
  if (!row.context_tool_estimate_known || row.context_estimate_source !== "request-shape") return;
  var estimate = el("p", "cost-details-caption", t("usage.toolEstimate")
    .replace("{n}", fmtInteger(row.context_tool_estimate)));
  estimate.title = usageCoverage(row.context_estimate_calls, row.calls);
  estimate.setAttribute("aria-label", estimate.textContent + "; " + estimate.title);
  parent.appendChild(estimate);
}
function usageTableHeader(modelColumn) {
  var header = el("tr");
  [modelColumn, "stats.inputTokens", "stats.cachedInput", "stats.outputTokens", "stats.reasoningTokens", "stats.calls", "stats.totalTokens"].forEach(function (key) {
    var th = i18nEl("th", "", key); th.setAttribute("scope", "col");
    header.appendChild(th);
  });
  return header;
}
function appendUsageOutputCharts(parent, rows) {
  if (!rows.length) return;
  var charts = el("section", "usage-output-charts");
  charts.setAttribute("aria-label", t("usage.outputChart"));
  charts.appendChild(i18nEl("h3", "usage-chart-title", "usage.outputChart"));
  charts.appendChild(i18nEl("p", "cost-details-caption", "usage.outputChartNote"));
  rows.forEach(function (row) {
    var item = el("div", "usage-output-chart");
    var heading = el("div", "usage-chart-heading");
    var identity = el("div", "usage-chart-model", row.model || "—");
    identity.appendChild(el("span", "cost-details-caption", row.provider || "—"));
    heading.appendChild(identity);
    var output = Math.max(0, statNumber(row.output));
    var known = !!row.has_reasoning;
    var reasoning = known ? Math.min(output, Math.max(0, statNumber(row.reasoning))) : 0;
    var remaining = output - reasoning;
    heading.appendChild(el("span", "usage-chart-total", t("stats.outputTokens") + ": " + fmtInteger(output)));
    item.appendChild(heading);
    var thinkingText = t("usage.reportedThinking") + ": " + (known ? fmtInteger(reasoning) : "—");
    var remainingText = t("usage.remainingOutput") + ": " + fmtInteger(remaining);
    var coverage = known ? usageCoverage(row.reasoning_reported_calls, row.calls) : t("usage.unreported");
    var bar = el("div", "usage-chart-bar" + (known ? "" : " usage-chart-unknown"));
    bar.setAttribute("role", "img");
    bar.setAttribute("aria-label", thinkingText + "; " + remainingText + "; " + coverage);
    var thinking = el("span", "usage-chart-thinking"), rest = el("span", "usage-chart-remaining");
    thinking.style.width = (output ? reasoning / output * 100 : 0) + "%";
    rest.style.width = (output ? remaining / output * 100 : 0) + "%";
    thinking.setAttribute("aria-hidden", "true"); rest.setAttribute("aria-hidden", "true");
    bar.appendChild(thinking); bar.appendChild(rest); item.appendChild(bar);
    var legend = el("div", "usage-chart-legend");
    legend.appendChild(el("span", "usage-chart-thinking-label", thinkingText));
    legend.appendChild(el("span", "usage-chart-remaining-label", remainingText));
    item.appendChild(legend);
    if (!known || statNumber(row.reasoning_reported_calls) < statNumber(row.calls))
      item.appendChild(el("p", "cost-details-caption usage-chart-coverage", coverage));
    charts.appendChild(item);
  });
  parent.appendChild(charts);
}
function appendUsageTiming(parent, row, compact) {
  if (!row.has_timing || statNumber(row.timing_reported_calls) < 1) return;
  var timing = el("div", "usage-timing" + (compact ? " usage-timing-compact" : ""));
  timing.appendChild(el("div", "usage-timing-total", t("usage.modelTime") + ": " + fmtDuration(Math.max(0, statNumber(row.duration_ms)))));
  if (statNumber(row.timing_reported_calls) < statNumber(row.calls))
    timing.appendChild(el("p", "cost-details-caption", usageCoverage(row.timing_reported_calls, row.calls)));
  var phasesKnown = statNumber(row.ttft_reported_calls) > 0 && statNumber(row.stream_reported_calls) > 0;
  if (phasesKnown) {
    var waiting = Math.max(0, statNumber(row.ttft_ms)), stream = Math.max(0, statNumber(row.stream_ms));
    var waitingText = t("usage.firstOutputWait") + ": " + fmtDuration(waiting);
    var streamText = t("usage.responseStream") + ": " + fmtDuration(stream);
    var coverage = usageCoverage(row.stream_reported_calls, row.calls);
    if (!compact) {
      var bar = el("div", "usage-chart-bar usage-time-bar");
      bar.setAttribute("role", "img"); bar.setAttribute("aria-label", waitingText + "; " + streamText + "; " + coverage);
      var waitPart = el("span", "usage-chart-thinking"), streamPart = el("span", "usage-chart-remaining");
      var measured = waiting + stream;
      waitPart.style.width = (measured ? waiting / measured * 100 : 0) + "%";
      streamPart.style.width = (measured ? stream / measured * 100 : 0) + "%";
      waitPart.setAttribute("aria-hidden", "true"); streamPart.setAttribute("aria-hidden", "true");
      bar.appendChild(waitPart); bar.appendChild(streamPart); timing.appendChild(bar);
    }
    var legend = el("div", "usage-chart-legend");
    legend.appendChild(el("span", compact ? "" : "usage-chart-thinking-label", waitingText));
    legend.appendChild(el("span", compact ? "" : "usage-chart-remaining-label", streamText));
    timing.appendChild(legend);
    if (stream >= 1) timing.appendChild(el("div", "cost-details-caption usage-stream-speed", t("usage.streamSpeed") + ": " +
      (Math.max(0, statNumber(row.stream_output_tokens)) * 1000 / stream).toFixed(1) + " tok/s"));
    if (statNumber(row.stream_reported_calls) < statNumber(row.calls) &&
        statNumber(row.stream_reported_calls) !== statNumber(row.timing_reported_calls))
      timing.appendChild(el("p", "cost-details-caption", coverage));
  }
  parent.appendChild(timing);
}
function renderUsageDetails(data) {
  var body = el("div", "cost-details-body usage-details-body"), totals = data.totals || {};
  var rows = Array.isArray(data.rows) ? data.rows : [];
  var hasLegacy = statNumber(totals.legacy_records) > 0 || rows.some(function (row) {
    return statNumber(row.legacy_records) > 0 || (Array.isArray(row.purposes) && row.purposes.some(function (purpose) {
      return purpose.purpose === "legacy" || statNumber(purpose.legacy_records) > 0;
    }));
  });
  body.appendChild(el("div", "cost-details-total", t("stats.totalTokens") + ": " + fmtInteger(totals.total)));
  body.appendChild(i18nEl("p", "cost-details-caption", "usage.partsNote"));
  body.appendChild(i18nEl("p", "cost-details-caption", "usage.unreported"));
  body.appendChild(i18nEl("p", "cost-details-caption", "usage.partialNote"));
  if (hasLegacy) body.appendChild(i18nEl("p", "cost-details-caption usage-legacy-note", "usage.legacyNote"));
  appendUsageOutputCharts(body, rows);
  if (rows.some(function (row) { return row.has_timing; })) body.appendChild(i18nEl("p", "cost-details-caption", "usage.timingNote"));
  if (!rows.length) body.appendChild(i18nEl("div", "usage-empty", "cost.empty"));
  else {
    var scroll = el("div", "cost-details-scroll"); scroll.tabIndex = 0;
    scroll.setAttribute("role", "region"); scroll.setAttribute("aria-label", t("usage.detailsTitle"));
    var table = el("table", "cost-details-table usage-details-table");
    var head = el("thead"); head.appendChild(usageTableHeader("stats.model")); table.appendChild(head);
    var tbody = el("tbody");
    rows.forEach(function (row) {
      var tr = el("tr"), identity = el("th", "cost-details-model", row.model || "—");
      identity.setAttribute("scope", "row");
      identity.appendChild(el("span", "cost-details-caption", row.provider || "—"));
      tr.appendChild(identity); appendUsageCells(tr, row); tbody.appendChild(tr);
      var purposes = Array.isArray(row.purposes) ? row.purposes : [];
      if (purposes.length || row.has_timing || (row.context_tool_estimate_known && row.context_estimate_source === "request-shape")) {
        var detailRow = el("tr", "usage-purpose-row"), cell = el("td"); cell.setAttribute("colspan", "7");
        if (purposes.length) {
          var details = el("details", "cost-details-rates usage-purpose-details");
          details.appendChild(i18nEl("summary", "", "usage.purposes"));
          var purposeTable = el("table", "cost-details-table usage-purpose-table");
          var purposeHead = el("thead"); purposeHead.appendChild(usageTableHeader("usage.purposes")); purposeTable.appendChild(purposeHead);
          var purposeBody = el("tbody");
          purposes.forEach(function (purpose) {
            var line = el("tr"), label = el("th", "", usagePurposeLabel(purpose.purpose));
            label.setAttribute("scope", "row"); appendUsageTiming(label, purpose, true);
            line.appendChild(label); appendUsageCells(line, purpose);
            purposeBody.appendChild(line);
          });
          purposeTable.appendChild(purposeBody); details.appendChild(purposeTable); cell.appendChild(details);
        }
        appendUsageTiming(cell, row, false); appendUsageToolEstimate(cell, row);
        detailRow.appendChild(cell); tbody.appendChild(detailRow);
      }
    });
    table.appendChild(tbody); scroll.appendChild(table); body.appendChild(scroll);
  }
  if (rows.length !== 1) appendUsageToolEstimate(body, totals);
  var unattributed = data.legacy_unattributed || {};
  if (statNumber(unattributed.total) > 0) body.appendChild(el("p", "cost-details-caption", t("usage.unattributed")
    .replace("{n}", fmtInteger(unattributed.total))));
  var gap = data.legacy_gap || {};
  if (statNumber(gap.total) > 0) body.appendChild(el("p", "cost-details-caption usage-gap-note", t("usage.legacyGap")
    .replace("{n}", fmtInteger(gap.total))));
  return body;
}
var usageDetailsState = null;
function refreshUsageDetails() {
  return usageDetailsState ? usageDetailsState.refresh() : Promise.resolve();
}
function openUsageDetails(sessionID) {
  if (activeAppDialog) activeAppDialog(null);
  var previousFocus = document.activeElement;
  var overlay = el("div", "app-dialog-overlay usage-details-overlay"), panel = el("div", "app-dialog cost-details-dialog usage-details-dialog");
  panel.setAttribute("role", "dialog"); panel.setAttribute("aria-modal", "true"); panel.setAttribute("aria-labelledby", "usage-details-title");
  var title = i18nEl("h2", "app-dialog-title", "usage.detailsTitle"); title.id = "usage-details-title"; panel.appendChild(title);
  var scopeLabel = el("label", "cost-details-scope"); scopeLabel.appendChild(i18nEl("span", "", "cost.scope"));
  var scope = el("select", "field-select");
  [["session", "cost.scope.session"], ["all", "cost.scope.all"]].forEach(function (choice) {
    var option = i18nEl("option", "", choice[1]); option.value = choice[0]; option.disabled = choice[0] === "session" && !sessionID; scope.appendChild(option);
  });
  scope.value = sessionID ? "session" : "all"; scopeLabel.appendChild(scope); panel.appendChild(scopeLabel);
  var content = el("div"); content.setAttribute("aria-live", "polite"); panel.appendChild(content);
  var actions = el("div", "app-dialog-actions"), refresh = i18nEl("button", "btn", "common.refresh"), close = i18nEl("button", "btn", "common.close");
  refresh.type = "button"; close.type = "button"; actions.appendChild(refresh); actions.appendChild(close); panel.appendChild(actions);
  overlay.appendChild(panel); document.body.appendChild(overlay);
  var state = {closed: false, seq: 0, request: null, refresh: load}; usageDetailsState = state;
  function finish() {
    if (state.closed) return;
    state.closed = true; state.seq++;
    if (state.request) state.request.abort();
    document.removeEventListener("keydown", onKeyDown, true); overlay.remove();
    if (usageDetailsState === state) usageDetailsState = null;
    if (activeAppDialog === finish) activeAppDialog = null;
    var fallback = $("#stats-block .stats-usage-button");
    if (previousFocus && previousFocus.isConnected) previousFocus.focus(); else if (fallback) fallback.focus();
  }
  function onKeyDown(event) {
    if (event.key === "Escape") { event.preventDefault(); event.stopImmediatePropagation(); finish(); }
    else if (event.key === "Tab") {
      var controls = Array.from(panel.querySelectorAll("button:not([disabled]), select, summary, [tabindex='0']"));
      var first = controls[0], last = controls[controls.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    }
  }
  function current(seq, requestScope) {
    if (state.closed || state.seq !== seq || scope.value !== requestScope) return false;
    if (requestScope === "session" && activeSessionID !== sessionID) { finish(); return false; }
    return true;
  }
  async function load() {
    if (state.closed) return;
    var seq = ++state.seq, requestScope = scope.value;
    if (state.request) state.request.abort(); state.request = new AbortController();
    if (!current(seq, requestScope)) return;
    if (content.contains(document.activeElement) || document.activeElement === refresh) scope.focus();
    content.replaceChildren(i18nEl("div", "usage-loading", "common.loading")); content.setAttribute("aria-busy", "true"); refresh.disabled = true;
    try {
      var data = await j("/api/usage" + (requestScope === "session" ? "?session=" + encodeURIComponent(sessionID) : ""), {signal: state.request.signal});
      if (current(seq, requestScope)) content.replaceChildren(renderUsageDetails(data));
    } catch (error) {
      if (current(seq, requestScope)) content.replaceChildren(el("div", "usage-empty", t("common.error") + ": " + error.message));
    } finally {
      if (!state.closed && seq === state.seq) { content.setAttribute("aria-busy", "false"); refresh.disabled = false; }
    }
  }
  activeAppDialog = finish; document.addEventListener("keydown", onKeyDown, true);
  close.addEventListener("click", finish); refresh.addEventListener("click", load); scope.addEventListener("change", load);
  overlay.addEventListener("mousedown", function (event) { if (event.target === overlay) finish(); });
  close.focus(); return load();
}

var costDetailsState = null;
var costRatesReadyMemo = Object.create(null), costRatesReadyKeys = [];
var costRatesLegacyPending = false, costRatesLegacyGeneration = 0;
function costRatesGenerationKey(generation) {
  return generation !== null && generation !== undefined ? "generation:" + String(generation) :
    "legacy:" + costRatesLegacyGeneration;
}
function notifyCostRatesReady(entry) {
  if (!entry.ready) return;
  var hud = entry.hud && !entry.hudDelivered && entry.hud();
  var popup = entry.popup && !entry.popupDelivered && entry.popup();
  if (hud) entry.hudDelivered = true;
  if (popup) entry.popupDelivered = true;
  if (hud || popup) renderStats();
  if (popup) refreshCostDetails();
}
function waitCostRatesReady(generation) {
  var key = costRatesGenerationKey(generation), entry = costRatesReadyMemo[key];
  if (entry) return entry.promise || Promise.resolve(entry.ready);
  entry = {ready: false, promise: null};
  costRatesReadyMemo[key] = entry; costRatesReadyKeys.push(key);
  // Retain recent completed generations for stale-response protection, while
  // bounding long-running UI memory and never evicting an active waiter.
  if (costRatesReadyKeys.length > 16) {
    costRatesReadyKeys.slice().forEach(function (oldKey) {
      if (costRatesReadyKeys.length > 16 && oldKey !== key && !costRatesReadyMemo[oldKey].promise) {
        delete costRatesReadyMemo[oldKey]; costRatesReadyKeys.splice(costRatesReadyKeys.indexOf(oldKey), 1);
      }
    });
  }
  entry.promise = j("/api/costs/ready").then(function (response) {
    entry.ready = !!(response && response.ok === true);
    return entry.ready;
  }, function () {
    return false; // No automatic retry after timeout, cancellation or error.
  }).finally(function () {
    entry.promise = null;
    notifyCostRatesReady(entry);
    entry.hud = null; entry.popup = null;
  });
  return entry.promise;
}
function observeCostRatesPending(pending, generation, kind, isCurrent) {
  // Missing fields on an older server are not an observed idle cycle.
  if (pending === null || pending === undefined) return;
  if (generation === null || generation === undefined) {
    if (!pending && costRatesLegacyPending) costRatesLegacyGeneration++;
    costRatesLegacyPending = pending;
  }
  if (!pending) return;
  var promise = waitCostRatesReady(generation);
  var entry = costRatesReadyMemo[costRatesGenerationKey(generation)];
  entry[kind] = isCurrent;
  if (!entry.promise) {
    // A report captured just before completion may arrive afterwards. Notify
    // its still-current consumer once, without issuing another ready request.
    notifyCostRatesReady(entry);
    entry.hud = null; entry.popup = null;
  }
  return promise;
}
function costRatesContextKey() {
  return JSON.stringify([activeSessionID, activeModelID, typeof activeProviderID === "string" ? activeProviderID : ""]);
}
function refreshCostDetails() {
  if (costDetailsState) return costDetailsState.refresh();
  return Promise.resolve();
}
function costDetailsTokens(row) {
  return t("stats.inputTokens") + ": " + fmtInteger(row.input) + " · " +
    t("stats.cachedInput") + ": " + fmtInteger(row.cached_input) + " · " +
    t("stats.outputTokens") + ": " + fmtInteger(row.output) + " · " +
    t("stats.reasoningTokens") + ": " + fmtInteger(row.reasoning);
}
function renderCostDetails(data, fetchRates) {
  var body = el("div", "cost-details-body");
  var rows = Array.isArray(data.rows) ? data.rows : [];
  var total = normalizeStats({cost: data.total}).cost;
  total.currency = data.currency || total.currency;
  var summary = el("div", "cost-details-total", t("stats.totalCost") + ": " + costPrimary(total));
  summary.appendChild(el("span", "cost-details-caption", costMeta(total)));
  body.appendChild(summary);
  if (!rows.length) body.appendChild(i18nEl("div", "usage-empty", "cost.empty"));
  else {
    var scroll = el("div", "cost-details-scroll");
    scroll.tabIndex = 0;
    scroll.setAttribute("role", "region");
    scroll.setAttribute("aria-label", t("cost.detailsTitle"));
    var table = el("table", "cost-details-table");
    var head = el("thead"), header = el("tr");
    ["stats.model", "stats.totalTokens", "cost.apiCalls", "stats.totalCost"].forEach(function (key) {
      var cell = i18nEl("th", "", key);
      cell.setAttribute("scope", "col");
      if (key === "cost.apiCalls") cell.title = t("cost.callsHint");
      header.appendChild(cell);
    });
    head.appendChild(header); table.appendChild(head);
    var tbody = el("tbody");
    rows.forEach(function (row) {
      var tr = el("tr");
      var identity = el("th", "cost-details-model", row.model || "—");
      identity.setAttribute("scope", "row");
      identity.appendChild(el("span", "cost-details-caption", row.provider || "—"));
      tr.appendChild(identity);
      var tokens = el("td", "cost-details-number", fmtInteger(row.total));
      tokens.title = costDetailsTokens(row);
      tokens.setAttribute("aria-label", t("stats.totalTokens") + ": " + fmtInteger(row.total) + "; " + tokens.title);
      tr.appendChild(tokens);
      tr.appendChild(el("td", "cost-details-number", fmtInteger(row.calls)));
      var cost = normalizeStats({cost: row.cost}).cost;
      cost.currency = data.currency || cost.currency;
      var amount = el("td", "cost-details-number", costPrimary(cost));
      amount.title = costMeta(cost);
      tr.appendChild(amount); tbody.appendChild(tr);
    });
    table.appendChild(tbody); scroll.appendChild(table); body.appendChild(scroll);
  }
  var aggregate = data.legacy_unattributed;
  if (aggregate && statNumber(aggregate.total) > 0) {
    var historical = el("section", "cost-details-legacy");
    historical.appendChild(i18nEl("h3", "cost-details-legacy-title", "cost.legacyAggregate"));
    var historicalCost = normalizeStats({cost: aggregate.cost}).cost;
    historicalCost.currency = data.currency || historicalCost.currency;
    historical.appendChild(el("div", "cost-details-caption", t("stats.totalTokens") + ": " + fmtInteger(aggregate.total) + " · " + costPrimary(historicalCost)));
    historical.appendChild(i18nEl("p", "cost-details-caption", "cost.legacyAggregateNote"));
    body.appendChild(historical);
  }
  var legacy = 0, missing = 0, dates = [], usdDates = [], sources = [];
  rows.forEach(function (row) {
    legacy += statNumber(row.legacy_calls);
    missing += statNumber(row.missing_fx_calls);
    (Array.isArray(row.rate_dates) ? row.rate_dates : []).forEach(function (date) {
      if (typeof date === "string" && dates.indexOf(date) < 0) dates.push(date);
    });
    (Array.isArray(row.usd_rate_dates) ? row.usd_rate_dates : []).forEach(function (date) {
      if (typeof date === "string" && usdDates.indexOf(date) < 0) usdDates.push(date);
    });
    (Array.isArray(row.rate_sources) ? row.rate_sources : []).forEach(function (source) {
      if (typeof source === "string" && source && sources.indexOf(source) < 0) sources.push(source);
    });
  });
  if (legacy) body.appendChild(el("p", "cost-details-caption", t("cost.legacyNote").replace("{n}", fmtInteger(legacy))));
  if (missing) {
    body.appendChild(el("p", "cost-details-caption", t("cost.missingFxNote").replace("{n}", fmtInteger(missing))));
    if (fetchRates) {
      var fetch = i18nEl("button", "btn cost-details-fetch-rates", "cost.fetchRates");
      fetch.type = "button";
      fetch.addEventListener("click", function () { fetchRates(fetch); });
      body.appendChild(fetch);
    }
  }
  if (dates.length) {
    var rates = el("details", "cost-details-rates");
    rates.appendChild(i18nEl("summary", "", "cost.ratesTitle"));
    dates.sort(); usdDates.sort(); sources.sort();
    var dateLabel = (sources.length && data.currency ? data.currency + " · " : "") + dates.join(", ");
    rates.appendChild(el("p", "cost-details-caption", t("cost.ratesNote")
      .replace("{source}", sources.length ? sources.join(", ") : data.rates_source || "—").replace("{dates}", dateLabel)));
    if (usdDates.length && (usdDates.length !== dates.length || usdDates.some(function (date, index) { return date !== dates[index]; })))
      rates.appendChild(el("p", "cost-details-caption cost-usd-rate-dates", t("cost.usdRatesNote").replace("{dates}", usdDates.join(", "))));
    body.appendChild(rates);
  }
  if (data.pending) body.appendChild(i18nEl("p", "cost-details-caption", "cost.pending"));
  return body;
}
function openCostDetails(sessionID) {
  if (activeAppDialog) activeAppDialog(null);
  var previousFocus = document.activeElement;
  var overlay = el("div", "app-dialog-overlay cost-details-overlay");
  var panel = el("div", "app-dialog cost-details-dialog");
  panel.setAttribute("role", "dialog");
  panel.setAttribute("aria-modal", "true");
  panel.setAttribute("aria-labelledby", "cost-details-title");
  var title = i18nEl("h2", "app-dialog-title", "cost.detailsTitle");
  title.id = "cost-details-title"; panel.appendChild(title);
  var scopeLabel = el("label", "cost-details-scope");
  scopeLabel.appendChild(i18nEl("span", "", "cost.scope"));
  var scope = el("select", "field-select");
  [["session", "cost.scope.session"], ["all", "cost.scope.all"]].forEach(function (choice) {
    var option = i18nEl("option", "", choice[1]);
    option.value = choice[0]; option.disabled = choice[0] === "session" && !sessionID;
    scope.appendChild(option);
  });
  scope.value = sessionID ? "session" : "all";
  scopeLabel.appendChild(scope); panel.appendChild(scopeLabel);
  var content = el("div"); content.setAttribute("aria-live", "polite"); panel.appendChild(content);
  var actions = el("div", "app-dialog-actions");
  var refresh = i18nEl("button", "btn", "common.refresh"); refresh.type = "button";
  var close = i18nEl("button", "btn", "common.close"); close.type = "button";
  actions.appendChild(refresh); actions.appendChild(close); panel.appendChild(actions);
  overlay.appendChild(panel); document.body.appendChild(overlay);
  var state = {closed: false, seq: 0, request: null, refresh: load};
  costDetailsState = state;
  function finish() {
    if (state.closed) return;
    state.closed = true; state.seq++;
    if (state.request) state.request.abort();
    document.removeEventListener("keydown", onKeyDown, true);
    overlay.remove();
    if (costDetailsState === state) costDetailsState = null;
    if (activeAppDialog === finish) activeAppDialog = null;
    var fallback = $("#stats-block .side-cost");
    if (previousFocus && previousFocus.isConnected) previousFocus.focus();
    else if (fallback) fallback.focus();
  }
  function onKeyDown(event) {
    if (event.key === "Escape") { event.preventDefault(); event.stopImmediatePropagation(); finish(); }
    else if (event.key === "Tab") {
      var controls = Array.from(panel.querySelectorAll("button:not([disabled]), select, summary, [tabindex='0']"));
      var first = controls[0], last = controls[controls.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    }
  }
  function current(seq, requestScope) {
    if (state.closed || seq !== state.seq || requestScope !== scope.value) return false;
    if (requestScope === "session" && activeSessionID !== sessionID) { finish(); return false; }
    return true;
  }
  async function load() {
    if (state.closed) return;
    var seq = ++state.seq;
    if (state.request) state.request.abort();
    state.request = new AbortController();
    var requestScope = scope.value;
    var requestContext = costRatesContextKey();
    if (!current(seq, requestScope)) return;
    if (content.contains(document.activeElement) || document.activeElement === refresh) scope.focus();
    content.replaceChildren(i18nEl("div", "usage-loading", "common.loading"));
    content.setAttribute("aria-busy", "true");
    refresh.disabled = true;
    try {
      var url = "/api/costs" + (requestScope === "session" ? "?session=" + encodeURIComponent(sessionID) : "");
      var data = await j(url, {signal: state.request.signal});
      if (!current(seq, requestScope)) return;
      content.replaceChildren(renderCostDetails(data, fetchRates));
      observeCostRatesPending(Object.prototype.hasOwnProperty.call(data, "pending") ? !!data.pending : null,
        data.rates_generation, "popup", function () {
        return current(seq, requestScope) && requestContext === costRatesContextKey();
      });
    } catch (error) {
      if (current(seq, requestScope)) content.replaceChildren(el("div", "usage-empty", t("common.error") + ": " + error.message));
    } finally {
      if (!state.closed && seq === state.seq) { content.setAttribute("aria-busy", "false"); refresh.disabled = false; }
    }
  }
  async function fetchRates(button) {
    if (state.closed || button.disabled) return;
    var seq = ++state.seq, requestScope = scope.value;
    if (!current(seq, requestScope)) return;
    if (state.request) state.request.abort();
    state.request = new AbortController();
    if (document.activeElement === button) scope.focus();
    button.disabled = true;
    button.setAttribute("aria-busy", "true");
    try {
      await j("/api/costs/rates", {method: "POST", headers: {"Content-Type": "application/json"},
        body: JSON.stringify({session_id: requestScope === "session" ? sessionID : ""}), signal: state.request.signal});
      if (!current(seq, requestScope)) return;
      await load();
      renderStats();
    } catch (error) {
      if (current(seq, requestScope)) {
        button.disabled = false;
        button.setAttribute("aria-busy", "false");
        var previousError = content.querySelector(".cost-details-error");
        if (previousError) previousError.remove();
        content.appendChild(el("p", "cost-details-caption cost-details-error", t("common.error") + ": " + error.message));
      }
    }
  }
  activeAppDialog = finish;
  document.addEventListener("keydown", onKeyDown, true);
  close.addEventListener("click", finish);
  overlay.addEventListener("mousedown", function (event) { if (event.target === overlay) finish(); });
  scope.addEventListener("change", load); refresh.addEventListener("click", load);
  close.focus();
  return load();
}

var statsRenderSeq = 0;
// One active conversation only. A model switch does not change the session
// average, and an unavailable refresh cannot erase its last good measurement.
var statsGenerationState = {session: null, persisted: null, live: null};
function statsGenerationForSession(session) {
  if (statsGenerationState.session !== session) {
    statsGenerationState = {session: session, persisted: null, live: null};
  }
  return statsGenerationState;
}
function rememberStatsLiveSpeed(turn, session) {
  var state = statsGenerationForSession(session);
  if (!session || !turn || turn.sessionID !== session || !turn.ev) return;
  var rate = turn.ev.generation_tps;
  if (typeof rate === "number" && Number.isFinite(rate) && rate > 0) {
    state.live = {rate: rate, model: String(turn.ev.model || "—")};
  }
}
function statsLiveTurnForSession(session) {
  return lastTurn && (!lastTurn.sessionID || lastTurn.sessionID === session) ? lastTurn : null;
}
function appendSessionGenerationSpeed(parent, speed, live, turn) {
  var known = speed && typeof speed.rate === "number" && Number.isFinite(speed.rate) && speed.rate > 0;
  var row = statRow(t("generation.session_speed"), known ? speed.rate.toFixed(1) + " tok/s" : "—");
  row.classList.add("generation-speed", "stats-session-generation-speed");
  row.title = t("generation.session_speed_hint");
  row.dataset.i18nTitle = "generation.session_speed_hint";
  parent.appendChild(row);
  // This is a last measured response, not a reconstructed session average.
  // Keep it separate from the persisted turn's model and token counts.
  if (!known && live && !generationSpeedText(turn && turn.ev)) {
    var fallback = statRow(t("generation.last_measured_speed"), live.rate.toFixed(1) + " tok/s");
    fallback.classList.add("generation-speed", "stats-live-generation-speed");
    fallback.title = t("generation.speed_hint") + " · " + live.model;
    parent.appendChild(fallback);
  }
}
function appendLastTurnStats(parent, turn) {
  if (!turn || !turn.ev) return;
  parent.appendChild(i18nEl("div", "stats-head", turn.kind === "model_call" ? "usage.lastModelCall" : "stats.turn"));
  var ev = turn.ev;
  parent.appendChild(statRow(t("stats.model"), ev.model || "—"));
  var evalTok = (ev.tok_in || 0) - (ev.tok_cached || 0);
  if (ev.tok_cached) parent.appendChild(statRow("cache / eval / gen", fmtCompactNumber(ev.tok_cached) + " / " + fmtCompactNumber(evalTok) + " / " + fmtCompactNumber(ev.tok_out)));
  else if (ev.tok_total) parent.appendChild(statRow("in / gen", fmtCompactNumber(ev.tok_in) + " / " + fmtCompactNumber(ev.tok_out)));
  if (ev.cache_hit_pct) parent.appendChild(statRow(t("run.cached"), ev.cache_hit_pct + "%"));
  if (ev.reasoning_tok) parent.appendChild(statRow(t("run.think"), fmtCompactNumber(ev.reasoning_tok)));
  var speed = generationSpeedText(ev);
  if (speed) {
    var speedRow = statRow(t("generation.speed"), speed);
    speedRow.classList.add("generation-speed", "stats-last-turn-generation-speed"); speedRow.title = t("generation.speed_hint");
    speedRow.dataset.i18nTitle = "generation.speed_hint"; parent.appendChild(speedRow);
  }
  if (turn.tools) parent.appendChild(statRow(t("run.tools"), String(turn.tools)));
}
function statsSelectionCurrent(seq, session, model, provider) {
  return seq === statsRenderSeq && session === activeSessionID && model === activeModelID &&
    provider === (typeof activeProviderID === "string" ? activeProviderID : "");
}
async function renderStats() {
  var targetBox = $("#stats-block");
  if (!targetBox || ui.sidebarHidden) return;
  var seq = ++statsRenderSeq;
  var requestSession = activeSessionID;
  var requestModel = activeModelID;
  var requestProvider = typeof activeProviderID === "string" ? activeProviderID : "";
  var speedState = statsGenerationForSession(requestSession);
  rememberStatsLiveSpeed(lastTurn, requestSession);
  // Retain the same session's meter while the requested snapshot is loading.
  // A different conversation must never inherit that meter, even briefly.
  if (targetBox.dataset.statsSession !== requestSession) {
    targetBox.innerHTML = "";
    appendSessionGenerationSpeed(targetBox, null, null, null);
    appendCompactContext(targetBox, null);
  }
  targetBox.dataset.statsSession = requestSession;
  var selection = JSON.stringify([requestModel, requestProvider]);
  if (targetBox.dataset.statsSelection !== selection) {
    var oldActive = targetBox.querySelector(".stats-active-context");
    if (oldActive) oldActive.remove();
    appendActiveContext(targetBox, {model: requestModel, provider: requestProvider, window: 0, windowSource: ""}, null, null);
  }
  targetBox.dataset.statsSelection = selection;
  targetBox.setAttribute("role", "region");
  targetBox.setAttribute("aria-label", t("side.stats"));
  targetBox.setAttribute("aria-live", "polite");
  targetBox.setAttribute("aria-busy", "true");
  var box = el("div");
  var costSnapshot = null;

  var cfgPromise = j("/api/config").catch(function () { return null; });
  try {
    var stats = normalizeStats(await j(statsURL()));
    if (!statsSelectionCurrent(seq, requestSession, requestModel, requestProvider)) return;
    var turn = stats.lastTurn || statsLiveTurnForSession(requestSession);
    appendLastTurnStats(box, turn);
    box.appendChild(i18nEl("div", "stats-head", "stats.sessionSection"));
    if (stats.session.provider) box.appendChild(statRow(t("stats.provider"), stats.session.provider));
    box.appendChild(statRow(t("stats.model"), stats.session.model || stats.model || "—"));
    if (stats.generationSpeed.rate !== null) speedState.persisted = stats.generationSpeed;
    appendSessionGenerationSpeed(box, stats.generationSpeed, speedState.live, turn);
    appendSidebarUsage(box, stats.tokens);
    if (stats.dailyTokens > 0) box.appendChild(statRow(t("stats.daily"), fmtCompactNumber(stats.dailyTokens)));
    appendCompactContext(box, stats.context);
    appendActiveContext(box, stats.activeContext, stats.context, stats.session);
    appendSidebarCost(box, stats.cost);
    costSnapshot = stats.cost;
  } catch (e) {
    if (!statsSelectionCurrent(seq, requestSession, requestModel, requestProvider)) return;
    var liveTurn = statsLiveTurnForSession(requestSession);
    appendLastTurnStats(box, liveTurn);
    appendSessionGenerationSpeed(box, speedState.persisted, speedState.live, liveTurn);
    box.appendChild(el("div", "side-empty", t("common.error") + ": " + e.message));
    appendCompactContext(box, null);
  }

  targetBox.replaceChildren.apply(targetBox, Array.from(box.childNodes));
  box = targetBox;
  if (costSnapshot) observeCostRatesPending(costSnapshot.ratesPendingKnown ? costSnapshot.ratesPending : null,
    costSnapshot.ratesGeneration, "hud", function () {
    return statsSelectionCurrent(seq, requestSession, requestModel, requestProvider) && targetBox.isConnected;
  });

  // Delegations stay visible when present, but an empty section would make the
  // narrow inspector feel like a dashboard rather than a compact HUD.
  if (workersSeen.length) {
    box.appendChild(i18nEl("div", "stats-head", "stats.workers"));
    workersSeen.slice(-8).forEach(function (wk) {
      box.appendChild(statRow(wk.name + (wk.status ? " · " + wk.status : ""), clip(wk.summary, 40) || "—"));
    });
  }

  // Orchestrator switch — visible without digging (config.toml knob).
  var cfg = await cfgPromise;
  if (statsSelectionCurrent(seq, requestSession, requestModel, requestProvider) && cfg) {
    var knob = null;
    (cfg.knobs || []).forEach(function (k) { if (k.key === "orchestrator") knob = k; });
    if (knob) {
      box.appendChild(i18nEl("div", "stats-head", "stats.orch"));
      var row = el("div", "stat-row");
      row.appendChild(i18nEl("span", "", "stats.orchDesc"));
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
        rowM.appendChild(i18nEl("span", "", "stats.orchModel"));
        supercliOrchPicker(rowM, knobM, function (v) {
          jpost("/api/config", { key: "orchestrator_model", value: v })
            .then(renderStats)
            .catch(function (e2) { toast(e2.message); });
        });
        box.appendChild(rowM);
      }
    }
  }
  if (statsSelectionCurrent(seq, requestSession, requestModel, requestProvider)) box.setAttribute("aria-busy", "false");
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
      base.appendChild(i18nEl("span", "pid", "stats.orchModelDef"));
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
    return search;
  }
  btn.addEventListener("click", function () {
    var search = pop.hidden ? fill() : null;
    pop.hidden = !pop.hidden;
    if (search) search.focus();
  });
  document.addEventListener("click", closeOrchPickerPopups);
  wrap.appendChild(btn);
  wrap.appendChild(pop);
  container.appendChild(wrap);
}


function closeOrchPickerPopups(e) {
  var pickers = document.getElementsByClassName("orch-pick");
  for (var i = 0; i < pickers.length; i++) {
    var wrap = pickers[i];
    if (!wrap.contains(e.target)) {
      var pop = wrap.querySelector(".orch-pop");
      if (pop) pop.hidden = true;
    }
  }
}
