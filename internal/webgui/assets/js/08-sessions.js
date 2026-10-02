"use strict";

/* ═══ sessions ═══ */

var sessionByID = {};
var sessionRuntimeRevision = 0;
var sessionResumeSeq = 0;
var sessionListSeq = 0;
var sessionListAbortCtl = null;
var sessionListSnapshot = null;

function setSessionOpening(id) {
  $$("#session-list .side-item").forEach(function (item) {
    var opening = !!id && item.dataset.sessionId === id;
    item.classList.toggle("opening", opening);
    item.classList.toggle("active", opening || item.dataset.sessionId === activeSessionID);
    if (opening) item.setAttribute("aria-busy", "true");
    else item.removeAttribute("aria-busy");
  });
}

function compactSessionModel(model) {
  var value = String(model || "").replace(/\.gguf$/i, "");
  if (value.length <= 32) return value;
  return value.slice(0, 21) + "…" + value.slice(-10);
}

function sessionDateGroup(iso) {
  var date = new Date(iso);
  if (isNaN(date)) return { key: "unknown", label: "" };
  var today = new Date();
  var startToday = new Date(today.getFullYear(), today.getMonth(), today.getDate());
  var startDate = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  var days = Math.round((startToday - startDate) / 86400000);
  if (days === 0) return { key: "today", label: t("session.today") };
  if (days === 1) return { key: "yesterday", label: t("session.yesterday") };
  var key = date.getFullYear() + "-" + date.getMonth() + "-" + date.getDate();
  var label;
  try {
    label = statsFormatter("sessionDate:" + (date.getFullYear() === today.getFullYear() ? "short" : "year"), {
      day: "numeric", month: "short", year: date.getFullYear() === today.getFullYear() ? undefined : "numeric",
    }, true).format(date);
  } catch (e) { label = date.toLocaleDateString(); }
  return { key: key, label: label };
}

async function loadSessions() {
  var list = $("#session-list");
  var runtimeRevision = sessionRuntimeRevision;
  var epoch = projectEpoch;
  var seq = ++sessionListSeq;
  if (sessionListAbortCtl) sessionListAbortCtl.abort();
  var controller = new AbortController();
  sessionListAbortCtl = controller;
  function stale() {
    return seq !== sessionListSeq || epoch !== projectEpoch || runtimeRevision !== sessionRuntimeRevision;
  }
  try {
    var rows = await j("/api/sessions?limit=40", { signal: controller.signal });
    if (stale()) return;
	sessionByID = {}; (rows || []).forEach(function(s){sessionByID[s.id]=s;});
    // Refreshing unchanged metadata should preserve rows, hover and keyboard
    // focus. Minute/locale/selection changes still refresh the visible labels.
    var snapshot = JSON.stringify([rows || [], activeSessionID, statsLocale(), Math.floor(Date.now() / 60000)]);
    if (snapshot === sessionListSnapshot) return;
    sessionListSnapshot = snapshot;
    list.innerHTML = "";
    if (!rows || !rows.length) {
      list.appendChild(i18nEl("div", "side-empty", "side.noSessions"));
      return;
    }
    var currentDateGroup = "";
    rows.forEach(function (s) {
      var activityAt = s.updated_at || s.started_at;
      var dateGroup = sessionDateGroup(activityAt);
      if (dateGroup.key !== currentDateGroup) {
        currentDateGroup = dateGroup.key;
        if (dateGroup.label) list.appendChild(el("div", "session-date", dateGroup.label));
      }
      var b = el("button", "side-item" + (s.id === activeSessionID ? " active" : ""));
      b.type = "button";
      b.dataset.sessionId = s.id;
      var title = el("span", "t");
      var titleText = el("span", "t-scroll", s.first_user_msg || s.id);
      title.appendChild(titleText);
      b.appendChild(title);

      function syncTitleMarquee() {
        var viewportWidth = title.clientWidth;
        var textWidth = titleText.scrollWidth;
        var overflow = textWidth - viewportWidth;
        if (overflow > 2) {
          var distance = overflow + 64;
          var duration = Math.max(2.8, Math.min(8, distance / 50 + 1.6));
          b.classList.add("title-overflow");
          title.style.setProperty("--session-marquee-distance", distance.toFixed(1) + "px");
          title.style.setProperty("--session-marquee-duration", duration.toFixed(2) + "s");
        } else {
          b.classList.remove("title-overflow");
          title.style.removeProperty("--session-marquee-distance");
          title.style.removeProperty("--session-marquee-duration");
        }
      }
      b.addEventListener("pointerenter", syncTitleMarquee);
      b.addEventListener("focusin", syncTitleMarquee);

      var sessionMeta = fmtWhen(activityAt) + " · " + s.message_count;
      if (s.model) sessionMeta += " · " + compactSessionModel(s.model);
      var meta = el("span", "s", sessionMeta);
      meta.title = s.model || "";
      b.appendChild(meta);
      b.addEventListener("click", function () { resumeSession(s.id, s); });

      var actions = el("span", "session-actions");
      var rename = el("span", "session-action rename", "\u270E");
      rename.setAttribute("role", "button");
      rename.tabIndex = 0;
      rename.title = t("session.rename");
      rename.setAttribute("aria-label", t("session.rename"));
      function doRename(e) {
        e.preventDefault();
        e.stopPropagation();
        renameSession(s);
      }
      rename.addEventListener("click", doRename);
      rename.addEventListener("keydown", function (e) { if (e.key === "Enter" || e.key === " ") doRename(e); });
      actions.appendChild(rename);

      var remove = el("span", "session-action delete", "\u00D7");
      remove.setAttribute("role", "button");
      remove.tabIndex = 0;
      remove.title = t("session.delete");
      remove.setAttribute("aria-label", t("session.delete"));
      function doDelete(e) {
        e.preventDefault();
        e.stopPropagation();
        deleteSession(s.id);
      }
      remove.addEventListener("click", doDelete);
      remove.addEventListener("keydown", function (e) { if (e.key === "Enter" || e.key === " ") doDelete(e); });
      actions.appendChild(remove);
      b.appendChild(actions);
      list.appendChild(b);
    });
  } catch (e) {
    if (e.name === "AbortError" || stale()) return;
    sessionListSnapshot = null;
    list.innerHTML = "";
    list.appendChild(i18nEl("div", "side-empty", "common.error"));
  } finally {
    if (sessionListAbortCtl === controller) sessionListAbortCtl = null;
  }
}

