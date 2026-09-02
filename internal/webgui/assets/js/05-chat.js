"use strict";

/* ═══ chat streaming ═══ */

var streaming = false, abortCtl = null, activeSessionID = "", projectEpoch = 0;
var sessionRuntimeReady = Promise.resolve();
var activeQuestionOverlay = null;
var runStart = 0, runTimer = null, runToolCount = 0;
var lastTurn = null;    // last done-event payload + elapsed (stats pane)
var workersSeen = [];   // worker notifications this browser session
var promptEl = $("#prompt"), sendBtn = $("#send-btn"), runStatus = $("#run-status");
var promptQueue = [], pendingImmediate = null, pauseQueue = false, unreadDone = 0;
var pendingAttachments = [];
var appFocused = !document.hidden && document.hasFocus();
var sentAttachmentStorageKey = "supercli-sent-attachments-v1";
var sentAttachmentIndex = loadSentAttachmentIndex();
var composerDraftStore = superCliUI.createComposerDraftStore({
  storageKey: "supercli-composer-drafts-v1",
  input: promptEl,
  getScope: function () {
    return activeSessionID ? "session:" + activeSessionID : "new:" + (activeWorkspacePath || "default");
  },
  onRestore: function () {
    promptEl.style.height = "auto";
    promptEl.style.height = Math.min(promptEl.scrollHeight, 200) + "px";
  },
});

function attachmentName(path) {
  var parts = String(path || "").split(/[\\/]/);
  return parts[parts.length - 1] || path;
}
function attachmentExtension(path) {
  var name = attachmentName(path).toLowerCase();
  var dot = name.lastIndexOf(".");
  return dot >= 0 ? name.slice(dot) : "";
}
function previewableAttachment(path) {
  return [".png", ".jpg", ".jpeg", ".webp", ".gif", ".pdf"].indexOf(attachmentExtension(path)) >= 0;
}
function imageAttachment(path) {
  return [".png", ".jpg", ".jpeg", ".webp", ".gif"].indexOf(attachmentExtension(path)) >= 0;
}
function attachmentPreviewSource(path) {
  return "/api/attachment/preview?path=" + encodeURIComponent(path);
}
function attachmentTranscriptText(text, paths) {
  paths = paths || [];
  if (!paths.length) return text;
  return text + "\n\n" + String.fromCodePoint(0x1F4CE) + " " + paths.map(attachmentName).join(", ");
}
function userMessageDisplayText(text, paths) {
  text = String(text || "");
  paths = paths || [];
  if (!paths.length) return text;
  var suffix = "\n\n" + String.fromCodePoint(0x1F4CE) + " " + paths.map(attachmentName).join(", ");
  if (text.slice(-suffix.length) === suffix) text = text.slice(0, -suffix.length);
  var documents = paths.filter(function (path) { return !imageAttachment(path); });
  if (documents.length) {
    text += "\n\n" + String.fromCodePoint(0x1F4CE) + " " + documents.map(attachmentName).join(", ");
  }
  return text;
}
function loadSentAttachmentIndex() {
  try {
    var parsed = JSON.parse(localStorage.getItem(sentAttachmentStorageKey) || "[]");
    return Array.isArray(parsed) ? parsed : [];
  } catch (e) { return []; }
}
function saveSentAttachmentIndex() {
  if (sentAttachmentIndex.length > 240) sentAttachmentIndex = sentAttachmentIndex.slice(-240);
  try { localStorage.setItem(sentAttachmentStorageKey, JSON.stringify(sentAttachmentIndex)); } catch (e) {}
  saveBlobKey(sentAttachmentStorageKey, sentAttachmentIndex);
}
function rememberSentAttachments(sessionID, seq, paths) {
  if (!sessionID || !seq || !(paths || []).length) return;
  sentAttachmentIndex = sentAttachmentIndex.filter(function (item) {
    return item.session !== sessionID || item.seq !== seq;
  });
  sentAttachmentIndex.push({ session: sessionID, seq: seq, paths: paths.slice(), at: Date.now() });
  saveSentAttachmentIndex();
}
function sentAttachmentsFor(sessionID, seq) {
  for (var i = sentAttachmentIndex.length - 1; i >= 0; i--) {
    var item = sentAttachmentIndex[i];
    if (item.session === sessionID && item.seq === seq) return (item.paths || []).slice();
  }
  return [];
}
function forgetSentAttachments(sessionID, fromSeq) {
  sentAttachmentIndex = sentAttachmentIndex.filter(function (item) {
    if (item.session !== sessionID) return true;
    return fromSeq && item.seq < fromSeq;
  });
  saveSentAttachmentIndex();
}
function renderSentAttachments(node, paths) {
  var images = (paths || []).filter(imageAttachment);
  if (!node || !images.length) return;
  var gallery = el("div", "sent-attachment-gallery" + (images.length === 1 ? " single" : ""));
  images.forEach(function (path) {
    var open = el("button", "sent-attachment-preview");
    open.type = "button";
    open.title = t("attachment.preview") + ": " + attachmentName(path);
    open.setAttribute("aria-label", open.title);
    open.addEventListener("click", function () { openAttachmentPreview(path); });
    var image = document.createElement("img");
    image.src = attachmentPreviewSource(path);
    image.alt = attachmentName(path);
    image.loading = "lazy";
    image.addEventListener("error", function () { open.remove(); });
    open.appendChild(image);
    gallery.appendChild(open);
  });
  node.appendChild(gallery);
}
function addAttachmentPaths(paths) {
  (paths || []).forEach(function (path) {
    if (pendingAttachments.indexOf(path) < 0 && pendingAttachments.length < 8) {
      pendingAttachments.push(path);
    }
  });
  renderAttachments();
}
async function uploadAttachmentFiles(files, successKey) {
  files = Array.prototype.slice.call(files || []).slice(0, 8 - pendingAttachments.length);
  if (!files.length) return;
  var form = new FormData();
  files.forEach(function (file, index) {
    var name = file.name || ("clipboard-" + (index + 1) + ".png");
    form.append("files", file, name);
  });
  var response = await fetch("/api/attachment/upload", { method: "POST", body: form });
  if (!response.ok) throw new Error((await response.text() || response.status).toString().trim());
  var result = await response.json();
  addAttachmentPaths(result.paths || []);
  toast(t(successKey) + " · " + (result.paths || []).length);
}
function closeAttachmentPreview() {
  var dialog = $("#attachment-preview-dialog");
  $("#attachment-preview-content").replaceChildren();
  if (dialog.open) dialog.close();
}
function openAttachmentPreview(path) {
  if (!previewableAttachment(path)) {
    toast(t("attachment.previewUnavailable"));
    return;
  }
  var dialog = $("#attachment-preview-dialog");
  var content = $("#attachment-preview-content");
  content.replaceChildren();
  $("#attachment-preview-title").textContent = attachmentName(path);
  var source = attachmentPreviewSource(path);
  if (attachmentExtension(path) === ".pdf") {
    var frame = document.createElement("iframe");
    frame.src = source + "#view=FitH";
    frame.title = attachmentName(path);
    content.appendChild(frame);
  } else {
    var image = document.createElement("img");
    image.src = source;
    image.alt = attachmentName(path);
    content.appendChild(image);
  }
  dialog.showModal();
}
function renderAttachments() {
  var host = $("#attachment-list");
  host.innerHTML = "";
  host.hidden = !pendingAttachments.length;
  pendingAttachments.forEach(function (path, index) {
    var isImage = imageAttachment(path);
    var chip = el("div", "attachment-chip" + (isImage ? " image-attachment" : ""));
    chip.title = path;
    var open = el("button", "attachment-open" + (previewableAttachment(path) ? " previewable" : ""));
    open.type = "button";
    open.title = previewableAttachment(path) ? t("attachment.preview") : path;
    open.addEventListener("click", function () { openAttachmentPreview(path); });
    if (isImage) {
      var thumbnail = document.createElement("img");
      thumbnail.className = "attachment-thumbnail";
      thumbnail.src = attachmentPreviewSource(path);
      thumbnail.alt = "";
      thumbnail.loading = "lazy";
      thumbnail.addEventListener("error", function () {
        chip.classList.add("thumbnail-unavailable");
        thumbnail.remove();
      });
      open.appendChild(thumbnail);
    }
    open.appendChild(el("span", "attachment-name", attachmentName(path)));
    chip.appendChild(open);
    var remove = el("button", "attachment-remove", "×");
    remove.type = "button";
    remove.setAttribute("aria-label", t("composer.remove"));
    remove.addEventListener("click", function () {
      pendingAttachments.splice(index, 1);
      renderAttachments();
    });
    chip.appendChild(remove);
    host.appendChild(chip);
  });
}
function clearAttachments() {
  pendingAttachments = [];
  renderAttachments();
}
async function pickAttachments() {
  var button = $("#attach-btn");
  button.disabled = true;
  try {
    var got = await j("/api/file-picker");
    addAttachmentPaths(got.paths || []);
  } catch (e) {
    toast(t("composer.attachFailed") + ": " + e.message);
  } finally {
    button.disabled = false;
  }
}

