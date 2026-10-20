"use strict";

// Shared host state, deliberately separate from session/transcript state. Only
// the server starts/owns the countdown; these timers only refresh and render.
var hostPowerState = { status: "idle" }, hostPowerOffline = false;
var hostPowerCancelFailed = false, hostPowerCancelling = false;
var hostPowerPoll = null, hostPowerClock = null, hostPowerRequest = null;
var hostPowerRevision = 0, hostPowerDismissed = "", hostPowerRefreshAgain = false;
var hostPowerBanner = el("section", "host-power");
hostPowerBanner.id = "host-power";
hostPowerBanner.hidden = true;
hostPowerBanner.setAttribute("aria-label", t("hostPower.title"));
var hostPowerTitle = el("strong", "host-power-title");
var hostPowerMessage = el("div", "host-power-message");
hostPowerMessage.setAttribute("role", "status");
var hostPowerGoal = el("div", "host-power-goal");
var hostPowerWarning = el("div", "host-power-warning");
hostPowerWarning.setAttribute("role", "alert");
var hostPowerCancel = el("button", "btn host-power-cancel");
hostPowerCancel.type = "button";
var hostPowerDismiss = el("button", "btn host-power-dismiss", "×");
hostPowerDismiss.type = "button";
hostPowerDismiss.setAttribute("aria-label", t("common.close"));
[hostPowerTitle, hostPowerMessage, hostPowerGoal, hostPowerWarning, hostPowerCancel, hostPowerDismiss].forEach(function (node) { hostPowerBanner.appendChild(node); });
// Outside stream/session containers: available on desktop and phone, including
// when a transcript or sidebar is loading. It never blocks application boot.
document.body.appendChild(hostPowerBanner);

