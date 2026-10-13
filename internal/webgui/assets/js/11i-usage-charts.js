/* ── Usage history charts (lazy-loaded SVG) ──
   Reads GET /api/stats/history?days=N and draws four small charts in the
   Usage panel: tokens per day, cost per day, tool time and time to first
   output. Nothing is fetched until the section scrolls into view. */

var USAGE_SVG_NS = "http://www.w3.org/2000/svg";
var usageHistoryRanges = [7, 30, 90];
var usageHistoryDays = 30;
var usageHistoryCache = {};
var USAGE_HISTORY_TTL_MS = 60000;
var USAGE_CHART_W = 480;
var USAGE_CHART_H = 170;
var USAGE_CHART_PAD = { left: 52, right: 8, top: 10, bottom: 22 };

function svgEl(tag, attrs, text) {
  var e = document.createElementNS(USAGE_SVG_NS, tag);
  if (attrs) {
    Object.keys(attrs).forEach(function (k) {
      if (attrs[k] != null) e.setAttribute(k, String(attrs[k]));
    });
  }
  if (text != null) e.textContent = text;
  return e;
}
function svgTitle(node, text) {
  node.appendChild(svgEl("title", null, text));
  return node;
}
function svgChart(w, h, label) {
  return svgEl("svg", {
    viewBox: "0 0 " + w + " " + h, "class": "uc-svg", role: "img",
    "aria-label": label, preserveAspectRatio: "xMidYMid meet",
  });
}
// niceMax rounds an axis maximum up to 1/2/5 x 10^n so grid labels stay
// readable.
function niceMax(v) {
  if (!(v > 0)) return 1;
  var p = Math.pow(10, Math.floor(Math.log10(v)));
  var n = v / p;
  return (n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10) * p;
}
function usageHistoryDayLabel(day, long) {
  var parts = String(day || "").split("-");
  if (parts.length !== 3) return String(day || "");
  var d = new Date(Number(parts[0]), Number(parts[1]) - 1, Number(parts[2]));
  if (isNaN(d)) return String(day);
  try {
    var opts = long ? { weekday: "short", month: "short", day: "numeric" } : { month: "short", day: "numeric" };
    return new Intl.DateTimeFormat(statsLocale(), opts).format(d);
  } catch (e) { return parts[1] + "-" + parts[2]; }
}
function usageChartFrame(svg, max, fmt) {
  var p = USAGE_CHART_PAD, innerH = USAGE_CHART_H - p.top - p.bottom;
  [0, 0.5, 1].forEach(function (f) {
    var y = p.top + innerH - innerH * f;
    svg.appendChild(svgEl("line", { x1: p.left, x2: USAGE_CHART_W - p.right, y1: y, y2: y, "class": f ? "uc-grid-line" : "uc-axis" }));
    svg.appendChild(svgEl("text", { x: p.left - 6, y: y + 3, "text-anchor": "end" }, fmt(max * f)));
  });
}
function usageChartXLabels(svg, days, slot) {
  var p = USAGE_CHART_PAD, step = Math.max(1, Math.ceil(days.length / 7));
  for (var i = 0; i < days.length; i += step) {
    svg.appendChild(svgEl("text", {
      x: p.left + slot * i + slot / 2, y: USAGE_CHART_H - 6, "text-anchor": "middle",
    }, usageHistoryDayLabel(days[i])));
  }
}