function updateAppBadge() {
  // The taskbar number means "finished while you were away", not queue depth.
  // Pending prompts already have their own visible queue and must not look like
  // unread results after the user returns to the app.
  var count = unreadDone;
  if (!ui.appBadge) count = 0;
  if (navigator.setAppBadge) {
    (count ? navigator.setAppBadge(count) : navigator.clearAppBadge()).catch(function () {});
  }
  if (window.supercliSetBadge) window.supercliSetBadge(count).catch(function () {});
  document.title = count ? "(" + count + ") SuperCli" : "SuperCli";
}
var promptQueueDragID = "";

function queuedTaskIndex(id) {
  return promptQueue.findIndex(function (item) { return item.id === id; });
}

async function moveQueuedTask(item, position) {
  var from = queuedTaskIndex(item.id);
  if (from < 0 || !promptQueue.length) return;
  position = Math.max(0, Math.min(promptQueue.length - 1, position));
  if (from === position) return;
  try {
    await j("/api/tasks", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: item.id, position: position }),
    });
    promptQueue.splice(from, 1);
    promptQueue.splice(position, 0, item);
    promptQueue.forEach(function (queued, index) { queued.position = index + 1; });
    renderPromptQueue();
  } catch (e) {
    toast(e.message);
    renderPromptQueue();
  }
}

async function chooseQueuedTaskPosition(item) {
  var from = queuedTaskIndex(item.id);
  if (from < 0) return;
  var value = await appPrompt(t("composer.moveTo"), String(from + 1), {
    message: t("composer.positionHint").replace("{count}", String(promptQueue.length)),
    maxLength: String(promptQueue.length).length,
  });
  if (value == null) return;
  if (!/^\d+$/.test(value)) {
    toast(t("composer.positionInvalid"));
    return;
  }
  var position = Number(value) - 1;
  if (position < 0 || position >= promptQueue.length) {
    toast(t("composer.positionInvalid"));
    return;
  }
  await moveQueuedTask(item, position);
}