async function rewindSession(id, selectedSeq, text, reason, rewindFiles, button) {
  if (!id || !selectedSeq || streaming) return false;
  if (button) button.disabled = true;
  try {
    var result = await jpost("/api/session/rewind", {
      session_id: id,
      selected_seq: selectedSeq,
      rewind_files: !!rewindFiles,
      reason: reason || "",
    });
		forgetSentAttachments(id, selectedSeq);
		await loadSessions();
    await resumeSession(id, sessionByID[id] || null);
    promptEl.value = text || "";
    promptEl.dispatchEvent(new Event("input"));
    promptEl.focus();
    promptEl.setSelectionRange(promptEl.value.length, promptEl.value.length);
    toast(t("workflow.rewindDone"));
		if (result.warning) toast(result.warning);
    return true;
  } catch (e) {
    var message = e.message;
    try {
      var detail = JSON.parse(e.message);
      if (detail.conflicts && detail.conflicts.length) {
        message = t("workflow.rewindConflict") + ": " + detail.conflicts.join(", ");
      }
    } catch (ignore) {}
    toast(message);
    if (button && button.isConnected) button.disabled = false;
    return false;
  }
}

async function renameSession(session) {
  var current = session.first_user_msg || "";
  var title = await appPrompt(t("session.namePrompt"), current, {
    message: t("session.renameHint"), confirmLabel: t("common.save"),
  });
  if (title === null) return;
  if (!title || title === current) return;
  try {
    await j("/api/sessions", {
      method: "PATCH", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: session.id, title: title }),
    });
    toast(t("session.renamed"));
    await loadSessions();
    renderStats();
  } catch (e) { toast(e.message); }
}

async function deleteSession(id) {
  if (streaming && id === activeSessionID) {
    toast(t("session.stopRun"));
    return;
  }
  if (!await appConfirm(t("session.deleteConfirm"), {
    title: t("session.delete"), danger: true, confirmLabel: t("common.remove"),
  })) return;
  try {
    await j("/api/sessions?id=" + encodeURIComponent(id), { method: "DELETE" });
    forgetSentAttachments(id);
    if (id === activeSessionID) newSession();
    else await loadSessions();
    toast(t("session.deleted"));
  } catch (e) { toast(e.message); }
}

