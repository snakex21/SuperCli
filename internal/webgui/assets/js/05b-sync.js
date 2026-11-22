"use strict";

// One connection per view; only the selected conversation carries chat events.
var syncClientID = newChatTurnID();
var syncRuns = Object.create(null);
var syncSession = "", syncOpeningSession = "";
var syncConnection = null, syncRetry = null, syncBackoff = 500, syncGeneration = 0;
var syncReady = Promise.resolve(), syncResolveReady = null;
var syncRefreshPending = false, syncRefreshAgain = false, syncListPending = false;
var syncFollowingTurn = "", syncRecoveryPending = false;
var syncClockOffsetMS = 0;
var syncProjectsPending = false;
// Running background agents per conversation (server background_agents frames).
var syncBackgroundCounts = Object.create(null);
// Reports held at the automatic wake-up cap (server background_wake_held
// frames): session -> {n, limit}. Shown as a banner with a Resume button.
var syncWakeHeld = Object.create(null);
function renderBackgroundAgents() {
  renderSessionBackground();
  renderWakeHeld();
  var badge = $("#background-agents");
  if (!badge) return;
  var n = (activeSessionID && syncBackgroundCounts[activeSessionID]) || 0;
  // With the coordinator idle the conversation is still working: say so
  // plainly ("Working in background (2)"), not just a small counter.
  var status = $("#run-status"), mainState = status && status.dataset && status.dataset.state || "";
  var mainIdle = mainState !== "running" && mainState !== "pending";
  // 1.0.38: the badge never contradicts the worker panel. While the
  // coordinator is idle, workers the panel shows as running count too, so
  // "Ready" next to "working: 22" in the panel always comes with the badge.
  if (mainIdle && typeof workerOverviewRunningCount === "function") n = Math.max(n, workerOverviewRunningCount());
  var idle = n > 0 && mainIdle;
  var text = n > 0 ? t(idle ? "background.working" : "background.agents").replace("{n}", String(n)) : "";
  if (badge.textContent !== text) badge.textContent = text;
  badge.hidden = n <= 0;
  badge.title = n > 0 ? t("background.agentsHint") : "";
  syncToggleClass(badge, "idle", idle);
  syncToggleClass($("#composer"), "bg-working", idle);
  syncToggleClass($("#status-dot"), "background", n > 0);
}
function renderWakeHeld() {
  var box = $("#wake-held");
  if (!box) return;
  var held = activeSessionID && syncWakeHeld[activeSessionID];
  box.hidden = !held;
  if (!held) return;
  var text = t("background.heldBanner").replace("{n}", String(held.n)).replace("{limit}", String(held.limit || ""));
  var label = box.querySelector(".wake-held-text");
  if (label && label.textContent !== text) label.textContent = text;
  var btn = box.querySelector(".wake-held-resume");
  if (btn && !btn._bound) {
    btn._bound = true;
    btn.addEventListener("click", resumeWakeHeld);
  }
}
// One click: the server restores the wake-up budget and the held reports
// start an automatic turn; the banner goes away with the server's frame.
function resumeWakeHeld() {
  var id = activeSessionID, box = $("#wake-held");
  if (!id || !box) return Promise.resolve(false);
  var btn = box.querySelector(".wake-held-resume");
  if (btn) btn.disabled = true;
  return Promise.resolve(jpost("/api/background/resume", {session: id})).then(function () {
    delete syncWakeHeld[id];
    renderWakeHeld();
    return true;
  }, function (error) {
    if (typeof toast === "function") toast(t("background.resumeFailed") + (error && error.message ? ": " + error.message : ""));
    return false;
  }).finally(function () { if (btn) btn.disabled = false; });
}
function syncToggleClass(node, name, on) {
  if (node && node.classList && typeof node.classList.toggle === "function") node.classList.toggle(name, on);
}
// Session list: a conversation whose agents work in the background is marked
// busy too (pulsing dot and "N in background"), not only while its own turn
// runs. Updated in place; the list is not rebuilt.
// Unanswered background-worker questions per conversation (badge "?").
var syncQuestionCounts = Object.create(null);
// A view that opens a conversation (or reloads) shows its pending questions
// again: a worker's question never disappears with the view.
var restoringQuestions = Object.create(null);
function restorePendingQuestions(id) {
  if (!id || restoringQuestions[id] || typeof j !== "function" || typeof showQuestion !== "function") return Promise.resolve();
  restoringQuestions[id] = true;
  return Promise.resolve(j("/api/questions?session=" + encodeURIComponent(id))).then(function (data) {
    if (id !== activeSessionID || !data || !Array.isArray(data.questions)) return;
    data.questions.forEach(function (q) {
      if (q && q.id && !(typeof questionOverlays === "object" && questionOverlays[q.id])) showQuestion(q);
    });
  }).catch(function () {}).finally(function () { delete restoringQuestions[id]; });
}
function renderSessionBackground() {
  var list = $("#session-list");
  if (!list || typeof list.querySelectorAll !== "function") return;
  list.querySelectorAll(".side-item[data-session-id]").forEach(function (item) {
    var q = syncQuestionCounts[item.dataset.sessionId] || 0;
    var qchip = item.querySelector(".side-q");
    if (q > 0) {
      if (!qchip) {
        qchip = el("span", "side-q", "?");
        var qmeta = item.querySelector(".s");
        if (qmeta) qmeta.appendChild(qchip); else item.appendChild(qchip);
      }
      qchip.title = t("question.waiting");
    } else if (qchip) qchip.remove();
    var n = syncBackgroundCounts[item.dataset.sessionId] || 0;
    item.classList.toggle("background", n > 0);
    var chip = item.querySelector(".side-bg");
    if (n > 0) {
      var text = t("background.agents").replace("{n}", String(n));
      if (!chip) {
        chip = el("span", "side-bg");
        var meta = item.querySelector(".s");
        if (meta) meta.appendChild(chip); else item.appendChild(chip);
      }
      if (chip.textContent !== text) chip.textContent = text;
      chip.title = t("background.agentsHint");
    } else if (chip) chip.remove();
  });
}
// A worker of a finished turn keeps running; its progress and report arrive
// here (no live run carries them) and update the same task card.
function syncBackgroundEvent(ev) {
  if (!ev) return;
  if (ev.type === "worker_progress" && typeof addWorkerProgress === "function") addWorkerProgress(ev);
  else if (ev.type === "worker" && typeof handleEvent === "function") handleEvent(ev, null);
  // A background worker's question (no turn is running): answer it here.
  else if (ev.type === "question" && ev.question && typeof showQuestion === "function") showQuestion(ev.question);
  else if (ev.type === "question_closed" && typeof closeQuestionOverlay === "function") closeQuestionOverlay(ev.id);
  else if (ev.type === "background_held" && typeof toast === "function") toast(t("background.held").replace("{n}", String(ev.run || 0)));
}
function syncRenderElsewhere() {
  if (typeof renderRunningElsewhere === "function") renderRunningElsewhere();
}
function syncLoadProjects() {
  if (syncProjectsPending || typeof loadProjects !== "function") return;
  syncProjectsPending = true;
  Promise.resolve().then(loadProjects).finally(function () { syncProjectsPending = false; });
}
function syncServerNow() { return Date.now() + syncClockOffsetMS; }
function syncRunElapsed(run, at) {
  return Math.max(0, run.startedAtMS > 0 ? (at > 0 ? at : syncServerNow()) - run.startedAtMS : Date.now() - run.start);
}
function syncStartRunTimer(run) {
  clearInterval(runTimer);
  if (typeof runActivityStart === "function" && activeRun === run && !runFinishing) runActivityStart(run.start);
  function update() {
    if (activeRun !== run || runFinishing) return;
    runStatus.textContent = pendingImmediate ? t("composer.sendingQueued") :
      t("composer.working") + " " + fmtDuration(syncRunElapsed(run));
  }
  update();
  runTimer = setInterval(update, 100);
}