async function editQueuedTask(item) {
  var prompt = await appPrompt(t("composer.editQueued"), item.text || item.prompt || "", {
    message: t("composer.editQueuedHint"),
    multiline: true,
    rows: 7,
  });
  if (prompt == null) return;
  try {
    await j("/api/tasks", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: item.id, prompt: prompt }),
    });
    item.prompt = prompt.trim();
    item.text = item.prompt;
    renderPromptQueue();
  } catch (e) { toast(e.message); }
}

function clearQueueDropState() {
  $$(".queue-row, .task-center-row").forEach(function (row) {
    row.classList.remove("queue-dragging", "queue-drop-before", "queue-drop-after");
  });
}

var promptQueueDragRow = null;
var promptQueueDragHost = null;
var promptQueueDragLastY = 0;
var promptQueueDropAccepted = false;

function queuedTaskRows(host) {
  return host ? Array.prototype.slice.call(host.children).filter(function (child) {
    return !!child.dataset.queueId;
  }) : [];
}

function animateQueuedTaskShift(host, mutate) {
  var before = {};
  queuedTaskRows(host).forEach(function (row) {
    before[row.dataset.queueId] = row.getBoundingClientRect().top;
  });
  mutate();
  queuedTaskRows(host).forEach(function (row) {
    var previousTop = before[row.dataset.queueId];
    if (previousTop == null || !row.animate) return;
    var delta = previousTop - row.getBoundingClientRect().top;
    if (Math.abs(delta) < 1) return;
    row.animate([
      { transform: "translateY(" + delta + "px)" },
      { transform: "translateY(0)" },
    ], { duration: 130, easing: "cubic-bezier(.2,.75,.25,1)" });
  });
}

function reorderQueuedTaskDOM(sourceRow, targetRow, clientY) {
  if (!sourceRow || !targetRow || sourceRow === targetRow || sourceRow.parentElement !== targetRow.parentElement) return;
  var host = sourceRow.parentElement;
  var rows = queuedTaskRows(host);
  var sourceIndex = rows.indexOf(sourceRow);
  var targetIndex = rows.indexOf(targetRow);
  var deltaY = clientY - promptQueueDragLastY;
  promptQueueDragLastY = clientY;
  if (sourceIndex < 0 || targetIndex < 0) return;
  if (sourceIndex < targetIndex && deltaY > 0) {
    animateQueuedTaskShift(host, function () { host.insertBefore(sourceRow, targetRow.nextSibling); });
  } else if (sourceIndex > targetIndex && deltaY < 0) {
    animateQueuedTaskShift(host, function () { host.insertBefore(sourceRow, targetRow); });
  }
}

function finishQueuedTaskDrag(commit) {
  if (!promptQueueDragID) return;
  var id = promptQueueDragID;
  var host = promptQueueDragHost;
  var orderedIDs = queuedTaskRows(host).map(function (row) { return row.dataset.queueId; });
  var position = orderedIDs.indexOf(id);
  var item = promptQueue[queuedTaskIndex(id)];
  promptQueueDragID = "";
  promptQueueDragRow = null;
  promptQueueDragHost = null;
  promptQueueDropAccepted = false;
  clearQueueDropState();
  if (!commit || !item || position < 0 || position === queuedTaskIndex(id)) {
    renderPromptQueue();
    return;
  }
  moveQueuedTask(item, position);
}

function ensureQueuedTaskDropHost(host) {
  if (!host || host._queueDropWired) return;
  host._queueDropWired = true;
  host.addEventListener("dragover", function (event) {
    if (promptQueueDragHost === host) event.preventDefault();
  });
  host.addEventListener("drop", function (event) {
    if (promptQueueDragHost !== host) return;
    event.preventDefault();
    promptQueueDropAccepted = true;
    finishQueuedTaskDrag(true);
  });
}

function wireQueuedTaskDrag(row, handle, item) {
  row.dataset.queueId = item.id;
  ensureQueuedTaskDropHost(row.parentElement);
  handle.draggable = true;
  handle.addEventListener("dragstart", function (event) {
    promptQueueDragID = item.id;
    promptQueueDragRow = row;
    promptQueueDragHost = row.parentElement;
    promptQueueDragLastY = event.clientY;
    promptQueueDropAccepted = false;
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", item.id);
    if (event.dataTransfer.setDragImage) {
      event.dataTransfer.setDragImage(row, Math.min(24, row.offsetWidth / 2), row.offsetHeight / 2);
    }
    setTimeout(function () {
      if (promptQueueDragRow === row) row.classList.add("queue-dragging");
    }, 0);
  });
  handle.addEventListener("dragend", function () {
    finishQueuedTaskDrag(promptQueueDropAccepted);
  });
  row.addEventListener("dragover", function (event) {
    if (!promptQueueDragID || promptQueueDragHost !== row.parentElement) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
    reorderQueuedTaskDOM(promptQueueDragRow, row, event.clientY);
  });
}

function queueDragHandle() {
  var handle = el("button", "queue-drag", "\u2630");
  handle.type = "button";
  handle.title = t("composer.dragQueue");
  handle.setAttribute("aria-label", t("composer.dragQueue"));
  return handle;
}

function queuePositionButton(item, index) {
  var button = el("button", "queue-index queue-index-button", String(index + 1).padStart(2, "0"));
  button.type = "button";
  button.title = t("composer.moveTo");
  button.setAttribute("aria-label", t("composer.moveTo") + ": " + String(index + 1));
  button.addEventListener("click", function () { chooseQueuedTaskPosition(item); });
  return button;
}

