"use strict";

/* ═══ sessions ═══ */

var sessionByID = {};
var sessionResumeSeq = 0;

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
    label = new Intl.DateTimeFormat(statsLocale(), {
      day: "numeric", month: "short", year: date.getFullYear() === today.getFullYear() ? undefined : "numeric",
    }).format(date);
  } catch (e) { label = date.toLocaleDateString(); }
  return { key: key, label: label };
}

async function loadSessions() {
  var list = $("#session-list");
  try {
    var rows = await j("/api/sessions?limit=40");
	sessionByID = {}; (rows || []).forEach(function(s){sessionByID[s.id]=s;});
    list.innerHTML = "";
    if (!rows || !rows.length) {
      list.appendChild(el("div", "side-empty", t("side.noSessions")));
      return;
    }
    var currentDateGroup = "";
    rows.forEach(function (s) {
      var dateGroup = sessionDateGroup(s.started_at);
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

      var sessionMeta = fmtWhen(s.started_at) + " · " + s.message_count;
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
    list.innerHTML = "";
    list.appendChild(el("div", "side-empty", t("common.error")));
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
var transcriptPageSize = 100;

function buildHistoryFragment(messages) {
  var historyCalls = {};
  var fragment = document.createDocumentFragment();
  streamAppendTarget = fragment;
  try {
    (messages || []).forEach(function (m) {
      if (m.role === "user") {
        addUserMsg(m.content, m.seq, sentAttachmentsFor(transcriptSessionID || activeSessionID, m.seq));
      } else if (m.role === "assistant") {
        (m.tool_calls || []).forEach(function (call) { historyCalls[call.id] = call; });
        if (!m.content) {
          if (m.turn) { addFileChanges(m.turn.file_changes); addTurnMeta(m.turn, m.turn.elapsed_ms || 0, m.turn.tool_calls || 0, m.seq); }
          return;
        }
        var node = addAssistantMsg();
        node._raw = m.content;
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
          addHistoryTask(task);
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
        sum.appendChild(el("span", "thint", historyInfo.hint));
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
        if (persistedArgs && !FILE_READ_TOOLS[persistedName]) {
          body.appendChild(el("div", "lbl", t("tool.input")));
          body.appendChild(el("pre", "", prettyJSON(persistedArgs)));
        }
        appendToolPayload(body, t("tool.output"), m.content || "", persistedName, false);
        row.appendChild(body);
        appendStream(row);
      }
    });
  } finally {
    streamAppendTarget = null;
  }
  return fragment;
}

function renderLoadedTranscript(preserveScroll) {
  var oldHeight = stage.scrollHeight;
  var oldTop = stage.scrollTop;
  stream.innerHTML = "";
  toolRows = {}; workerRows = {}; openToolOrder = [];
  lastTurn = null; workersSeen = [];
  hideWelcome();
  if (transcriptHasMore) {
    var older = el("button", "history-older", t("session.older"));
    older.type = "button";
    older.addEventListener("click", loadOlderTranscript);
    stream.appendChild(older);
  }
  stream.appendChild(buildHistoryFragment(loadedTranscriptMessages));
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
    loadedTranscriptMessages = (page.messages || []).concat(loadedTranscriptMessages);
    transcriptHasMore = !!page.has_more;
    transcriptBeforeSeq = page.before_seq || 0;
    renderLoadedTranscript(true);
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

async function resumeSession(id, session) {
  if (streaming) {
    toast(t("session.stopRun"));
    return false;
  }
  var resumeSeq = ++sessionResumeSeq;
  var epoch = projectEpoch;
  var releaseRuntimeReady;
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
    transcriptSessionID = id;
    loadedTranscriptMessages = msgs;
    transcriptHasMore = !!page.has_more;
    transcriptBeforeSeq = page.before_seq || 0;
    stream.innerHTML = "";
    toolRows = {}; workerRows = {}; openToolOrder = [];
    lastTurn = null; workersSeen = [];
    hideWelcome();
    var historyCalls = {};
	var historyFragment = document.createDocumentFragment();
	if (transcriptHasMore) {
	  var older = el("button", "history-older", t("session.older"));
	  older.type = "button";
	  older.addEventListener("click", loadOlderTranscript);
	  historyFragment.appendChild(older);
	}
	streamAppendTarget = historyFragment;
	(msgs || []).forEach(function (m) {
      if (m.role === "user") {
        addUserMsg(m.content, m.seq, sentAttachmentsFor(id, m.seq));
	      } else if (m.role === "assistant") {
        (m.tool_calls || []).forEach(function (call) { historyCalls[call.id] = call; });
        if (!m.content) {
          if (m.turn) { addFileChanges(m.turn.file_changes); addTurnMeta(m.turn, m.turn.elapsed_ms || 0, m.turn.tool_calls || 0, m.seq); }
          return;
        }
        var node = addAssistantMsg();
        node._raw = m.content;
        renderAssistant(node);
        // History replay: thinking folded (only live streams open it).
	        node.querySelectorAll("details[data-think-id]").forEach(function (d) { d.open = false; });
        if (m.turn) {
          addFileChanges(m.turn.file_changes);
          addTurnMeta(m.turn, m.turn.elapsed_ms || 0, m.turn.tool_calls || 0, m.seq);
        }
      } else if (m.role === "tool") {
        var task = (m.name === "task" || String(m.content || "").indexOf("<task-notification>") >= 0) ?
          parseTaskNotification(m.content) : null;
        if (task) {
          addHistoryTask(task);
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
        sum.appendChild(el("span", "thint", historyInfo.hint));
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
        if (persistedArgs && !FILE_READ_TOOLS[persistedName]) {
          body.appendChild(el("div", "lbl", t("tool.input")));
          body.appendChild(el("pre", "", prettyJSON(persistedArgs)));
        }
        appendToolPayload(body, t("tool.output"), m.content || "", persistedName, false);
        row.appendChild(body);
        appendStream(row);
      }
	});
	streamAppendTarget = null;
	stream.appendChild(historyFragment);
	smartScroll(true);
    loadSessions();
    renderStats();
    promptEl.focus();
    // Remembered provider/model restoration continues after the conversation
    // is already usable. Errors retain the current model and are surfaced by
    // restoreSessionRuntime without rolling back the opened transcript.
    runtimeQueued = true;
    restoreSessionRuntime(session).then(releaseRuntimeReady, releaseRuntimeReady);
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
