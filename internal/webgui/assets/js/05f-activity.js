"use strict";

/* Coordinator activity: ONE short status line on the input bar that updates
   in place ("Running tests · 0:12"), with the turn's step list on expand and
   a calm "No activity for 2 min" + Continue once nothing has arrived for
   longer than the current step allows. It replaces #run-status while a turn
   runs and disappears when the turn ends.

   The model (runActivityNew/Note/View) is pure: every call takes `now`, so
   tests drive it with a fake clock. The view below owns the DOM and a 1 s tick. */

var RUN_ACTIVITY_HISTORY = 40;

function runActivityNew(now) {
  var step = {kind: "thinking", detail: "", since: now};
  return {start: now, last: now, step: step, open: {}, order: [], waitUser: null, history: [], shown: null, ended: false};
}
function runActivityStepLabel(step) { return activityLabel(step); }
// Make `step` current. The same label keeps its clock (a new reasoning chunk
// is not a new step); a finished step joins the history.
function runActivitySet(model, step, now) {
  var cur = model.step;
  if (cur && cur.kind === step.kind && runActivityStepLabel(cur) === runActivityStepLabel(step) &&
      (cur.detail || "") === (step.detail || "")) {
    if (step.workerCommandMs !== undefined) cur.workerCommandMs = step.workerCommandMs;
    return cur;
  }
  if (cur) {
    model.history.push({label: runActivityStepLabel(cur), detail: cur.detail || "", ms: Math.max(0, now - cur.since)});
    if (model.history.length > RUN_ACTIVITY_HISTORY) model.history.splice(0, model.history.length - RUN_ACTIVITY_HISTORY);
  }
  step.since = step.since > 0 && step.since <= now ? step.since : now;
  model.step = step;
  return step;
}
// The most recent call still running decides the step; none -> thinking.
function runActivityFromOpen(model, now) {
  for (var i = model.order.length - 1; i >= 0; i--) {
    var open = model.open[model.order[i]];
    if (open) return runActivitySet(model, open, now);
  }
  return runActivitySet(model, {kind: "thinking", detail: ""}, now);
}
function runActivityNote(model, ev, now) {
  if (!model || model.ended || !ev) return model;
  model.last = Math.max(model.last, now);
  switch (ev.type) {
    case "reasoning":
      if (!model.order.length && !model.waitUser) runActivitySet(model, {kind: "thinking", detail: ""}, now);
      break;
    case "message":
      if (!model.order.length && !model.waitUser) runActivitySet(model, {kind: "writing", detail: ""}, now);
      break;
    case "tool_call":
      var step = activityStep(ev.name, ev.args);
      // An async delegation returns at once; it is not a wait.
      if (step.kind === "worker" && step.async) step = {kind: "tool", tool: ev.name, detail: step.detail};
      step.callID = ev.id || "";
      if (ev.id) { model.open[ev.id] = step; model.order.push(ev.id); }
      if (!model.waitUser) runActivitySet(model, step, now);
      break;
    case "tool_result":
      if (ev.id && model.open[ev.id]) {
        delete model.open[ev.id];
        model.order = model.order.filter(function (id) { return id !== ev.id; });
      }
      if (!model.waitUser) runActivityFromOpen(model, now);
      break;
    case "worker_progress":
      // The worker a synchronous delegation waits for, and whether it now
      // runs a command (then its timeout bounds the silence, not 60 s).
      var parent = ev.parent_call_id && model.open[ev.parent_call_id];
      if (!parent) {
        Object.keys(model.open).forEach(function (id) {
          var s = model.open[id];
          if (s.kind === "worker" && s.worker && s.worker === ev.id) parent = s;
        });
      }
      if (parent && parent.kind === "worker") {
        if (ev.id && !parent.worker) parent.worker = ev.id;
        if (ev.kind === "tool_call" && ACTIVITY_COMMAND_TOOLS[ev.tool]) {
          var a = activityArgs(ev.args);
          parent.workerCommandMs = Number(a.timeout_ms) > 0 ? Number(a.timeout_ms) : activityCommandTimeoutMS;
        } else if (ev.kind === "tool_result" || ev.kind === "finished") delete parent.workerCommandMs;
      }
      break;
    case "question":
      if (!model.waitUser) model.waitUser = model.step;
      runActivitySet(model, {kind: "user", detail: ev.question && (ev.question.question || ev.question.header) || ""}, now);
      break;
    case "question_closed":
      if (model.waitUser) { model.waitUser = null; runActivityFromOpen(model, now); }
      break;
    case "notice":
      var retry = activityRetryStep(ev.text);
      // Consecutive retries are one step: its clock counts from the first.
      if (retry && model.step && model.step.kind === "retry") retry.since = model.step.since;
      if (retry && !model.waitUser) runActivitySet(model, retry, now);
      break;
    case "done":
    case "error":
      model.ended = true;
      break;
  }
  return model;
}
// What the line shows at `now`. userWaiting: a question/approval is open in
// this view (it outranks everything and is never "silent").
function runActivityView(model, now, userWaiting) {
  if (!model || model.ended) return null;
  // A question shown outside this turn's stream (e.g. an approval) still
  // gets a running clock from the moment it was first seen.
  if (userWaiting && model.step.kind !== "user") model.userSince = model.userSince || now;
  else model.userSince = 0;
  var step = userWaiting ? {kind: "user", detail: model.step.kind === "user" ? model.step.detail : "", since: model.step.kind === "user" ? model.step.since : model.userSince} : model.step;
  var label = runActivityStepLabel(step);
  // The user's turn and a retry are shown at once (a retry is never a
  // flicker: it waits seconds); other changes wait out the 1 s debounce.
  var shown = step.kind === "user" || step.kind === "retry" ? (model._liveShown = {label: label, since: step.since, at: now}) :
    activityDebounce(model, label, step.since, now);
  var quiet = Math.max(0, now - model.last), limit = userWaiting ? 0 : activitySilenceMS(step);
  var view = {kind: step.kind, label: shown.label, step: activityClock(now - shown.since), turn: activityClock(now - model.start),
    detail: step.detail || "", silent: limit > 0 && quiet >= limit, quiet: quiet};
  if (view.silent) view.label = t("activity.quiet").replace("{time}", activityQuietText(quiet));
  return view;
}