async function restoreSessionRuntime(session) {
  if (!ui.rememberSessionRuntime || !session || !session.model) return;
  try {
    await jpost("/api/model", { model: session.model, provider: session.provider || "" });
    if (session.runtime_known) {
      await jpost("/api/reasoning", { level: session.reasoning_effort || "default" });
    }
    // The runtime is ready after the two mutations above. Catalog and health
    // are presentation refreshes and must never delay opening the transcript.
    loadModels();
    checkHealth();
  } catch (e) {
    toast(t("session.runtimeFailed"));
  }
}

var transcriptAbortCtl = null;
var loadedTranscriptMessages = [];
var transcriptHasMore = false;
var transcriptBeforeSeq = 0;
var transcriptSessionID = "";
// Keep the initial DOM bounded. Tool outputs and reasoning can make one
// persisted message very large; older pages remain available on demand.
var transcriptPageSize = 60;

function historyPager() {
  var button = i18nEl("button", "history-older", "session.older");
  button.type = "button";
  // Only unresolved boundary calls retain rows. Removing the pager releases
  // this index on a new session/live turn, without another global DOM cache.
  button._pendingHistoryCalls = Object.create(null);
  button.addEventListener("click", loadOlderTranscript);
  return button;
}

function resolveHistoryToolCall(entry, call) {
  var row = entry.row, message = entry.message, body = row._body;
  var name = call.name || message.name || "tool", args = call.arguments || "";
  var info = args ? toolHint(name, args) : {name: toolDisplayName(name), hint: clip(message.content || "", 90)};
  row._tname.title = name;
  row._tname.textContent = info.name;
  row._thint.textContent = info.hint;
  var mutationLabel = mutationOutcomeLabel(name, message.content, /^error:/i.test(String(message.content || "")));
  if (mutationLabel) row._tname.textContent = t(mutationLabel);
  if (row._cancelHistoryPayload) row._cancelHistoryPayload();
  row._cancelHistoryPayload = null;
  if (body.childNodes.length && row._historyName === name) {
    // An already opened boundary result keeps its expensive output viewer.
    // Only its newly discovered input needs adding ahead of the output.
    if (args && !FILE_READ_TOOLS[name]) {
      var input = document.createDocumentFragment();
      input.appendChild(i18nEl("div", "lbl", "tool.input"));
      input.appendChild(el("pre", "", prettyJSON(args)));
      body.insertBefore(input, body.firstChild);
    }
  } else {
    body.innerHTML = "";
    row._cancelHistoryPayload = appendHistoryToolPayload(row, body, args, message.content, name);
  }
  row._historyName = name;
  appendToolMediaPreview(row, message.content, name, /^error:/i.test(String(message.content || "")));
}