// usageBarChart draws stacked daily bars. stacks: [{cls, label, values}].
function usageBarChart(label, days, stacks, fmt) {
  var p = USAGE_CHART_PAD;
  var innerW = USAGE_CHART_W - p.left - p.right, innerH = USAGE_CHART_H - p.top - p.bottom;
  var totals = days.map(function (_, i) {
    return stacks.reduce(function (sum, s) { return sum + Math.max(0, s.values[i] || 0); }, 0);
  });
  var max = niceMax(Math.max.apply(null, totals.concat([0])));
  var svg = svgChart(USAGE_CHART_W, USAGE_CHART_H, label);
  usageChartFrame(svg, max, fmt);
  var slot = innerW / Math.max(1, days.length);
  var barW = Math.max(1, slot * 0.72);
  days.forEach(function (day, i) {
    var g = svgEl("g", { "class": "uc-hit" });
    var x = p.left + slot * i + (slot - barW) / 2;
    var y = p.top + innerH;
    var lines = [usageHistoryDayLabel(day, true)];
    stacks.forEach(function (s) {
      var v = Math.max(0, s.values[i] || 0);
      lines.push(s.label + ": " + fmt(v));
      if (!v) return;
      var h = innerH * v / max;
      y -= h;
      g.appendChild(svgEl("rect", { x: x, y: y, width: barW, height: Math.max(0.5, h), "class": s.cls }));
    });
    // Full-height transparent hit area so empty days still show a tooltip.
    g.insertBefore(svgEl("rect", { x: p.left + slot * i, y: p.top, width: slot, height: innerH, "class": "uc-hit-area" }), g.firstChild);
    svg.appendChild(svgTitle(g, lines.join("\n")));
  });
  usageChartXLabels(svg, days, slot);
  return svg;
}

// usageLineChart draws one series; days without a value break the line.
function usageLineChart(label, days, values, fmt) {
  var p = USAGE_CHART_PAD;
  var innerW = USAGE_CHART_W - p.left - p.right, innerH = USAGE_CHART_H - p.top - p.bottom;
  var max = niceMax(Math.max.apply(null, values.map(function (v) { return v || 0; }).concat([0])));
  var svg = svgChart(USAGE_CHART_W, USAGE_CHART_H, label);
  usageChartFrame(svg, max, fmt);
  var slot = innerW / Math.max(1, days.length);
  var run = [];
  function flush() {
    if (run.length > 1) svg.appendChild(svgEl("polyline", { points: run.join(" "), "class": "uc-line" }));
    run = [];
  }
  days.forEach(function (day, i) {
    var v = values[i];
    if (v == null || !(v > 0)) { flush(); return; }
    var x = p.left + slot * i + slot / 2, y = p.top + innerH - innerH * v / max;
    run.push(x.toFixed(1) + "," + y.toFixed(1));
    svg.appendChild(svgTitle(svgEl("circle", { cx: x, cy: y, r: 2.5, "class": "uc-dot" }),
      usageHistoryDayLabel(day, true) + ": " + fmt(v)));
  });
  flush();
  usageChartXLabels(svg, days, slot);
  return svg;
}

// usageHBarChart ranks named values (tool wall time) as horizontal bars.
function usageHBarChart(label, rows, fmt) {
  var rowH = 20, labelW = 140, valueW = 64, w = USAGE_CHART_W;
  var h = rows.length * rowH + 4;
  var max = Math.max.apply(null, rows.map(function (r) { return r.value; }).concat([1]));
  var svg = svgChart(w, h, label);
  rows.forEach(function (r, i) {
    var y = 2 + i * rowH;
    var barMax = w - labelW - valueW - 8;
    var g = svgEl("g", { "class": "uc-hit" });
    g.appendChild(svgEl("text", { x: labelW - 8, y: y + rowH / 2 + 3, "text-anchor": "end" }, clip(r.name, 20)));
    g.appendChild(svgEl("rect", { x: labelW, y: y + 4, width: Math.max(1, barMax * r.value / max), height: rowH - 8, "class": "uc-s-tool" }));
    g.appendChild(svgEl("text", { x: w - 4, y: y + rowH / 2 + 3, "text-anchor": "end" }, fmt(r.value)));
    svg.appendChild(svgTitle(g, r.name + ": " + fmt(r.value)));
  });
  return svg;
}

