"use strict";

function subscriptionPercent(value) {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= 100 ? value : null;
}
function subscriptionPercentText(value) {
  var percent = subscriptionPercent(value);
  return percent === null ? "—" : statsFormatter("subscriptionPercent", {maximumFractionDigits: 1}).format(percent) + "%";
}
// A window with no duration, no reset time and no usage above zero carries no
// information: the ChatGPT backend reports such a placeholder "secondary"
// window (used 0 %, window 0 min, reset 0) on plans that have a single limit.
function subscriptionWindowInformative(window) {
  if (!window) return false;
  var seconds = window.window_seconds, used = window.used_percent;
  return (typeof seconds === "number" && seconds > 0) || !!window.resets_at ||
    (typeof used === "number" && Number.isFinite(used) && used > 0);
}
function subscriptionWindowLabel(window) {
  var seconds = window.window_seconds;
  var duration = t(window.id === "secondary" ? "acct.usage.windowAdditional" : "acct.usage.windowUnknown");
  if (seconds > 0) {
    var key = "acct.usage.durationSeconds", n = seconds;
    if (seconds % 86400 === 0) { key = "acct.usage.durationDays"; n /= 86400; }
    else if (seconds % 3600 === 0) { key = "acct.usage.durationHours"; n /= 3600; }
    duration = t(key).replace("{n}", fmtInteger(n));
  }
  var family = {seven_day_opus: "Opus", seven_day_sonnet: "Sonnet", seven_day_oauth_apps: "OAuth"}[window.id];
  return family ? family + " · " + duration : duration;
}
// An expired window (reset time passed) has unknown usage: never show its old
// percentage. A merely old snapshot (stale, reset still ahead) stays visible
// and is marked as outdated until the background refresh replaces it.
function subscriptionWindowExpired(window) {
  return !!window.resets_at && !(Date.parse(window.resets_at) > Date.now());
}
function subscriptionWindowUsable(window) { return !subscriptionWindowExpired(window); }
function subscriptionWindowFresh(window) {
  return !window.stale && subscriptionWindowUsable(window);
}
function subscriptionPrimaryWindow(account) {
  var windows = (account.windows || []).filter(subscriptionWindowInformative);
  var primary = function (w) { return w.id === "five_hour" || w.id === "primary"; };
  return windows.find(function (w) { return primary(w) && subscriptionWindowFresh(w); }) ||
    windows.find(function (w) { return subscriptionWindowFresh(w); }) ||
    windows.find(function (w) { return primary(w) && subscriptionWindowUsable(w); }) ||
    windows.find(function (w) { return subscriptionWindowUsable(w); });
}
// True when any logged-in account lacks a current snapshot.
function subscriptionUsageNeedsRefresh(accounts) {
  return (accounts || []).some(function (a) {
    var windows = a.windows || [];
    return !windows.length || windows.some(function (w) { return !subscriptionWindowFresh(w); });
  });
}
// Background quota refresh: single-flight in the page, throttled on the server
// (per account), silent on failure. Statistics re-render only when a snapshot
// actually changed, so rendering never waits for a provider request.
var subscriptionAutoRefresh = {inflight: null, lastAttempt: 0};
function requestSubscriptionUsageRefresh(force) {
  if (subscriptionAutoRefresh.inflight) return subscriptionAutoRefresh.inflight;
  if (!force && Date.now() - subscriptionAutoRefresh.lastAttempt < 60000) return Promise.resolve(false);
  subscriptionAutoRefresh.lastAttempt = Date.now();
  subscriptionAutoRefresh.inflight = jpost("/api/subscription/usage/auto", {force: !!force}).then(function (res) {
    var refreshed = !!(res && res.refreshed);
    if (refreshed) {
      if (typeof renderStats === "function") renderStats();
      if (typeof refreshCostDetails === "function") refreshCostDetails();
    }
    return refreshed;
  }).catch(function () { return false; }).finally(function () { subscriptionAutoRefresh.inflight = null; });
  return subscriptionAutoRefresh.inflight;
}
function startSubscriptionUsageAutoRefresh() {
  requestSubscriptionUsageRefresh(false);
  setInterval(function () { requestSubscriptionUsageRefresh(false); }, 5 * 60000);
}
function subscriptionCostPrimary(cost) {
  var accounts = cost.subscriptionUsage || [];
  if (!accounts.length) return t("cost.subscriptionValue");
  return accounts.map(function (a) {
    var window = subscriptionPrimaryWindow(a);
    var value = window ? subscriptionPercent(window.remaining_percent) : null;
    var meta = window ? subscriptionWindowLabel(window) + (subscriptionWindowFresh(window) ? "" : " · " + t("acct.usage.stale")) : "";
    return a.provider + " · " + t("subscription.remaining").replace("{value}", subscriptionPercentText(value)) + (meta ? " (" + meta + ")" : "");
  }).join(" · ");
}
function renderSubscriptionWindow(window, compact) {
  var fresh = subscriptionWindowFresh(window), usable = subscriptionWindowUsable(window);
  var root = el("span", "subscription-window" + (fresh ? "" : " stale"));
  root.setAttribute("data-patch-key", String(window.id || ""));
  var remaining = usable ? subscriptionPercent(window.remaining_percent) : null;
  var line = el("span", "subscription-window-line");
  line.appendChild(el("span", "", subscriptionWindowLabel(window)));
  line.appendChild(el("strong", "", t("subscription.remaining").replace("{value}", subscriptionPercentText(remaining))));
  root.appendChild(line);
  if (remaining !== null) {
    var meter = el("span", "subscription-meter");
    meter.setAttribute("role", "meter");
    meter.setAttribute("aria-label", subscriptionWindowLabel(window) + " · " + t("acct.usage.remaining"));
    meter.setAttribute("aria-valuemin", "0"); meter.setAttribute("aria-valuemax", "100"); meter.setAttribute("aria-valuenow", String(remaining));
    var fill = el("span"); fill.style.width = remaining + "%";
    if (remaining < 15) meter.classList.add("low");
    meter.appendChild(fill); root.appendChild(meter);
  }
  var reset = window.resets_at ? fmtDateTime(window.resets_at) : "—";
  root.appendChild(el("span", "subscription-window-meta", (!fresh ? t("acct.usage.stale") + " · " : "") + t("acct.usage.reset") + ": " + reset));
  if (!compact && usable) root.appendChild(el("span", "subscription-window-meta", t("acct.usage.used") + ": " + subscriptionPercentText(window.used_percent)));
  return root;
}
function renderSubscriptionSummary(accounts, compact) {
  var root = el("span", "subscription-summary" + (compact ? " compact" : ""));
  (accounts || []).forEach(function (account) {
    var card = el("span", "subscription-summary-account");
    card.appendChild(el("strong", "subscription-account-name", account.provider + (account.account && account.account !== "default" ? " · " + account.account : "")));
    card.setAttribute("data-patch-key", account.provider + "|" + (account.account || ""));
    var windows = (account.windows || []).filter(function (w) {
      return subscriptionWindowInformative(w) && (!compact || ["primary", "secondary", "five_hour", "seven_day"].indexOf(w.id) >= 0);
    });
    if (!windows.length) card.appendChild(el("span", "subscription-window-meta", t("acct.usage.noSnapshot")));
    windows.forEach(function (w) { card.appendChild(renderSubscriptionWindow(w, compact)); });
    root.appendChild(card);
  });
  return root;
}