function buildHistoryFragment(messages, pager, prepend) {
  var historyCalls = Object.create(null);
  var pendingCalls = pager && pager._pendingHistoryCalls;
  var newerWorkers = prepend ? Object.assign(Object.create(null), workerRows) : null;
  var fragment = document.createDocumentFragment();
  var previousTarget = streamAppendTarget, previousLive = transcriptLiveAppend;
  streamAppendTarget = fragment;
  transcriptLiveAppend = false;
  try {
    // A reused call ID in the incoming page must resolve a newer-page result
    // against its nearest preceding call, not the first historical occurrence.
    // Do this before creating new pending rows, then render this page forward.
    if (pendingCalls) {
      for (var index = (messages || []).length - 1; index >= 0; index--) {
        var prior = messages[index];
        if (prior.role !== "assistant") continue;
        var calls = prior.tool_calls || [];
        for (var callIndex = calls.length - 1; callIndex >= 0; callIndex--) {
          var call = calls[callIndex];
          if (!pendingCalls[call.id]) continue;
          pendingCalls[call.id].forEach(function (entry) { resolveHistoryToolCall(entry, call); });
          delete pendingCalls[call.id];
        }
      }
    }
    (messages || []).forEach(function (m) {
      if (m.role === "user") {
        addUserMsg(m.content, m.seq, m.attachments && m.attachments.length ? m.attachments : sentAttachmentsFor(transcriptSessionID || activeSessionID, m.seq));
      } else if (m.role === "assistant") {
        (m.tool_calls || []).forEach(function (call) { historyCalls[call.id] = call; });
        if (!m.content) {
          if (m.turn) { addFileChanges(m.turn.file_changes); addTurnMeta(m.turn, m.turn.elapsed_ms || 0, m.turn.tool_calls || 0, m.seq); }
          return;
        }
        var node = addAssistantMsg();
        node._raw = m.content;
        node._history = true;
        renderAssistant(node);
        node.querySelectorAll("details[data-think-id]").forEach(function (d) { d.open = false; });
        if (m.turn) {
          addFileChanges(m.turn.file_changes);
          addTurnMeta(m.turn, m.turn.elapsed_ms || 0, m.turn.tool_calls || 0, m.seq);
        }
      } else if (m.role === "tool") {
        var task = (m.name === "task" || String(m.content || "").indexOf("<task-notification>") >= 0) ?
          parseTaskNotification(m.content) : null;
        if (task) {
          var newerWorker = newerWorkers && newerWorkers[task.id];
          addHistoryTask(task);
          if (newerWorker) workerRows[task.id] = newerWorker;
          return;
        }
        var persistedCall = historyCalls[m.tool_call_id] || null;
        var persistedName = (persistedCall && persistedCall.name) || m.name || "tool";
        var persistedArgs = persistedCall ? persistedCall.arguments : "";
        var historyInfo = persistedArgs ? toolHint(persistedName, persistedArgs) :
          { name: toolDisplayName(persistedName), hint: clip(m.content || "", 90) };
        var row = document.createElement("details");
        row.className = "tool-row done";
        var sum = el("summary");
        var historyName = el("span", "tname", historyInfo.name);
        historyName.title = persistedName;
        sum.appendChild(historyName);
        var historyHint = el("span", "thint", historyInfo.hint);
        sum.appendChild(historyHint);
        var historyStat = el("span", "tstat", "");
        var historyChanges = toolChangeStats(persistedName, m.content);
        var historyMutationLabel = mutationOutcomeLabel(persistedName, m.content, /^error:/i.test(String(m.content || "")));
        if (historyMutationLabel) historyName.textContent = t(historyMutationLabel);
        if (historyChanges.added) historyStat.appendChild(el("span", "change-add", "+" + historyChanges.added));
        if (historyChanges.removed) historyStat.appendChild(el("span", "change-remove", "−" + historyChanges.removed));
        if (historyChanges.diff || historyMutationLabel) row.classList.add("has-changes");
        sum.appendChild(historyStat);
        row.appendChild(sum);
        var body = el("div", "tbody");
        row._body = body; row._tname = historyName; row._thint = historyHint;
        row._historyName = persistedName;
        row._cancelHistoryPayload = appendHistoryToolPayload(row, body, persistedArgs, m.content, persistedName);
        if (!persistedCall && m.tool_call_id && pendingCalls) {
          if (!pendingCalls[m.tool_call_id]) pendingCalls[m.tool_call_id] = [];
          pendingCalls[m.tool_call_id].push({row: row, message: m});
        }
        row.appendChild(body);
        appendStream(row);
        appendToolMediaPreview(row, m.content, persistedName, /^error:/i.test(String(m.content || "")));
      }
    });
  } finally {
    streamAppendTarget = previousTarget;
    transcriptLiveAppend = previousLive;
  }
  return fragment;
}

function renderLoadedTranscript(preserveScroll) {
  var oldHeight = stage.scrollHeight;
  var oldTop = stage.scrollTop;
  stream.innerHTML = "";
  toolRows = {}; workerRows = {}; openToolOrder = [];
  resetWorkerOverview();
  lastTurn = null; workersSeen = [];
  hideWelcome();
  var older = transcriptHasMore ? historyPager() : null;
  if (older) stream.appendChild(older);
  stream.appendChild(buildHistoryFragment(loadedTranscriptMessages, older));
  // Rows own their complete lazy payloads and unresolved boundary messages.
  // The raw page wrappers are no longer read after this successful render.
  loadedTranscriptMessages = [];
  if (preserveScroll) {
    stage.scrollTop = oldTop + Math.max(0, stage.scrollHeight - oldHeight);
  } else {
    smartScroll(true);
  }
}

