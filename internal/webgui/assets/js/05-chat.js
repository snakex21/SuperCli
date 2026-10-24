"use strict";

/* ═══ chat streaming ═══ */

var streaming = false, runFinishing = false, abortCtl = null, activeSessionID = "", projectEpoch = 0;
var sessionRuntimeReady = Promise.resolve();
var activeQuestionOverlay = null;
var questionOverlays = Object.create(null);
var runStart = 0, runTimer = null, runToolCount = 0;
var lastTurn = null;    // last done-event payload + elapsed (stats pane)
// Each model step is recorded on the server as it ends. Refresh the visible
// stats pane during a turn too, at most once per second, never when hidden.
var liveStatsTimer = null;
function scheduleLiveStats() {
  if (liveStatsTimer !== null || ui.sidebarHidden || typeof renderStats !== "function") return;
  var tab = $("#side-tab-stats");
  if (!tab || !tab.classList.contains("active")) return;
  liveStatsTimer = setTimeout(function () { liveStatsTimer = null; renderStats(); }, 1000);
}
var workersSeen = [];   // worker notifications this browser session
var promptEl = $("#prompt"), sendBtn = $("#send-btn"), runStatus = $("#run-status");
var promptQueue = [], pendingImmediate = null, pauseQueue = false, unreadDone = 0;
var queueDispatching = false, queueDispatchItem = null;
var promptQueueLoadedEpoch = -1, promptQueueLoadSeq = 0;
var promptQueueReady = Promise.resolve();
// The run this view renders. A project switch detaches it: the server keeps
// running it in its own project while this view moves on.
var activeRun = null;
// Runs left running in the background, by conversation ID. Opening that
// conversation again re-renders the run from its buffered events.
var detachedRuns = Object.create(null);
// The error of a background run that ended while nobody watched it, by
// conversation ID; shown once that conversation is opened.
var failedRuns = Object.create(null);
var chatTurnSequence = 0, runStopRevision = 0;
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
  return attachmentExtension(path) === ".pdf" || !!attachmentMediaType(path);
}
function imageAttachment(path) {
  return [".png", ".jpg", ".jpeg", ".webp", ".gif"].indexOf(attachmentExtension(path)) >= 0;
}
function attachmentMediaType(path) {
  if (imageAttachment(path)) return "image";
  var ext = attachmentExtension(path);
  if ([".mp4", ".webm", ".mov"].indexOf(ext) >= 0) return "video";
  if ([".mp3", ".wav", ".ogg"].indexOf(ext) >= 0) return "audio";
  return "";
}
function attachmentMimeKind(mime) {
  if (["image/png", "image/jpeg", "image/gif", "image/webp"].indexOf(mime) >= 0) return "image";
  if (["video/mp4", "video/webm", "video/quicktime"].indexOf(mime) >= 0) return "video";
  if (["audio/mpeg", "audio/wave", "audio/wav", "audio/x-wav", "audio/ogg", "application/ogg"].indexOf(mime) >= 0) return "audio";
  return "";
}
function attachmentPreviewSource(path) {
  return "/api/attachment/preview?path=" + encodeURIComponent(path);
}
function attachmentThumbnailSource(path, size) {
  return attachmentPreviewSource(path) + "&thumbnail=" + size;
}
function attachmentTranscriptText(text, paths) {
  paths = paths || [];
  if (!paths.length) return text;
  return text + "\n\n" + String.fromCodePoint(0x1F4CE) + " " + paths.map(attachmentName).join(", ");
}
// The persisted prompt ends with a "📎 a.png, b.pdf" line so the model keeps
// the file names. Every attachment is shown as a tile, so the bubble drops
// that final single-line paragraph instead of repeating the names.
function userMessageDisplayText(text, paths) {
  text = String(text || "");
  paths = paths || [];
  if (!paths.length) return text;
  var cut = text.lastIndexOf("\n\n");
  var last = text.slice(cut >= 0 ? cut + 2 : 0);
  if (last.indexOf("\n") < 0 && last.indexOf(String.fromCodePoint(0x1F4CE) + " ") === 0) {
    text = cut >= 0 ? text.slice(0, cut).replace(/\s+$/, "") : "";
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
function renderSentAttachments(node, paths, mediaKind, previewPath) {
  var override = ["image", "video", "audio"].indexOf(mediaKind) >= 0 ? mediaKind : "";
  var mediaPaths = (paths || []).filter(function (path) { return !!(override || attachmentMediaType(path)); });
  // A tool preview passes an explicit media kind; only user attachments get
  // tiles for documents and other files.
  var filePaths = override ? [] : (paths || []).filter(function (path) { return !attachmentMediaType(path); });
  if (!node) return;
  if (filePaths.length) {
    var files = el("div", "sent-file-attachments");
    filePaths.forEach(function (path) {
      var tile = el("button", "sent-file-attachment" + (previewableAttachment(path) ? " previewable" : ""));
      tile.type = "button";
      tile.title = path;
      tile.setAttribute("aria-label", attachmentName(path));
      var ext = attachmentExtension(path).slice(1).toUpperCase();
      tile.appendChild(el("span", "sent-file-badge", ext ? ext.slice(0, 4) : "FILE"));
      tile.appendChild(el("span", "attachment-name", attachmentName(path)));
      if (previewableAttachment(path)) {
        tile.addEventListener("click", function () { openAttachmentPreview(path); });
      }
      files.appendChild(tile);
    });
    node.appendChild(files);
  }
  if (!mediaPaths.length) return;
  var gallery = el("div", "sent-attachment-gallery" + (mediaPaths.length === 1 ? " single" : ""));
  mediaPaths.forEach(function (path) {
    var open = el("button", "sent-attachment-preview");
    open.type = "button";
    open.title = t("attachment.preview") + ": " + attachmentName(path);
    open.setAttribute("aria-label", open.title);
    var kind = override || attachmentMediaType(path);
    open.addEventListener("click", function () {
      if (kind === "image" && typeof openImageLightbox === "function") {
        openImageLightbox(mediaPaths.filter(function (p) { return (override || attachmentMediaType(p)) === "image"; }), path, open, previewPath);
      } else openAttachmentPreview(path, kind, previewPath);
    });
    if (kind === "image") {
      var image = document.createElement("img");
      image.alt = attachmentName(path);
      image.loading = "lazy";
      image.decoding = "async";
      image.addEventListener("error", function () {
        // Go's bundled codecs have no WebP decoder. Keep its existing preview
        // behavior without sending other failed thumbnails through full decode.
        if (attachmentExtension(path) === ".webp" && !image._originalFallback) {
          image._originalFallback = true;
          image.src = attachmentPreviewSource(previewPath || path);
          return;
        }
        image.remove();
        open.classList.add("sent-media-attachment");
        open.appendChild(el("span", "attachment-name", attachmentName(path)));
      }, {once: false});
      image.src = attachmentThumbnailSource(previewPath || path, "transcript");
      open.appendChild(image);
    } else {
      // No media element or source is created before an explicit click.
      open.classList.add("sent-media-attachment");
      open.appendChild(el("span", "attachment-name", attachmentName(path)));
    }
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
var attachmentUpload = null;
function attachmentUploadBusy(edit) {
  return !!(attachmentUpload && attachmentUpload.edit === (edit || null) && attachmentUpload.valid());
}
function cancelAttachmentUpload() { if (attachmentUpload) attachmentUpload.controller.abort(); }
window.addEventListener("pagehide", cancelAttachmentUpload);
async function uploadAttachmentFiles(files, successKey, edit) {
  files = Array.prototype.slice.call(files || []);
  if (!files.length) return;
  if (attachmentUpload && attachmentUpload.valid()) throw new Error("An attachment upload is already in progress; wait before adding more files");
  cancelAttachmentUpload();
  var paths = edit ? edit.attachments : pendingAttachments;
  var epoch = projectEpoch, session = activeSessionID, workspace = typeof activeWorkspacePath === "undefined" ? "" : activeWorkspacePath;
  var draftScope = composerDraftStore.scope(), originalPaths = paths.slice(), controller = new AbortController();
  var operation = {edit: edit || null, controller: controller, valid: function () {
    return !controller.signal.aborted && epoch === projectEpoch && session === activeSessionID &&
      workspace === (typeof activeWorkspacePath === "undefined" ? "" : activeWorkspacePath) && draftScope === composerDraftStore.scope() &&
      (edit ? queueEdit === edit && !edit.conflict && !edit.saving && edit.attachments === paths : !queueEdit && pendingAttachments === paths) &&
      paths.length === originalPaths.length && paths.every(function (path, i) { return path === originalPaths[i]; });
  }};
  attachmentUpload = operation;
  if (edit) { edit.uploading = true; if (edit.refresh) edit.refresh(); }
  var watch = setInterval(function () { if (!operation.valid()) controller.abort(); }, 50);
  function fence() {
    if (!operation.valid()) { controller.abort(); throw new DOMException("Attachment scope changed; upload cancelled", "AbortError"); }
  }
  try {
    fence();
    // The module is loaded by index before any chooser can dispatch. The small
    // fallback keeps standalone/legacy image-only UI fixtures functional.
    var prepared = window.SuperCliVideoAttachments ? await SuperCliVideoAttachments.prepare(files, {slots: 8 - paths.length, signal: controller.signal}) : {files: files, warnings: []};
    fence();
    var total = 0;
    if (prepared.files.length + paths.length > 8) throw new Error("Too many attachments (maximum 8); no files were uploaded");
    var form = new FormData();
    prepared.files.forEach(function (file, index) {
      if (file.size > 32 * 1024 * 1024) throw new Error("Attachment exceeds 32 MiB per-file limit");
      total += file.size;
      form.append("files", file, file.name || ("clipboard-" + (index + 1) + ".png"));
    });
    if (total > 64 * 1024 * 1024) throw new Error("Attachments exceed 64 MiB request limit");
    prepared.warnings.forEach(function (warning) { toast(warning); });
    fence(); // Never retry this POST automatically, including an ambiguous abort.
    var response = await fetch("/api/attachment/upload", { method: "POST", body: form, signal: controller.signal });
    if (!response.ok) throw new Error((await response.text() || response.status).toString().trim());
    var result = await response.json();
    fence();
    // Another client can switch the server workspace before this tab receives
    // its project event. Do not stage that workspace's result into this draft.
    function workspaceKey(path) {
      path = String(path).replace(/\\/g, "/").replace(/\/$/, "");
      return /^[a-z]:/i.test(path) ? path.toLowerCase() : path;
    }
    if (result.workspace && workspace && workspaceKey(result.workspace) !== workspaceKey(workspace)) {
      controller.abort(); throw new DOMException("Attachment workspace changed; upload cancelled", "AbortError");
    }
    if (!Array.isArray(result.paths) || result.paths.length !== prepared.files.length || result.paths.length + paths.length > 8) throw new Error("Incomplete attachment upload response; no attachments added");
    result.paths.forEach(function (path) { paths.push(path); });
    if (edit) { if (edit.refresh) edit.refresh(); } else renderAttachments();
    toast(t(successKey) + " · " + result.paths.length);
  } finally {
    clearInterval(watch);
    if (attachmentUpload === operation) attachmentUpload = null;
    if (edit) { edit.uploading = false; if (queueEdit === edit && edit.refresh) edit.refresh(); }
  }
}
function clearAttachmentPreviewContent() {
  var content = $("#attachment-preview-content");
  content.querySelectorAll("img, iframe").forEach(function (media) {
    media.removeAttribute("src");
  });
  content.querySelectorAll("video, audio").forEach(function (media) {
    media.pause();
    media.removeAttribute("src");
    media.load();
  });
  content.replaceChildren();
}
function closeAttachmentPreview() {
  var dialog = $("#attachment-preview-dialog");
  clearAttachmentPreviewContent();
  if (dialog.open) dialog.close();
}
function openAttachmentPreview(path, mediaKind, previewPath) {
  var override = ["image", "video", "audio"].indexOf(mediaKind) >= 0 ? mediaKind : "";
  var kind = override || attachmentMediaType(path);
  if (kind === "image" && typeof openImageLightbox === "function") {
    openImageLightbox([path], path, document.activeElement, previewPath); return;
  }
  if (!kind && !previewableAttachment(path)) {
    toast(t("attachment.previewUnavailable"));
    return;
  }
  var dialog = $("#attachment-preview-dialog");
  var content = $("#attachment-preview-content");
  clearAttachmentPreviewContent();
  $("#attachment-preview-title").textContent = attachmentName(path);
  var source = attachmentPreviewSource(previewPath || path);
  if (!override && attachmentExtension(path) === ".pdf") {
    var frame = document.createElement("iframe");
    frame.src = source + "#view=FitH";
    frame.title = attachmentName(path);
    content.appendChild(frame);
  } else if (kind === "video" || kind === "audio") {
    var media = document.createElement(kind);
    media.controls = true;
    media.preload = "none";
    media.src = source;
    content.appendChild(media);
  } else {
    var image = document.createElement("img");
    image.src = source;
    image.alt = attachmentName(path);
    content.appendChild(image);
  }
  dialog.showModal();
}
function renderAttachments(host, paths, onRemove) {
  host = host || $("#attachment-list");
  paths = paths || pendingAttachments;
  host.innerHTML = "";
  host.hidden = !paths.length;
  paths.forEach(function (path, index) {
    var isImage = imageAttachment(path);
    var chip = el("div", "attachment-chip" + (isImage ? " image-attachment" : ""));
    chip.title = path;
    var open = el("button", "attachment-open" + (previewableAttachment(path) ? " previewable" : ""));
    open.type = "button";
    open.title = previewableAttachment(path) ? t("attachment.preview") : path;
    open.addEventListener("click", function () {
      if (isImage && typeof openImageLightbox === "function") openImageLightbox(paths.filter(imageAttachment), path, open);
      else openAttachmentPreview(path);
    });
    if (isImage) {
      var thumbnail = document.createElement("img");
      thumbnail.className = "attachment-thumbnail";
      thumbnail.alt = "";
      thumbnail.loading = "lazy";
      thumbnail.decoding = "async";
      thumbnail.addEventListener("error", function () {
        if (attachmentExtension(path) === ".webp" && !thumbnail._originalFallback) {
          thumbnail._originalFallback = true;
          thumbnail.src = attachmentPreviewSource(path);
          return;
        }
        chip.classList.add("thumbnail-unavailable");
        thumbnail.remove();
      });
      thumbnail.src = attachmentThumbnailSource(path, "composer");
      open.appendChild(thumbnail);
    }
    open.appendChild(el("span", "attachment-name", attachmentName(path)));
    chip.appendChild(open);
    var remove = el("button", "attachment-remove", "×");
    remove.type = "button";
    remove.setAttribute("aria-label", t("composer.remove"));
    remove.addEventListener("click", function () {
      if (onRemove) onRemove(index);
      else { pendingAttachments.splice(index, 1); renderAttachments(); }
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

var queueEdit = null, queueMerging = false, queueMergeMode = "off";
// One deferred automatic handoff, owned by the completing run's scope. Opening
// an idle/paused editor never creates it. Durable claims remain server-owned.
var queueAutoDrain = null, queueEditWrite = null;
function queueDrainScope(root) {
  return {root: root, epoch: projectEpoch, session: typeof activeSessionID === "string" ? activeSessionID : "", stop: typeof runStopRevision === "number" ? runStopRevision : 0};
}
function queueDrainScopeValid(scope) {
  return scope && scope.epoch === projectEpoch && scope.session === (typeof activeSessionID === "string" ? activeSessionID : "") && scope.stop === (typeof runStopRevision === "number" ? runStopRevision : 0);
}
function queueEditorBlocks(item) {
  return !!(item && item.id && ((queueEdit && queueEdit.id === item.id) || (queueEditWrite && queueEditWrite.id === item.id)));
}
function queueDispatchGuard(item, automatic) {
  if (automatic && (!queueDrainScopeValid(automatic) || pauseQueue || (queueAutoDrain && queueAutoDrain.loading))) return false;
  if (!queueEditorBlocks(item)) return true;
  if (automatic && promptQueue[0] && promptQueue[0].id === item.id && !queueAutoDrain) {
    queueAutoDrain = {scope: automatic, id: item.id, loading: false};
  }
  return false;
}
async function reloadQueueForEdit() {
  loadPromptQueue();
  // A sync reload may supersede ours. Wait for the latest durable view, not a
  // stale response or the cached row that the editor originally opened.
  for (;;) {
    var ready = promptQueueReady;
    var loaded = await ready;
    if (ready === promptQueueReady) return loaded !== false;
  }
}
async function resumeQueueAutoDrain() {
  var pending = queueAutoDrain;
  if (!pending) return;
  if (!queueDrainScopeValid(pending.scope) || pauseQueue) { queueAutoDrain = null; return; }
  if (pending.loading || streaming || queueDispatching || queueMerging || queueEditWrite || (queueEdit && queueEdit.id === pending.id)) return;
  pending.loading = true;
  try {
    if (!await reloadQueueForEdit()) {
      if (queueAutoDrain === pending && queueDrainScopeValid(pending.scope)) {
        queueAutoDrain = null; pauseQueue = true; toast(t("chat.connError")); renderPromptQueue();
      }
      return;
    }
    if (queueAutoDrain !== pending || !queueDrainScopeValid(pending.scope) || pauseQueue || streaming || queueDispatching || queueMerging) return;
    var head = promptQueue[0];
    if (!head || queueEditorBlocks(head)) return;
    queueAutoDrain = null; // Consume before any async preparation or sync/EOF.
    dispatchQueuedTask(head, pending.scope);
  } finally { pending.loading = false; }
}
function closeQueueEditor(edit, restoreFocus) {
  if (!edit || queueEdit !== edit) return;
  queueEdit = null;
  document.removeEventListener("keydown", edit.onKeyDown, true);
  if (activeAppDialog === edit.close) activeAppDialog = null;
  if (edit.dialog.open) edit.dialog.close();
  edit.dialog.remove();
  var trigger = restoreFocus !== false && edit.epoch === projectEpoch ? edit.trigger : null;
  if (trigger && !trigger.isConnected && edit.triggerHost && edit.triggerHost.isConnected) {
    trigger = Array.from(edit.triggerHost.querySelectorAll("[data-queue-edit-id]")).find(function (button) {
      return button.dataset.queueEditId === edit.id && button.className === edit.trigger.className && !button.disabled;
    });
  }
  if (trigger && trigger.isConnected && !trigger.disabled) trigger.focus();
  else promptEl.focus();
  if (restoreFocus !== false) resumeQueueAutoDrain();
}
function syncQueueEditor() {
  if (queueEdit && queueEdit.epoch !== projectEpoch) { queueAutoDrain = null; closeQueueEditor(queueEdit, false); }
  if (queueEdit && !promptQueue.some(function (item) { return item.id === queueEdit.id && !isQueuedTaskTransition(item); })) {
    // Another client may claim/remove the row. Keep the human's unsaved draft
    // visible instead of silently closing it during refresh or a 409 reload.
    queueAutoDrain = null;
    queueEdit.conflict = true;
    if (!queueEdit.error.textContent) queueEdit.error.textContent = t("common.error") + ": " + queueEdit.id + " · " + t("composer.stopped");
  } else if (queueEdit) { queueEdit.conflict = false; }
  return !!queueEdit;
}
function editQueuedTask(item, trigger) {
  if (queueMerging) return; // A submitted merge owns the rows until its reply.
  if (!promptQueue.some(function (q) { return q.id === item.id && !isQueuedTaskTransition(q); })) return;
  if (syncQueueEditor() && queueEdit.id === item.id) { queueEdit.input.focus(); return; }
  if (activeAppDialog) activeAppDialog(null);
  trigger = trigger || document.activeElement;
  queueEdit = { id: item.id, text: item.text || item.prompt || "", attachments: (item.attachments || []).slice(), uploading: false, saving: false, epoch: projectEpoch,
    trigger: trigger, triggerHost: trigger && trigger.parentElement && trigger.parentElement.parentElement };
  appendQueueEditor(queueEdit);
}
async function saveQueuedTaskEdit() {
  if (!syncQueueEditor() || queueEdit.uploading || queueEdit.saving || queueEditWrite) return;
  var edit = queueEdit;
  if (!edit.text.trim() && !edit.attachments.length) {
    edit.error.textContent = t("dialog.required"); edit.input.focus(); return;
  }
  edit.saving = true;
  queueEditWrite = edit;
  var failed = false;
  edit.refresh();
  try {
    await j("/api/tasks", { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ id: edit.id, prompt: edit.text, attachments: edit.attachments }) });
    if (queueEdit !== edit || !syncQueueEditor()) return;
    if (!await reloadQueueForEdit()) throw new Error(t("chat.connError"));
    if (queueEdit === edit && syncQueueEditor() && !edit.conflict) closeQueueEditor(edit);
  } catch (e) {
    failed = true;
    if (queueEdit === edit && syncQueueEditor()) {
      edit.error.textContent = e.message; toast(e.message); await loadPromptQueue();
    } else { queueAutoDrain = null; }
  } finally {
    edit.saving = false;
    if (queueEditWrite === edit) queueEditWrite = null;
    if (queueEdit && syncQueueEditor()) queueEdit.refresh();
    if (queueEdit !== edit && !failed) resumeQueueAutoDrain();
  }
}
function queuedMessageLabel(index, count) {
  return t("composer.messageNumber").replace("{index}", String(index)).replace("{count}", String(count));
}
async function mergeQueuedMessages(send, automatic) {
  if (queueMerging || queueDispatching || queueDispatchItem || pendingImmediate || !promptQueue.length) return false;
  // Auto merge mutates every row, not only the dispatch head. Never include
  // an editor or detached in-flight PATCH, even when the head itself is free.
  if (automatic && (!queueDrainScopeValid(automatic) || pauseQueue || queueEdit || queueEditWrite)) return false;
  var operationScope = automatic || queueDrainScope(), initiallyPaused = pauseQueue;
  if (promptQueue.every(function (q) { return q.merged; })) {
    if (send && !streaming) { pauseQueue = false; runQueuedTask(promptQueue[0], false); }
    return true;
  }
  var epoch = projectEpoch, sendMerged = false;
  queueAutoDrain = null;
  queueMerging = true;
  renderPromptQueue();
  try {
    var batches = await jpost("/api/tasks?merge=1", { ids: promptQueue.map(function (q) { return q.id; }), label: t("composer.messageNumber") });
    if (epoch !== projectEpoch) return false;
    ++promptQueueLoadSeq;
    promptQueue = batches || [];
    promptQueue.forEach(function (q) { q.text = q.prompt; });
    // The mutation may have finished after Stop. Its durable rows are useful,
    // but its old dispatch authority must never clear the newer pause.
    if (!queueDrainScopeValid(operationScope) || (!initiallyPaused && pauseQueue)) return false;
    pauseQueue = false;
    closeQueueEditor(queueEdit, false);
    sendMerged = send && !streaming && !!promptQueue[0];
    return true;
  } catch (e) { if (epoch === projectEpoch) { pauseQueue = true; toast(e.message); await loadPromptQueue(); } return false; }
  finally { queueMerging = false; renderPromptQueue(); if (sendMerged && queueDrainScopeValid(operationScope) && !pauseQueue && !streaming) runQueuedTask(promptQueue[0], false); }
}
function appendQueueEditor(edit) {
  var editor = el("dialog", "app-dialog queue-editor"); edit.dialog = editor;
  editor.setAttribute("aria-modal", "true");
  editor.setAttribute("aria-labelledby", "queue-editor-title");
  var title = i18nEl("h2", "app-dialog-title", "composer.editQueued"); title.id = "queue-editor-title";
  var input = el("textarea", "app-dialog-input queue-edit-input"); input.value = edit.text; input.rows = 16; edit.input = input;
  input.setAttribute("aria-label", t("composer.editQueued"));
  input.addEventListener("input", function () { if (queueEdit === edit && !edit.saving) edit.text = input.value; });
  var error = el("div", "app-dialog-error"); error.setAttribute("aria-live", "polite"); edit.error = error;
  var save = i18nEl("button", "btn primary", "common.save"); save.type = "button";
  save.addEventListener("click", function () { if (queueEdit === edit) return saveQueuedTaskEdit(); });
  var cancel = i18nEl("button", "btn", "common.cancel"); cancel.type = "button";
  edit.close = function () { closeQueueEditor(edit); };
  cancel.addEventListener("click", edit.close);
  editor.addEventListener("cancel", function (event) { event.preventDefault(); edit.close(); });
  editor.addEventListener("close", edit.close);
  editor.addEventListener("mousedown", function (event) {
    if (event.target !== editor) return;
    var rect = editor.getBoundingClientRect();
    if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) {
      event.preventDefault(); edit.close();
    }
  });
  edit.onKeyDown = function (event) {
    // A nested attachment preview owns its own keys and focus until it closes.
    if (!editor.contains(document.activeElement)) return;
    if (event.key === "Escape") {
      event.preventDefault(); event.stopImmediatePropagation(); edit.close();
    } else if (event.key === "Tab") {
      var controls = Array.from(editor.querySelectorAll("button:not([disabled]), textarea, input:not([hidden])"));
      var first = controls[0], last = controls[controls.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    }
  };
  // Do not let composer/chat keyboard shortcuts handle events from the editor.
  editor.addEventListener("keydown", function (event) { event.stopPropagation(); });
  var files = el("div", "attachment-list queue-edit-attachments");
  var picker = document.createElement("input"); picker.type = "file"; picker.multiple = true; picker.hidden = true;
  var attach = i18nEl("button", "btn", "composer.attach"); attach.type = "button";
  attach.addEventListener("click", function () { picker.click(); });
  function refresh() {
    var previousFocus = document.activeElement, hadFocus = editor.contains(previousFocus);
    renderAttachments(files, edit.attachments, function (index) {
      if (queueEdit !== edit || !syncQueueEditor() || edit.saving) return;
      edit.attachments.splice(index, 1); refresh();
    });
    input.readOnly = edit.saving;
    editor.setAttribute("aria-busy", String(edit.uploading || edit.saving));
    save.disabled = edit.uploading || edit.saving || !!queueEditWrite;
    attach.disabled = edit.uploading || edit.saving || edit.attachments.length >= 8;
    files.querySelectorAll(".attachment-remove").forEach(function (button) { button.disabled = edit.saving; });
    if (hadFocus && (!previousFocus.isConnected || previousFocus.disabled)) input.focus();
  }
  edit.refresh = refresh;
  async function upload(selected, key) {
    if (queueEdit !== edit || !syncQueueEditor() || edit.uploading || edit.saving) return;
    edit.uploading = true; refresh();
    try { await uploadAttachmentFiles(selected, key, edit); }
    catch (e) {
      if (queueEdit === edit && syncQueueEditor()) {
        error.textContent = t("composer.attachFailed") + ": " + e.message; toast(error.textContent);
      }
    }
    finally { edit.uploading = false; if (queueEdit === edit && syncQueueEditor()) refresh(); }
  }
  picker.addEventListener("change", function () { upload(picker.files, "composer.attach"); picker.value = ""; });
  editor.addEventListener("paste", function (event) {
    if (!event.clipboardData) return;
    var selected = Array.prototype.slice.call(event.clipboardData.files || []);
    if (!selected.length) selected = Array.prototype.slice.call(event.clipboardData.items || []).filter(function (item) { return item.kind === "file"; }).map(function (item) { return item.getAsFile(); }).filter(Boolean);
    if (!selected.length) return;
    if (!event.clipboardData.getData("text/plain")) event.preventDefault();
    event.stopPropagation(); upload(selected, "composer.pasted");
  });
  editor.addEventListener("dragover", function (event) {
    if (event.dataTransfer && Array.prototype.indexOf.call(event.dataTransfer.types || [], "Files") >= 0) {
      event.preventDefault(); event.stopPropagation(); editor.classList.add("drop-target");
    }
  });
  editor.addEventListener("dragleave", function () { editor.classList.remove("drop-target"); });
  editor.addEventListener("drop", function (event) {
    editor.classList.remove("drop-target");
    if (!event.dataTransfer || !event.dataTransfer.files.length) return;
    event.preventDefault(); event.stopPropagation(); upload(event.dataTransfer.files, "composer.dropped");
  });
  var actions = el("div", "app-dialog-actions queue-edit-actions");
  actions.appendChild(attach); actions.appendChild(cancel); actions.appendChild(save);
  editor.appendChild(title); editor.appendChild(input); editor.appendChild(files); editor.appendChild(picker);
  editor.appendChild(error); editor.appendChild(actions);
  document.body.appendChild(editor); refresh();
  activeAppDialog = edit.close;
  document.addEventListener("keydown", edit.onKeyDown, true);
  editor.showModal(); input.focus();
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
  syncQueueEditor();
  var host = $("#prompt-queue");
  host.innerHTML = "";
  host.hidden = !promptQueue.length;
  if (promptQueue.length) {
    var merge = i18nEl("button", "queue-merge queue-action", "composer.sendTogether"); merge.type = "button";
    merge.disabled = queueMerging || !!queueDispatchItem || queueDispatching || !!pendingImmediate;
    merge.addEventListener("click", function () { mergeQueuedMessages(true); });
    host.appendChild(merge);
  }
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
    var position = queuePositionButton(item, index);
    position.disabled = isQueuedTaskTransition(item);
    row.appendChild(position);
    var text = el("button", "queue-text", item.text);
    text.type = "button";
    text.title = t("composer.editQueued");
    appendQueuedAttachmentBadge(text, item);
    text.dataset.queueEditId = item.id;
    text.addEventListener("click", function () { editQueuedTask(item, text); });
    row.appendChild(text);
    if ((item.attachments || []).some(imageAttachment)) appendQueuedImageThumbnails(row, item);
    var now = i18nEl("button", "queue-action", "composer.sendNow"); now.type = "button";
    now.addEventListener("click", function () { runQueuedTask(item, true); });
    styleQueuedTaskTransition(row, item, now);
    var remove = el("button", "queue-remove", "×"); remove.type = "button"; remove.title = t("composer.remove");
    remove.addEventListener("click", function () { removeQueuedTask(item.id); });
    remove.disabled = isQueuedTaskTransition(item);
    text.disabled = remove.disabled;
    row.appendChild(now); row.appendChild(remove);
    host.appendChild(row);
    if (!isQueuedTaskTransition(item)) wireQueuedTaskDrag(row, drag, item);
  });
  updateAppBadge();
  renderTaskCenter();
}
// Queued attachments keep the /api/chat representation (staged file paths),
// so the durable row survives reloads and other clients see the same files.
async function enqueuePrompt(text, attachments) {
  attachments = (attachments || []).slice();
  try {
    var body = { session_id: activeSessionID, prompt: text };
    if (attachments.length) body.attachments = attachments;
    var q = await jpost("/api/tasks", body);
    q.text = q.prompt;
    q.attachments = q.attachments || [];
    promptQueue = promptQueue.filter(function (item) { return item.id !== q.id; });
    promptQueue.push(q);
    pauseQueue = false;
    renderPromptQueue();
  } catch (e) { toast(e.message); return false; }
  setRunState("running", promptQueue.length + " · " + t("composer.queued"));
  return true;
}
function loadPromptQueue() {
  syncQueueEditor();
  var epoch = projectEpoch, seq = ++promptQueueLoadSeq;
  promptQueueReady = (async function () {
    try {
      var state = await j("/api/tasks?state=1");
      if (epoch !== projectEpoch || seq !== promptQueueLoadSeq) return;
      queueMergeMode = state.queue_merge || "off";
      promptQueue = state.tasks || [];
      promptQueue.forEach(function (q) { q.text = q.prompt; q.attachments = q.attachments || []; });
      // Only the first idle load asks for Resume. Refreshing Tasks must never
      // manufacture a pause, nor clear an explicit Stop/send-failure pause.
      if (promptQueueLoadedEpoch !== epoch) {
        pauseQueue = pauseQueue || (promptQueue.length > 0 && !streaming && !state.running);
        promptQueueLoadedEpoch = epoch;
      }
      renderPromptQueue();
      return true;
    } catch (e) { return false; /* Keep the last known queue; never authorize a save from it. */ }
  })();
  return promptQueueReady;
}

async function loadWorkers() {
  var host = $("#worker-list"); if (!host) return;
  $("#worker-heading").textContent = t("task.workers");
  var bindings = typeof workerStatusBindings === "function" ? workerStatusBindings() : null;
  var epoch = bindings ? workerStatusEpoch : 0, scope = bindings ? workerStatusScope() : "";
  try {
    var got = await j("/api/workers");
    renderWorkers(got && got.workers ? got.workers : []);
    if (bindings && got && Array.isArray(got.workers) && epoch === workerStatusEpoch && scope === workerStatusScope()) {
      applyWorkerStatusSnapshot(got.workers, bindings);
    }
  } catch (e) {
    host.innerHTML = "";
    host.appendChild(el("div", "side-empty", e.message));
  }
}
function renderWorkers(workers) {
  var host = $("#worker-list"); if (!host) return; host.innerHTML = "";
  if (!workers.length) {
    host.appendChild(i18nEl("div", "side-empty", "task.noWorkersProcess"));
    return;
  }
  workers.slice().reverse().forEach(function (worker) {
    var row = el("div", "task-center-row worker-center-row");
    var state = String(worker.status || "created");
    row.appendChild(el("span", "task-center-index worker-state " + state, state === "running" || state === "created" ? "●" : "✓"));
    var copy = el("div", "task-center-copy");
    copy.appendChild(el("span", "t", worker.id + " · " + (worker.agent || "worker")));
    var meta = el("span", "s");
    meta.appendChild(el("span", "", state + " · " + (worker.steps || 0) + " "));
    meta.appendChild(i18nEl("span", "", "task.steps"));
    meta.appendChild(el("span", "", " · " + ((worker.tokens_in || 0) + (worker.tokens_out || 0)) + " tok"));
    copy.appendChild(meta);
    if (worker.description) copy.title = worker.description;
    row.appendChild(copy);
    if (state === "running" || state === "created") {
      var stop = i18nEl("button", "queue-action", "composer.stop");
      stop.addEventListener("click", async function () {
        try { await jpost("/api/workers", { id: worker.id, action: "stop" }); await loadWorkers(); }
        catch (e) { toast(e.message); }
      });
      row.appendChild(stop);
    }
    host.appendChild(row);
  });
}
function queuedAttachmentCount(item) {
  return item && Array.isArray(item.attachments) ? item.attachments.length : 0;
}
function appendQueuedAttachmentBadge(host, item) {
  var count = queuedAttachmentCount(item);
  if (!count) return null;
  var label = t("composer.queuedAttachments").replace("{count}", String(count));
  var badge = el("span", "queue-attach-count", "\uD83D\uDCCE" + count);
  badge.title = label;
  badge.setAttribute("aria-label", label);
  host.appendChild(badge);
  return badge;
}
// keepAttachments: an accepted dispatch retires the row, but the started run
// now owns its staged files. A user removal lets the server delete them.
async function removeQueuedTask(id, keepAttachments, ownsView) {
  try {
    await j("/api/tasks?id=" + encodeURIComponent(id) + (keepAttachments ? "&keep_attachments=1" : ""), { method: "DELETE" });
    if (!ownsView || ownsView()) {
      promptQueue = promptQueue.filter(function (q) { return q.id !== id; });
      renderPromptQueue();
    }
    return true;
  } catch (e) { if (!ownsView || ownsView()) toast(e.message); return false; }
}
function isQueuedTaskTransition(item) {
  var selected = pendingImmediate || queueDispatchItem;
  return !!(selected && selected.id && selected.id === item.id);
}
function styleQueuedTaskTransition(row, item, button) {
  var selected = isQueuedTaskTransition(item);
  row.classList.toggle("queue-transition", selected);
  button.classList.toggle("queue-sending", selected);
  if (selected) {
    row.setAttribute("aria-busy", "true");
    button.dataset.i18nText = "composer.sendingQueued";
    button.textContent = t("composer.sendingQueued");
    button.i18nTextNode = button.firstChild;
  }
  button.disabled = !!(pendingImmediate || queueDispatching || queueDispatchItem);
}
function showQueueTransition(item) {
  queueDispatchItem = item;
  sendBtn.textContent = t("composer.queue");
  sendBtn.classList.add("queue");
  $("#stop-run-btn").hidden = false;
  $("#interrupt-btn").hidden = true;
  setRunState("running", t("composer.sendingQueued"));
  renderPromptQueue();
}
async function dispatchQueuedTask(item, automatic) {
  if (queueDispatching || queueMerging || streaming || !queueDispatchGuard(item, automatic)) return;
  queueAutoDrain = null;
  queueDispatching = true;
  showQueueTransition(item);
  var launched = false, deferredForEdit = false;
  // Capture authority once, before any async prepare/preflight/merge. A queued
  // session switch is intentional, but never mint a fresh Stop revision later.
  var scope = Object.assign({}, automatic || queueDrainScope(), {session: item.session_id || (typeof activeSessionID === "string" ? activeSessionID : "")});
  try {
    if (!await prepareQueuedTask(item) || !queueDrainScopeValid(scope) || (pauseQueue && !isAfterRunPrompt(item))) return;
    if (!item.afterRun && !item.merged && queueMergeMode === "auto" && !queueEdit && !queueEditWrite) {
      if (promptQueue.length > 0) {
        queueDispatching = false; queueDispatchItem = null;
        if (!await reloadQueueForEdit() || !queueDrainScopeValid(scope) || pauseQueue) return;
        item = promptQueue.find(function (q) { return q.id === item.id; });
        if (!item) return;
        // Last check before the mutation: an editor/PATCH may have appeared
        // during either await. Send the free row separately instead of merging.
        if (!queueEdit && !queueEditWrite) {
          if (!await mergeQueuedMessages(false, scope) || !queueDrainScopeValid(scope) || pauseQueue) return;
          item = promptQueue[0];
          if (!item) return;
        }
        if (!queueDispatchGuard(item, automatic && scope)) { deferredForEdit = true; return; }
        queueDispatching = true; showQueueTransition(item);
        if (!await prepareQueuedTask(item) || !queueDrainScopeValid(scope)) return;
      }
    }
    if (!queueDrainScopeValid(scope) || (pauseQueue && !isAfterRunPrompt(item))) return;
    // Remove the durable queue entry only after /api/chat accepts it.
    launched = true;
    sendPrompt(item.text, item.attachments || [], item.draft || null, item, scope);
  } catch (e) {
    toast(e.message);
  } finally {
    if (!launched) {
      restoreUnsentAfterRun(item);
      if (item.draft && !promptEl.value && composerDraftStore.scope() === item.draft.scope) {
        composerDraftStore.restore(item.draft.scope);
      }
      if (!deferredForEdit && scope.epoch === projectEpoch) pauseQueue = true;
      queueDispatching = false;
      queueDispatchItem = null;
      sendBtn.textContent = t("composer.send");
      sendBtn.classList.remove("queue");
      $("#stop-run-btn").hidden = true;
      $("#interrupt-btn").hidden = true;
      setRunState("idle", t("composer.ready"));
      renderPromptQueue();
    }
  }
}
async function runQueuedTask(item, interrupt) {
  if (pendingImmediate || queueDispatching || queueDispatchItem) return;
  pauseQueue = false;
  if (streaming) {
    if (interrupt) interruptAndSend(item.text, item);
    return;
  }
  await dispatchQueuedTask(item);
}
function renderTaskCenter() {
  syncQueueEditor();
  var host = $("#task-list"); if (!host) return; host.innerHTML = "";
  if (!promptQueue.length) { host.appendChild(i18nEl("div", "side-empty", "composer.queueEmpty")); return; }
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
    var meta = el("span", "s", item.session_id ? clip(item.session_id, 18) : t("session.new"));
    appendQueuedAttachmentBadge(meta, item);
    copy.appendChild(meta);
    copy.dataset.queueEditId = item.id;
    copy.addEventListener("click", function () { editQueuedTask(item, copy); });
    row.appendChild(copy);
    var edit = i18nEl("button", "queue-action", "common.edit"); edit.type = "button";
    edit.dataset.queueEditId = item.id;
    edit.addEventListener("click", function () { editQueuedTask(item, edit); });
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
    var go = i18nEl("button", "queue-action", "composer.sendNow");
    go.addEventListener("click", function () { runQueuedTask(item, true); });
    styleQueuedTaskTransition(row, item, go);
    row.appendChild(go);
    if (isQueuedTaskTransition(item)) {
      copy.disabled = edit.disabled = up.disabled = down.disabled = true;
      position.disabled = true;
    }
    host.appendChild(row);
    if (!isQueuedTaskTransition(item)) wireQueuedTaskDrag(row, drag, item);
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
    empty.appendChild(i18nEl("div", "side-goal-empty-title", "goal.sideEmpty"));
    empty.appendChild(i18nEl("div", "side-goal-empty-copy", "goal.sideEmptyHint"));
    var create = i18nEl("button", "side-goal-manage", "goal.create");
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
  progressMeta.appendChild(i18nEl("span", "", "goal.progress"));
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
    // Keep the current selection: restoring stale sidebar metadata here can
    // undo a model/reasoning change. sendPrompt awaits runtime readiness.
    return true;
  }
  return await resumeSession(item.session_id, sessionByID[item.session_id] || null, true);
}
function interruptAndSend(text, item, attachments, draft) {
  if (pendingImmediate || queueDispatching || queueDispatchItem) return;
  pendingImmediate = item || { text: text, session_id: activeSessionID, attachments: attachments || [], draft: draft || null };
  if (item && attachments && attachments.length) pendingImmediate.attachments = attachments;
  pauseQueue = false;
  showQueueTransition(pendingImmediate);
  $("#interrupt-btn").disabled = true;
  if (streaming) {
    if (abortCtl && !runFinishing) abortCtl.abort();
  } else {
    var next = pendingImmediate;
    pendingImmediate = null;
    dispatchQueuedTask(next);
  }
}

// Generation has ended, but transcript/checkpoint writes still own this turn.
// Release presentation now; keep the serialization fence until HTTP EOF/receipt.
function finishRunPresentation() {
  runFinishing = true;
  clearInterval(runTimer);
  runTimer = null;
  if (!pendingImmediate) {
    sendBtn.textContent = t("composer.send");
    sendBtn.classList.remove("queue");
    $("#stop-run-btn").hidden = true;
    $("#interrupt-btn").hidden = true;
    $("#status-dot").classList.remove("busy");
  }
}
// A run can leave this view only after the server accepted its prompt and
// while nothing waits for the user here (an open question or a typed handoff).
function canDetachActiveRun() {
  return !!(streaming && activeRun && activeRun.accepted && activeRun.sessionID && !pendingImmediate &&
    !Object.keys(questionOverlays).length);
}
function detachActiveRun() {
  if (!canDetachActiveRun()) return false;
  activeRun.detached = true;
  detachedRuns[activeRun.sessionID] = activeRun;
  activeRun = null;
  queueDispatchItem = null;
  queueDispatching = false;
  finishRunPresentation();
  streaming = false;
  runFinishing = false;
  if (typeof resumeWorkerStatusSync === "function") resumeWorkerStatusSync();
  abortCtl = null;
  releaseLiveTranscriptBlocks();
  setRunState("idle", t("composer.ready"));
  loadSessions();
  return true;
}
// A background run that can be shown again: still going, and its first
// message is known, so saved history before it can be rendered without
// repeating anything the replay adds.
function detachedRunFor(id) {
  var run = id ? detachedRuns[id] : null;
  return run && run.detached && !run.terminal && run.userSeq > 0 ? run : null;
}
function reattachDetachedRun(id) {
  var run = detachedRunFor(id);
  if (!run && typeof syncRunFor === "function") run = syncRunFor(id);
  if (!run || streaming || queueDispatching) return false;
  delete detachedRuns[id];
  run.reattach();
  return true;
}
// Every event is kept for a later replay; streamed text is merged so a long
// answer stays one entry instead of thousands of deltas.
function rememberRunEvent(run, ev) {
  if (ev.type === "session" && ev.session_id) run.sessionID = ev.session_id;
  if (ev.type === "session_activity") { run.activity = ev; run.userSeq = ev.user_seq || 0; }
  if (ev.type === "done" || ev.type === "error") run.terminal = true;
  if (ev.type === "error") run.failure = ev;
  if (ev.type === "question_closed") {
    run.events = run.events.filter(function (old) { return !(old.type === "question" && old.question && old.question.id === ev.id); });
  }
  if ((ev.type === "message" || ev.type === "reasoning") && ev.text) {
    var last = run.events[run.events.length - 1];
    if (last && last.type === ev.type && last.merged) { last.text += ev.text; return; }
    run.events.push({ type: ev.type, text: ev.text, merged: true });
    return;
  }
  run.events.push(ev);
}
function isAfterRunPrompt(item) {
  return !!(item && item.afterRun && item.stopRevision === runStopRevision);
}
function restoreUnsentAfterRun(item) {
  if (!item || !item.afterRun || !item.draft || promptEl.value) return;
  var scope = composerDraftStore.scope();
  if (scope !== item.draft.scope && (!item.session_id || item.session_id !== activeSessionID)) return;
  promptEl.value = item.draft.text;
  promptEl.style.height = "auto";
  if (composerDraftStore.capture) composerDraftStore.capture();
  if (scope !== item.draft.scope) composerDraftStore.clear(item.draft.scope, item.draft.text);
  addAttachmentPaths(item.attachments || []);
}
function sendAfterRun(text, attachments, draft) {
  pendingImmediate = {text: text, session_id: activeSessionID, attachments: attachments, draft: draft, afterRun: true, stopRevision: runStopRevision};
  // A manual next message bypasses the pause for this one turn only. Stop must
  // not silently resume older queued work when the user starts a new prompt.
  showQueueTransition(pendingImmediate);
}

function newChatTurnID() {
  if (window.crypto && window.crypto.randomUUID) return window.crypto.randomUUID();
  return Date.now().toString(36) + "-" + (++chatTurnSequence).toString(36) + "-" + Math.random().toString(36).slice(2);
}
function setRunState(state, text) {
  runStatus.textContent = text || "";
  $("#status-dot").classList.toggle("busy", state === "running");
}

async function sendPrompt(text, attachments, draft, queuedItem, dispatchScope) {
  if (streaming || (queueDispatching && !queuedItem) || (!queuedItem && attachmentUploadBusy())) return;
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
  if (queuedItem && ((dispatchScope && !queueDrainScopeValid(dispatchScope)) || (queuedItem.afterRun && !isAfterRunPrompt(queuedItem)) || (pauseQueue && !isAfterRunPrompt(queuedItem)))) {
    restoreUnsentAfterRun(queuedItem);
    if (draft && !promptEl.value && composerDraftStore.scope() === draft.scope) {
      composerDraftStore.restore(draft.scope);
    }
    queueDispatching = false;
    queueDispatchItem = null;
    sendBtn.textContent = t("composer.send");
    sendBtn.classList.remove("queue");
    $("#stop-run-btn").hidden = true;
    $("#interrupt-btn").hidden = true;
    setRunState("idle", t("composer.ready"));
    renderPromptQueue();
    return;
  }
  queueAutoDrain = null; // A new root turn supersedes any deferred old handoff.
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
  var run = { detached: false, accepted: false, terminal: false, sessionID: activeSessionID, userSeq: 0, activity: null, events: [] };
  activeRun = run;
  runFinishing = false;
  queueDispatching = false;
  transcriptLiveAppend = true;
  abortCtl = new AbortController();
  run.abortCtl = abortCtl;
  runToolCount = 0;
  // Keep worker identity and previous cards across coordinator turns.
  toolRows = {}; openToolOrder = [];
  pruneWorkerOverview();
  sendBtn.textContent = t("composer.queue");
  sendBtn.classList.add("queue");
  sendBtn.type = "submit";
  $("#stop-run-btn").hidden = false;
  $("#interrupt-btn").hidden = false;
  $("#interrupt-btn").disabled = false;
  var persistedText = attachmentTranscriptText(text, attachments);
  var liveUserNode = addUserMsg(text, 0, attachments);
  var current = null;
  var terminalSeen = false;
  var stopped = false, handoffBlocked = false;
  var turnID = newChatTurnID();
  run.turnID = turnID;
  var disconnectTransport = abortCtl.abort.bind(abortCtl);
  run.disconnect = disconnectTransport;
  var stopInFlight = false, stopDelivered = false;
  function stopServerRun() {
    if ((!requestAccepted && !acceptanceUncertain) || stopInFlight || stopDelivered) return;
    stopInFlight = true;
    // Do not pass the Stop response to abort(reason): fetch would reject with
    // that JSON object instead of an AbortError.
    jpost("/api/sync/stop", {session_id: run.sessionID, turn_id: turnID}).then(function () {
      stopDelivered = true;
      disconnectTransport();
    }).catch(function (error) { toast(error.message); }).finally(function () { stopInFlight = false; });
  }
  run.abortCtl.abort = function () { run.stopRequested = true; stopServerRun(); };
  var draftAccepted = false, requestAccepted = false, acceptanceUncertain = false;
  var acceptanceScope = {epoch: projectEpoch, session: activeSessionID};
  function ownsAcceptanceView() {
    return activeRun === run && !run.detached && projectEpoch === acceptanceScope.epoch &&
      (activeSessionID === acceptanceScope.session || activeSessionID === run.sessionID);
  }
  // No second POST: observe this exact turn's durable receipt. Each read and the
  // whole observation window are bounded, including when the phone is offline.
  async function reconcileAcceptance() {
    // Cancellation can target a registered turn even before durable acceptance.
    // Do not make Stop wait for a receipt which might never become accepted.
    for (var attempt = 0; attempt < 4 && ownsAcceptanceView(); attempt++) {
      if (run.stopRequested) stopServerRun();
      var ctl = new AbortController();
      var timeout = setTimeout(function () { ctl.abort(); }, 2000);
      try {
        var receipt = await j("/api/chat/completion?id=" + encodeURIComponent(turnID) + "&status=1", {signal: ctl.signal});
        if (!ownsAcceptanceView()) return false;
        if (receipt && receipt.turn_id && receipt.turn_id !== turnID) return false;
        if (receipt && receipt.accepted && receipt.session_id) {
          run.sessionID = receipt.session_id;
          activeSessionID = receipt.session_id;
          transcriptSessionID = receipt.session_id;
          requestAccepted = true;
          acceptPrompt();
          if (receipt.done) { terminalSeen = true; run.terminal = true; }
          if (run.stopRequested) { stopped = true; if (!receipt.done) stopServerRun(); }
          if (queueRemoval) await queueRemoval;
          return true;
        }
        if (receipt && receipt.done) return false;
      } catch (statusError) {
        // 404 may race request admission; network failure is not rejection.
      } finally { clearTimeout(timeout); }
      if (attempt < 3) await new Promise(function (resolve) { setTimeout(resolve, 300); });
    }
    return false;
  }
  var queueRemoval = null, queueClaimLost = false;
  // DELETE may finish after detach/navigation, or after ordinary foreground
  // cleanup cleared activeRun. Only the latter still owns this queue view.
  function ownsAcceptedQueueView() {
    return !run.detached && projectEpoch === acceptanceScope.epoch && activeSessionID === run.sessionID &&
      (!activeRun || activeRun === run);
  }
  function acceptPrompt() {
    if (draftAccepted) return;
    draftAccepted = true;
    run.accepted = true;
    if (draft) composerDraftStore.clear(draft.scope, draft.text);
    if (queuedItem && queuedItem.id) {
      queueRemoval = removeQueuedTask(queuedItem.id, true, ownsAcceptedQueueView).then(function (removed) {
        if (!ownsAcceptedQueueView()) return;
        if (!removed) pauseQueue = true;
        queueDispatchItem = null;
        renderPromptQueue();
      });
    } else {
      queueDispatchItem = null;
      renderPromptQueue();
    }
  }
  var lastProgressAt = Date.now();
  runStart = Date.now();
  run.start = runStart;
  setRunState("running", t("composer.working"));
  function startRunTimer() {
    clearInterval(runTimer);
    runTimer = setInterval(function () {
      if (runFinishing) return;
      var now = Date.now();
      var quiet = now - lastProgressAt;
      if (pendingImmediate) {
        runStatus.textContent = t("composer.sendingQueued");
        return;
      }
      runStatus.textContent = (quiet >= 10000 ? t("composer.waiting") + " " + fmtDuration(quiet) :
        t("composer.working") + " " + fmtDuration(now - runStart));
    }, 100);
  }
  startRunTimer();
  // Shows this background run in the current view again: the caller has
  // rendered saved history before its first message; the replay adds the rest.
  run.reattach = function () {
    if (transcriptAbortCtl) transcriptAbortCtl.abort();
    transcriptAbortCtl = null;
    transcriptHasMore = false;
    transcriptBeforeSeq = 0;
    loadedTranscriptMessages = [];
    var pager = stream.querySelector(".history-older");
    if (pager) pager.remove();
    run.detached = false;
    activeRun = run;
    streaming = true;
    runFinishing = false;
    abortCtl = run.abortCtl;
    runStart = run.start;
    transcriptLiveAppend = true;
    toolRows = {}; openToolOrder = [];
    sendBtn.textContent = t("composer.queue");
    sendBtn.classList.add("queue");
    $("#stop-run-btn").hidden = false;
    $("#interrupt-btn").hidden = false;
    $("#interrupt-btn").disabled = false;
    setRunState("running", t("composer.working"));
    liveUserNode = addUserMsg(text, 0, attachments);
    if (run.activity) bindLiveMessageReceipt(liveUserNode, run.activity);
    current = null;
    run.events.forEach(function (ev) {
      if (ev.type === "session" || ev.type === "session_activity") return;
      if (ev.type === "finishing") {
        finishRunPresentation();
        closeAssistantReasoning(current);
        flushAssistantRender(current);
        return;
      }
      current = handleEvent(ev, current);
    });
    lastProgressAt = Date.now();
    if (!runFinishing) startRunTimer();
  };

  try {
    var resp = await superCliUI.chatResponse("/api/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        prompt: text,
        turn_id: turnID,
        client_id: typeof syncClientID === "string" ? syncClientID : "",
        queue_task_id: queuedItem && queuedItem.id || "",
        session_id: activeSessionID,
        attachments: attachments,
      }),
      signal: run.abortCtl.signal,
    });
    if (!resp.ok || !resp.body) {
      addEventLine(t("common.error") + ": " + resp.status, "error");
      return;
    }
    requestAccepted = true;
    acceptanceUncertain = true;
    if (run.stopRequested) stopServerRun();
    await superCliUI.readSSE(resp.body, function (ev) {
      if (ev.type === "queue_claimed" && queuedItem && queuedItem.id) {
        // Same neutral result over SSE and phone WebSocket. Replace the
        // optimistic message with the winner's shared run, without pausing.
        queueClaimLost = true;
        draftAccepted = true;
        handoffBlocked = true;
        terminalSeen = true;
        promptQueue = promptQueue.filter(function (q) { return q.id !== queuedItem.id; });
        if (liveUserNode && liveUserNode.remove) liveUserNode.remove();
        return;
      }
      rememberRunEvent(run, ev);
      if (ev.queue_paused && !pendingImmediate && !run.detached) pauseQueue = true;
      if (run.detached) {
        if (ev.type === "done" || ev.type === "error") terminalSeen = true;
        return;
      }
      lastProgressAt = Date.now();
      if (ev.type === "session_activity") {
        acceptPrompt();
        bindLiveMessageReceipt(liveUserNode, ev);
      }
      if (ev.type === "finishing") {
        finishRunPresentation();
        closeAssistantReasoning(current);
        flushAssistantRender(current);
        setRunState(pendingImmediate ? "running" : "idle", t(pendingImmediate ? "composer.sendingQueued" : "run.done"));
      }
      if (ev.type === "done" || ev.type === "error") {
        terminalSeen = true;
        finishRunPresentation();
      }
      current = handleEvent(ev, current);
    });
    if (run.detached || queueClaimLost) return;
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
    acceptanceUncertain = true;
    // Use this run's signal, not the controller of a newer foreground run.
    var aborted = run.abortCtl.signal.aborted || !!(e && e.name === "AbortError");
    if (run.detached) {
      if (aborted) run.aborted = true;
      return;
    }
    if (aborted) {
      stopped = true;
      addEventLine(t("chat.stopped"), "", "stop");
      setRunState(pendingImmediate ? "running" : "idle", t(pendingImmediate ? "composer.sendingQueued" : "composer.stopped"));
    } else {
      var message = e && e.message || (typeof e === "string" ? e : "");
      addEventLine(t("chat.connError") + (message ? ": " + message : ""), "error");
      setRunState("idle", t("chat.connError"));
    }
  } finally {
    if (!draftAccepted && !terminalSeen && acceptanceUncertain && !(requestAccepted && run.stopRequested)) {
      // HTTP success alone is not durable acceptance. A lost response can hide
      // either a running turn or rejection; neither permits an automatic drain.
      handoffBlocked = true;
      await reconcileAcceptance();
      if (!ownsAcceptanceView() && !draftAccepted) return;
      // Acceptance can make the run detachable while DELETE is still pending.
      // Fence foreground state, but still relinquish that old run to sync below.
      if (ownsAcceptanceView()) {
        if (!draftAccepted || terminalSeen) {
          pauseQueue = true;
          restoreUnsentAfterRun(pendingImmediate);
          if (pendingImmediate && pendingImmediate.draft && !promptEl.value) composerDraftStore.restore(pendingImmediate.draft.scope);
        } else handoffBlocked = false;
      }
    }
    if (!queueClaimLost && typeof syncFollowRun === "function" && !terminalSeen && !run.stopRequested && draftAccepted) syncFollowRun(run);
    if (run.following) return;
    if (run.detached) {
      if (detachedRuns[run.sessionID] === run) delete detachedRuns[run.sessionID];
      if (run.sessionID && !run.aborted && (run.failure || !run.terminal)) {
        var failure = run.failure || { type: "error", err: t("chat.streamEnded") };
        failedRuns[run.sessionID] = failure;
        if (run.sessionID !== activeSessionID || streaming) toast(t("common.error") + ": " + chatErrorText(failure));
      }
      loadSessions();
      // A view that opened this conversation while it ran shows the saved end.
      if (run.sessionID === activeSessionID && !streaming && !queueDispatching) {
        resumeSession(run.sessionID, sessionByID[run.sessionID] || null);
      }
      return;
    }
    if (activeRun === run) activeRun = null;
    sealAssistantSegment(current);
    closeQuestionOverlay();
    abortCtl = null;
    finishRunPresentation();
    settleOpenTools();
    if (typeof addLiveAssistantActions === "function") addLiveAssistantActions();
    releaseLiveTranscriptBlocks();
    // One completion wait replaces transcript polling after Stop. The server
    // closes this receipt only after the canceled loop finishes its writes.
    if (stopped && requestAccepted) {
      if (pendingImmediate) setRunState("running", t("composer.sendingQueued"));
      try {
        var completed = await j("/api/chat/completion?id=" + encodeURIComponent(turnID));
        if (completed && completed.accepted) acceptPrompt();
        if (completed && completed.session_id) {
          activeSessionID = completed.session_id;
          transcriptSessionID = completed.session_id;
          if (pendingImmediate && pendingImmediate.afterRun && !pendingImmediate.session_id) pendingImmediate.session_id = completed.session_id;
        }
      } catch (completionError) {
        handoffBlocked = true;
        pauseQueue = true;
        restoreUnsentAfterRun(pendingImmediate);
        if (pendingImmediate && pendingImmediate.draft && !promptEl.value) {
          composerDraftStore.restore(pendingImmediate.draft.scope);
        }
        toast(completionError.message);
      }
    }
    if (draft && !draftAccepted && !promptEl.value && composerDraftStore.scope() === draft.scope) {
      composerDraftStore.restore(draft.scope);
    }
    // Keep writes serialized, while the composer can accept one next prompt.
    if (queueRemoval) await queueRemoval;
    if (queuedItem && !draftAccepted) {
      restoreUnsentAfterRun(queuedItem);
      pauseQueue = true;
    }
    queueDispatchItem = pendingImmediate;
    var userSeq = draftAccepted ? await addLatestMessageRewind(liveUserNode, persistedText, 1) : 0;
    if (userSeq) rememberSentAttachments(activeSessionID, userSeq, attachments);
    // Presentation refreshes do not block the next model request.
    loadSessions();
    if (!overlay.hidden && currentSection === "goal" && sections.goal) sections.goal();
    if ($("#side-tab-goal").classList.contains("active")) loadSideGoal();
    renderStats();
    var immediate = pendingImmediate;
    pendingImmediate = null;
    streaming = false;
    runFinishing = false;
    if (typeof resumeWorkerStatusSync === "function") resumeWorkerStatusSync();
    var automatic = !immediate ? queueDrainScope(run.turnID) : null;
    var next = handoffBlocked ? null : (immediate && (isAfterRunPrompt(immediate) || !pauseQueue) ? immediate : (!pauseQueue ? promptQueue[0] : null));
    if (next && !queueDispatchGuard(next, automatic)) next = null;
    if (next) {
      dispatchQueuedTask(next, automatic);
    } else {
      queueDispatchItem = null;
      queueDispatching = false;
      sendBtn.textContent = t("composer.send");
      sendBtn.classList.remove("queue");
      sendBtn.type = "submit";
      $("#stop-run-btn").hidden = true;
      $("#interrupt-btn").hidden = true;
      if (runStatus.textContent.indexOf(t("composer.working")) === 0 || runStatus.textContent === t("composer.sendingQueued")) setRunState("idle", t("composer.ready"));
      $("#status-dot").classList.remove("busy");
      promptEl.focus();
      renderPromptQueue();
    }
    if (queueClaimLost) {
      loadPromptQueue();
      if (typeof syncConnect === "function") syncConnect();
    }
  }
}


// A provider can report reasoning tokens without streaming any thinking text.
// The server sends the bare count for exactly that case; the sentence is built
// here so it follows the selected UI language instead of being frozen in Go.
function reasoningFallbackText(tokens) {
  if (!tokens || tokens <= 0) return "";
  return t("reasoning.noSummary").replace("{n}", fmtInteger(tokens));
}

// A known failure arrives as a catalog key so the sentence follows the UI
// language; ev.err stays the fallback for everything still unclassified.
function chatErrorText(ev) {
  if (ev.err_code) return t(ev.err_code).replace("{n}", ev.err_name || "");
  return ev.err || "error";
}

function handleEvent(ev, current) {
  if (typeof refreshHostPower === "function" && /^(tool_result|done|error)$/.test(ev.type)) refreshHostPower();
  var remoteRun = typeof activeRun !== "undefined" && activeRun && activeRun.remote ? activeRun : null;
  var eventAt = remoteRun ? ev.at : 0;
  switch (ev.type) {
    case "session":
      if (ev.session_id) {
        activeSessionID = ev.session_id;
        transcriptSessionID = ev.session_id;
        if (typeof syncSelectSession === "function") syncSelectSession(ev.session_id);
      }
      return current;
    case "session_activity":
      if (ev.session_id === activeSessionID) loadSessions();
      return current;
    case "message":
      if (!current || current._sealed) current = addAssistantMsg();
      closeAssistantReasoning(current);
      appendAssistantSource(current, ev.text);
      scheduleAssistantRender(current);
      return current;
    case "reasoning":
      var reasoningText = ev.text || reasoningFallbackText(ev.reasoning_tok);
      if (!reasoningText) return current;
      if (!current || current._sealed) current = addAssistantMsg();
      appendAssistantReasoning(current, reasoningText);
      return current;
    case "tool_call":
      sealAssistantSegment(current);
      addToolCall(ev.name, ev.args, ev.id, eventAt);
      if (typeof scheduleLiveStats === "function") scheduleLiveStats();
      return null;
    case "tool_result":
      addToolResult(ev.id, ev.output, ev.err, eventAt);
      if (!ev.err && Array.isArray(ev.images)) appendNativeToolImages(toolRows[ev.id], ev.images);
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
	case "question_closed":
      closeQuestionOverlay(ev.id);
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
      var savedActivity = validWorkerActivity(ev.worker_activity, task.id);
      if (savedActivity && savedActivity.run !== ev.run) savedActivity = null;
      var boundCall = savedActivity && savedActivity.version === 1 && savedActivity.id === task.id &&
        savedActivity.run === ev.run && typeof savedActivity.parent_call_id === "string" ? savedActivity.parent_call_id : null;
      var workerRow = findTaskRow(task.id, task.agent, boundCall, ev.run || 0);
      if (workerRow && boundCall && ((workerRow._taskID && workerRow._taskID !== task.id) ||
          (workerRow._taskRun > 0 && workerRow._taskRun !== ev.run))) workerRow = null;
      if (workerRow) {
        clearInterval(workerRow._clock);
        workerRow._clock = null;
        if (task.id && !workerRows[task.id]) workerRows[task.id] = workerRow;
        // Recover a missed progress stream locally. A complete live trail
        // keeps its existing expandable DOM and the user's open details.
        if (savedActivity && workerActivityNeedsRecovery(workerRow, savedActivity)) workerRow._savedWorkerActivity = savedActivity;
        if (boundCall && !workerRow._taskRun) workerRow._taskRun = ev.run;
        renderTaskResult(workerRow, task, toolElapsed(workerRow, eventAt), workerRow._taskPrompt, task.status === "failed");
        return current;
      }
      // Unbound/legacy reports still belong in history, not on a newer row.
      addHistoryTask(task, "", savedActivity);
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
      releaseAssistantParagraphCaches(current);
      var elapsed = remoteRun && typeof syncRunElapsed === "function" ?
        syncRunElapsed(remoteRun, eventAt) : Date.now() - runStart;
      lastTurn = { ev: ev, elapsed: elapsed, tools: runToolCount, sessionID: activeSessionID };
      if (typeof rememberStatsLiveSpeed === "function") rememberStatsLiveSpeed(lastTurn, activeSessionID);
      addFileChanges(ev.file_changes);
      addTurnMeta(ev, elapsed);
      if (typeof resumeWorkerStatusSync === "function") resumeWorkerStatusSync();
      if (pendingImmediate || (!pauseQueue && promptQueue.length)) {
        setRunState("running", t("composer.sendingQueued"));
      } else {
        setRunState("idle", t("run.done") + " · " + fmtDuration(elapsed));
        notifyDone(elapsed);
      }
      return null;
    case "error":
      closeAssistantReasoning(current);
      flushAssistantRender(current);
      releaseAssistantParagraphCaches(current);
      addFileChanges(ev.file_changes);
      addEventLine(chatErrorText(ev), "error", "error");
      setRunState("idle", t("common.error"));
      return null;
    default:
      return current;
  }
}

function closeQuestionOverlay(id) {
  var ids = id ? [id] : Object.keys(questionOverlays);
  ids.forEach(function (key) {
    if (questionOverlays[key]) questionOverlays[key].remove();
    delete questionOverlays[key];
  });
  var remaining = Object.keys(questionOverlays);
  activeQuestionOverlay = remaining.length ? questionOverlays[remaining[remaining.length - 1]] : null;
}

function showQuestion(q) {
  closeQuestionOverlay(q.id);
  var overlay = el("div", "question-inline");
  var panel = el("form", "question-panel");
  panel.setAttribute("aria-label", q.header || t("question.title"));
  var head = el("div", "question-head");
  head.appendChild(el("span", "question-kicker", q.header || t("question.title")));
  var questionText = el("h2", "question-text");
  function refreshQuestionText() {
    questionText.textContent = q.confirmation && q.question_key === "hostPower.confirm" && t(q.question_key) !== q.question_key
      ? localizedHostPowerText(q.question_key, q.question_args || {}) : (q.question || "");
  }
  registerLanguageRefresh(questionText, refreshQuestionText);
  refreshQuestionText();
  head.appendChild(questionText);
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
      var copyBtn = i18nEl("button", "question-copy", "question.copy"); copyBtn.type = "button";
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
  var cancel = i18nEl("button", "btn", "question.cancel"); cancel.type = "button";
  var submit = i18nEl("button", "btn primary", "question.submit"); submit.type = "submit";
  actions.appendChild(cancel); actions.appendChild(submit); panel.appendChild(actions);
  overlay.appendChild(panel); appendStream(overlay);
  questionOverlays[q.id] = overlay; activeQuestionOverlay = overlay; smartScroll(true);

  function answer(cancelled) {
    var selected = Array.prototype.slice.call(panel.querySelectorAll('input[name="question-choice"]:checked')).map(function (x) { return x.value; });
    var own = custom.value.trim();
    if (!cancelled && !selected.length && !own) { error.textContent = t("question.pick"); return; }
    submit.disabled = true; cancel.disabled = true;
    jpost("/api/question/answer", { id: q.id, selected: selected, custom: own, cancelled: !!cancelled })
      .then(function () { closeQuestionOverlay(q.id); setRunState("running", t(activeQuestionOverlay ? "question.waiting" : "composer.working")); })
      .catch(function (e) { error.textContent = e.message; submit.disabled = false; cancel.disabled = false; });
  }
  panel.addEventListener("submit", function (e) { e.preventDefault(); answer(false); });
  cancel.addEventListener("click", function () { answer(true); });
  setTimeout(function () { var first = panel.querySelector("input"); if (first) first.focus(); }, 0);
}

/* Local commands run before chat/queue/interrupt handling and keep attachments. */
var localSlashSuggestions = null;
function runLocalSlashCommand(rawText) {
  var match = /^\/skills(?:\s+([\s\S]*))?$/i.exec(rawText.trim());
  if (!match) return false;
  promptEl.value = "";
  promptEl.style.height = "auto";
  composerDraftStore.clear(composerDraftStore.scope(), rawText);
  updateLocalSlashSuggestions();
  openSkills(match[1] || "");
  return true;
}
function completeSkillsSuggestion() {
  promptEl.value = "/skills ";
  promptEl.dispatchEvent(new Event("input", { bubbles: true }));
  promptEl.focus();
}
function updateLocalSlashSuggestions() {
  var value = promptEl.value.replace(/^\s+/, "").toLowerCase();
  var show = value.charAt(0) === "/" && "/skills".indexOf(value) === 0;
  if (!show) {
    if (localSlashSuggestions) localSlashSuggestions.hidden = true;
    return;
  }
  if (!localSlashSuggestions) {
    localSlashSuggestions = el("div", "attachment-list");
    localSlashSuggestions.id = "slash-suggestions";
    var button = el("button", "btn");
    button.type = "button";
    button.appendChild(el("code", "", "/skills — "));
    button.appendChild(i18nEl("span", "", "cmd.skills"));
    button.addEventListener("click", completeSkillsSuggestion);
    localSlashSuggestions.appendChild(button);
    promptEl.parentNode.insertBefore(localSlashSuggestions, promptEl);
  }
  localSlashSuggestions.hidden = false;
}

$("#composer").addEventListener("submit", async function (e) {
  e.preventDefault();
  if (attachmentUploadBusy()) { toast("Attachments are still being prepared/uploaded; wait before sending"); return; }
  var rawText = promptEl.value;
  if (runLocalSlashCommand(rawText)) return;
  var text = rawText.trim();
  if (!text && !pendingAttachments.length) return;
  var afterRun = streaming && runFinishing && !pendingImmediate;
  var attachments = pendingAttachments.slice();
  var draft = { scope: composerDraftStore.scope(), text: rawText };
  promptEl.value = "";
  promptEl.style.height = "auto";
  if (afterRun) {
    clearAttachments();
    sendAfterRun(text, attachments, draft);
    return;
  }
  if (streaming || queueDispatching) {
    if (await enqueuePrompt(text, attachments)) {
      if (attachments.length) clearAttachments();
      composerDraftStore.clear(draft.scope, draft.text);
    } else composerDraftStore.restore(draft.scope);
    return;
  }
  clearAttachments();
  sendPrompt(text, attachments, draft);
});
$("#stop-run-btn").addEventListener("click", function () {
  pauseQueue = true;
  runStopRevision++;
  queueAutoDrain = null;
  restoreUnsentAfterRun(pendingImmediate || queueDispatchItem);
  pendingImmediate = null;
  finishRunPresentation();
  setRunState("idle", t("composer.stopped"));
  if (abortCtl) abortCtl.abort();
});
$("#interrupt-btn").addEventListener("click", function () {
  if (attachmentUploadBusy()) { toast("Attachments are still being prepared/uploaded; wait before sending"); return; }
  var rawText = promptEl.value;
  if (runLocalSlashCommand(rawText)) return;
  if (pendingImmediate || queueDispatching || queueDispatchItem) return;
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
$("#attachment-preview-dialog").addEventListener("close", clearAttachmentPreviewContent);
$("#attachment-preview-dialog").addEventListener("click", function (event) {
  if (event.target === this) closeAttachmentPreview();
});
promptEl.addEventListener("keydown", function (e) {
  if (e.key === "Tab" && localSlashSuggestions && !localSlashSuggestions.hidden) {
    e.preventDefault();
    completeSkillsSuggestion();
    return;
  }
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    $("#composer").requestSubmit();
  }
});
promptEl.addEventListener("input", function () {
  updateLocalSlashSuggestions();
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
  queueAutoDrain = null;
  // A running conversation keeps going in the background.
  if (streaming && !detachActiveRun()) { toast(t("session.switchWait")); return; }
  if (queueDispatching) return;
  clearAttachments();
  if (transcriptAbortCtl) transcriptAbortCtl.abort();
  transcriptAbortCtl = null;
  loadedTranscriptMessages = [];
  transcriptHasMore = false;
  transcriptBeforeSeq = 0;
  transcriptSessionID = "";
  activeSessionID = "";
  if (typeof syncSelectSession === "function") syncSelectSession("");
  stream.innerHTML = "";
  toolRows = {}; workerRows = {}; openToolOrder = [];
  resetWorkerOverview();
  lastTurn = null; workersSeen = [];
  showWelcome();
  setRunState("idle", t("composer.ready"));
  composerDraftStore.restore();
  loadSessions();
  renderStats();
  promptEl.focus();
}
$("#new-session").addEventListener("click", newSession);