/* View: the line in the composer row, Continue, and the expandable steps. */
var runActivity = null, runActivityTimer = null, runActivityContinuing = false;
function runActivityNow() { return Date.now(); }
function runActivityEventTime(ev) {
  var now = runActivityNow();
  // Replayed events carry the server time; map it onto this clock.
  if (ev && ev.at > 0) {
    var local = ev.at - (typeof syncClockOffsetMS === "number" ? syncClockOffsetMS : 0);
    if (local <= now && local > now - 24 * 3600 * 1000) return local;
  }
  return now;
}
function runActivityStart(startMs) {
  var now = runActivityNow();
  runActivity = runActivityNew(startMs > 0 && startMs <= now ? startMs : now);
  runActivity.step.since = now;
  runActivityContinuing = false;
  loadActivitySettings();
  clearInterval(runActivityTimer);
  runActivityTimer = setInterval(renderRunActivity, 1000);
  renderRunActivity();
}
function runActivityStop() {
  if (runActivity) runActivity.ended = true;
  runActivity = null;
  clearInterval(runActivityTimer);
  runActivityTimer = null;
  renderRunActivity();
}
function runActivityEvent(ev) {
  if (!runActivity || !ev) return;
  runActivityNote(runActivity, ev, runActivityEventTime(ev));
  if (runActivity.ended) { runActivityStop(); return; }
  // Events repaint the label only; the clock moves on the 1 s tick.
  renderRunActivity();
}
var activitySettingsLoaded = 0;
function loadActivitySettings() {
  if (typeof j !== "function" || Date.now() - activitySettingsLoaded < 60000) return;
  activitySettingsLoaded = Date.now();
  j("/api/runtime").then(function (info) {
    if (info && typeof info.command_timeout_ms === "number" && info.command_timeout_ms >= 0) activityCommandTimeoutMS = info.command_timeout_ms;
  }).catch(function () { activitySettingsLoaded = 0; });
}
function runActivityUserWaiting() {
  return typeof waitingForUser === "function" && waitingForUser();
}
function renderRunActivity() {
  var box = document.getElementById("run-activity");
  if (!box) return;
  var composer = document.getElementById("composer");
  var view = runActivity ? runActivityView(runActivity, runActivityNow(), runActivityUserWaiting()) : null;
  if (!view) {
    if (!box.hidden) box.hidden = true;
    if (composer) composer.classList.remove("activity-live");
    var steps = document.getElementById("run-activity-steps");
    if (steps && !steps.hidden) { steps.hidden = true; }
    var line0 = box.querySelector(".run-activity-line");
    if (line0) line0.setAttribute("aria-expanded", "false");
    return;
  }
  box.hidden = false;
  if (composer) composer.classList.add("activity-live");
  if (box.dataset.kind !== view.kind) box.dataset.kind = view.kind;
  box.classList.toggle("silent", view.silent);
  var line = box.querySelector(".run-activity-line");
  setNodeText(box.querySelector(".run-activity-label"), view.label);
  setNodeText(box.querySelector(".run-activity-time"), view.silent ? "" : view.step);
  var title = [view.detail, t("activity.turn").replace("{time}", view.turn)].filter(Boolean).join("\n");
  if (line.title !== title) line.title = title;
  var cont = box.querySelector(".run-activity-continue");
  cont.hidden = !view.silent;
  cont.disabled = runActivityContinuing;
  if (view.silent && cont.title !== t("activity.quietHint")) cont.title = t("activity.quietHint");
  var panel = document.getElementById("run-activity-steps");
  if (panel && !panel.hidden) renderRunActivitySteps(panel, view);
}
function renderRunActivitySteps(panel, view) {
  var model = runActivity;
  if (!model) return;
  var rows = model.history.filter(function (h) { return h.ms >= 1000; }).map(function (h) {
    return {label: h.label, detail: h.detail, time: activityClock(h.ms), done: true};
  });
  rows.push({label: runActivityStepLabel(model.step), detail: model.step.detail || "", time: view.step, done: false});
  var head = panel.querySelector(".run-activity-steps-head");
  if (!head) {
    head = el("div", "run-activity-steps-head");
    panel.appendChild(head);
  }
  setNodeText(head, t("activity.steps") + " · " + t("activity.turn").replace("{time}", view.turn));
  var list = panel.querySelector(".run-activity-steps-list");
  if (!list) { list = el("ol", "run-activity-steps-list"); panel.appendChild(list); }
  while (list.children.length > rows.length) list.lastElementChild.remove();
  rows.forEach(function (r, i) {
    var li = list.children[i];
    if (!li) {
      li = el("li", "run-activity-step");
      li.append(el("span", "run-activity-step-label"), el("span", "run-activity-step-detail"), el("span", "run-activity-step-time"));
      list.appendChild(li);
    }
    li.classList.toggle("current", !r.done);
    setNodeText(li.children[0], r.label);
    setNodeText(li.children[1], clip(r.detail, 140));
    if (li.children[1].title !== r.detail) li.children[1].title = r.detail;
    setNodeText(li.children[2], r.time);
  });
}
function toggleRunActivitySteps() {
  var panel = document.getElementById("run-activity-steps"), line = document.querySelector("#run-activity .run-activity-line");
  if (!panel || !runActivity) return;
  panel.hidden = !panel.hidden;
  if (line) line.setAttribute("aria-expanded", panel.hidden ? "false" : "true");
  renderRunActivity();
}
// Continue = ask the model to carry on through the normal chat path: stop
// the silent turn and send a short message (Interrupt & send).
function continueRunActivity() {
  if (!runActivity || runActivityContinuing || typeof interruptAndSend !== "function") return;
  runActivityContinuing = true;
  renderRunActivity();
  interruptAndSend(t("activity.continuePrompt"));
}
(function bindRunActivity() {
  var line = document.querySelector("#run-activity .run-activity-line");
  var cont = document.querySelector("#run-activity .run-activity-continue");
  if (line) line.addEventListener("click", toggleRunActivitySteps);
  if (cont) cont.addEventListener("click", continueRunActivity);
  if (typeof registerLanguageRefresh === "function" && line) registerLanguageRefresh(line, function () {
    if (runActivity) runActivity._liveShown = null; // the shown label is a translated string
    renderRunActivity();
  });
})();