// usageDonut draws a ring chart (SVG circles on a 100-unit circumference)
// with a legend of label, value and share. rows: {label, value, cls}.
function usageDonut(label, rows, fmt, centerText) {
  rows = rows.filter(function (r) { return r.value > 0; });
  var total = rows.reduce(function (s, r) { return s + r.value; }, 0);
  if (total <= 0) return null;
  var wrap = el("div", "uc-donut");
  var svg = svgChart(42, 42, label);
  svg.setAttribute("class", "uc-svg uc-donut-svg");
  var R = 15.915, offset = 25;
  svg.appendChild(svgEl("circle", { cx: 21, cy: 21, r: R, "class": "uc-donut-track" }));
  rows.forEach(function (r) {
    var pct = r.value * 100 / total;
    var seg = svgEl("circle", {
      cx: 21, cy: 21, r: R, "class": "uc-donut-seg " + r.cls,
      "stroke-dasharray": pct.toFixed(3) + " " + (100 - pct).toFixed(3),
      "stroke-dashoffset": offset.toFixed(3),
    });
    svg.appendChild(svgTitle(seg, r.label + ": " + fmt(r.value) + " (" + Math.round(pct) + "%)"));
    offset -= pct;
  });
  svg.appendChild(svgEl("text", { x: 21, y: 22.5, "text-anchor": "middle", "class": "uc-donut-center" },
    centerText != null ? centerText : fmt(total)));
  wrap.appendChild(svg);
  var legend = el("div", "uc-donut-legend");
  rows.forEach(function (r) {
    var pct = r.value * 100 / total;
    var item = el("div", "uc-donut-item");
    item.title = r.label + ": " + fmt(r.value);
    item.appendChild(el("span", "uc-swatch " + r.cls));
    item.appendChild(el("span", "uc-donut-name", r.label));
    item.appendChild(el("span", "uc-donut-val", (pct < 1 ? "<1" : Math.round(pct)) + "% · " + fmt(r.value)));
    legend.appendChild(item);
  });
  wrap.appendChild(legend);
  return wrap;
}
function usagePalette(i) { return "uc-p" + (i % 8); }
var USAGE_CONTEXT_COLORS = {
  system: "uc-p4", tools: "uc-p5", user: "uc-p0", assistant: "uc-p2",
  tool_calls: "uc-p1", tool_results: "uc-p3", other: "uc-p7", free: "uc-pfree", buffer: "uc-pbuf",
};
function usageDonutBlock(titleKey, donut) {
  var block = el("div", "uc-donut-block");
  block.appendChild(i18nEl("h4", "uc-card-title", titleKey));
  block.appendChild(donut);
  return block;
}
// usageContextDonuts shows what fills the current window (by kind) and which
// tools' results take the most room.
function usageContextDonuts(context) {
  var cats = (context && context.categories) || [];
  var used = 0, size = 0;
  var catRows = cats.map(function (c) {
    var v = statNumber(c.tokens);
    size += v;
    if (c.key !== "free" && c.key !== "buffer") used += v;
    return { label: t("context.cat." + c.key), value: v, cls: USAGE_CONTEXT_COLORS[c.key] || "uc-p7" };
  });
  var toolRows = ((context && context.toolResults) || []).map(function (u, i) {
    return { label: u.name ? u.name + (u.count ? " ×" + u.count : "") : t("context.other"), value: statNumber(u.tokens), cls: usagePalette(i) };
  });
  var catDonut = usageDonut(t("context.windowMap"), catRows, fmtCompactNumber,
    size > 0 ? Math.round(used * 100 / size) + "%" : null);
  var toolDonut = usageDonut(t("context.toolResults"), toolRows, fmtCompactNumber);
  if (!catDonut && !toolDonut) return null;
  var row = el("div", "uc-donut-row");
  if (catDonut) row.appendChild(usageDonutBlock("context.windowMap", catDonut));
  if (toolDonut) row.appendChild(usageDonutBlock("context.toolResults", toolDonut));
  return row;
}

function usageChartLegend(stacks) {
  var legend = el("div", "uc-legend");
  stacks.forEach(function (s) {
    var item = el("span", "uc-legend-item");
    item.appendChild(el("span", "uc-swatch " + s.cls));
    item.appendChild(document.createTextNode(s.label));
    legend.appendChild(item);
  });
  return legend;
}
function usageChartCard(titleKey, meta, content) {
  var card = el("div", "uc-card");
  var head = el("div", "uc-card-head");
  head.appendChild(i18nEl("h4", "uc-card-title", titleKey));
  if (meta) head.appendChild(el("span", "uc-card-meta", meta));
  card.appendChild(head);
  if (content) card.appendChild(content);
  else card.appendChild(i18nEl("div", "uc-empty", "usage.history.empty"));
  return card;
}
function usageHistoryCost(cost, currency) {
  cost = cost || {};
  var amount = cost.amount == null ? null : Number(cost.amount);
  return {
    state: cost.state || "unknown", partial: !!cost.partial, source: cost.source || "",
    amount: Number.isFinite(amount) ? amount : null, currency: cost.currency || currency || "USD",
  };
}

