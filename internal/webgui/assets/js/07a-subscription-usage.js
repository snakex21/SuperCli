"use strict";

function subscriptionPercent(value) {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= 100 ? value : null;
}
function subscriptionPercentText(value) {
  var percent = subscriptionPercent(value);
  return percent === null ? "—" : statsFormatter("subscriptionPercent", {maximumFractionDigits: 1}).format(percent) + "%";
}
function subscriptionWindowLabel(window) {
  var seconds = window.window_seconds, duration = t("acct.usage.windowUnknown");
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
  var windows = account.windows || [];
  var primary = function (w) { return w.id === "five_hour" || w.id === "primary"; };
  return windows.find(function (w) { return primary(w) && subscriptionWindowFresh(w); }) ||
    windows.find(function (w) { return subscriptionWindowFresh(w); }) ||
    windows.find(function (w) { return primary(w) && subscriptionWindowUsable(w); }) ||
    windows.find(function (w) { return subscriptionWindowUsable(w); });
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
    var windows = (account.windows || []).filter(function (w) { return !compact || ["primary", "secondary", "five_hour", "seven_day"].indexOf(w.id) >= 0; });
    if (!windows.length) card.appendChild(el("span", "subscription-window-meta", t("acct.usage.noSnapshot")));
    windows.forEach(function (w) { card.appendChild(renderSubscriptionWindow(w, compact)); });
    root.appendChild(card);
  });
  return root;
}