function renderPromptQueue() {
  var host = $("#prompt-queue");
  host.innerHTML = "";
  host.hidden = !promptQueue.length;
  if (pauseQueue && promptQueue.length) {
    var resume = el("button", "queue-resume", "▶ " + t("composer.resume"));
    resume.type = "button";
    resume.addEventListener("click", function () {
      pauseQueue = false;
      var next = promptQueue[0];
      if (next && !streaming) runQueuedTask(next, false);
    });
    host.appendChild(resume);
  }
  promptQueue.forEach(function (item, index) {
    var row = el("div", "queue-row");
    var drag = queueDragHandle();
    row.appendChild(drag);
    row.appendChild(queuePositionButton(item, index));
    var text = el("button", "queue-text", item.text);
    text.type = "button";
    text.title = t("composer.editQueued");
    text.addEventListener("click", function () { editQueuedTask(item); });
    row.appendChild(text);
    var now = el("button", "queue-action", t("composer.sendNow")); now.type = "button";
    now.addEventListener("click", function () { runQueuedTask(item, true); });
    var remove = el("button", "queue-remove", "×"); remove.type = "button"; remove.title = t("composer.remove");
    remove.addEventListener("click", function () { removeQueuedTask(item.id); });
    row.appendChild(now); row.appendChild(remove);
    host.appendChild(row);
    wireQueuedTaskDrag(row, drag, item);
  });
  updateAppBadge();
  renderTaskCenter();
}
async function enqueuePrompt(text) {
  try {
    var q = await jpost("/api/tasks", { session_id: activeSessionID, prompt: text });
    q.text = q.prompt;
    promptQueue.push(q);
    pauseQueue = false;
    renderPromptQueue();
  } catch (e) { toast(e.message); return false; }
  setRunState("running", promptQueue.length + " · " + t("composer.queued"));
  return true;
}
async function loadPromptQueue() {
  try { promptQueue = await j("/api/tasks") || []; promptQueue.forEach(function (q) { q.text = q.prompt; }); pauseQueue = promptQueue.length > 0; }
  catch (e) { promptQueue = []; }
  renderPromptQueue();
}

async function loadWorkers() {
  var host = $("#worker-list"); if (!host) return;
  $("#worker-heading").textContent = ui.lang === "pl" ? "Agenci pomocniczy" : "Workers";
  try {
    var got = await j("/api/workers");
    renderWorkers(got && got.workers ? got.workers : []);
  } catch (e) {
    host.innerHTML = "";
    host.appendChild(el("div", "side-empty", e.message));
  }
}
function renderWorkers(workers) {
  var host = $("#worker-list"); if (!host) return; host.innerHTML = "";
  if (!workers.length) {
    host.appendChild(el("div", "side-empty", ui.lang === "pl" ? "Brak agentów w tym procesie." : "No workers in this process."));
    return;
  }
  workers.slice().reverse().forEach(function (worker) {
    var row = el("div", "task-center-row worker-center-row");
    var state = String(worker.status || "created");
    row.appendChild(el("span", "task-center-index worker-state " + state, state === "running" || state === "created" ? "●" : "✓"));
    var copy = el("div", "task-center-copy");
    copy.appendChild(el("span", "t", worker.id + " · " + (worker.agent || "worker")));
    var meta = state + " · " + (worker.steps || 0) + " " + (ui.lang === "pl" ? "kroków" : "steps") + " · " + ((worker.tokens_in || 0) + (worker.tokens_out || 0)) + " tok";
    copy.appendChild(el("span", "s", meta));
    if (worker.description) copy.title = worker.description;
    row.appendChild(copy);
    if (state === "running" || state === "created") {
      var stop = el("button", "queue-action", ui.lang === "pl" ? "zatrzymaj" : "stop");
      stop.addEventListener("click", async function () {
        try { await jpost("/api/workers", { id: worker.id, action: "stop" }); await loadWorkers(); }
        catch (e) { toast(e.message); }
      });
      row.appendChild(stop);
    }
    host.appendChild(row);
  });
}
async function removeQueuedTask(id) {
  try { await j("/api/tasks?id=" + encodeURIComponent(id), { method: "DELETE" }); promptQueue = promptQueue.filter(function (q) { return q.id !== id; }); renderPromptQueue(); return true; }
  catch (e) { toast(e.message); return false; }
}
async function runQueuedTask(item, interrupt) {
  if (interrupt && streaming) {
    pendingImmediate = item;
    pauseQueue = false;
    if (abortCtl) abortCtl.abort();
    return;
  }
  if (!await prepareQueuedTask(item)) {
    pauseQueue = true;
    renderPromptQueue();
    return;
  }
  if (!await removeQueuedTask(item.id)) return;
  pauseQueue = false;
  sendPrompt(item.text, item.attachments || []);
}
function renderTaskCenter() {
  var host = $("#task-list"); if (!host) return; host.innerHTML = "";
  if (!promptQueue.length) { host.appendChild(el("div", "side-empty", ui.lang === "pl" ? "Brak oczekujących zadań." : "No queued tasks.")); return; }
  promptQueue.forEach(function (item, i) {
    var row = el("div", "task-center-row");
    var drag = queueDragHandle();
    row.appendChild(drag);
    var position = queuePositionButton(item, i);
    position.classList.add("task-center-index");
    row.appendChild(position);
    var copy = el("button", "task-center-copy queue-copy-edit");
    copy.type = "button";
    copy.title = t("composer.editQueued");
    copy.appendChild(el("span", "t", item.text));
    copy.appendChild(el("span", "s", item.session_id ? clip(item.session_id, 18) : t("session.new")));
    copy.addEventListener("click", function () { editQueuedTask(item); });
    row.appendChild(copy);
    var edit = el("button", "queue-action", t("common.edit"));
    edit.addEventListener("click", function () { editQueuedTask(item); });
    row.appendChild(edit);
    var up = el("button", "queue-action queue-order", "\u2191");
    up.title = t("composer.moveUp");
    up.disabled = i === 0;
    up.addEventListener("click", function () { moveQueuedTask(item, i - 1); });
    row.appendChild(up);
    var down = el("button", "queue-action queue-order", "\u2193");
    down.title = t("composer.moveDown");
    down.disabled = i + 1 === promptQueue.length;
    down.addEventListener("click", function () { moveQueuedTask(item, i + 1); });
    row.appendChild(down);
    var go = el("button", "queue-action", t("composer.sendNow"));
    go.addEventListener("click", function () { runQueuedTask(item, true); });
    row.appendChild(go);
    host.appendChild(row);
    wireQueuedTaskDrag(row, drag, item);
  });
}