function renderUsageHistoryCharts(view) {
  var series = Array.isArray(view && view.series) ? view.series : [];
  var days = series.map(function (d) { return d.day; });
  var grid = el("div", "uc-grid");

  var tokenStacks = [
    { cls: "uc-s-evaluated", label: t("stats.evaluatedInput"), values: series.map(function (d) { return statNumber(d.tokens && d.tokens.evaluated); }) },
    { cls: "uc-s-cached", label: t("stats.cachedInput"), values: series.map(function (d) { return statNumber(d.tokens && d.tokens.cached); }) },
    { cls: "uc-s-output", label: t("stats.outputTokens"), values: series.map(function (d) { return statNumber(d.tokens && d.tokens.output); }) },
  ];
  var tokenTotal = 0, calls = 0;
  series.forEach(function (d, i) {
    calls += statNumber(d.calls);
    tokenStacks.forEach(function (s) { tokenTotal += s.values[i]; });
  });
  var tokenContent = null;
  if (tokenTotal > 0) {
    tokenContent = el("div");
    tokenContent.appendChild(usageBarChart(t("usage.history.tokens"), days, tokenStacks, fmtCompactNumber));
    tokenContent.appendChild(usageChartLegend(tokenStacks));
  }
  grid.appendChild(usageChartCard("usage.history.tokens",
    fmtCompactNumber(tokenTotal) + " · " + t("usage.history.calls").replace("{n}", fmtInteger(calls)), tokenContent));

  var currency = (view && view.currency) || "USD";
  var costValues = series.map(function (d) { return usageHistoryCost(d.cost, currency).amount || 0; });
  var hasCost = costValues.some(function (v) { return v > 0; });
  var total = usageHistoryCost(view && view.total_cost, currency);
  grid.appendChild(usageChartCard("usage.history.cost", costPrimary(total),
    hasCost ? usageBarChart(t("usage.history.cost"), days,
      [{ cls: "uc-s-cost", label: t("usage.history.cost"), values: costValues }],
      function (v) { return fmtMoney(v, total.currency); }) : null));

  var models = {};
  series.forEach(function (d) {
    (Array.isArray(d.models) ? d.models : []).forEach(function (m) {
      var name = String(m.model || m.provider || "?");
      var tk = m.tokens || {};
      models[name] = (models[name] || 0) + statNumber(tk.evaluated) + statNumber(tk.cached) + statNumber(tk.output);
    });
  });
  var modelRows = Object.keys(models).map(function (k) { return { label: k, value: models[k] }; })
    .sort(function (a, b) { return b.value - a.value; });
  if (modelRows.length > 8) {
    var rest = modelRows.slice(7).reduce(function (s, r) { return s + r.value; }, 0);
    modelRows = modelRows.slice(0, 7).concat([{ label: t("context.other"), value: rest }]);
  }
  modelRows.forEach(function (r, i) { r.cls = usagePalette(i); });
  grid.appendChild(usageChartCard("usage.history.byModel", modelRows.length ? fmtInteger(modelRows.length) : "",
    usageDonut(t("usage.history.byModel"), modelRows, fmtCompactNumber)));

  var allTools = (Array.isArray(view && view.tools) ? view.tools : []).filter(function (tool) {
    return statNumber(tool.time_ms) > 0;
  }).map(function (tool) { return { label: String(tool.name || "?"), value: statNumber(tool.time_ms) }; });
  var tools = allTools.slice(0, 7);
  if (allTools.length > 7) {
    tools.push({ label: t("context.other"), value: allTools.slice(7).reduce(function (s, r) { return s + r.value; }, 0) });
  }
  tools.forEach(function (r, i) { r.cls = usagePalette(i); });
  var toolTotal = series.reduce(function (sum, d) { return sum + statNumber(d.tool_time_ms); }, 0);
  var toolCard = usageChartCard("usage.history.toolTime", toolTotal > 0 ? fmtDuration(toolTotal) : "",
    tools.length ? usageDonut(t("usage.history.toolTime"), tools, fmtDuration) : null);
  if (tools.length) toolCard.appendChild(i18nEl("div", "uc-note", "usage.history.toolNote"));
  grid.appendChild(toolCard);

  var ttft = series.map(function (d) { return statNumber(d.ttft_calls) > 0 ? statNumber(d.avg_ttft_ms) : null; });
  var ttftCalls = 0, ttftSum = 0;
  series.forEach(function (d) {
    ttftCalls += statNumber(d.ttft_calls);
    ttftSum += statNumber(d.avg_ttft_ms) * statNumber(d.ttft_calls);
  });
  grid.appendChild(usageChartCard("usage.history.ttft", ttftCalls ? fmtDuration(ttftSum / ttftCalls) : "",
    ttftCalls ? usageLineChart(t("usage.history.ttft"), days, ttft, fmtDuration) : null));
  return grid;
}

