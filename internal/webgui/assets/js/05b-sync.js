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
  if (!run || run.remote || run.following || run.stopRequested) return;
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
  var run = syncRunFor(id);
  if (syncRefreshAgain && !syncRefreshPending) { syncRefreshAgain = false; syncRefreshView(); }
  else if (run && !run.attached && activeSessionID === id) syncRefreshView();
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
    accepted: true, detached: true, attached: false, userSeq: 0, events: [], current: null, start: info.started_at_ms > 0 ? info.started_at_ms - syncClockOffsetMS : Date.now(), startedAtMS: info.started_at_ms || 0};
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
    var node = addUserMsg(run.prompt, 0, []);
    if (run.activity) bindLiveMessageReceipt(node, run.activity);
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
  if (frame.type === "snapshot") {
    if (activeRun && activeRun.remote) syncReleaseView(activeRun);
    Object.keys(detachedRuns).forEach(function (id) { if (detachedRuns[id].remote) delete detachedRuns[id]; });
    syncRuns = Object.create(null);
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
    if (frame.queue_paused && id === activeSessionID && !pendingImmediate) pauseQueue = true;
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
        Promise.all([sessionRuntimeReady, typeof promptQueueReady !== "undefined" ? promptQueueReady : null]).then(function () {
          var next = immediate && (isAfterRunPrompt(immediate) || !pauseQueue) ? immediate :
            (!pauseQueue && typeof promptQueue !== "undefined" ? promptQueue[0] : null);
          if (next && !streaming) dispatchQueuedTask(next);
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
  var generation = syncGeneration;
  syncReady = new Promise(function (resolve) { syncResolveReady = resolve; });
  var query = "?session=" + encodeURIComponent(syncSession) + "&client=" + encodeURIComponent(syncClientID);
  function receive(data) {
    if (generation !== syncGeneration) return;
    var frame = superCliUI.decodeSSEFrame(data);
    if (frame) syncHandleFrame(frame);
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
  syncFollowingTurn = "";
  syncOpeningSession = "";
  if (syncSession !== id || !syncConnection) { syncSession = id; syncConnect(); }
}
function syncWatchSession(id) {
  syncFollowingTurn = "";
  syncOpeningSession = id;
  if (syncSession !== id || !syncConnection) { syncSession = id; syncConnect(); }
  return syncReady;
}
function syncSuspend() {
  captureTranscriptScroll();
  syncDisconnect();
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
}
window.addEventListener("pageshow", syncResume);
window.addEventListener("online", syncResume);
window.addEventListener("pagehide", syncSuspend);