var sideGoalSeq = 0;
function renderSideGoal(got) {
  var host = $("#side-goal");
  if (!host) return;
  host.innerHTML = "";
  host.removeAttribute("aria-busy");
  if (!got) {
    var empty = el("div", "side-goal-empty");
    empty.appendChild(el("div", "side-goal-empty-title", t("goal.sideEmpty")));
    empty.appendChild(el("div", "side-goal-empty-copy", t("goal.sideEmptyHint")));
    var create = el("button", "side-goal-manage", t("goal.create"));
    create.type = "button";
    create.addEventListener("click", function () { openPanel("goal"); });
    empty.appendChild(create);
    host.appendChild(empty);
    return;
  }

  var tasks = got.tasks || [];
  var open = tasks.filter(function (task) { return task.status !== "done" && task.status !== "skipped"; });
  var terminal = tasks.length - open.length;
  var intro = el("div", "side-goal-intro");
  var status = el("div", "side-goal-status");
  status.appendChild(el("span", "side-goal-dot"));
  status.appendChild(document.createTextNode(goalStatus(got.status)));
  intro.appendChild(status);
  intro.appendChild(el("div", "side-goal-title", got.title));
  host.appendChild(intro);

  var progressMeta = el("div", "side-goal-progress-meta");
  progressMeta.appendChild(el("span", "", t("goal.progress")));
  progressMeta.appendChild(el("span", "", terminal + " / " + tasks.length));
  host.appendChild(progressMeta);
  var progress = el("div", "side-goal-progress");
  var fill = el("span");
  fill.style.width = (tasks.length ? terminal * 100 / tasks.length : 0) + "%";
  progress.appendChild(fill);
  progress.setAttribute("aria-label", t("goal.progress") + ": " + terminal + " / " + tasks.length);
  host.appendChild(progress);

  if (!open.length) {
	var verificationCopy = t("goal.awaitingVerification");
	var verificationClass = " awaiting";
	if (got.verification_status === "passed") {
	  verificationCopy = t("goal.readyToFinish");
	  verificationClass = " passed";
	} else if (got.verification_status === "failed") {
	  verificationCopy = t("goal.verificationFailed");
	  verificationClass = " failed";
	}
	host.appendChild(el("div", "side-goal-all-done" + verificationClass, verificationCopy));
    return;
  }
  var active = open.filter(function (task) { return task.status === "in_progress"; })[0] || null;
  host.appendChild(el("div", "side-goal-steps-label", active ? t("goal.currentStep") : t("goal.nextSteps")));
  var visible = active ? [active].concat(open.filter(function (task) { return task !== active; })) : open;
  visible.slice(0, 3).forEach(function (task) {
    var row = el("div", "side-goal-step" + (task === active ? " current" : ""));
    row.appendChild(el("span", "side-goal-step-index", String(task.seq).padStart(2, "0")));
    var copy = el("div", "side-goal-step-copy");
    copy.appendChild(el("div", "side-goal-step-title", task.title));
    copy.appendChild(el("div", "side-goal-step-status", goalStatus(task.status)));
    row.appendChild(copy);
    host.appendChild(row);
  });
  if (visible.length > 3) host.appendChild(el("div", "side-goal-more", "+" + (visible.length - 3) + " · " + t("goal.openSteps")));
}
async function loadSideGoal() {
  var host = $("#side-goal");
  if (!host) return;
  var seq = ++sideGoalSeq;
  host.setAttribute("aria-busy", "true");
  try {
    var got = await j("/api/goal");
    if (seq === sideGoalSeq) renderSideGoal(got);
  } catch (e) {
    if (seq !== sideGoalSeq) return;
    host.removeAttribute("aria-busy");
    host.innerHTML = "";
    host.appendChild(el("div", "side-empty", e.message));
  }
}
$("#reload-tasks").addEventListener("click", function () { loadPromptQueue(); loadWorkers(); });
$("#manage-side-goal").addEventListener("click", function () { openPanel("goal"); });
async function prepareQueuedTask(item) {
  if (!item || !item.session_id) return true;
  if (activeSessionID === item.session_id && transcriptSessionID === item.session_id) {
    if (sessionByID[item.session_id]) await restoreSessionRuntime(sessionByID[item.session_id]);
    return true;
  }
  return await resumeSession(item.session_id, sessionByID[item.session_id] || null);
}
function interruptAndSend(text, item, attachments, draft) {
  pendingImmediate = item || { text: text, session_id: activeSessionID, attachments: attachments || [], draft: draft || null };
  if (item && attachments && attachments.length) pendingImmediate.attachments = attachments;
  pauseQueue = false;
  if (abortCtl) abortCtl.abort(); else {
    var next = pendingImmediate;
    pendingImmediate = null;
    prepareQueuedTask(next).then(function () { sendPrompt(next.text, next.attachments || [], next.draft || null); });
  }
}

function setRunState(state, text) {
  runStatus.textContent = text || "";
  $("#status-dot").classList.toggle("busy", state === "running");
}

