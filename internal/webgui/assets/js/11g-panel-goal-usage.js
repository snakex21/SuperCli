"use strict";

sections.goal = async function () {
  try { renderGoalPanel(await j("/api/goal")); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
  }
};

/* ── Usage (stats + context report) ── */

function usageLeadMetric(label, value, meta, extraClass) {
  var metric = el("div", "usage-lead-metric" + (extraClass ? " " + extraClass : ""));
  metric.appendChild(el("div", "usage-lead-label", label));
  var main = el("div", "usage-lead-value", value);
  main.title = value;
  metric.appendChild(main);
  if (meta) metric.appendChild(el("div", "usage-lead-meta", meta));
  metric.setAttribute("aria-label", label + ": " + value + (meta ? ", " + meta : ""));
  return metric;
}
function usageFact(label, value, note) {
  var item = el("div", "usage-fact");
  item.appendChild(el("dt", "usage-fact-label", label));
  var dd = el("dd", "usage-fact-value", value == null || value === "" ? "—" : String(value));
  dd.title = dd.textContent;
  item.appendChild(dd);
  if (note) item.appendChild(el("div", "usage-fact-note", note));
  return item;
}
function messageBreakdown(session) {
  return fmtInteger(session.userMessages) + " " + t("stats.userMessages") + " · " +
    fmtInteger(session.assistantMessages) + " " + t("stats.assistantMessages") + " · " +
    fmtInteger(session.toolMessages) + " " + t("stats.toolMessages");
}
function contextItems(context) {
  return [
    { key: "user", label: t("context.user"), value: context.breakdown.user },
    { key: "assistant", label: t("context.assistant"), value: context.breakdown.assistant },
    { key: "tools", label: t("context.tools"), value: context.breakdown.tools },
    { key: "other", label: t("context.other"), value: context.breakdown.other },
  ];
}
function normalizedContext(raw) {
  raw = raw || {};
  var breakdown = raw.breakdown || {};
  var windowTokens = statNumber(raw.window);
  var compactThreshold = statNumber(raw.compact_threshold, raw.compactThreshold);
  var compactPercent = windowTokens > 0 && compactThreshold > 0
    ? Math.max(0, Math.min(100, compactThreshold * 100 / windowTokens))
    : 80;
  return {
    window: windowTokens, estimatedUsed: statNumber(raw.estimated_used, raw.estimatedUsed),
    percent: Math.max(0, Math.min(100, statNumber(raw.percent))),
    compactThreshold: compactThreshold, compactPercent: compactPercent,
    breakdown: {
      user: statNumber(breakdown.user), assistant: statNumber(breakdown.assistant),
      tools: statNumber(breakdown.tools), other: statNumber(breakdown.other),
    },
  };
}
function contextState(percent, compactPercent) {
  if (percent >= compactPercent) return { key: "context.compactionRecommended", cls: "danger" };
  if (percent >= 60) return { key: "context.approaching", cls: "warning" };
  return { key: "context.safe", cls: "safe" };
}
function renderContextInspector(context) {
  var section = el("section", "usage-section usage-context");
  var head = el("div", "usage-section-head");
  head.appendChild(el("h3", "usage-section-title", t("stats.currentContext")));
  if (context.window > 0) {
    head.appendChild(el("div", "usage-section-value", context.percent + "% · " +
      fmtInteger(context.estimatedUsed) + " / " + fmtInteger(context.window)));
  }
  section.appendChild(head);

  var items = contextItems(context);
  var total = items.reduce(function (sum, item) { return sum + item.value; }, 0);
  if (total <= 0) {
    section.appendChild(el("div", "usage-empty", t("usage.contextEmpty")));
    return section;
  }

  var state = contextState(context.percent, context.compactPercent);
  var status = el("div", "context-status " + state.cls);
  status.appendChild(el("span", "context-status-dot"));
  status.appendChild(el("strong", "", t(state.key)));
  section.appendChild(status);

  var ariaParts = [t("stats.currentContext") + " " + context.percent + "%", t(state.key)];
  var meter = el("div", "context-budget");
  meter.setAttribute("role", "img");
  var used = el("div", "context-used");
  used.style.width = context.percent + "%";
  items.forEach(function (item) {
    var pct = item.value * 100 / total;
    ariaParts.push(item.label + " " + Math.round(pct) + "%");
    if (item.value <= 0) return;
    var segment = el("span", "context-segment context-" + item.key);
    segment.style.width = pct + "%";
    segment.title = item.label + ": " + fmtInteger(item.value) + " (" + Math.round(pct) + "%)";
    used.appendChild(segment);
  });
  meter.appendChild(used);
  var pruneMarker = el("span", "context-threshold threshold-60");
  pruneMarker.style.left = "60%";
  pruneMarker.title = "60% \u00b7 " + t("context.pruneThreshold");
  meter.appendChild(pruneMarker);
  var compactMarker = el("span", "context-threshold threshold-80");
  compactMarker.style.left = context.compactPercent + "%";
  compactMarker.title = Math.round(context.compactPercent) + "% \u00b7 " + t("context.compactThreshold");
  meter.appendChild(compactMarker);
  meter.setAttribute("aria-label", ariaParts.join(". "));
  section.appendChild(meter);

  var thresholdLegend = el("div", "context-threshold-legend");
  thresholdLegend.appendChild(el("span", "", "60% \u00b7 " + t("context.pruneThreshold")));
  thresholdLegend.appendChild(el("span", "", Math.round(context.compactPercent) + "% \u00b7 " + t("context.compactThreshold")));
  section.appendChild(thresholdLegend);

  var legend = el("div", "context-legend");
  items.forEach(function (item) {
    var pct = total > 0 ? Math.round(item.value * 100 / total) : 0;
    var entry = el("div", "context-legend-item");
    entry.appendChild(el("span", "context-dot context-" + item.key));
    entry.appendChild(el("span", "context-name", item.label));
    entry.appendChild(el("span", "context-value", pct + "% · " + fmtCompactNumber(item.value)));
    legend.appendChild(entry);
  });
  section.appendChild(legend);
  var footer = el("div", "context-actions");
  footer.appendChild(el("div", "usage-caption", t("context.compactHint")));
  var compact = el("button", "btn context-compact", t("context.compactNow"));
  compact.type = "button";
  compact.disabled = !activeSessionID;
  compact.title = t("context.compactHint");
  compact.addEventListener("click", async function () {
    if (!activeSessionID) { toast(t("context.selectSession")); return; }
    if (streaming) { toast(t("context.busy")); return; }
    compact.disabled = true;
    compact.classList.add("busy");
    compact.textContent = t("context.compacting");
    try {
      var result = await jpost("/api/context/compact", { session_id: activeSessionID });
      var next = normalizedContext(result.context);
      section.replaceWith(renderContextInspector(next));
      toast(t("context.compacted").replace("{n}", fmtInteger(result.removed)));
    } catch (err) {
      toast(err.message);
      compact.disabled = false;
      compact.classList.remove("busy");
      compact.textContent = t("context.compactNow");
    }
  });
  footer.appendChild(compact);
  section.appendChild(footer);
  return section;
}
function priceField(label, name, value, required, hint) {
  var wrap = el("label", "price-field");
  wrap.appendChild(el("span", "price-field-label", label));
  var input = el("input", "field-input");
  input.type = "number";
  input.name = name;
  input.min = "0";
  input.max = "1000000";
  input.step = "0.000001";
  input.inputMode = "decimal";
  input.required = !!required;
  input.value = value === null ? (required ? "" : "0") : String(value);
  input.setAttribute("aria-label", label + " · " + t("price.unit"));
  wrap.appendChild(input);
  if (hint) wrap.appendChild(el("span", "price-field-hint", hint));
  return { root: wrap, input: input };
}
function priceValue(input, optional) {
  var raw = input.value.trim();
  if (raw === "") return optional ? 0 : NaN;
  return Number(raw);
}
function validPrice(value) {
  return Number.isFinite(value) && value >= 0 && value <= 1000000;
}
function renderPriceDetails(stats) {
  var cost = stats.cost;
  var state = normalizedCostState(cost);
  var details = el("details", "usage-rates");
  details.open = state === "unknown" || state === "partial";
  details.appendChild(el("summary", "usage-rates-summary", t("price.title")));
  var body = el("div", "usage-rates-body");

  var rates = el("dl", "price-rates");
  rates.appendChild(usageFact(t("cost.source"), costSourceLabel(cost.source) || "—"));
  rates.appendChild(usageFact(t("price.input"), cost.inputPerMillion === null ? "—" : fmtMoney(cost.inputPerMillion, cost.currency, true)));
  rates.appendChild(usageFact(t("price.cache"), cost.cachedInputPerMillion === null ? "—" : fmtMoney(cost.cachedInputPerMillion, cost.currency, true)));
  rates.appendChild(usageFact(t("price.output"), cost.outputPerMillion === null ? "—" : fmtMoney(cost.outputPerMillion, cost.currency, true)));
  body.appendChild(rates);
  var coverage = costCoverage(cost);
  if (coverage) body.appendChild(el("div", "price-note", coverage));
  if (stats.tokens.cachedInput > 0 && !cost.cacheDiscountKnown && ["estimated", "manual", "partial"].indexOf(state) >= 0) {
    body.appendChild(el("div", "price-note warning", t("cost.cacheUnknown")));
  }

  var form = el("form", "manual-price-form");
  form.setAttribute("aria-label", t("price.title"));
  form.appendChild(el("p", "price-hint", t("price.hint")));
  var identity = el("div", "price-identity");
  identity.appendChild(el("code", "", (stats.session.provider || "—") + " / " + (stats.session.model || stats.model || "—")));
  form.appendChild(identity);
  var fields = el("div", "price-fields");
  var inputField = priceField(t("price.input"), "input_per_million", cost.inputPerMillion, true);
  var cacheField = priceField(t("price.cache"), "cached_input_per_million", cost.cachedInputPerMillion, false, t("price.cacheHint"));
  var outputField = priceField(t("price.output"), "output_per_million", cost.outputPerMillion, true);
  fields.appendChild(inputField.root);
  fields.appendChild(cacheField.root);
  fields.appendChild(outputField.root);
  form.appendChild(fields);

  var actions = el("div", "price-actions");
  var save = el("button", "btn primary", t("price.save"));
  save.type = "submit";
  actions.appendChild(save);
  var remove = null;
  if (cost.manual || cost.state === "manual") {
    remove = el("button", "btn danger", t("price.remove"));
    remove.type = "button";
    actions.appendChild(remove);
  }
  var status = el("span", "price-status");
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  actions.appendChild(status);
  form.appendChild(actions);

  var provider = stats.session.provider || "";
  var model = stats.session.model || stats.model || "";
  if (!provider || !model) {
    save.disabled = true;
    status.textContent = t("price.identityMissing");
  }
  function setPriceBusy(busy) {
    save.disabled = busy || !provider || !model;
    if (remove) remove.disabled = busy;
    inputField.input.disabled = busy;
    cacheField.input.disabled = busy;
    outputField.input.disabled = busy;
  }
  form.addEventListener("submit", async function (e) {
    e.preventDefault();
    var input = priceValue(inputField.input, false);
    var cached = priceValue(cacheField.input, true);
    var output = priceValue(outputField.input, false);
    if (![input, cached, output].every(validPrice)) {
      status.textContent = t("price.invalid");
      return;
    }
    setPriceBusy(true);
    status.textContent = t("common.loading");
    try {
      await jpost("/api/model/price", {
        provider: provider, model: model, input_per_million: input,
        cached_input_per_million: cached, output_per_million: output,
      });
      toast(t("price.saved"));
      await sections.usage();
      renderStats();
    } catch (err) {
      status.textContent = err.message;
      setPriceBusy(false);
    }
  });
  if (remove) {
    remove.addEventListener("click", async function () {
      if (!await appConfirm(t("price.removeConfirm"), {
        title: t("price.remove"), danger: true, confirmLabel: t("common.remove"),
      })) return;
      setPriceBusy(true);
      status.textContent = t("common.loading");
      try {
        await jpost("/api/model/price", { provider: provider, model: model, remove: true });
        toast(t("price.removed"));
        await sections.usage();
        renderStats();
      } catch (err) {
        status.textContent = err.message;
        setPriceBusy(false);
      }
    });
  }
  body.appendChild(form);
  details.appendChild(body);
  return details;
}
function renderUsageInspector(stats) {
  var root = el("div", "usage-inspector");
  root.setAttribute("role", "region");
  root.setAttribute("aria-label", t("panel.usage"));
  if (!stats.session.id) root.appendChild(el("div", "usage-empty top", t("usage.noSession")));

  var lead = el("div", "usage-lead");
  lead.appendChild(usageLeadMetric(t("stats.totalTokens"), fmtInteger(stats.tokens.total), t("usage.session"), "tokens"));
  var costMetric = usageLeadMetric(t("stats.totalCost"), costPrimary(stats.cost), costMeta(stats.cost),
    "cost cost-state-" + normalizedCostState(stats.cost));
  var coverage = costCoverage(stats.cost);
  if (coverage) costMetric.appendChild(el("div", "usage-lead-coverage", coverage));
  lead.appendChild(costMetric);
  root.appendChild(lead);

  root.appendChild(renderPerformanceTelemetry(stats.telemetry));

  var factsSection = el("section", "usage-section");
  factsSection.appendChild(el("h3", "usage-section-title", t("usage.details")));
  var facts = el("dl", "usage-facts");
  facts.appendChild(usageFact(t("stats.provider"), stats.session.provider || "—", stats.session.providerType));
  facts.appendChild(usageFact(t("stats.model"), stats.session.model || stats.model || "—"));
  facts.appendChild(usageFact(t("stats.inputTokens"), fmtInteger(stats.tokens.input)));
  facts.appendChild(usageFact(t("stats.outputTokens"), fmtInteger(stats.tokens.output)));
  if (stats.tokens.hasCached) {
    facts.appendChild(usageFact(t("stats.evaluatedInput"), fmtInteger(stats.tokens.evaluatedInput)));
    facts.appendChild(usageFact(t("stats.cachedInput"), fmtInteger(stats.tokens.cachedInput)));
  }
  if (stats.tokens.hasReasoning) facts.appendChild(usageFact(t("stats.reasoningTokens"), fmtInteger(stats.tokens.reasoning)));
  facts.appendChild(usageFact(t("stats.calls"), fmtInteger(stats.cost.calls)));
  facts.appendChild(usageFact(t("stats.messages"), fmtInteger(stats.session.messages), messageBreakdown(stats.session)));
  facts.appendChild(usageFact(t("stats.toolCalls"), fmtInteger(stats.session.toolCalls)));
  facts.appendChild(usageFact(t("usage.daily"), fmtInteger(stats.dailyTokens)));
  facts.appendChild(usageFact(t("stats.contextWindow"), stats.context.window > 0 ? fmtInteger(stats.context.window) : "—"));
  facts.appendChild(usageFact(t("stats.created"), fmtDateTime(stats.session.createdAt)));
  facts.appendChild(usageFact(t("stats.updated"), fmtDateTime(stats.session.updatedAt)));
  factsSection.appendChild(facts);
  root.appendChild(factsSection);

  root.appendChild(renderContextInspector(stats.context));
  root.appendChild(renderPriceDetails(stats));
  return root;
}