function syncRecoverSession() {
  if (!syncFollowingTurn || syncSession || syncRecoveryPending) return;
  var turn = syncFollowingTurn;
  syncRecoveryPending = true;
  // A connection may be lost before even the fresh session ID was received.
  j("/api/chat/completion?id=" + encodeURIComponent(turn)).then(function (receipt) {
    if (syncFollowingTurn !== turn || activeSessionID || !receipt.session_id) return;
    syncFollowingTurn = "";
    activeSessionID = receipt.session_id;
    syncSelectSession(receipt.session_id);
  }).catch(function () {}).finally(function () { syncRecoveryPending = false; });
}

function syncFollowRun(run) {
  if (!run || run.remote || run.following || run.stopRequested || run.accepted === false) return;
  run.following = true;
  syncFollowingTurn = run.sessionID ? "" : run.turnID;
  if (run.disconnect) run.disconnect();
  if (detachedRuns[run.sessionID] === run) delete detachedRuns[run.sessionID];
  syncReleaseView(run);
  // The old chat transport no longer owns delivery; request its replay too.
  syncClientID = newChatTurnID();
  syncSession = activeSessionID;
  syncConnect();
}

function syncOwnRun(info) {
  return info.client_id === syncClientID ||
    (activeRun && !activeRun.remote && !activeRun.following && activeRun.turnID === info.turn_id) ||
    (detachedRuns[info.session_id] && !detachedRuns[info.session_id].remote && !detachedRuns[info.session_id].following);
}
function syncRunFor(id) {
  var run = syncRuns[id];
  return run && run.remote && run.userSeq > 0 ? run : null;
}
function syncLoadSessions() {
  if (syncListPending) return;
  syncListPending = true;
  Promise.resolve().then(function () { syncListPending = false; return loadSessions(); });
}
function syncReleaseView(run) {
  if (activeRun !== run) return;
  Object.keys(questionOverlays).forEach(closeQuestionOverlay);
  finishRunPresentation();
  activeRun = null;
  streaming = false;
  runFinishing = false;
  if (typeof resumeWorkerStatusSync === "function") resumeWorkerStatusSync();
  abortCtl = null;
  run.attached = false;
  run.detached = true;
  queueDispatching = false;
  queueDispatchItem = pendingImmediate;
  releaseLiveTranscriptBlocks();
  setRunState("idle", t("composer.ready"));
}
function syncSettleTranscriptScroll() {
  settleTranscriptScroll(function () {
    return !syncResolveReady && !syncRefreshPending && !syncRefreshAgain && !syncOpeningSession;
  });
}
function syncRefreshView() {
  if (!syncSession || activeSessionID !== syncSession ||
      (activeRun && !activeRun.remote)) return;
  if (syncOpeningSession) { syncRefreshAgain = true; return; }
  if (syncRefreshPending) { syncRefreshAgain = true; return; }
  syncRefreshPending = true;
  var id = syncSession;
  Promise.resolve(resumeSession(id, sessionByID[id], false, true)).finally(function () {
    syncRefreshPending = false;
    if (syncRefreshAgain) { syncRefreshAgain = false; syncRefreshView(); }
    syncSettleTranscriptScroll();
  });
}
function syncSessionOpened(id) {
  if (syncOpeningSession === id) syncOpeningSession = "";
  renderBackgroundAgents();
  var run = syncRunFor(id);
  if (syncRefreshAgain && !syncRefreshPending) { syncRefreshAgain = false; syncRefreshView(); }
  else if (run && !run.attached && activeSessionID === id) syncRefreshView();
}
// A turn of the viewed conversation started elsewhere (another window won
// the queued message, a phone, ...): its user message is shown at once and
// stays until this view renders that run or saved history that contains it.
// Before, a queued message vanished from this view when its row left the
// queue and came back only after a history reload (seconds on a long chat).
var syncEcho = null; // {session, text, node, turn, finishedAt}
function syncLastUserText() {
  if (typeof stream === "undefined" || !stream || typeof stream.querySelectorAll !== "function") return "";
  var users = stream.querySelectorAll(".msg-user");
  var last = users[users.length - 1];
  var text = last && last.querySelector(".msg-user-text");
  return text ? text.textContent : "";
}
function syncEchoNode(text) {
  var node = addUserMsg(text, 0, []);
  node.classList.add("pending-remote");
  return node;
}
function syncEchoRunStarted(run) {
  if (!run || !run.remote || run.auto || !run.prompt || !run.sessionID || run.sessionID !== activeSessionID) return;
  if (activeRun && !activeRun.remote) return; // this view's own turn is shown
  if (syncEcho && syncEcho.session === run.sessionID && syncEcho.text === run.prompt && syncEcho.node && syncEcho.node.isConnected) {
    syncEcho.turn = run.turnID; // our bubble of a lost queue claim
    return;
  }
  var node = syncLastUserText() === userMessageDisplayText(run.prompt, []) ? null : syncEchoNode(run.prompt);
  syncEcho = {session: run.sessionID, text: run.prompt, node: node, turn: run.turnID, finishedAt: 0};
}
function syncEchoRunFinished(turnID) {
  if (syncEcho && (!syncEcho.turn || syncEcho.turn === turnID)) syncEcho.finishedAt = Date.now();
}
// The run is now rendered from its own events: the echo is no longer needed.
function syncEchoAttached(run) {
  if (syncEcho && syncEcho.turn === run.turnID) syncEcho = null;
}
// After saved history was rendered: drop the echo once history contains it
// (or history fetched after the run finished does not); otherwise put the
// pending bubble back, so the message never disappears in between.
function syncAfterHistoryRender(id, fetchStartedAt) {
  var echo = syncEcho;
  if (!echo || echo.session !== id) return;
  if ((activeRun && !activeRun.remote) || syncLastUserText() === userMessageDisplayText(echo.text, []) ||
      (echo.finishedAt && fetchStartedAt >= echo.finishedAt)) { syncEcho = null; return; }
  if (!echo.node || !echo.node.isConnected) echo.node = syncEchoNode(echo.text);
}
// A queued message this view tried to start was started by another view:
// keep this view's own bubble as the echo of that run.
function syncAdoptLostClaim(node, text) {
  if (!node) return;
  if (syncEcho && syncEcho.text === text && syncEcho.node && syncEcho.node.isConnected && syncEcho.node !== node) {
    node.remove(); // the winner's message is already shown here
    return;
  }
  node.classList.add("pending-remote");
  var run = syncRuns[activeSessionID];
  var live = run && run.remote && run.prompt === text;
  syncEcho = {session: activeSessionID, text: text, node: node, turn: live ? run.turnID : "", finishedAt: live ? 0 : Date.now()};
}
function syncRemember(run, ev) {
  if (ev.type === "session_activity") { run.userSeq = ev.user_seq || 0; run.activity = ev; }
  if (ev.type === "question_closed") {
    run.events = run.events.filter(function (old) { return !(old.type === "question" && old.question && old.question.id === ev.id); });
  }
  var last = run.events[run.events.length - 1];
  if ((ev.type === "message" || ev.type === "reasoning") && last && last.type === ev.type) {
    // Don't mutate an object already handed to the renderer.
    run.events[run.events.length - 1] = Object.assign({}, last, {text: (last.text || "") + (ev.text || ""), reasoning_tok: (last.reasoning_tok || 0) + (ev.reasoning_tok || 0)});
  } else run.events.push(ev);
}
function syncRender(run, ev) {
  if (ev.type === "session" || ev.type === "session_activity") return;
  if (ev.type === "finishing") { finishRunPresentation(); return; }
  run.current = handleEvent(ev, run.current);
  if (ev.type === "done" || ev.type === "error") finishRunPresentation();
}
function syncMakeRun(info) {
  var run = {remote: true, sessionID: info.session_id, turnID: info.turn_id, prompt: info.prompt,
    project_path: info.project_path, project_name: info.project_name, title: info.title, state: info.state,
    auto: !!info.auto, accepted: true, detached: true, attached: false, userSeq: 0, events: [], current: null, start: info.started_at_ms > 0 ? info.started_at_ms - syncClockOffsetMS : Date.now(), startedAtMS: info.started_at_ms || 0};
  run.abortCtl = {abort: function () {
    jpost("/api/sync/stop", {session_id: run.sessionID, turn_id: run.turnID}).catch(function (error) { toast(error.message); });
  }};
  run.reattach = function () {
    run.detached = false;
    run.attached = true;
    run.current = null;
    activeRun = run;
    streaming = true;
    runFinishing = false;
    abortCtl = run.abortCtl;
    runStart = run.start;
    runToolCount = 0;
    transcriptLiveAppend = true;
    toolRows = {}; openToolOrder = [];
    sendBtn.textContent = t("composer.queue");
    sendBtn.classList.add("queue");
    $("#stop-run-btn").hidden = false;
    $("#interrupt-btn").hidden = false;
    $("#interrupt-btn").disabled = false;
    setRunState("running", t("composer.working"));
    if (run.auto) {
      // Server-started turn for finished background agents: not user input.
      addEventLine(t("background.wake"), "auto-wake", "auto");
    } else {
      syncEchoAttached(run);
      var node = addUserMsg(run.prompt, 0, []);
      if (run.activity) bindLiveMessageReceipt(node, run.activity);
    }
    syncStartRunTimer(run);
    run.events.forEach(function (ev) { syncRender(run, ev); });
  };
  return run;
}
function syncHandleFrame(frame) {
  if (frame.server_now_ms > 0) syncClockOffsetMS = frame.server_now_ms - Date.now();
  if (frame.type === "sessions_changed") { syncLoadSessions(); syncLoadProjects(); return; }
  if (frame.type === "queue_changed") {
    if (typeof loadPromptQueue === "function") loadPromptQueue();
    return;
  }
  if (frame.type === "background_wake_held") {
    if (frame.count > 0) syncWakeHeld[frame.session_id] = {n: frame.count, limit: frame.limit || 0};
    else delete syncWakeHeld[frame.session_id];
    renderWakeHeld();
    return;
  }
  if (frame.type === "background_questions") {
    if (frame.count > 0) syncQuestionCounts[frame.session_id] = frame.count;
    else delete syncQuestionCounts[frame.session_id];
    renderSessionBackground();
    return;
  }
  if (frame.type === "background_agents") {
    if (frame.count > 0) syncBackgroundCounts[frame.session_id] = frame.count;
    else delete syncBackgroundCounts[frame.session_id];
    renderBackgroundAgents();
    return;
  }
  if (frame.type === "background_event") {
    if (frame.session_id === activeSessionID && !syncOpeningSession) syncBackgroundEvent(frame.event);
    return;
  }
  if (frame.type === "data_changed") {
    if (typeof panelLiveRefresh === "function") panelLiveRefresh(frame.kind);
    return;
  }
  if (frame.type === "snapshot") {
    if (activeRun && activeRun.remote) syncReleaseView(activeRun);
    Object.keys(detachedRuns).forEach(function (id) { if (detachedRuns[id].remote) delete detachedRuns[id]; });
    syncRuns = Object.create(null);
    syncBackgroundCounts = Object.create(null);
    Object.keys(frame.background || {}).forEach(function (id) { syncBackgroundCounts[id] = frame.background[id]; });
    syncWakeHeld = Object.create(null);
    Object.keys(frame.held || {}).forEach(function (id) { syncWakeHeld[id] = {n: frame.held[id], limit: frame.limit || 0}; });
    syncQuestionCounts = Object.create(null);
    Object.keys(frame.questions || {}).forEach(function (id) { syncQuestionCounts[id] = frame.questions[id]; });
    if (activeSessionID && syncQuestionCounts[activeSessionID]) restorePendingQuestions(activeSessionID);
    renderBackgroundAgents();
    (frame.runs || []).forEach(function (info) {
      if (syncFollowingTurn && info.turn_id === syncFollowingTurn && !syncSession) {
        activeSessionID = info.session_id;
        syncFollowingTurn = "";
        syncSelectSession(info.session_id);
      }
      if (syncOwnRun(info)) { syncRuns[info.session_id] = info; return; }
      var run = syncMakeRun(info);
      syncRuns[info.session_id] = run;
      if (info.session_id === syncSession) (frame.events || []).forEach(function (ev) { syncRemember(run, ev); });
    });
    syncRenderElsewhere();
    syncLoadProjects();
    syncBackoff = 500;
    if (typeof loadPromptQueue === "function") loadPromptQueue();
    // Changes published while disconnected were missed; open views re-check.
    if (typeof panelLiveRefresh === "function") { panelLiveRefresh("goal"); panelLiveRefresh("memory"); }
    if (syncResolveReady) { syncResolveReady(); syncResolveReady = null; }
    syncLoadSessions();
    syncRecoverSession();
    syncRefreshView();
    syncSettleTranscriptScroll();
    return;
  }
  var id = frame.session_id;
  if (frame.type === "run_started") {
    syncRuns[id] = syncOwnRun(frame) ? frame : syncMakeRun(frame);
    if (id === activeSessionID) syncEchoRunStarted(syncRuns[id]);
    syncRenderElsewhere();
    syncLoadSessions();
    return;
  }
  var run = syncRuns[id];
  if (frame.type === "run_updated") {
    if (run) {
      run.state = frame.state; run.title = frame.title;
      run.project_path = frame.project_path; run.project_name = frame.project_name;
      syncRenderElsewhere();
    }
    return;
  }
  if (frame.type === "run_event") {
    if (!run || !run.remote || syncOwnRun(frame)) return;
    syncRemember(run, frame.event);
    if (run.attached && !run.detached && activeRun === run && id === activeSessionID) syncRender(run, frame.event);
    else if (run.userSeq && id === syncSession && !syncOpeningSession && !syncRefreshPending) syncRefreshView();
  } else if (frame.type === "run_finished") {
    syncEchoRunFinished(frame.turn_id);
    if (frame.queue_paused && id === activeSessionID && !pendingImmediate) {
      pauseQueue = true;
      if (typeof queueAutoDrain !== "undefined") queueAutoDrain = null;
    }
    delete syncRuns[id];
    syncRenderElsewhere();
    if (run && run.remote) {
      delete detachedRuns[id];
      syncReleaseView(run);
      if (id === syncSession) {
        // run_finished is sent only after the writer and session gate release.
        syncRefreshView();
        var immediate = pendingImmediate;
        pendingImmediate = null;
        if (frame.queue_paused && !immediate) pauseQueue = true;
        // Match the local HTTP-EOF handoff, including transport takeover.
        var automatic = !immediate && typeof queueDrainScope === "function" ? queueDrainScope(run.turn_id || id) : null;
        Promise.all([sessionRuntimeReady, typeof promptQueueReady !== "undefined" ? promptQueueReady : null]).then(function () {
          var next = immediate && (isAfterRunPrompt(immediate) || !pauseQueue) ? immediate :
            (!pauseQueue && typeof promptQueue !== "undefined" ? promptQueue[0] : null);
          if (next && !streaming) dispatchQueuedTask(next, automatic);
        });
      }
    }
    syncLoadSessions();
  }
}
function syncDisconnect() {
  ++syncGeneration;
  clearTimeout(syncRetry); syncRetry = null;
  if (syncConnection) syncConnection.close();
  syncConnection = null;
  if (syncResolveReady) { syncResolveReady(); syncResolveReady = null; }
}
function syncConnect() {
  syncDisconnect();
  if (document.hidden) return;
  if (typeof refreshHostPower === "function") refreshHostPower();
  var generation = syncGeneration;
  syncReady = new Promise(function (resolve) { syncResolveReady = resolve; });
  var query = "?session=" + encodeURIComponent(syncSession) + "&client=" + encodeURIComponent(syncClientID);
  function receive(data) {
    if (generation !== syncGeneration) return;
    var frame = superCliUI.decodeSSEFrame(data);
    if (frame) {
      // Existing push traffic discovers newly armed host state without idle
      // polling; refresh is independent of sidebar/session loading.
      if (typeof refreshHostPower === "function" && (frame.type === "snapshot" || frame.type === "run_started" || frame.type === "run_finished" || (frame.type === "run_event" && frame.event && /^(tool_result|ask_user|done|error)$/.test(frame.event.type)))) refreshHostPower();
      syncHandleFrame(frame);
    }
  }
  function retry() {
    if (generation !== syncGeneration) return;
    syncDisconnect();
    if (document.hidden) return;
    syncRetry = setTimeout(syncConnect, syncBackoff);
    syncBackoff = Math.min(syncBackoff * 2, 15000);
  }
  if (/^(localhost|127(?:\.[0-9]+){3}|\[?::1\]?)$/i.test(location.hostname)) {
    var source = new EventSource("/api/sync" + query);
    syncConnection = source;
    source.onmessage = function (event) { receive("data: " + event.data); };
    source.onerror = retry;
  } else {
    var socket = new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/api/sync/ws" + query);
    syncConnection = socket;
    socket.onmessage = function (event) { receive(event.data); };
    socket.onerror = retry;
    socket.onclose = retry;
  }
}
function syncSelectSession(id) {
  if (syncSession !== id && typeof queueAutoDrain !== "undefined") queueAutoDrain = null;
  syncFollowingTurn = "";
  syncOpeningSession = "";
  if (syncSession !== id || !syncConnection) { syncSession = id; syncConnect(); }
}
function syncWatchSession(id) {
  if (syncSession !== id && typeof queueAutoDrain !== "undefined") queueAutoDrain = null;
  syncFollowingTurn = "";
  syncOpeningSession = id;
  if (syncSession !== id || !syncConnection) { syncSession = id; syncConnect(); }
  return syncReady;
}
function syncSuspend() {
  captureTranscriptScroll();
  syncDisconnect();
  if (typeof pauseWorkerStatusSync === "function") pauseWorkerStatusSync();
}
document.addEventListener("visibilitychange", function () {
  if (document.hidden) syncSuspend();
  else syncResume();
});
function syncResume() {
  resumeTranscriptScroll();
  if (activeRun && !activeRun.remote) syncFollowRun(activeRun);
  Object.keys(detachedRuns).forEach(function (id) { if (!detachedRuns[id].remote) syncFollowRun(detachedRuns[id]); });
  syncSession = activeSessionID; syncConnect();
  if (typeof resumeWorkerStatusSync === "function") resumeWorkerStatusSync();
}
window.addEventListener("focus", function () {
  if (typeof resumeWorkerStatusSync === "function") resumeWorkerStatusSync();
});
window.addEventListener("pageshow", syncResume);
window.addEventListener("online", syncResume);
window.addEventListener("pagehide", syncSuspend);