async function sendPrompt(text, attachments, draft) {
  if (streaming) return;
  attachments = (attachments || []).slice();
  text = String(text || "").trim();
  if (!text && attachments.length) text = t("composer.inspectAttachments");
  if (!text) return;
  // A reopened conversation becomes visible before its remembered model has
  // necessarily finished switching. Never let an immediate Enter use the
  // previous session's runtime; if another session wins meanwhile, await it.
  for (;;) {
    var readyRuntime = sessionRuntimeReady;
    await readyRuntime;
    if (readyRuntime === sessionRuntimeReady) break;
  }
  if (streaming) return;
  // Once a live turn starts, the DOM becomes append-only again. Retire the
  // history pager so a late older-page response cannot rebuild the transcript
  // over messages currently arriving from SSE.
  if (transcriptAbortCtl) transcriptAbortCtl.abort();
  transcriptAbortCtl = null;
  transcriptHasMore = false;
  transcriptBeforeSeq = 0;
  loadedTranscriptMessages = [];
  var olderHistory = stream.querySelector(".history-older");
  if (olderHistory) olderHistory.remove();
  streaming = true;
  transcriptLiveAppend = true;
  abortCtl = new AbortController();
  runToolCount = 0;
  toolRows = {}; workerRows = {}; openToolOrder = [];
  sendBtn.textContent = t("composer.queue");
  sendBtn.classList.add("queue");
  sendBtn.type = "submit";
  $("#stop-run-btn").hidden = false;
  $("#interrupt-btn").hidden = false;
  var persistedText = attachmentTranscriptText(text, attachments);
  var liveUserNode = addUserMsg(text, 0, attachments);
  var current = null;
  var terminalSeen = false;
  var stopped = false;
  var draftAccepted = false;
  var lastProgressAt = Date.now();
  runStart = Date.now();
  setRunState("running", t("composer.working"));
  runTimer = setInterval(function () {
    var now = Date.now();
    var quiet = now - lastProgressAt;
    runStatus.textContent = (quiet >= 10000 ? t("composer.waiting") + " " + fmtDuration(quiet) :
      t("composer.working") + " " + fmtDuration(now - runStart));
  }, 100);

  try {
    var resp = await fetch("/api/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        prompt: text,
        session_id: activeSessionID,
        attachments: attachments,
      }),
      signal: abortCtl.signal,
    });
    if (!resp.ok || !resp.body) {
      addEventLine(t("common.error") + ": " + resp.status, "error");
      return;
    }
    draftAccepted = true;
    if (draft) composerDraftStore.clear(draft.scope, draft.text);
    await superCliUI.readSSE(resp.body, function (ev) {
      lastProgressAt = Date.now();
      if (ev.type === "done" || ev.type === "error") terminalSeen = true;
      current = handleEvent(ev, current);
    });
    flushAssistantRender(current);
    if (!terminalSeen) {
      if (activeSessionID) {
        try {
          var page = await j("/api/transcript?id=" + encodeURIComponent(activeSessionID) + "&limit=24");
          var messages = (page && page.messages) || [];
          for (var ri = messages.length - 1; ri >= 0; ri--) {
            if (messages[ri].role === "assistant" && messages[ri].content) {
              var full = String(messages[ri].content);
              if (!current) current = addAssistantMsg();
              if (full.length > String(current._raw || "").length) {
                current._raw = full;
                flushAssistantRender(current);
              }
              break;
            }
          }
        } catch (recoverErr) {}
      }
      addEventLine(t("chat.streamEnded"), "error", "error");
      setRunState("idle", t("common.error"));
    }
  } catch (e) {
    if (e.name === "AbortError") {
      stopped = true;
      addEventLine(t("chat.stopped"), "", "stop");
      setRunState("idle", t("composer.stopped"));
    } else {
      addEventLine(t("chat.connError") + ": " + e.message, "error");
      setRunState("idle", t("chat.connError"));
    }
	} finally {
	  if (draft && !draftAccepted && !promptEl.value && composerDraftStore.scope() === draft.scope) {
	    composerDraftStore.restore(draft.scope);
	  }
	  sealAssistantSegment(current);
	  closeQuestionOverlay();
	  streaming = false;
    abortCtl = null;
    clearInterval(runTimer);
    settleOpenTools();
    releaseLiveTranscriptBlocks();
    sendBtn.textContent = t("composer.send");
    sendBtn.classList.remove("queue");
    sendBtn.type = "submit";
    $("#stop-run-btn").hidden = true;
    $("#interrupt-btn").hidden = true;
    if (runStatus.textContent.indexOf(t("composer.working")) === 0) setRunState("idle", t("composer.ready"));
    $("#status-dot").classList.remove("busy");
    promptEl.focus();
    renderStats();
    var userSeq = await addLatestMessageRewind(liveUserNode, persistedText, stopped ? 8 : 1);
    if (userSeq) rememberSentAttachments(activeSessionID, userSeq, attachments);
    await loadSessions();
    // A model may have updated the durable goal through its tool during this
    // turn. Keep an already-open Goal panel in sync without polling.
    if (!overlay.hidden && currentSection === "goal" && sections.goal) sections.goal();
    if ($("#side-tab-goal").classList.contains("active")) loadSideGoal();
    var immediate = pendingImmediate;
    pendingImmediate = null;
	var next = null;
	if (immediate) {
	  if (await prepareQueuedTask(immediate)) {
	    if (!immediate.id || await removeQueuedTask(immediate.id)) next = immediate;
	  } else {
	    pauseQueue = true;
	  }
	}
    if (!next && !pauseQueue && promptQueue.length) {
      var queued = promptQueue[0];
	  if (!await prepareQueuedTask(queued)) {
	    pauseQueue = true;
	  } else if (!await removeQueuedTask(queued.id)) {
	    queued = null;
	  }
	  if (queued && !pauseQueue) next = { text: queued.text, attachments: queued.attachments || [] };
    }
    renderPromptQueue();
    if (next) setTimeout(function () { sendPrompt(next.text, next.attachments || [], next.draft || null); }, 0);
  }
}