function renderPerformanceTelemetry(telemetry) {
  var section = el("section", "usage-section telemetry-section");
  var head = el("div", "usage-section-head");
  head.appendChild(el("h3", "usage-section-title", t("telemetry.title")));
  if (telemetry.samples) {
    var sampleLabel = fmtInteger(telemetry.samples) + " " + t("telemetry.samples");
    if (telemetry.scope) sampleLabel += " · " + t("telemetry.scope." + telemetry.scope);
    head.appendChild(el("div", "usage-section-value", sampleLabel));
  }
  section.appendChild(head);
  if (!telemetry.samples) {
    section.appendChild(el("div", "usage-empty", t("telemetry.empty")));
    return section;
  }

  var verdict = el("div", "telemetry-verdict");
  verdict.appendChild(el("strong", "", t("telemetry.bottleneck." + telemetry.bottleneck)));
  verdict.appendChild(el("span", "", " " + fmtInteger(telemetry.bottleneckShare) + "%"));
  section.appendChild(verdict);

  var pipeline = telemetry.modelMS + telemetry.toolsMS + telemetry.cliMS;
  var split = el("div", "telemetry-split");
  [["model", telemetry.modelMS], ["tools", telemetry.toolsMS], ["cli", telemetry.cliMS]].forEach(function (part) {
    var seg = el("span", "telemetry-segment telemetry-" + part[0]);
    seg.style.width = (pipeline > 0 ? part[1] * 100 / pipeline : 0) + "%";
    split.appendChild(seg);
  });
  section.appendChild(split);

  var facts = el("dl", "usage-facts telemetry-facts");
  facts.appendChild(usageFact(t("telemetry.model"), fmtDuration(telemetry.modelMS)));
  facts.appendChild(usageFact(t("telemetry.tools"), fmtDuration(telemetry.toolsMS)));
  facts.appendChild(usageFact(t("telemetry.cli"), fmtDuration(telemetry.cliMS)));
  facts.appendChild(usageFact(t("telemetry.average"), fmtDuration(telemetry.averageMS)));
  facts.appendChild(usageFact(t("telemetry.steps"), fmtInteger(telemetry.steps)));
  if (telemetry.persistMS) facts.appendChild(usageFact(t("telemetry.persist"), fmtDuration(telemetry.persistMS)));
  if (telemetry.tools.length) {
    facts.appendChild(usageFact(t("telemetry.topTools"), telemetry.tools.map(function (tool) {
      return tool.name + " " + fmtDuration(statNumber(tool.duration_ms));
    }).join(" · ")));
  }
  section.appendChild(facts);

  if (telemetry.signals.length) {
    var notes = el("div", "telemetry-signals");
    telemetry.signals.forEach(function (signal) {
      notes.appendChild(el("p", "", t("telemetry.signal." + signal)));
    });
    section.appendChild(notes);
  }
  return section;
}

var usageRenderSeq = 0;
sections.usage = async function () {
  var seq = ++usageRenderSeq;
  var requestSession = activeSessionID;
  panelContent.innerHTML = "";
  panelContent.setAttribute("aria-busy", "true");
  panelContent.appendChild(el("div", "usage-loading", t("common.loading")));
  try {
    var stats = normalizeStats(await j(statsURL()));
    if (seq !== usageRenderSeq || currentSection !== "usage" || requestSession !== activeSessionID) return;
    panelContent.innerHTML = "";
    panelContent.appendChild(renderUsageInspector(stats));
  } catch (e) {
    if (seq === usageRenderSeq && currentSection === "usage") {
      panelContent.innerHTML = "";
      panelContent.appendChild(el("div", "usage-empty top", t("common.error") + ": " + e.message));
    }
  }
  if (seq === usageRenderSeq && currentSection === "usage") panelContent.setAttribute("aria-busy", "false");
};

/* ── Files ── */

var filesCwd = "";