async function loadUsageHistory(section, days) {
  var seq = (section._ucSeq || 0) + 1;
  section._ucSeq = seq;
  var body = section.querySelector(".uc-body");
  body.setAttribute("aria-busy", "true");
  var cached = usageHistoryCache[days];
  var view = cached && Date.now() - cached.at < USAGE_HISTORY_TTL_MS ? cached.view : null;
  if (!view) {
    body.innerHTML = "";
    body.appendChild(i18nEl("div", "uc-empty", "common.loading"));
  }
  try {
    if (!view) {
      view = await j("/api/stats/history?days=" + days);
      usageHistoryCache[days] = { at: Date.now(), view: view };
    }
    if (seq !== section._ucSeq || !section.isConnected) return;
    body.innerHTML = "";
    body.appendChild(renderUsageHistoryCharts(view));
  } catch (e) {
    if (seq !== section._ucSeq || !section.isConnected) return;
    body.innerHTML = "";
    body.appendChild(el("div", "uc-empty", t("common.error") + ": " + e.message));
  }
  body.setAttribute("aria-busy", "false");
}

function renderUsageHistorySection() {
  var section = el("section", "usage-section usage-history");
  var head = el("div", "usage-section-head");
  head.appendChild(i18nEl("h3", "usage-section-title", "usage.history.title"));
  var range = el("div", "uc-range");
  range.setAttribute("role", "group");
  range.setAttribute("aria-label", t("usage.history.range"));
  var loaded = false;
  usageHistoryRanges.forEach(function (n) {
    var btn = el("button", "", t("usage.history.days").replace("{n}", String(n)));
    btn.type = "button";
    btn.dataset.days = String(n);
    btn.setAttribute("aria-pressed", n === usageHistoryDays ? "true" : "false");
    btn.addEventListener("click", function () {
      usageHistoryDays = n;
      range.querySelectorAll("button").forEach(function (b) {
        b.setAttribute("aria-pressed", b.dataset.days === String(n) ? "true" : "false");
      });
      loaded = true;
      loadUsageHistory(section, n);
    });
    range.appendChild(btn);
  });
  head.appendChild(range);
  section.appendChild(head);
  var body = el("div", "uc-body");
  body.setAttribute("aria-live", "polite");
  section.appendChild(body);

  function start() {
    if (loaded) return;
    loaded = true;
    loadUsageHistory(section, usageHistoryDays);
  }
  if (typeof IntersectionObserver === "function") {
    var io = new IntersectionObserver(function (entries) {
      if (entries.some(function (e) { return e.isIntersecting; })) {
        io.disconnect();
        start();
      }
    }, { rootMargin: "200px" });
    io.observe(section);
  } else {
    setTimeout(start, 0);
  }
  return section;
}