function handleEvent(ev, current) {
  switch (ev.type) {
    case "session":
      if (ev.session_id) activeSessionID = ev.session_id;
      return current;
    case "message":
      if (!current || current._sealed) current = addAssistantMsg();
      closeAssistantReasoning(current);
      current._raw += ev.text;
      scheduleAssistantRender(current);
      return current;
    case "reasoning":
      if (!current || current._sealed) current = addAssistantMsg();
      appendAssistantReasoning(current, ev.text || "");
      return current;
    case "tool_call":
      sealAssistantSegment(current);
      addToolCall(ev.name, ev.args, ev.id);
      return null;
    case "tool_result":
      addToolResult(ev.id, ev.output, ev.err);
      return current;
    case "file_changes":
      addFileChanges(ev.file_changes);
      return current;
    case "compact":
      addEventLine(ev.text || "", "", "compact");
      return null;
    case "notice":
      addEventLine(ev.text || "", "", noticeTag(ev.text || ""));
      return current;
	case "worker_progress":
      addWorkerProgress(ev);
	  return current;
	case "question":
	  if (ev.question) showQuestion(ev.question);
	  setRunState("running", t("question.waiting"));
	  return current;
    case "worker":
      workersSeen.push({ name: ev.name || "worker", status: ev.status || "", summary: ev.output || "" });
      var task = parseTaskNotification(ev.text) || {
        id: ev.id || "", agent: ev.name || "worker", status: ev.status || "done",
        summary: ev.output || "", result: "",
      };
      var workerRow = findTaskRow(task.id, task.agent);
      if (workerRow) {
        clearInterval(workerRow._clock);
        workerRow._clock = null;
        if (task.id) workerRows[task.id] = workerRow;
        renderTaskResult(workerRow, task, performance.now() - workerRow._t0, workerRow._taskPrompt, task.status === "failed");
        return current;
      }
      addEventLine((ev.name || "worker") + " · " + (ev.status || "") + (ev.output ? " — " + clip(ev.output, 160) : ""), "", "worker");
      return current;
    case "reflection":
      addEventLine(clip(ev.text || "", 120), "", "reflection");
      return current;
    case "sisyphus":
      addEventLine(clip(ev.text || "goal continuation", 120), "", "goal");
      return current;
    case "done":
      closeAssistantReasoning(current);
      flushAssistantRender(current);
      var elapsed = Date.now() - runStart;
      lastTurn = { ev: ev, elapsed: elapsed, tools: runToolCount };
      addFileChanges(ev.file_changes);
      addTurnMeta(ev, elapsed);
      setRunState("idle", t("run.done") + " · " + fmtDuration(elapsed));
      notifyDone(elapsed);
      return null;
    case "error":
      closeAssistantReasoning(current);
      flushAssistantRender(current);
      addFileChanges(ev.file_changes);
      addEventLine(ev.err || "error", "error", "error");
      setRunState("idle", t("common.error"));
      return null;
    default:
      return current;
  }
}

function closeQuestionOverlay() {
  if (activeQuestionOverlay) activeQuestionOverlay.remove();
  activeQuestionOverlay = null;
}

function showQuestion(q) {
  closeQuestionOverlay();
  var overlay = el("div", "question-inline");
  var panel = el("form", "question-panel");
  panel.setAttribute("aria-label", q.header || t("question.title"));
  var head = el("div", "question-head");
  head.appendChild(el("span", "question-kicker", q.header || t("question.title")));
  head.appendChild(el("h2", "question-text", q.question || ""));
  panel.appendChild(head);

  var options = el("div", "question-options" + ((q.options || []).some(function (o) { return o.image; }) ? " visual" : ""));
  (q.options || []).forEach(function (opt, idx) {
    var card = el("label", "question-option");
    var input = document.createElement("input");
    input.type = q.multi_select ? "checkbox" : "radio";
    input.name = "question-choice";
    input.value = opt.label;
    card.appendChild(input);
    if (opt.image) {
      var preview = el("button", "question-image-button");
      preview.type = "button";
      preview.title = t("question.zoom");
      var img = document.createElement("img");
      img.src = opt.image;
      img.alt = opt.label;
      preview.appendChild(img);
      preview.addEventListener("click", function (e) {
        e.preventDefault();
        var light = el("div", "question-lightbox");
        var full = document.createElement("img"); full.src = opt.image; full.alt = opt.label;
        light.appendChild(full);
        light.addEventListener("click", function () { light.remove(); });
        overlay.appendChild(light);
      });
      card.appendChild(preview);
    }
    var copy = el("span", "question-option-copy");
    copy.appendChild(el("strong", "question-option-label", opt.label));
    if (opt.description) copy.appendChild(el("span", "question-option-desc", opt.description));
    if (opt.preview) copy.appendChild(el("span", "question-option-preview", opt.preview));
    if (opt.image_prompt) {
      var details = document.createElement("details"); details.className = "question-prompt";
      var summary = document.createElement("summary"); summary.textContent = t("question.prompt");
      details.appendChild(summary);
      details.appendChild(el("code", "", opt.image_prompt));
      var copyBtn = el("button", "question-copy", t("question.copy")); copyBtn.type = "button";
      copyBtn.addEventListener("click", function (e) {
        e.preventDefault();
        navigator.clipboard.writeText(opt.image_prompt).then(function () { toast(t("question.copied")); });
      });
      details.appendChild(copyBtn); copy.appendChild(details);
    }
    card.appendChild(copy);
    options.appendChild(card);
  });
  panel.appendChild(options);
  var custom = document.createElement("textarea");
  custom.className = "question-custom"; custom.rows = 3; custom.placeholder = t("question.custom");
  if (q.allow_custom !== false) panel.appendChild(custom);
  var error = el("div", "question-error"); panel.appendChild(error);
  var actions = el("div", "question-actions");
  var cancel = el("button", "btn", t("question.cancel")); cancel.type = "button";
  var submit = el("button", "btn primary", t("question.submit")); submit.type = "submit";
  actions.appendChild(cancel); actions.appendChild(submit); panel.appendChild(actions);
  overlay.appendChild(panel); appendStream(overlay); activeQuestionOverlay = overlay; smartScroll(true);

  function answer(cancelled) {
    var selected = Array.prototype.slice.call(panel.querySelectorAll('input[name="question-choice"]:checked')).map(function (x) { return x.value; });
    var own = custom.value.trim();
    if (!cancelled && !selected.length && !own) { error.textContent = t("question.pick"); return; }
    submit.disabled = true; cancel.disabled = true;
    jpost("/api/question/answer", { id: q.id, selected: selected, custom: own, cancelled: !!cancelled })
      .then(function () { closeQuestionOverlay(); setRunState("running", t("composer.working")); })
      .catch(function (e) { error.textContent = e.message; submit.disabled = false; cancel.disabled = false; });
  }
  panel.addEventListener("submit", function (e) { e.preventDefault(); answer(false); });
  cancel.addEventListener("click", function () { answer(true); });
  setTimeout(function () { var first = panel.querySelector("input"); if (first) first.focus(); }, 0);
}