/* In-place updates. A periodic or live refresh must not clear and rebuild a
   panel that already shows data (visible flicker, lost hover/scroll/focus):
   the existing nodes stay and only changed text, attributes and widths are
   written. Only listener-free subtrees are patched; a node whose
   data-patch-key differs is replaced, so keyed controls never keep a stale
   closure for another account. */
function patchDomInPlace(target, source) {
  if (!target || !source) return target || source;
  if (target.nodeType !== source.nodeType || target.nodeName !== source.nodeName ||
      (target.nodeType === 1 && target.getAttribute("data-patch-key") !== source.getAttribute("data-patch-key"))) {
    target.replaceWith(source);
    return source;
  }
  if (target.nodeType !== 1) {
    if (target.nodeValue !== source.nodeValue) target.nodeValue = source.nodeValue;
    return target;
  }
  Array.from(target.attributes).forEach(function (a) { if (!source.hasAttribute(a.name)) target.removeAttribute(a.name); });
  Array.from(source.attributes).forEach(function (a) { if (target.getAttribute(a.name) !== a.value) target.setAttribute(a.name, a.value); });
  if ("disabled" in source && target.disabled !== source.disabled) target.disabled = source.disabled;
  var next = Array.from(source.childNodes);
  next.forEach(function (child, i) {
    var current = target.childNodes[i];
    if (current) patchDomInPlace(current, child); else target.appendChild(child);
  });
  while (target.childNodes.length > next.length) target.removeChild(target.lastChild);
  return target;
}
// Puts `nodes` into `host` in order. A new node whose data-patch-key matches a
// current child of `host` is patched into that child instead, and children
// that stay are never detached (only new nodes are inserted around them).
function reconcileChildren(host, nodes) {
  var keep = new Set();
  var final = nodes.filter(Boolean).map(function (node) {
    var key = node.nodeType === 1 ? node.getAttribute("data-patch-key") : null;
    if (!key) return node;
    var old = Array.from(host.children).find(function (child) {
      return !keep.has(child) && child.getAttribute("data-patch-key") === key;
    });
    if (!old) return node;
    keep.add(old);
    return patchDomInPlace(old, node);
  });
  var wanted = new Set(final);
  Array.from(host.childNodes).forEach(function (child) { if (!wanted.has(child)) child.remove(); });
  final.forEach(function (node, i) {
    var at = host.childNodes[i];
    if (at !== node) host.insertBefore(node, at || null);
  });
  return final;
}
// Update an existing subscription summary in place (or return a new one).
function updateSubscriptionSummary(existing, accounts, compact) {
  var fresh = renderSubscriptionSummary(accounts, compact);
  return existing ? patchDomInPlace(existing, fresh) : fresh;
}