function localizedHostPowerText(key, args) {
  return t(key).replace(/\{(goal|seconds)\}/g, function (_, name) {
    return Object.prototype.hasOwnProperty.call(args, name) ? String(args[name]) : "{" + name + "}";
  });
}
function hostPowerActive() {
  return hostPowerState.status === "armed" || hostPowerState.status === "countdown";
}
function hostPowerIdentity(state) {
  return JSON.stringify([state.status, state.job_id, state.goal_id, state.deadline, state.error]);
}
function hostPowerSnapshot(value) {
  if (!value || ["idle", "armed", "countdown", "cancelled", "blocked", "executing", "failed", "executed"].indexOf(value.status) < 0) throw new Error("Invalid host power snapshot");
  if (value.status === "countdown" && (!Number.isFinite(value.deadline) || value.deadline <= 0)) throw new Error("Missing host power deadline");
  return value;
}
function renderHostPower() {
  var status = hostPowerState.status;
  hostPowerBanner.hidden = status === "idle" || hostPowerDismissed === hostPowerIdentity(hostPowerState);
  hostPowerBanner.dataset.status = status;
  hostPowerTitle.textContent = t("hostPower.title");
  hostPowerBanner.setAttribute("aria-label", t("hostPower.title"));
  // Never count down from receipt time, and never turn zero into a claim that
  // execution/cancellation succeeded. Only a subsequent server reply can do so.
  var seconds = Math.max(0, Math.ceil((Number(hostPowerState.deadline || 0) - Date.now() - (typeof syncClockOffsetMS === "number" ? syncClockOffsetMS : 0)) / 1000));
  var message = status === "idle" ? "" : localizedHostPowerText("hostPower." + status, { seconds: seconds });
  if (hostPowerMessage.textContent !== message) hostPowerMessage.textContent = message;
  hostPowerGoal.textContent = hostPowerState.goal || "";
  hostPowerGoal.hidden = !hostPowerState.goal;
  hostPowerWarning.textContent = (hostPowerOffline ? t("hostPower.offline") : "") + (hostPowerCancelFailed ? " " + t("hostPower.cancelFailed") : "");
  hostPowerWarning.hidden = !hostPowerOffline && !hostPowerCancelFailed;
  hostPowerCancel.textContent = t("hostPower.cancel");
  hostPowerCancel.hidden = !hostPowerActive();
  hostPowerCancel.disabled = hostPowerCancelling;
  hostPowerDismiss.hidden = hostPowerActive();
  hostPowerDismiss.setAttribute("aria-label", t("common.close"));
}
function scheduleHostPower() {
  clearTimeout(hostPowerPoll); hostPowerPoll = null;
  clearInterval(hostPowerClock); hostPowerClock = null;
  if (document.hidden) return;
  if (hostPowerActive() || hostPowerState.status === "executing") {
    hostPowerPoll = setTimeout(refreshHostPower, 2000);
  }
  if (hostPowerState.status === "countdown") hostPowerClock = setInterval(renderHostPower, 250);
}
function applyHostPower(value) {
  var next = hostPowerSnapshot(value);
  if (next.status === "cancelled" || next.job_id !== hostPowerState.job_id) hostPowerCancelFailed = false;
  hostPowerState = next;
  hostPowerOffline = false;
  renderHostPower();
  scheduleHostPower();
}
// Bound stalled network requests too: silence is not a successful cancel.
function hostPowerFetch(path, options) {
  var controller = new AbortController();
  var timer = setTimeout(function () { controller.abort(); }, 8000);
  options = options || {};
  options.signal = controller.signal;
  return fetch(path, options).finally(function () { clearTimeout(timer); });
}
function refreshHostPower() {
  if (hostPowerCancelling || document.hidden) return Promise.resolve();
  if (hostPowerRequest) {
    // A tool/sync event may arrive while an older idle GET is in flight. Queue
    // one follow-up so newly armed state isn't lost until the next tool ends.
    hostPowerRefreshAgain = true;
    return hostPowerRequest;
  }
  var revision = hostPowerRevision;
  hostPowerRequest = hostPowerFetch("/api/host-power", { cache: "no-store" }).then(function (reply) {
    if (!reply.ok) throw new Error("Host power HTTP " + reply.status);
    return reply.json();
  }).then(function (value) {
    if (revision === hostPowerRevision) applyHostPower(value);
  }).catch(function () {
    if (revision !== hostPowerRevision) return;
    hostPowerOffline = true;
    renderHostPower(); scheduleHostPower();
  }).finally(function () {
    hostPowerRequest = null;
    if (hostPowerRefreshAgain) { hostPowerRefreshAgain = false; refreshHostPower(); }
    else scheduleHostPower();
  });
  return hostPowerRequest;
}
function cancelHostPower() {
  if (hostPowerCancelling || !hostPowerActive()) return Promise.resolve();
  hostPowerCancelling = true;
  ++hostPowerRevision; // In-flight GETs cannot resurrect a cancelled plan.
  renderHostPower();
  return hostPowerFetch("/api/host-power/cancel", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" }).then(function (reply) {
    if (!reply.ok) throw new Error("Host power cancel HTTP " + reply.status);
    return reply.json();
  }).then(function (value) {
    // Even HTTP 200 is not proof of cancellation if the server reports that
    // execution already won the race. Render its authoritative status instead.
    if (value.status === "idle") throw new Error("Cancellation was not confirmed");
    hostPowerCancelFailed = value.status !== "cancelled";
    applyHostPower(value);
  }).catch(function () {
    hostPowerCancelFailed = true; hostPowerOffline = true;
    // Keep the last-known status AND deadline: the host may still power off.
    renderHostPower(); scheduleHostPower();
  }).finally(function () { hostPowerCancelling = false; renderHostPower(); });
}
hostPowerCancel.addEventListener("click", cancelHostPower);
hostPowerDismiss.addEventListener("click", function () {
  if (!hostPowerActive()) {
    hostPowerDismissed = hostPowerIdentity(hostPowerState); renderHostPower();
  }
});
registerLanguageRefresh(hostPowerBanner, renderHostPower);
document.addEventListener("visibilitychange", function () {
  if (document.hidden) { clearTimeout(hostPowerPoll); clearInterval(hostPowerClock); }
  else { scheduleHostPower(); refreshHostPower(); }
});
window.addEventListener("online", refreshHostPower);
window.addEventListener("focus", refreshHostPower);
window.addEventListener("offline", function () { hostPowerOffline = true; renderHostPower(); });
window.addEventListener("pagehide", function () { clearTimeout(hostPowerPoll); clearInterval(hostPowerClock); });
refreshHostPower();