async function loadOlderTranscript() {
  if (!transcriptHasMore || !transcriptBeforeSeq || !transcriptSessionID) return;
  var sessionID = transcriptSessionID;
  var button = stream.querySelector(".history-older");
  if (button) {
    button.disabled = true;
    button.textContent = t("session.loadingOlder");
  }
  if (transcriptAbortCtl) transcriptAbortCtl.abort();
  var controller = new AbortController();
  transcriptAbortCtl = controller;
  try {
    var page = await j("/api/transcript?id=" + encodeURIComponent(sessionID) +
      "&limit=" + transcriptPageSize + "&before=" + transcriptBeforeSeq, { signal: controller.signal });
    if (sessionID !== transcriptSessionID || controller !== transcriptAbortCtl) return;
    var olderMessages = page.messages || [];
    var oldHeight = stage.scrollHeight;
    var oldTop = stage.scrollTop;
    // An older page must not rebuild the existing transcript: it may contain
    // expanded results, selected text, or new messages from the active stream.
    // Older task notifications also must not replace the latest worker backlink.
    var currentWorkers = workerRows;
    var olderWorkers = Object.assign(Object.create(null), currentWorkers);
    var liveAppend = transcriptLiveAppend;
    var fragment;
    transcriptLiveAppend = false;
    workerRows = olderWorkers;
    try {
      fragment = buildHistoryFragment(olderMessages, button, true);
    } finally {
      transcriptLiveAppend = liveAppend;
      workerRows = currentWorkers;
    }
    Object.keys(olderWorkers).forEach(function (id) {
      if (!currentWorkers[id]) currentWorkers[id] = olderWorkers[id];
    });
    transcriptHasMore = !!page.has_more;
    transcriptBeforeSeq = page.before_seq || 0;
    stream.insertBefore(fragment, button ? button.nextSibling : stream.firstChild);
    if (button) {
      if (transcriptHasMore) {
        button.disabled = false;
        button.textContent = t("session.older");
      } else button.remove();
    }
    stage.scrollTop = Math.max(0, oldTop + stage.scrollHeight - oldHeight);
  } catch (e) {
    if (e.name !== "AbortError") {
      toast(t("common.error") + ": " + e.message);
      if (button && button.isConnected) {
        button.disabled = false;
        button.textContent = t("session.older");
      }
    }
  } finally {
    if (controller === transcriptAbortCtl) transcriptAbortCtl = null;
  }
}

async function resumeSession(id, session, fromQueue) {
  if (streaming || (queueDispatching && !fromQueue)) {
    toast(t("session.stopRun"));
    return false;
  }
  var resumeSeq = ++sessionResumeSeq;
  var epoch = projectEpoch;
  var releaseRuntimeReady;
  var previousRuntimeReady = sessionRuntimeReady;
  var runtimeQueued = false;
  sessionRuntimeReady = new Promise(function (resolve) { releaseRuntimeReady = resolve; });
  if (transcriptAbortCtl) transcriptAbortCtl.abort();
  var controller = new AbortController();
  transcriptAbortCtl = controller;
  setSessionOpening(id);
  try {
    var page = await j("/api/transcript?id=" + encodeURIComponent(id) + "&limit=" + transcriptPageSize,
      { signal: controller.signal });
    var msgs = page.messages || [];
    if (epoch !== projectEpoch || resumeSeq !== sessionResumeSeq) return false;
    activeSessionID = id;
    composerDraftStore.restore("session:" + id);
    transcriptSessionID = id;
    loadedTranscriptMessages = msgs;
    transcriptHasMore = !!page.has_more;
    transcriptBeforeSeq = page.before_seq || 0;
    renderLoadedTranscript(false);
    loadSessions();
    renderStats();
    promptEl.focus();
    // Remembered provider/model restoration continues after the conversation
    // is already usable. Errors retain the current model and are surfaced by
    // restoreSessionRuntime without rolling back the opened transcript.
    runtimeQueued = true;
    Promise.resolve(previousRuntimeReady).then(function () {
      if (epoch !== projectEpoch || resumeSeq !== sessionResumeSeq) return;
      return restoreSessionRuntime(sessionByID[id] || session);
    }).then(releaseRuntimeReady, releaseRuntimeReady);
    return true;
  } catch (e) {
    if (e.name !== "AbortError") toast(t("common.error") + ": " + e.message);
    return false;
  } finally {
    if (!runtimeQueued) releaseRuntimeReady();
    streamAppendTarget = null;
    if (controller === transcriptAbortCtl) transcriptAbortCtl = null;
    if (resumeSeq === sessionResumeSeq) setSessionOpening("");
  }
}
$("#reload-sessions").addEventListener("click", loadSessions);