$("#composer").addEventListener("submit", async function (e) {
  e.preventDefault();
  var rawText = promptEl.value;
  var text = rawText.trim();
  if (!text && !pendingAttachments.length) return;
  if (streaming && pendingAttachments.length) {
    toast(t("composer.attachQueue"));
    return;
  }
  var attachments = pendingAttachments.slice();
  var draft = { scope: composerDraftStore.scope(), text: rawText };
  promptEl.value = "";
  promptEl.style.height = "auto";
  if (streaming) {
    if (await enqueuePrompt(text)) composerDraftStore.clear(draft.scope, draft.text);
    else composerDraftStore.restore(draft.scope);
    return;
  }
  clearAttachments();
  sendPrompt(text, attachments, draft);
});
$("#stop-run-btn").addEventListener("click", function () {
  pauseQueue = true;
  if (abortCtl) abortCtl.abort();
});
$("#interrupt-btn").addEventListener("click", function () {
  var rawText = promptEl.value;
  var text = rawText.trim();
  if (!text && !pendingAttachments.length) return;
  var attachments = pendingAttachments.slice();
  var draft = { scope: composerDraftStore.scope(), text: rawText };
  promptEl.value = ""; promptEl.style.height = "auto";
  clearAttachments();
  interruptAndSend(text, null, attachments, draft);
});
$("#attach-btn").addEventListener("click", pickAttachments);
$("#attachment-preview-close").addEventListener("click", closeAttachmentPreview);
$("#attachment-preview-dialog").addEventListener("click", function (event) {
  if (event.target === this) closeAttachmentPreview();
});
promptEl.addEventListener("keydown", function (e) {
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    $("#composer").requestSubmit();
  }
});
promptEl.addEventListener("input", function () {
  this.style.height = "auto";
  this.style.height = Math.min(this.scrollHeight, 200) + "px";
});
promptEl.addEventListener("paste", function (event) {
  var files = event.clipboardData ? Array.prototype.slice.call(event.clipboardData.files || []) : [];
  if (!files.length) return;
  if (!event.clipboardData.getData("text/plain")) event.preventDefault();
  uploadAttachmentFiles(files, "composer.pasted").catch(function (error) {
    toast(t("composer.attachFailed") + ": " + error.message);
  });
});
$("#composer").addEventListener("dragover", function (event) {
  if (!event.dataTransfer) return;
  var hasFiles = event.dataTransfer.files.length ||
    Array.prototype.some.call(event.dataTransfer.items || [], function (item) { return item.kind === "file"; });
  if (!hasFiles) return;
  event.preventDefault();
  this.classList.add("drop-target");
});
$("#composer").addEventListener("dragleave", function () { this.classList.remove("drop-target"); });
$("#composer").addEventListener("drop", function (event) {
  this.classList.remove("drop-target");
  if (!event.dataTransfer || !event.dataTransfer.files.length) return;
  event.preventDefault();
  uploadAttachmentFiles(event.dataTransfer.files, "composer.dropped").catch(function (error) {
    toast(t("composer.attachFailed") + ": " + error.message);
  });
});
$$(".welcome .hints button").forEach(function (b) {
  b.addEventListener("click", function () { if (!streaming) sendPrompt(b.dataset.prompt, []); });
});

function newSession() {
  if (streaming) return;
  clearAttachments();
  if (transcriptAbortCtl) transcriptAbortCtl.abort();
  transcriptAbortCtl = null;
  loadedTranscriptMessages = [];
  transcriptHasMore = false;
  transcriptBeforeSeq = 0;
  transcriptSessionID = "";
  activeSessionID = "";
  stream.innerHTML = "";
  toolRows = {}; workerRows = {}; openToolOrder = [];
  lastTurn = null; workersSeen = [];
  showWelcome();
  setRunState("idle", t("composer.ready"));
  composerDraftStore.restore();
  loadSessions();
  renderStats();
  promptEl.focus();
}
$("#new-session").addEventListener("click", newSession);
