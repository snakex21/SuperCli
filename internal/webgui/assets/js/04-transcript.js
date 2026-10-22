"use strict";

/* ═══ transcript ═══ */

var stage = $("#stage"), stream = $("#stream"), welcome = $("#welcome");
var streamAppendTarget = null;
// Off-screen transcript blocks use content-visibility:auto for long-chat
// performance. Chromium/WebView2 can wrongly cull a freshly changing flex
// child when later tool rows change the tail geometry, making streamed prose
// appear to be replaced by the tool call. Keep only this turn's new blocks
// fully painted; old history remains virtualized.
var transcriptLiveAppend = false;
var transcriptFollowTail = true;
var smartScrollFrame = null;
var smartScrollForced = false;
var smartScrollPending = false;
var transcriptResumeScroll = null, transcriptResumeScrollVersion = 0;
var initialTranscriptTail = null, initialTranscriptTailVersion = 0;

function cancelInitialTranscriptTail() {
  ++initialTranscriptTailVersion;
  if (initialTranscriptTail && initialTranscriptTail.frame !== null) cancelAnimationFrame(initialTranscriptTail.frame);
  initialTranscriptTail = null;
}

// Only a newly opened latest page needs this short reveal/layout handshake.
// Reading the scroller's geometry keeps off-screen blocks virtualized; never
// measure every row or keep following after the initial geometry has settled.
function settleInitialTranscriptTail() {
  cancelInitialTranscriptTail();
  if (savedTranscriptScroll()) { smartScroll(true); return; }
  if (document.hidden || !stream.lastElementChild) return;
  var state = initialTranscriptTail = {
    version: initialTranscriptTailVersion, sessionID: activeSessionID,
    epoch: typeof projectEpoch === "number" ? projectEpoch : 0,
    resumeSeq: typeof sessionResumeSeq === "number" ? sessionResumeSeq : 0,
    tail: stream.lastElementChild, frame: null, frames: 0, height: -1, stable: 0
  };
  transcriptFollowTail = true;
  function reveal() {
    if (initialTranscriptTail !== state || state.version !== initialTranscriptTailVersion) return;
    state.frame = null;
    if (state.sessionID !== activeSessionID ||
        state.epoch !== (typeof projectEpoch === "number" ? projectEpoch : 0) ||
        state.resumeSeq !== (typeof sessionResumeSeq === "number" ? sessionResumeSeq : 0) ||
        state.tail !== stream.lastElementChild || !state.tail.isConnected ||
        document.hidden || savedTranscriptScroll() || streamAppendTarget) {
      cancelInitialTranscriptTail();
      return;
    }
    var height = stage.scrollHeight;
    var gap = height - stage.clientHeight - stage.scrollTop;
    state.stable = height === state.height && Math.abs(gap) <= 1 ? state.stable + 1 : 0;
    state.height = height;
    stage.scrollTop = height;
    // Three stable frames allow content-visibility to replace its estimates.
    // The hard cap bounds even unusual asynchronous layout; no token observer.
    if (++state.frames >= 16 || state.stable >= 3) {
      cancelInitialTranscriptTail();
      transcriptFollowTail = nearBottom();
      return;
    }
    state.frame = requestAnimationFrame(reveal);
  }
  state.frame = requestAnimationFrame(reveal);
}

function savedTranscriptScroll() {
  if (transcriptResumeScroll && transcriptResumeScroll.sessionID !== activeSessionID) transcriptResumeScroll = null;
  return transcriptResumeScroll;
}
function captureTranscriptScroll() {
  cancelInitialTranscriptTail();
  // visibilitychange and pagehide can both fire; never replace the original intent
  // with geometry from a hidden viewport or a partially rebuilt transcript.
  if (!savedTranscriptScroll()) transcriptResumeScroll = {
    sessionID: activeSessionID, followTail: transcriptFollowTail, top: stage.scrollTop
  };
  transcriptResumeScroll.resuming = false;
  ++transcriptResumeScrollVersion;
}
function resumeTranscriptScroll() {
  cancelInitialTranscriptTail();
  var saved = savedTranscriptScroll();
  if (!saved || document.hidden) return;
  saved.resuming = true;
  transcriptFollowTail = saved.followTail;
  ++transcriptResumeScrollVersion;
}
function settleTranscriptScroll(ready) {
  var saved = savedTranscriptScroll();
  if (!saved || !saved.resuming || document.hidden) return;
  cancelInitialTranscriptTail();
  var version = ++transcriptResumeScrollVersion;
  function restore(final) {
    if (version !== transcriptResumeScrollVersion || savedTranscriptScroll() !== saved || document.hidden || !ready() || streamAppendTarget) return;
    transcriptFollowTail = saved.followTail;
    stage.scrollTop = saved.followTail ? stage.scrollHeight : saved.top;
    if (final) transcriptResumeScroll = null;
    else requestAnimationFrame(function () { restore(true); });
  }
  // Keep layout-generated scroll events from cancelling following while the
  // refreshed history, replay and viewport changes reach their painted geometry.
  requestAnimationFrame(function () { restore(false); });
}
function cancelTranscriptResumeScroll() {
  cancelInitialTranscriptTail();
  if (!document.hidden && savedTranscriptScroll()) {
    transcriptResumeScroll = null;
    ++transcriptResumeScrollVersion;
    transcriptFollowTail = nearBottom();
    smartScrollPending = smartScrollForced = false;
  }
}

function thinkingDisclosureOpen(history) {
  return typeof ui !== "undefined" && typeof ui.thinkingExpanded === "boolean" ? ui.thinkingExpanded : !history;
}
function toolDisclosureOpen() { return typeof ui !== "undefined" && ui.toolsExpanded === true; }
function saveDisclosurePreference(kind, open) {
  if (typeof ui === "undefined") return;
  ui[kind === "tools" ? "toolsExpanded" : "thinkingExpanded"] = open;
  saveUI();
}

stream.addEventListener("click", function (event) {
  var target = event.target;
  var button = target && target.closest && target.closest(".code-copy");
  if (button && stream.contains(button)) {
    event.preventDefault();
    copyCodeBlock(button);
    return;
  }
  if (event.defaultPrevented || !target || !target.closest) return;
  var summary = target.closest("summary"), row = summary && summary.parentNode;
  if (!row || !stream.contains(row) || !row.classList ||
      target.closest("a,button,input,select,textarea,label")) return;
  if (row.classList.contains("tool-row")) saveDisclosurePreference("tools", !row.open);
  else if (row.classList.contains("think-block")) saveDisclosurePreference("thinking", !row.open);
});

function appendStream(node) {
  if (transcriptLiveAppend && node && node.classList) node.classList.add("transcript-live");
  (streamAppendTarget || stream).appendChild(node);
}

function releaseLiveTranscriptBlocks() {
  transcriptLiveAppend = false;
  stream.querySelectorAll(".transcript-live").forEach(function (node) {
    node.classList.remove("transcript-live");
  });
}

function nearBottom() { return stage.scrollHeight - stage.scrollTop - stage.clientHeight < 120; }
stage.addEventListener("scroll", function () {
  if (!initialTranscriptTail && !savedTranscriptScroll()) transcriptFollowTail = nearBottom();
}, { passive: true });
["wheel", "touchstart", "pointerdown", "keydown"].forEach(function (name) {
  stage.addEventListener(name, cancelTranscriptResumeScroll, { passive: true });
});
// Keyboard scrolling may target the document rather than the scroller. Typing
// in the composer is not scroll intent. Pointerdown above also covers dragging
// the stage's native scrollbar, before its first scroll event.
document.addEventListener("keydown", function (event) {
  if (["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " "].indexOf(event.key) < 0) return;
  if (event.target && event.target.closest && event.target.closest("input,textarea,select,[contenteditable]")) return;
  cancelInitialTranscriptTail();
}, { passive: true });
function flushSmartScroll() {
  if (smartScrollFrame !== null) {
    cancelAnimationFrame(smartScrollFrame);
    smartScrollFrame = null;
  }
  if (!smartScrollPending) return;
  // Read geometry after the frame's text writes. Reading before a paced paint
  // followed the preceding line height and made the transcript jump next frame.
  var saved = savedTranscriptScroll();
  if (!streamAppendTarget && (!saved || saved.followTail) && (smartScrollForced || transcriptFollowTail)) stage.scrollTop = stage.scrollHeight;
  smartScrollPending = false;
  smartScrollForced = false;
}
function smartScroll(force) {
  if (streamAppendTarget) return;
  var saved = savedTranscriptScroll();
  if (saved) {
    if (!saved.followTail) return;
    force = true;
  }
  if (force) {
    transcriptFollowTail = true;
    smartScrollForced = true;
  }
  if (!transcriptFollowTail && !smartScrollForced) return;
  smartScrollPending = true;
  if (smartScrollFrame !== null) return;
  // Batch tool/SSE bursts, and let a pending paced paint own the scroll read.
  smartScrollFrame = requestAnimationFrame(flushSmartScroll);
}
function hideWelcome() { if (welcome) welcome.style.display = "none"; }
function showWelcome() { if (welcome) welcome.style.display = ""; }

function addUserMsg(text, seq, attachments, messageID, sessionID) {
  hideWelcome();
  attachments = (attachments || []).slice();
  var m = el("div", "msg-user");
  // Rewind puts these files back in the composer together with the text.
  m._attachments = attachments;
  var displayText = userMessageDisplayText(text, attachments);
  if (displayText) m.appendChild(el("div", "msg-user-text", displayText));
  renderSentAttachments(m, attachments);
  if (seq && messageID) addMessageRewind(m, seq, text, messageID, sessionID);
  appendStream(m);
  smartScroll(true);
  return m;
}

function addMessageRewind(node, seq, text, messageID, sessionID) {
  var ownerSessionID = sessionID || activeSessionID;
  messageID = String(messageID || "");
  if (!node || !seq || !ownerSessionID || !/^[1-9][0-9]*$/.test(messageID) || node.querySelector(".msg-rewind")) return;
  var button = el("button", "msg-rewind", "↶ " + t("workflow.rewind"));
  button.type = "button";
  button.title = t("workflow.rewindHint");
  button.setAttribute("aria-label", t("workflow.rewind"));
  button.addEventListener("click", function (event) {
    event.preventDefault();
    event.stopPropagation();
    showRewindDialog(ownerSessionID, seq, text, button, messageID, (node._attachments || []).slice());
  });
  node.appendChild(button);
}

async function showRewindDialog(sessionID, seq, text, trigger, messageID, attachments) {
  if (trigger) trigger.disabled = true;
  var preview = null;
  try {
    preview = await j("/api/checkpoint/rewind?session=" + encodeURIComponent(sessionID) + "&from_seq=" + seq + "&message_id=" + encodeURIComponent(messageID));
  } catch (e) {}
  if (trigger) trigger.disabled = false;
  var overlay = el("div", "question-overlay rewind-dialog");
  var panel = el("div", "question-panel compact");
  panel.appendChild(i18nEl("div", "question-kicker", "workflow.rewindWhy"));
  panel.appendChild(i18nEl("div", "question-option-desc", "workflow.rewindWhyHint"));
	panel.appendChild(i18nEl("div", "question-option-desc rewind-warning", "workflow.rewindPermanent"));
  var input = el("textarea", "question-custom");
  input.rows = 3;
  input.maxLength = 400;
  input.placeholder = t("workflow.rewindReason");
  panel.appendChild(input);
  var rewindFiles = null;
  if (preview && preview.available && (preview.files || []).length) {
    var fileOption = el("label", "question-option rewind-files");
    rewindFiles = document.createElement("input");
    rewindFiles.type = "checkbox";
		rewindFiles.checked = true;
    fileOption.appendChild(rewindFiles);
    var fileCopy = el("span", "question-option-copy");
    fileCopy.appendChild(i18nEl("strong", "", "workflow.rewindFiles"));
    fileCopy.appendChild(el("span", "question-option-desc",
      t("workflow.rewindFilesHint").replace("{c}", fmtInteger(preview.checkpoints || 0)).replace("{n}", fmtInteger((preview.files || []).length))));
    fileOption.appendChild(fileCopy);
    panel.appendChild(fileOption);
  }
  var actions = el("div", "question-actions");
  var cancel = i18nEl("button", "btn", "common.cancel");
  var confirm = i18nEl("button", "btn primary", "workflow.rewindContinue");
  cancel.type = confirm.type = "button";
  function close() { overlay.remove(); }
  cancel.addEventListener("click", close);
  confirm.addEventListener("click", async function () {
    confirm.disabled = true;
    cancel.disabled = true;
    var ok = await rewindSession(sessionID, seq, text, input.value.trim(), !!(rewindFiles && rewindFiles.checked), trigger, messageID, attachments);
    if (ok) close();
    else { confirm.disabled = false; cancel.disabled = false; }
  });
  actions.appendChild(cancel);
  actions.appendChild(confirm);
  panel.appendChild(actions);
  overlay.appendChild(panel);
  document.body.appendChild(overlay);
  input.focus();
}

function bindLiveMessageReceipt(node, event) {
  if (!node || !event || event.type !== "session_activity" || !event.session_id || event.user_seq <= 0 ||
      !/^[1-9][0-9]*$/.test(String(event.user_message_id || ""))) return;
  node._userReceipt = {sessionID: event.session_id, seq: event.user_seq, messageID: String(event.user_message_id)};
}

async function addLatestMessageRewind(node, text) {
  // This receipt belongs to this live invocation, captured from its successful
  // session_activity. Text matching or latest(seq) cannot establish ownership.
  var receipt = node && node._userReceipt;
  if (!node || !node.isConnected || !receipt) return 0;
  addMessageRewind(node, receipt.seq, text, receipt.messageID, receipt.sessionID);
  return receipt.seq;
}
function addAssistantMsg() {
  var m = el("div", "msg-assistant");
  m._raw = "";
  m._renderTimer = null;
  m._reasoningOpen = false;
  appendStream(m);
  return m;
}
// A blank line outside a fence, or a complete fence boundary, cannot be
// changed by later Markdown. Keep those DOM nodes and only repaint the tail.
function stableMarkdownEnd(text) {
  var tokens = /```|\n[^\S\n]*\n/g, match, fenced = false, end = 0;
  while ((match = tokens.exec(text)) !== null) {
    if (match[0] === "```") {
      if (fenced) end = tokens.lastIndex;
      else end = match.index;
      fenced = !fenced;
    } else if (!fenced) end = tokens.lastIndex;
  }
  return end;
}

function markdownStream(target) {
  // Comments delimit a section without introducing elements that would alter
  // p:last-child, margins, selection containers, or the final DOM hierarchy.
  var start = document.createComment("markdown-start");
  var end = document.createComment("markdown-end");
  target.appendChild(start);
  target.appendChild(end);
  return {start: start, end: end, source: "", committed: 0, tailNodes: [], lineBlock: null, code: null, codeText: ""};
}

function appendMarkdownHTML(state, html) {
  var template = document.createElement("template");
  template.innerHTML = html;
  var nodes = Array.from(template.content.childNodes);
  state.end.parentNode.insertBefore(template.content, state.end);
  return nodes;
}

function clearMarkdownTail(state) {
  state.tailNodes.forEach(function (node) { node.remove(); });
  state.tailNodes = [];
  state.lineBlock = null;
  state.paragraph = null;
  state.code = null;
  state.codeText = "";
  state.codeLang = "";
}

function removeMarkdownSection(state) {
  var node = state.start;
  while (node) {
    var next = node.nextSibling;
    node.remove();
    if (node === state.end) break;
    node = next;
  }
}


// Keep the unfinished paragraph in place. Ordinary prose updates its existing
// text node; formatted prose uses the existing renderer and parses only a new HTML
// suffix when the already rendered inline markup remains unchanged.
function updateMarkdownParagraph(state, text) {
  // HTML normalizes CR and NUL; let its parser handle these rare inputs.
  var value = text.trim(), plain = text.indexOf("\n") < 0 &&
    !/[\r\x00\x60*_~\[\]]/.test(value) &&
    !/^(?:#{1,6}\s|>(?:\s|$)|[-+]\s|\d+[.)]\s|-{3,})/.test(value);
  // A plain single-line suffix preserves completed inline markup. Delimiters,
  // block starts and end-sensitive italic markers keep the full-render path.
  var body, block = state.paragraph;
  // A completed paragraph keeps its DOM, but needs its descriptor again only
  // if this same section receives more source or a recovery update.
  if (!block && state.tailNodes.length === 1) {
    var existing = state.tailNodes[0], first = existing.firstChild;
    if (existing.nodeName === "P" && existing.parentNode === state.end.parentNode) {
      var textNode = first && first.nodeType === 3 && !first.nextSibling ? first : null;
      block = state.paragraph = {node: existing, text: textNode,
        html: textNode ? null : existing.innerHTML, source: state.source.slice(state.committed).trim()};
    }
  }
  var suffix = block && block.source ? value.slice(block.source.length) : "";
  var extendsInline = !plain && block && block.html !== null && block.source &&
    value.indexOf(block.source) === 0 && text.indexOf("\n") < 0 &&
    text.indexOf("\r") < 0 && text.indexOf("\x00") < 0 &&
    !/[\r\x00\x60*_~\[\]()]/.test(suffix) && !/[*_]$/.test(block.source) &&
    !/^(?:#{1,6}\s|>(?:\s|$)|[-*+]\s|\d+[.)]\s|-{3,})/.test(value);
  if (extendsInline) body = block.html + escHtml(suffix);
  else if (!plain) {
    var html = renderMarkdownish(text);
    if (html.indexOf("<p>") !== 0 || html.indexOf("</p>") !== html.length - 4) return false;
    body = html.slice(3, -4);
  } else if (!value) return false;
  if (!block) {
    clearMarkdownTail(state);
    var root = el("p");
    state.end.parentNode.insertBefore(root, state.end);
    state.tailNodes = [root];
    block = state.paragraph = {node: root, text: null, html: ""};
  }
  if (plain) {
    if (!block.text) {
      block.node.textContent = "";
      block.text = document.createTextNode("");
      block.node.appendChild(block.text);
    }
    if (value !== block.text.data) block.text.data = value;
    block.html = null;
  } else {
    var previous = block.html === null ? escHtml(block.text.data) : block.html;
    if (body !== previous) {
      if (body.indexOf(previous) === 0) {
        var template = document.createElement("template");
        template.innerHTML = body.slice(previous.length);
        var fragment = template.content;
        var first = fragment.firstChild, last = block.node.lastChild;
        if (first && last && first.nodeType === 3 && last.nodeType === 3) {
          last.data += first.data;
          first.remove();
        }
        block.node.appendChild(fragment);
      } else block.node.innerHTML = body;
    }
    block.text = null;
    block.html = body;
  }
  block.source = value;
  return true;
}

// A complete table/list row cannot change when another row arrives. Retain
// those nodes and parse only the open row, falling back for mixed block syntax.
function updateMarkdownLineBlock(state, text) {
  var firstEnd = text.indexOf("\n"), first = (firstEnd < 0 ? text : text.slice(0, firstEnd)).trim();
  var kind, header = "", start = 0, align, width, listPattern;
  var secondEnd = firstEnd < 0 ? -1 : text.indexOf("\n", firstEnd + 1);
  if (secondEnd >= 0 && first.charAt(0) === "|" &&
      /^\|[\s\-:|]+\|$/.test(text.slice(firstEnd + 1, secondEnd).trim())) {
    kind = "table";
    start = secondEnd + 1;
    header = text.slice(0, start);
    align = markdownTableAlign(text.slice(firstEnd + 1, secondEnd).trim());
    width = markdownTableCells(first).length;
  } else if (/^[-*+]\s+(.+)/.test(first)) {
    kind = "ul"; listPattern = /^[-*+]\s+(.+)/;
  } else if (/^\d+[.)]\s+(.+)/.test(first)) {
    kind = "ol"; listPattern = /^\d+[.)]\s+(.+)/;
  } else return false;
  var block = state.lineBlock;
  var same = block && block.kind === kind && block.header === header && text.indexOf(block.source) === 0;
  var lines = text.slice(same ? block.complete : start).split("\n"), pending = lines.pop();
  function valid(line) {
    line = line.trim();
    return kind === "table" ? line.indexOf("|") >= 0 : listPattern.test(line);
  }
  // A partial line can still become a paragraph, a different list, or a fence.
  // Check before touching retained DOM so the original renderer stays authoritative.
  if (!lines.every(valid)) return false;
  if (!same) {
    clearMarkdownTail(state);
    var root = el(kind === "table" ? "div" : kind, kind === "table" ? "md-table-wrap" : "");
    var parent = root;
    if (kind === "table") {
      var table = el("table"), head = el("thead");
      head.innerHTML = '<tr>' + markdownTableCellHTML(first, align, width, true) + '</tr>';
      table.appendChild(head);
      parent = el("tbody"); table.appendChild(parent); root.appendChild(table);
    }
    state.end.parentNode.insertBefore(root, state.end);
    state.tailNodes = [root];
    block = state.lineBlock = {kind: kind, header: header, source: "", complete: start,
      parent: parent, pending: null, pendingHTML: null, after: [], afterHTML: ""};
  }
  function row(line) {
    var html = kind === "table" ? markdownTableCellHTML(line.trim(), align, width, false) :
      mdInline(escHtml(line.trim().match(listPattern)[1]));
    if (!block.pending) block.pending = el(kind === "table" ? "tr" : "li");
    if (html !== block.pendingHTML) { block.pending.innerHTML = html; block.pendingHTML = html; }
    return block.pending;
  }
  var added = document.createDocumentFragment();
  lines.forEach(function (line) {
    var node = row(line);
    if (node.parentNode !== block.parent) added.appendChild(node);
    block.pending = null; block.pendingHTML = null;
    block.complete += line.length + 1;
  });
  block.parent.appendChild(added);
  // While a new marker is arriving (e.g. "- " without its item yet), the
  // full renderer shows a small paragraph after the finished list. Keep that
  // temporary block separate instead of rebuilding every completed item.
  var pendingValid = valid(pending);
  var afterHTML = pendingValid ? "" : renderMarkdownish(pending);
  if (afterHTML !== block.afterHTML) {
    block.after.forEach(function (node) { node.remove(); });
    block.after = afterHTML ? appendMarkdownHTML(state, afterHTML) : [];
    block.afterHTML = afterHTML;
    state.tailNodes = [state.tailNodes[0]].concat(block.after);
  }
  if (pendingValid) {
    var node = row(pending);
    if (node.parentNode !== block.parent) block.parent.appendChild(node);
  }
  block.source = text;
  return true;
}

function updateMarkdownStream(state, text) {
  if (text === state.source) return;
  // Empty comments are removed by a multiline regex before block parsing.
  // Future whitespace (including CR/LS/PS) can change how much it consumes,
  // even after a blank line. Use the original full-block rendering for this
  // uncommon case instead of guessing a permanently stable boundary.
  var fullRender = /<!--\s*-->/.test(text);
  if (fullRender || state.fullRender || text.indexOf(state.source) !== 0) {
    // Split protocol tags, a replaced recovery snapshot, or trimmed reasoning
    // can revise the unfinished section. Never append to a stale prefix.
    while (state.start.nextSibling !== state.end) state.start.nextSibling.remove();
    state.source = "";
    state.committed = 0;
    state.tailNodes = [];
    state.lineBlock = null;
    state.paragraph = null;
    state.code = null;
    state.codeText = "";
    state.codeLang = "";
  }
  state.fullRender = fullRender;
  if (fullRender) {
    state.tailNodes = appendMarkdownHTML(state, renderMarkdownish(text));
    state.source = text;
    return;
  }
  var tail = text.slice(state.committed);
  var end = stableMarkdownEnd(tail);
  if (end) {
    clearMarkdownTail(state);
    appendMarkdownHTML(state, renderMarkdownish(tail.slice(0, end)));
    state.committed += end;
    tail = tail.slice(end);
  }
  // Native reasoning closes with a newline before prose. Completed leading
  // blank lines are inert Markdown; skip them so list/table/code fast paths
  // also apply to an answer immediately following the thought.
  var leading = /^(?:[^\S\n]*\n)+/.exec(tail);
  if (leading) { state.committed += leading[0].length; tail = tail.slice(leading[0].length); }
  if (updateMarkdownLineBlock(state, tail)) { state.source = text; return; }
  // A large unfinished fenced code block is plain text. Append its new bytes
  // without rebuilding the pre/code elements on every display frame.
  var header = /^```([^\n]*)\n/.exec(tail);
  if (header && tail.indexOf("```", 3) < 0) {
    var code = tail.slice(header[1] ? header[0].length : 3), trim = code.length;
    while (trim && /\s/.test(code.charAt(trim - 1))) trim--;
    code = code.slice(0, trim);
    var lang = header[1].trim();
    if (!state.code || state.codeLang !== lang) {
      clearMarkdownTail(state);
      var pre = el("pre");
      pre.dataset.lang = lang;
      pre.innerHTML = codeCopyButtonHTML();
      var codeNode = el("code");
      state.code = document.createTextNode("");
      codeNode.appendChild(state.code);
      pre.appendChild(codeNode);
      state.end.parentNode.insertBefore(pre, state.end);
      state.tailNodes = [pre];
      state.codeText = "";
      state.codeLang = lang;
    }
    if (code.indexOf(state.codeText) === 0) state.code.appendData(code.slice(state.codeText.length));
    else state.code.data = code;
    state.codeText = code;
  } else if (!updateMarkdownParagraph(state, tail)) {
    clearMarkdownTail(state);
    state.tailNodes = appendMarkdownHTML(state, renderMarkdownish(tail));
  }
  state.source = text;
}

function assistantPart(part, history) {
  var container, target;
  if (part.kind === "thinking") {
    // There is at most one reasoning block per assistant segment, matching
    // renderText. A folded thought does not need a Markdown DOM until opened.
    var template = document.createElement("template");
    _thinkId = 0;
    template.innerHTML = renderThinkBlock("");
    container = template.content.firstElementChild;
    target = container.querySelector(".think-content");
    container.open = thinkingDisclosureOpen(history);
  } else {
    container = target = document.createDocumentFragment();
  }
  var state = {kind: part.kind, node: container, text: part.text, markdown: null, complete: !!history};
  function paint() {
    if (state.kind === "thinking" && !container.open) return;
    if (!state.markdown) state.markdown = markdownStream(target);
    updateMarkdownStream(state.markdown, state.text);
    if (state.complete) state.markdown.paragraph = null;
  }
  function reveal() { if (container.open) paint(); }
  state.paint = paint;
  state.remove = function () {
    if (state.kind === "thinking") {
      container.removeEventListener("toggle", reveal);
      container.remove();
    }
    else removeMarkdownSection(state.markdown);
  };
  if (part.kind === "thinking") container.addEventListener("toggle", reveal);
  paint();
  return state;
}

// Checkpoints belong to one active node. A direct replacement or recovery
// snapshot uses the full parser; only our exact append helper advances source.
function assistantPartsForNode(node) {
  var source = String(node._raw || "");
  if (node._history || node._sealed) {
    node._partsCache = null;
    return assistantTextParts(source);
  }
  var checkpoint = node._partsCache;
  if (!checkpoint || checkpoint.source !== source) {
    checkpoint = node._partsCache = {source: source, offset: 0, parts: [], renderedThinking: false};
  }
  return assistantTextParts(source, checkpoint);
}

function appendAssistantSource(node, text) {
  var checkpoint = node._partsCache, previous = node._raw;
  node._raw += text;
  if (checkpoint && checkpoint.source === previous) checkpoint.source = node._raw;
  else node._partsCache = null;
}

function renderAssistant(node) {
  // The original source is authoritative. A renderer failure must not stop
  // SSE delivery, and recovery may replace a source rather than append to it.
  try {
    var parts = node._displayParts || assistantPartsForNode(node);
    if (!node._assistantParts) {
      node.textContent = "";
      node._assistantParts = [];
    }
    var states = node._assistantParts;
    for (var i = 0; i < parts.length; i++) {
      var part = parts[i], state = states[i];
      // Usage-only or delayed native reasoning can arrive after visible prose.
      // Insert its leading block without rebuilding the unchanged answer DOM.
      if (i === 0 && part.kind === "thinking" && parts.length === 2 && states.length === 1 &&
          state && state.kind === "markdown" && state.text === parts[1].text && state.markdown &&
          state.markdown.start && state.markdown.start.parentNode === node) {
        state = assistantPart(part, !!node._history);
        node.insertBefore(state.node, states[0].markdown.start);
        states.unshift(state);
      }
      if (!state || state.kind !== part.kind) {
        while (states.length > i) states.pop().remove();
        state = assistantPart(part, !!node._history);
        states.push(state);
        node.appendChild(state.node);
      } else {
        state.text = part.text;
        // Unopened thoughts keep their latest text without materializing DOM.
        if (state.markdown) state.paint();
      }
    }
    while (states.length > parts.length) states.pop().remove();
    autoCollapseThinking(node);
    // Only a complete paint may satisfy repeated done/EOF/seal flushes.
    node._renderedSource = node._displayParts ? null : node._raw;
    if (node._history || node._sealed) releaseAssistantParagraphCaches(node);
  } catch (renderErr) {
    node._renderedSource = null;
    node.textContent = node._raw;
    node._assistantParts = null;
    node._displayParts = null;
    node._pacedParts = null;
    node._partsCache = null;
    if (window.console && console.error) console.error("renderAssistant", renderErr);
  }
}

// Providers can send reasoning and prose in irregular packets. Pace only the
// received text of each section, retaining the complete raw transcript. A new
// answer section paints immediately even if its thought has pending display
// frames. Small packets use recent arrival gaps (at most 320 ms plus a paint);
// fast streams need only a few frames. Large/expensive updates drain directly.
function paintAssistant(node, now, withinFrame) {
  var start = performance.now();
  renderAssistant(node);
  node._renderCost = performance.now() - start;
  node._lastPaintAt = now;
  if (withinFrame) { smartScrollPending = true; flushSmartScroll(); }
  else smartScroll();
}

function assistantPrefixEnd(text, end) {
  // Parts already exclude reasoning tags. Keep UTF-16 pairs intact.
  if (end > 0 && end < text.length && /[\uD800-\uDBFF]/.test(text.charAt(end - 1))) end++;
  return Math.min(end, text.length);
}

function paceAssistantPart(state, source, now, expensive) {
  if (source === state.source) return;
  var gap = now - state.lastAt;
  state.lastAt = now;
  // A long upstream pause is not the resumed stream's cadence.
  if (gap > 600) state.gaps.length = 0;
  else if (gap >= 1) {
    state.gaps.push(gap);
    if (state.gaps.length > 6) state.gaps.shift();
  }
  state.source = source;
  if (expensive || source.length - state.text.length >= 2048) {
    state.text = source;
    state.queue.length = 0;
    return;
  }
  var cadence = state.gaps.length ? Math.max.apply(null, state.gaps) : 40;
  var until = now + Math.max(24, Math.min(320, cadence + Math.max(8, cadence * .08)));
  if (!state.queue.length) { state.from = state.text.length; state.at = now; }
  var last = state.queue[state.queue.length - 1];
  if (last && until <= last.until) last.length = source.length;
  else state.queue.push({length: source.length, until: until});
}

function queueAssistantPaint(node) {
  if (node._renderTimer != null) return;
  if (smartScrollFrame !== null) {
    cancelAnimationFrame(smartScrollFrame);
    smartScrollFrame = null;
  }
  node._renderTimer = requestAnimationFrame(function () {
    node._renderTimer = null;
    if (node.isConnected === false) { node._pacedParts = null; node._displayParts = null; node._partsCache = null; flushSmartScroll(); return; }
    var now = performance.now(), states = node._pacedParts || [], changed = false;
    if (!states.some(function (state) { return state.queue.length; })) { flushSmartScroll(); return; }
    // Native rAF follows the display refresh rate. Let cheap formatting use every
    // frame; budget its measured work to roughly 1/8 of the interval, capped at
    // the previous 16 ms cadence for moderately expensive updates.
    var paintInterval = Math.min(16, (node._renderCost || 0) * 8);
    if (now - node._lastPaintAt < paintInterval) { flushSmartScroll(); queueAssistantPaint(node); return; }
    states.forEach(function (state) {
      if (!state.queue.length) return;
      var end = state.source.length;
      if (node._renderCost > 6 || end - state.text.length >= 2048) state.queue.length = 0;
      else {
        while (state.queue.length && state.queue[0].until <= now) {
          var finished = state.queue.shift();
          state.from = finished.length;
          state.at = finished.until;
        }
        if (state.queue.length) {
          var progress = Math.max(0, (now - state.at) / (state.queue[0].until - state.at));
          end = assistantPrefixEnd(state.source, Math.ceil(state.from + (state.queue[0].length - state.from) * progress));
        }
      }
      if (end > state.text.length) { state.text = state.source.slice(0, end); changed = true; }
    });
    if (changed) paintAssistant(node, now, true);
    else flushSmartScroll();
    if ((node._pacedParts || []).some(function (state) { return state.queue.length; })) queueAssistantPaint(node);
  });
}

function scheduleAssistantRender(node) {
  if (!node) return;
  var now = performance.now(), parts = assistantPartsForNode(node), changed = false;
  var states = node._pacedParts || (node._pacedParts = []);
  if (states.length !== parts.length) changed = true;
  parts.forEach(function (part, index) {
    var state = states[index];
    if (!state || state.kind !== part.kind || (part.text !== state.source && part.text.indexOf(state.source) !== 0)) {
      states[index] = {kind: part.kind, source: part.text, text: part.text, queue: [], gaps: [], lastAt: now};
      changed = true;
    } else {
      var displayed = state.text;
      paceAssistantPart(state, part.text, now, node._renderCost > 6);
      if (displayed !== state.text) changed = true;
    }
  });
  states.length = parts.length;
  // A new section is a semantic boundary: finish the preceding section before
  // showing it. Otherwise delayed thought characters keep moving the answer
  // after it has already appeared, despite the provider having finished thinking.
  states.slice(0, -1).forEach(function (state) {
    if (!state.queue.length) return;
    state.text = state.source;
    state.queue.length = 0;
    changed = true;
  });
  node._displayParts = states;
  if (changed) { node._firstPainted = true; paintAssistant(node, now); }
  if ((node._pacedParts || []).some(function (state) { return state.queue.length; })) queueAssistantPaint(node);
  else if (node._renderTimer != null) { cancelAnimationFrame(node._renderTimer); node._renderTimer = null; }
}

function flushAssistantRender(node) {
  if (!node) return;
  var alreadyRendered = !node._displayParts && !node._pacedParts &&
    node._renderedSource != null && node._renderedSource === node._raw;
  if (node._renderTimer != null) { cancelAnimationFrame(node._renderTimer); node._renderTimer = null; }
  node._pacedParts = null;
  node._displayParts = null;
  if (alreadyRendered) smartScroll();
  else paintAssistant(node, performance.now());
  node._partsCache = null;
}

function releaseAssistantParagraphCaches(node) {
  if (!node) return;
  (node._assistantParts || []).forEach(function (part) {
    part.complete = true;
    if (part.markdown) part.markdown.paragraph = null;
  });
}

// Without an explicit Shift+T/click preference, a live thought folds once it
// is finished: the answer started after it or the segment was sealed.
function autoCollapseThinking(node) {
  if (typeof ui !== "undefined" && typeof ui.thinkingExpanded === "boolean") return;
  var states = node._assistantParts || [];
  states.forEach(function (state, i) {
    if (state.kind !== "thinking" || state.autoClosed) return;
    if (!node._sealed && !node._history && i === states.length - 1) return;
    state.autoClosed = true;
    state.node.open = false;
  });
}

function closeAssistantReasoning(node) {
  if (!node || !node._reasoningOpen) return;
  node._nativeReasoningEnd = node._raw.length;
  appendAssistantSource(node, "</thinking>\n");
  node._reasoningOpen = false;
}

function appendAssistantReasoning(node, text) {
  if (!node || !text) return;
  if (node._reasoningOpen) appendAssistantSource(node, text);
  else if (!node._raw) {
    appendAssistantSource(node, "<thinking>" + text);
    node._reasoningOpen = true;
  } else {
    var source = node._raw, end = node._nativeReasoningEnd;
    // Only this host-created leading native block accepts delayed fragments.
    // Inline/legacy protocol tags keep the existing parser's semantics.
    if (end > 0 && source.indexOf("<thinking>") === 0 && source.slice(end, end + 11) === "</thinking>") {
      node._raw = source.slice(0, end) + text + source.slice(end);
      node._nativeReasoningEnd += text.length;
    } else {
      node._raw = "<thinking>" + text + "</thinking>" + source;
      node._nativeReasoningEnd = "<thinking>".length + text.length;
    }
    node._partsCache = null;
  }
  scheduleAssistantRender(node);
}

// A provider response may contain visible prose and native tool calls in the
// same assistant message. Seal that prose before rendering the first tool row
// so no delayed Markdown paint can visually reuse or overwrite its segment.
function sealAssistantSegment(node) {
  if (!node || node._sealed) return;
  closeAssistantReasoning(node);
  flushAssistantRender(node);
  node._sealed = true;
  autoCollapseThinking(node);
  releaseAssistantParagraphCaches(node);
  node.classList.add("assistant-segment-complete");
}
function addEventLine(text, cls, tag) {
  var line = el("div", "event-line" + (cls ? " " + cls : ""));
  if (tag) {
    var tg = el("span", "tag", "[" + tag + "] ");
    line.appendChild(tg);
  }
  line.appendChild(document.createTextNode(text));
  appendStream(line);
  smartScroll();
  return line;
}

// noticeTag classifies backend notice text into a quiet [tag].
function noticeTag(text) {
  if (/^pruned /.test(text)) return "prune";
  if (/^preflight:/.test(text)) return "preflight";
  if (/^draft-verify:/.test(text)) return "draft";
  if (/^task:/.test(text)) return "task";
  if (/^hid \d+ old/.test(text)) return "context";
  if (/^consult:/.test(text)) return "consult";
  return "note";
}

/* Tool rows */
var toolRows = {}; // provider tool-call id -> row
var workerRows = {}; // worker task id -> latest delegated-task row

// This overview is fed by the existing event stream; it never polls the backend.
var workerOverview = {};
var workerOverviewAutoOpen = true, workerOverviewUserToggled = false, workerOverviewBound = null;
var workerOverviewView = null;
var workerOverviewRank = { running: 0, unknown: 1, failed: 1, stopped: 1, done: 2 };
function workerLabel(id) { return String(id || "Worker").replace(/^worker-(\d+)$/, "Worker $1"); }
function resetWorkerOverview() {
  pauseWorkerStatusSync();
  workerOverview = {};
  workerOverviewView = null;
  workerOverviewUserToggled = false;
  var panel = $("#worker-overview");
  if (panel) panel.hidden = true;
  var list = $("#worker-overview-list");
  if (list) list.replaceChildren();
}
// A new run keeps only workers that still need attention; settled ones stay in the transcript.
function pruneWorkerOverview() {
  workerOverviewUserToggled = false;
  Object.keys(workerOverview).forEach(function (id) {
    var w = workerOverview[id];
    if (w.status === "done" || w.status === "stopped") { w.button.remove(); delete workerOverview[id]; }
  });
  renderWorkerOverview();
}
function workerStatusTerminal(status) { return status === "done" || status === "failed" || status === "stopped"; }
function updateWorkerOverview(id, agentName, status, activity, row) {
  if (!id) return;
  var item = workerOverview[id];
  // Only the bound latest invocation owns the live overview. Old rows remain
  // renderable in the transcript, including their complete reports.
  if (row && workerRows[id] && workerRows[id] !== row) return;
  if (item && item.run && (!row || !row._taskRun)) return;
  var nextRow = row || item && item.row;
  var nextRun = row && row._taskRun || item && item.run || 0;
  var nextAgent = agentName || item && item.agent || "";
  var nextStatus = status || item && item.status || "running";
  var nextActivity = activity != null ? activity : item && item.activity;
  // A delayed start/tool callback cannot resurrect the same finished invocation.
  if (item && item.row === nextRow && item.run === nextRun && workerStatusTerminal(item.status) && nextStatus === "running") return;
  var label = workerLabel(id) + (nextAgent ? " · " + nextAgent : "");
  var detail = clip(String(nextActivity || "").split("\n")[0], 140);
  var state = taskStatusLabel(nextStatus);
  var title = clip([id, nextRow && nextRow._workerModelInfo, nextActivity].filter(Boolean).join(" · "), 400);
  // Keep raw activity/row ownership exact, even when clipped text looks equal.
  // Check the live DOM too: language changes, replaced roots and altered views
  // must still repair synchronously, without replacing a focused worker button.
  if (item && item.row === nextRow && item.run === nextRun && item.agent === nextAgent &&
      item.status === nextStatus && item.activity === nextActivity &&
      (!nextRow || nextRow.isConnected) && item.button.dataset.state === nextStatus &&
      item.button.children.length === 4 && item.button.children[1] === item.label &&
      item.button.children[2] === item.detail && item.button.children[3] === item.state &&
      item.label.textContent === label && item.detail.textContent === detail &&
      item.state.textContent === state && item.button.title === title && workerOverviewViewMatches()) return;
  if (!item) {
    item = workerOverview[id] = { id: id };
    item.button = el("button", "worker-overview-item");
    item.button.type = "button";
    item.button.addEventListener("click", function () {
      if (!item.row || !item.row.isConnected) return;
      item.row.open = true;
      item.row._body.hidden = false;
      item.row.scrollIntoView({ block: "center", behavior: "smooth" });
    });
    item.button.appendChild(el("span", "worker-overview-dot"));
    item.label = el("span", "worker-overview-name");
    item.detail = el("span", "worker-overview-activity");
    item.state = el("span", "worker-overview-status");
    item.button.append(item.label, item.detail, item.state);
    $("#worker-overview-list").appendChild(item.button);
  }
  if (item.button.children.length !== 4 || item.button.children[1] !== item.label ||
      item.button.children[2] !== item.detail || item.button.children[3] !== item.state) {
    item.button.replaceChildren(item.button.querySelector(".worker-overview-dot") || el("span", "worker-overview-dot"), item.label, item.detail, item.state);
  }
  item.run = nextRun;
  item.agent = nextAgent;
  item.status = nextStatus;
  item.activity = nextActivity;
  item.row = nextRow;
  item.button.dataset.state = nextStatus;
  item.label.textContent = label;
  item.detail.textContent = detail;
  item.state.textContent = state;
  item.button.title = title;
  renderWorkerOverview();
}
// After stream/history boundaries, reconcile only displayed worker IDs.
// Healthy progress stays event-driven; no model calls or global-worker imports.
var workerStatusEpoch = 0, workerStatusTimer = null, workerStatusRequest = null;
var workerStatusArmed = false, workerStatusFailures = 0;
function workerStatusScope() {
  return (typeof activeSessionID === "string" ? activeSessionID : "") + "/" + (typeof projectEpoch === "number" ? projectEpoch : 0);
}
function pauseWorkerStatusSync() {
  workerStatusEpoch++;
  workerStatusArmed = false;
  workerStatusRequest = null;
  clearTimeout(workerStatusTimer);
  workerStatusTimer = null;
}
function workerStatusBindings() {
  return Object.keys(workerRows).map(function (id) {
    var row = workerRows[id], item = workerOverview[id];
    if (!row || !row.isConnected || row._workerSnapshotMismatch || workerStatusTerminal(row._taskStatus)) return null;
    if (row._taskStatus !== "running" && (!item || (item.status !== "running" && item.status !== "unknown"))) return null;
    return {id: id, row: row, run: row._taskRun || 0, activity: row._taskActivitySerial || 0};
  }).filter(Boolean);
}
function workerActivityText(activity) {
  if (!activity || !activity.tool) return "";
  var info = toolHint(activity.tool, activity.args || "{}");
  return (info.name || activity.tool) + (info.hint ? " · " + info.hint : "");
}
function markWorkerStatusUnconfirmed(binding, mismatch) {
  var row = binding.row;
  if (workerRows[binding.id] !== row || (row._taskRun || 0) !== binding.run || workerStatusTerminal(row._taskStatus)) return;
  if (mismatch) row._workerSnapshotMismatch = true;
  row._workerStatusUnconfirmed = true;
  clearInterval(row._clock);
  row._clock = null;
  row.classList.remove("running");
  row._stat.textContent = t("task.statusUnconfirmed");
  updateWorkerOverview(binding.id, row._taskAgent, "unknown", row._taskActivity || row._taskPrompt, row);
}
function applyWorkerStatusSnapshot(workers, bindings) {
  var snapshots = Object.create(null);
  (workers || []).forEach(function (worker) { if (worker && worker.id) snapshots[worker.id] = worker; });
  bindings.forEach(function (binding) {
    var row = binding.row, worker = snapshots[binding.id];
    if (workerRows[binding.id] !== row || !row.isConnected || (row._taskRun || 0) !== binding.run) return;
    if (!worker) { markWorkerStatusUnconfirmed(binding, true); return; }
    var run = Number(worker.run) || 0;
    if (!binding.run || run !== binding.run) {
      // A retained global ID alone cannot establish an unversioned history run.
      markWorkerStatusUnconfirmed(binding, true);
      return;
    }
    var status = worker.status === "created" ? "running" : worker.status;
    if (status !== "running" && !workerStatusTerminal(status)) { markWorkerStatusUnconfirmed(binding, false); return; }
    if (workerStatusTerminal(row._taskStatus)) return;
    row._workerSnapshotMismatch = false;
    row._workerStatusUnconfirmed = false;
    row._taskStatus = status;
    if (worker.activity && (row._taskActivitySerial || 0) === binding.activity) {
      var activity = workerActivityText(worker.activity);
      if (activity) row._taskActivity = activity;
    }
    row._stat.textContent = taskStatusLabel(status);
    row.classList.toggle("running", status === "running");
    if (workerStatusTerminal(status)) {
      clearInterval(row._clock);
      row._clock = null;
      row.classList.remove("running");
      row.classList.toggle("failed", status === "failed");
      row.classList.add("done");
      row._stat.textContent = taskStatusLabel(status);
    }
    updateWorkerOverview(binding.id, worker.agent || row._taskAgent, status, row._taskActivity || row._taskPrompt, row);
  });
}
function scheduleWorkerStatusSync() {
  clearTimeout(workerStatusTimer);
  workerStatusTimer = null;
  if (!workerStatusArmed || document.hidden || workerStatusFailures >= 3 || !workerStatusBindings().length) return;
  if (typeof streaming === "boolean" && streaming && !runFinishing) return;
  workerStatusTimer = setTimeout(refreshWorkerStatusSnapshot, workerStatusFailures ? 5000 * workerStatusFailures : 3000);
}
function refreshWorkerStatusSnapshot() {
  clearTimeout(workerStatusTimer);
  workerStatusTimer = null;
  if (!workerStatusArmed || document.hidden || typeof j !== "function") return Promise.resolve();
  if (workerStatusRequest) return workerStatusRequest;
  if (typeof streaming === "boolean" && streaming && !runFinishing) return Promise.resolve();
  var bindings = workerStatusBindings();
  if (!bindings.length) return Promise.resolve();
  var epoch = workerStatusEpoch, scope = workerStatusScope();
  var request = Promise.resolve().then(function () {
    if (epoch !== workerStatusEpoch || scope !== workerStatusScope() || document.hidden) return null;
    return j("/api/workers");
  }).then(function (got) {
    if (epoch !== workerStatusEpoch || scope !== workerStatusScope() || document.hidden) return;
    if (!got || !Array.isArray(got.workers)) throw new Error("Invalid worker snapshot");
    workerStatusFailures = 0;
    applyWorkerStatusSnapshot(got.workers, bindings);
  }).catch(function () {
    if (epoch !== workerStatusEpoch || scope !== workerStatusScope() || document.hidden) return;
    workerStatusFailures++;
    bindings.forEach(function (binding) { markWorkerStatusUnconfirmed(binding, false); });
  }).finally(function () {
    if (workerStatusRequest !== request) return;
    workerStatusRequest = null;
    if (epoch === workerStatusEpoch && scope === workerStatusScope()) scheduleWorkerStatusSync();
  });
  workerStatusRequest = request;
  return request;
}
function resumeWorkerStatusSync() {
  Object.keys(workerRows).forEach(function (id) { workerRows[id]._workerSnapshotMismatch = false; });
  workerStatusArmed = true;
  workerStatusFailures = 0;
  return refreshWorkerStatusSnapshot();
}
function workerOverviewOrder(w) { var r = workerOverviewRank[w.status]; return r == null ? 2 : r; }
function workerOverviewSummary(counts) {
  var parts = [t("task.workers")];
  [["running", "task.active"], ["done", "task.done"], ["failed", "task.failed"], ["stopped", "task.stopped"]].forEach(function (p) {
    if (counts[p[0]]) parts.push(t(p[1]) + ": " + counts[p[0]]);
  });
  if (counts.unknown) parts.push(t("task.statusUnconfirmed") + ": " + counts.unknown);
  return parts.join(" · ");
}
function workerOverviewViewMatches() {
  var view = workerOverviewView, panel = $("#worker-overview"), list = $("#worker-overview-list");
  return !!(view && view.panel === panel && view.list === list && panel.isConnected && !panel.hidden &&
    list.isConnected && list.children.length === view.items.length &&
    view.items.every(function (w, index) { return list.children[index] === w.button; }) &&
    $("#worker-overview-summary").textContent === workerOverviewSummary(view.counts) &&
    (workerOverviewUserToggled || panel.open === workerOverviewAutoOpen));
}
function renderWorkerOverview() {
  var panel = $("#worker-overview");
  if (!panel) return;
  if (workerOverviewBound !== panel) {
    workerOverviewBound = panel;
    panel.addEventListener("toggle", function () {
      if (panel.open !== workerOverviewAutoOpen) workerOverviewUserToggled = true;
    });
  }
  // Working first, then problems, then finished; numeric id within a group.
  var items = Object.values(workerOverview).sort(function (a, b) {
    return workerOverviewOrder(a) - workerOverviewOrder(b) ||
      a.id.localeCompare(b.id, undefined, { numeric: true });
  });
  var list = $("#worker-overview-list");
  items.forEach(function (w, index) {
    var next = list.children[index] || null;
    if (next !== w.button) list.insertBefore(w.button, next);
  });
  while (list.children.length > items.length) list.lastElementChild.remove();
  var counts = {};
  items.forEach(function (w) { counts[w.status] = (counts[w.status] || 0) + 1; });
  $("#worker-overview-summary").textContent = workerOverviewSummary(counts);
  workerOverviewView = { panel: panel, list: list, items: items, counts: counts };
  panel.hidden = !items.length;
  // Collapse once nothing is working so the panel does not cover the chat,
  // unless the user already chose otherwise during this run.
  if (items.length && !workerOverviewUserToggled) {
    workerOverviewAutoOpen = !!(counts.running || counts.unknown);
    panel.open = workerOverviewAutoOpen;
  }
}
function taskRowTitle(row, agentName, id) {
  return (id ? workerLabel(id) : t("task.delegation")) +
    (agentName ? " · " + agentName : "") +
    (row._toolName === "send_message" ? " · " + t("task.continue") : "");
}

var openToolOrder = []; // ids without results yet

// Tools without their own label reuse the closest existing translation.
var TOOL_NAME_ALIASES = { load_session_image: "read_image" };
function toolDisplayName(name) {
  var key = "tool." + (TOOL_NAME_ALIASES[name] || name);
  var translated = t(key);
  return translated === key ? name : translated;
}

function workerToolNameNode(name) {
  var node = el("span", "activity-name");
  var match = String(name || "tool").match(/^(.*?)(\s*×\s*\d+)$/);
  var tool = match ? match[1].trim() : name, count = match ? match[2].replace(/\s+/g, "") : "";
  var refresh = function () { node.textContent = toolDisplayName(tool || "tool") + count; };
  refresh();
  if (typeof registerLanguageRefresh === "function") registerLanguageRefresh(node, refresh);
  node.title = String(name || "tool");
  return node;
}

function validWorkerActivity(log, id) {
  return log && log.version === 1 && log.id === id && Number.isInteger(log.run) && log.run > 0 &&
    Array.isArray(log.calls) && log.calls.length <= 256 ? log : null;
}
function recordedWorkerActivity(row, note) {
  return validWorkerActivity(row._savedWorkerActivity, note.id);
}

function workerActivityNeedsRecovery(row, log) {
  if (!row._activity || row._activitySummary) return true;
  var seen = Object.create(null);
  row._activity.querySelectorAll('.task-activity-item').forEach(function (item) {
    if (item.dataset.workerCallId) seen[item.dataset.workerCallId] = item;
  });
  return log.calls.some(function (call) {
    var item = seen[call.id];
    return !item || (call.completed === true && !item.classList.contains(call.error ? "failed" : "done")) ||
      ((call.output || call.error) && !item.querySelector('.activity-result'));
  });
}

function workerActivityStatus(key) {
  if (key !== "task.statusUnconfirmed") return i18nEl("span", "activity-status", key);
  var status = el("span", "activity-status", "—");
  status.title = t(key);
  status.dataset.i18nTitle = status.dataset.i18nAria = key;
  status.setAttribute("aria-label", t(key));
  return status;
}

// Keep the released one-line activity layout. Its whole row opens the owned
// preview; a separate "Output" row must not inflate every collapsed call.
function setWorkerActivityDetail(item, args, output, error, handle, partial) {
  if (!args && !output && !error && !handle) return;
  var detail = item.querySelector(".activity-result");
  if (!detail) {
    detail = el("details", "activity-result");
    var summary = el("summary", "activity-summary");
    while (item.firstChild) summary.appendChild(item.firstChild);
    detail.appendChild(summary);
    detail.appendChild(el("div", "activity-payload"));
    item.appendChild(detail);
    detail.addEventListener("toggle", function () {
      if (detail.open && detail._loadWorkerDetail) detail._loadWorkerDetail();
    });
  }
  var payload = detail.querySelector(".activity-payload");
  var session = transcriptSessionID || activeSessionID;
  var state = {handle: handle, loaded: false, loading: false};
  detail._workerDetailState = state;
  function render(args, output, error, incomplete) {
    payload.innerHTML = "";
    function append(value, failed) {
      if (!value) return;
      if (!failed && detail.open) {
        // Same viewer as main-transcript tool rows (line numbers, highlighting).
        var before = payload.childNodes.length;
        appendToolPayload(payload, t("tool.output"), String(value), item.dataset.workerTool || "", false, args);
        for (var i = before; i < payload.childNodes.length; i++) {
          if (!payload.childNodes[i].classList.contains("lbl")) payload.childNodes[i].classList.add("activity-output");
        }
        return;
      }
      payload.appendChild(i18nEl("div", "lbl", failed ? "tool.error" : "tool.output"));
      var result = detail.open ? toolOutputPre(String(value), failed) : el("pre", failed ? "tool-output error" : "tool-output", String(value));
      result.classList.add("activity-output");
      payload.appendChild(result);
    }
    append(output, false);
    append(error, true);
    if (output || error) item.title = clip(String(error || output), 300);
    if (args) {
      payload.appendChild(i18nEl("div", "lbl", "tool.input"));
      var input = detail.open ? toolInputPre(String(args)) : el("pre", "", String(args));
      input.classList.add("activity-args");
      payload.appendChild(input);
    }
    if (incomplete) payload.appendChild(i18nEl("div", "task-activity-note", "task.activityPartial"));
  }
  render(args, output, error, partial);
  if (!detail.open) renderToolPayloadWhenOpen(detail, function () {
    if (detail._workerDetailState === state && !state.loaded) render(args, output, error, partial);
  });
  detail._loadWorkerDetail = function () {
    if (state.loaded || state.loading || !session || !/^worker_ui_[0-9a-f]{32}$/.test(handle || "")) return;
    state.loading = true;
    detail._workerDetailRequest = j("/api/tool-output?session=" + encodeURIComponent(session) + "&handle=" + encodeURIComponent(handle)).then(function (data) {
      if (detail._workerDetailState !== state || !detail.isConnected) return;
      if (!data || ["args", "output", "error"].some(function (key) { return data[key] != null && typeof data[key] !== "string"; })) throw Error("invalid tool detail");
      state.loaded = true;
      // A collapsed result keeps its full text off the DOM until reopened.
      renderToolPayloadWhenOpen(detail, function () {
        if (detail._workerDetailState === state) render(data.args, data.output, data.error, false);
      });
    }).catch(function () {
      if (detail._workerDetailState !== state || !detail.isConnected) return;
      if (!payload.querySelector('.task-activity-note')) payload.appendChild(i18nEl("div", "task-activity-note", "task.activityUnavailable"));
    }).finally(function () { state.loading = false; });
  };
  if (detail.open) detail._loadWorkerDetail();
}

function fillRecordedWorkerActivity(activity, log, expanded) {
  activity.innerHTML = "";
  log.calls.forEach(function (call) {
    if (!call || typeof call.tool !== "string") return;
    var item = el("div", "task-activity-item");
    item.dataset.workerCallId = call.id || "";
    applyActivityToolKind(item, call.tool);
    var state = call.completed === true ? (call.error ? "failed" : "done") : "unknown";
    item.classList.add(state);
    item.appendChild(el("span", "activity-dot"));
    item.appendChild(workerToolNameNode(call.tool));
    item.appendChild(el("span", "activity-hint", toolHint(call.tool, call.args || "{}").hint || ""));
    item.appendChild(workerActivityStatus(state === "unknown" ? "task.statusUnconfirmed" : "task." + state));
    setWorkerActivityDetail(item, call.args, call.output, call.error, call.detail_handle, call.partial);
    var detail = item.querySelector(".activity-result");
    if (detail) detail.open = !!(expanded && expanded[call.id]);
    activity.appendChild(item);
  });
  if (!log.calls.length) activity.appendChild(i18nEl("div", "task-activity-empty", "task.activityUnavailable"));
  if (log.omitted > 0) activity.appendChild(i18nEl("div", "task-activity-note", "task.activityPartial"));
}

// Colour group of a tool row title: reading, writing, running or web.
function toolKind(name) {
  name = String(name || "");
  if (superCliUI.fileMutationTools[name] || /^edit_/.test(name) || name === "remember") return "write";
  if (name === "ctx_execute" || name === "process_session") return "run";
  if (/^web_/.test(name) || name === "mcp_bridge") return "web";
  if (/^(read_|search_|list_dir$|recall$|load_session_image$)/.test(name)) return "read";
  return "";
}
function applyActivityToolKind(item, name) {
  item.dataset.workerTool = name || "";
  var kind = toolKind(name);
  if (kind) item.classList.add("tk-" + kind);
}
function applyToolKind(row, name) {
  row.classList.remove("tk-read", "tk-write", "tk-run", "tk-web");
  var kind = toolKind(name);
  if (kind) row.classList.add("tk-" + kind);
}

function commandPreview(command) {
  if (!Array.isArray(command)) command = [command];
  return "$ " + command.filter(function (part) { return part != null && String(part) !== ""; }).map(function (part) {
    part = String(part);
    return /^[\w@%+=:,./\\-]+$/.test(part) ? part : JSON.stringify(part);
  }).join(" ");
}

function toolHint(name, args) {
  try {
    var a = JSON.parse(args || "{}");
    if (name === "task") {
      var kind = a.agent || "general";
      return {
        name: t("task.delegation") + " · " + kind + (a.advise ? " (" + t("task.advice") + ")" : ""),
        hint: clip(a.prompt || "", 90), agent: kind, prompt: a.prompt || "",
      };
    }
    if (name === "send_message") {
      return { name: t("task.continue") + " · " + (a.to || "worker"),
        hint: clip(a.message || "", 90), agent: "", prompt: a.message || "", workerID: a.to || "" };
    }
    var display = toolDisplayName(name);
    if (a.command) {
      var action = name === "process_session" && a.action ? String(a.action) + " · " : "";
      return { name: display, hint: clip(action + commandPreview(a.command), 150) };
    }
    if (a.cmd) return { name: display, hint: clip(commandPreview(a.cmd), 150) };
    if ((name === "move" || name === "copy") && a.src) {
      return { name: display, hint: clip(String(a.src) + " → " + String(a.dest || ""), 150) };
    }
    var keys = ["path", "file", "dir", "query", "pattern", "prompt", "text"];
    for (var i = 0; i < keys.length; i++) {
      if (a[keys[i]]) {
        var detail = String(a[keys[i]]);
        if (a.line) detail += " · " + a.line;
        else if (a.from) detail += " · " + a.from + (a.to ? "–" + a.to : "");
        return { name: display, hint: clip(detail, 150) };
      }
    }
    var flat = Object.keys(a).map(function (k) { return k + "=" + clip(JSON.stringify(a[k]), 30); }).join(" ");
    return { name: display, hint: clip(flat, 150) };
  } catch (e) {
    return { name: toolDisplayName(name), hint: clip(args || "", 150) };
  }
}

var DIFF_TOOLS = {
  edit_line: true, edit_lines: true, insert_after: true, delete_lines: true,
  apply_patch: true, patch: true,
};

var FILE_MUTATION_TOOLS = superCliUI.fileMutationTools;

function mutationOutcomeLabel(name, output, isError) {
  var kind = superCliUI.mutationKind(name, output, isError);
  return {
    created: "change.fileCreated",
    modified: "change.fileModified",
    deleted: "change.fileDeleted",
    "folder-created": "change.folderCreated",
    moved: "change.fileMoved",
    copied: "change.fileCopied",
  }[kind] || "";
}

var FILE_READ_TOOLS = {
  read_lines: true, read_context: true, read_many: true,
};
// Tools whose output can show numbered "N | code" lines of the file they name.
var NUMBERED_CODE_TOOLS = {
  read_lines: true, read_context: true, read_many: true, patch_file: true,
  search_code: true,
};

// Fence-style language tag of a tool file path: "a/b.go:1-40" → "go".
function toolPathLang(path) {
  var m = /\.([A-Za-z0-9+#_-]{1,12})(?::\d+(?:-\d+)?)?\s*$/.exec(String(path || ""));
  return m ? m[1].toLowerCase() : "";
}
function toolArgsFile(args) {
  try {
    var parsed = JSON.parse(args || "{}");
    if (!parsed) return "";
    if (typeof parsed.file === "string") return parsed.file;
    return typeof parsed.path === "string" ? parsed.path : "";
  } catch (e) { return ""; }
}

function appendFileReadPayload(body, text, args) {
  var viewer = el("div", "tool-file-view");
  // read_lines/read_context name the file in args; read_many per section.
  var lang = toolPathLang(toolArgsFile(args));
  String(text == null ? "" : text).split(/\r?\n/).forEach(function (line) {
    // search_code marks matching lines with a leading ">".
    var numbered = line.match(/^(>?)\s*(\d+)\s*\|\s?(.*)$/);
    if (numbered) {
      var row = el("div", numbered[1] ? "file-code-line hit" : "file-code-line");
      row.appendChild(el("span", "file-line-no", numbered[2]));
      var src = numbered[3], code = el("code", "file-line-code");
      // highlightCodeHTML escapes everything it returns, or null for plain.
      var html = src && lang && typeof highlightCodeHTML === "function" ? highlightCodeHTML(src, lang) : null;
      if (html === null) code.textContent = src || " ";
      else code.innerHTML = html;
      row.appendChild(code);
      viewer.appendChild(row);
      return;
    }
    var section = line.match(/^==\s*(.*?)\s*==$/);
    if (section) {
      lang = toolPathLang(section[1]);
      viewer.appendChild(el("div", "file-code-section", section[1]));
      return;
    }
    if (/^\[read_many:/.test(line)) {
      viewer.appendChild(el("div", "file-code-summary", line));
      return;
    }
    if (line || viewer.childNodes.length === 0) {
      viewer.appendChild(el("div", /^error:/i.test(line) ? "file-code-note error" : "file-code-note", line || " "));
    }
  });
  body.appendChild(viewer);
}

function toolChangeStats(name, text) {
  if (!DIFF_TOOLS[name]) return { added: 0, removed: 0, diff: false };
  var stats = { added: 0, removed: 0, diff: false };
  // Folded history still needs its summary. Inspect line starts without
  // allocating an array and substring for every line of a large diff.
  // Keep the original prefix rules, including the four-minus exclusion.
  var raw = String(text || ""), offset = 0;
  while (offset < raw.length) {
    var sign = raw.charCodeAt(offset);
    if (sign === 43 && (raw.charCodeAt(offset + 1) !== 43 || raw.charCodeAt(offset + 2) !== 43)) stats.added++;
    else if (sign === 45 && (raw.charCodeAt(offset + 1) !== 45 || raw.charCodeAt(offset + 2) !== 45 || raw.charCodeAt(offset + 3) !== 45)) stats.removed++;
    var end = raw.indexOf("\n", offset);
    if (end < 0) break;
    offset = end + 1;
  }
  stats.diff = stats.added > 0 || stats.removed > 0;
  return stats;
}

// Replayed tool rows start folded. Build their potentially large file/diff
// viewers only when expanded, while the transcript keeps the original text.
// Rendering callbacks belong to their row; clearing a transcript also releases
// their raw payload. Expanded rows render immediately, folded rows wait for use.
function renderToolPayloadWhenOpen(row, render) {
  var rendered = false;
  function renderIfOpen() {
    if (!row.open || rendered) return;
    rendered = true;
    row.removeEventListener("toggle", renderIfOpen);
    try { render(); } finally { render = null; }
  }
  row.addEventListener("toggle", renderIfOpen);
  renderIfOpen();
  return function () {
    rendered = true;
    render = null;
    row.removeEventListener("toggle", renderIfOpen);
  };
}

function appendHistoryToolPayload(row, body, args, text, name) {
  row._historyPayloadRendered = false;
  return renderToolPayloadWhenOpen(row, function () {
    if (args && !FILE_READ_TOOLS[name]) {
      body.appendChild(i18nEl("div", "lbl", "tool.input"));
      body.appendChild(toolInputPre(args));
    }
    appendToolPayload(body, t("tool.output"), text || "", name, false, args);
    row._historyPayloadRendered = true;
  });
}

function bindFullToolOutput(row, message) {
  var handle = message.tool_output_handle, session = transcriptSessionID || activeSessionID;
  if (!session || !/^out_[0-9a-f]{6,32}$/.test(handle || "") || row._fullOutputHandle === handle) return;
  row._fullOutputHandle = handle;
  renderToolPayloadWhenOpen(row, function () {
    row._fullOutputRequest = j("/api/tool-output?session=" + encodeURIComponent(session) + "&handle=" + encodeURIComponent(handle)).then(function (data) {
      if (!row.isConnected || row._fullOutputHandle !== handle) return;
      if (!data || typeof data.text !== "string") throw Error("missing tool output");
      row._fullOutputText = data.text;
      renderToolPayloadWhenOpen(row, function () {
        if (!row.isConnected || row._fullOutputHandle !== handle) return;
        var name = row._historyName || message.name || "tool";
        var task = (name === "task" || name === "send_message") ? parseTaskNotification(data.text) : null;
        if (task) {
          renderTaskResult(row, task, null, row._taskPrompt, task.status === "failed", true);
        } else if (!row.classList.contains("task-row")) {
          if (row._cancelHistoryPayload) row._cancelHistoryPayload();
          row._body.innerHTML = "";
          appendHistoryToolPayload(row, row._body, row._historyArgs || "", data.text, name);
        }
      });
    }).catch(function () {
      if (row.isConnected) row._body.appendChild(i18nEl("div", "task-activity-note", "task.activityUnavailable"));
    });
  });
}

// Media is user-visible output, not hidden diagnostic JSON. Its thumbnail stays
// outside folded tool details in both live and restored conversations.
function toolMediaDescriptor(text, name, isError) {
  // Owned screenshots retain SendScreenshot's canonical top-level metadata.
  // Ordinary process snapshots start with id; do not parse their large stdout
  // merely because the process tool can also return an image.
  if (name === "process_session" && (typeof text !== "string" ||
      !text.startsWith('{"type":"image",'))) return null;
  // load_session_image re-sends an image already in this conversation; its
  // result ends with "path: <absolute image file>".
  if (name === "load_session_image" && !isError) {
    var loaded = /\npath: (.+?)\s*$/.exec(String(text == null ? "" : text));
    var loadedPath = loaded && loaded[1];
    if (loadedPath && !/[\x00-\x1f]/.test(loadedPath) && /^(?:[a-z]:[\\/]|\/|\\\\)/i.test(loadedPath) &&
        /\.(?:png|jpe?g|gif|webp)$/i.test(loadedPath)) return {path: loadedPath, kind: "image", previewPath: ""};
    return null;
  }
  if ((name === "send_screenshot" || name === "process_session" || name === "show_media" || name === "generate_image" || name === "generate_video" || name === "headless_control") && !isError) {
    try {
      var media = JSON.parse(String(text == null ? "" : text));
      var mediaPath = media && media.path;
      // Accept only recognized local file metadata; never arbitrary model HTML.
      if (typeof mediaPath === "string" && !/[\x00-\x1f]/.test(mediaPath) &&
          /^(?:[a-z]:[\\/]|\/|\\\\)/i.test(mediaPath) &&
          !media.save_error && !media.error &&
          ((name !== "send_screenshot" && name !== "headless_control" && name !== "process_session") || media.type === "image") &&
          (name !== "process_session" || media.source === "window") &&
          (name !== "headless_control" || media.source === "qmp" || media.source === "browser") &&
          media.type === attachmentMimeKind(media.media_type) &&
          ["image", "video", "audio"].indexOf(media.type) >= 0) {
        var previewPath = (name === "send_screenshot" || name === "headless_control" || name === "process_session") && typeof media.preview_path === "string" &&
          /^snapshot:[A-Za-z0-9._-]+$/.test(media.preview_path) ? media.preview_path : "";
        return {path: mediaPath, kind: media.type, previewPath: previewPath};
      }
    } catch (e) {}
  }
  return null;
}

function appendToolMediaPreview(row, text, name, isError) {
  if (name === "darwin") {
    if (typeof appendDarwinCard === "function") appendDarwinCard(row, text, isError);
    return;
  }
  if (!row || !row.parentNode || row._mediaPreview) return;
  var media = toolMediaDescriptor(text, name, isError);
  if (!media) return;
  // load_session_image re-sends an image already shown earlier in the chat.
  // Keep its thumbnail inside the folded row instead of duplicating it.
  if (name === "load_session_image" && row._body) {
    row._mediaPreview = true;
    renderToolPayloadWhenOpen(row, function () {
      var inner = el("div", "tool-media-preview inline");
      renderSentAttachments(inner, [media.path], media.kind, media.previewPath);
      row._body.appendChild(inner);
    });
    return;
  }
  var preview = el("div", "tool-media-preview");
  if (row.classList.contains("transcript-live")) preview.classList.add("transcript-live");
  renderSentAttachments(preview, [media.path], media.kind, media.previewPath);
  preview.querySelectorAll("img").forEach(function (image) {
    image.addEventListener("load", function () { smartScroll(); }, {once: true});
  });
  row.parentNode.insertBefore(preview, row.nextSibling);
  row._mediaPreview = preview;
}

function appendToolPayload(body, label, text, name, isError, args) {
  var raw = String(text == null ? "" : text);
  if (FILE_READ_TOOLS[name] && !isError) {
    appendFileReadPayload(body, raw, args);
    return;
  }
  if (NUMBERED_CODE_TOOLS[name] && !isError && /^>?\s*\d+ \|/m.test(raw)) {
    body.appendChild(el("div", "lbl", label));
    appendFileReadPayload(body, raw, args);
    return;
  }
  // 04c-tool-views.js: readable views (tree, cards, task list); raw fallback.
  if (!isError && typeof renderRichToolPayload === "function" && renderRichToolPayload(body, label, raw, name, args)) return;
  body.appendChild(el("div", "lbl", label));
  if (name === "ctx_execute" && !isError) {
    try {
      var execution = JSON.parse(raw);
      if (execution && Object.prototype.hasOwnProperty.call(execution, "exit_code")) {
        var meta = el("div", "tool-exec-meta");
        meta.appendChild(el("span", execution.exit_code === 0 ? "ok" : "err", t("tool.exit").replace("{code}", execution.exit_code)));
        if (execution.duration_ms != null) meta.appendChild(el("span", "", fmtDuration(Number(execution.duration_ms))));
        if (execution.workdir) meta.appendChild(el("span", "", execution.workdir));
        body.appendChild(meta);
        if (execution.stdout) appendToolPayload(body, t("tool.stdout"), execution.stdout, "", false);
        if (execution.stderr) appendToolPayload(body, t("tool.stderr"), execution.stderr, "", true);
        if (!execution.stdout && !execution.stderr) body.appendChild(el("pre", "tool-output", t("tool.empty")));
        return;
      }
    } catch (e) {}
  }
  var changes = toolChangeStats(name, raw);
  if (!changes.diff) {
    body.appendChild(toolOutputPre(raw, isError));
    return;
  }
  var diff = el("div", "tool-diff");
  raw.split(/\r?\n/).forEach(function (line) {
    var cls = "diff-line";
    if (/^\+(?!\+\+)/.test(line)) cls += " added";
    else if (/^-(?!---)/.test(line)) cls += " removed";
    else cls += " context";
    diff.appendChild(el("div", cls, line || " "));
  });
  body.appendChild(diff);
}

function setToolResultStatus(row, elapsed, err, changes) {
  row._stat.innerHTML = "";
  if (changes && changes.added) row._stat.appendChild(el("span", "change-add", "+" + changes.added));
  if (changes && changes.removed) row._stat.appendChild(el("span", "change-remove", "−" + changes.removed));
  row._stat.appendChild(el("span", err ? "status-error" : "", (err ? "× · " : "") + fmtDuration(elapsed)));
}

// task results use a compact XML envelope for the model. Keep that protocol
// out of the UI: extract its stable fields and render the report as content.
function parseTaskNotification(text) {
  var src = String(text || "").trim();
  var open = "<task-notification>", close = "</task-notification>";
  if (src.indexOf(open) !== 0) return null;
  var end = src.lastIndexOf(close);
  if (end < open.length) return null;
  var suffix = src.slice(end + close.length).trim();
  var header = src.slice(open.length, end).trim(), result = "";
  var resultAt = header.indexOf("<result>");
  if (resultAt >= 0) {
    var resultEnd = header.lastIndexOf("</result>");
    if (resultEnd < resultAt || header.slice(resultEnd + "</result>".length).trim()) return null;
    // Reports may contain quoted XML, including another complete notification.
    result = header.slice(resultAt + "<result>".length, resultEnd).trim();
    header = header.slice(0, resultAt).trim();
  }
  var fields = {};
  while (header) {
    var field = header.match(/^<(task-id|agent|worker-model|reasoning-effort|status|summary|tools|continuation)>([\s\S]*?)<\/\1>/);
    if (!field || fields[field[1]] != null) return null;
    fields[field[1]] = field[2].trim();
    header = header.slice(field[0].length).trim();
  }
  if (!/^worker-\d+$/.test(fields["task-id"] || "") || !fields.agent || /[<>\r\n]/.test(fields.agent)) return null;
  if (!/^(created|running|done|completed|failed|stopped|unknown)$/.test(fields.status || "")) return null;
  if (suffix && suffix.indexOf("[Attached: worker tool observations (historical snapshots).]") !== 0 &&
      !new RegExp("^\\[Worker " + fields["task-id"] + " run [1-9][0-9]* tool observations at [0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\\.[0-9]+)?Z; historical snapshots, not current workspace state; outputs may be truncated\\](?:\\r?\\n|$)").test(suffix)) return null;
  return {
    id: fields["task-id"], agent: fields.agent,
    status: fields.status, summary: fields.summary || "",
    tools: fields.tools || "", result: result,
    model: fields["worker-model"] || "", reasoning_effort: fields["reasoning-effort"] || "",
  };
}

function taskStatusLabel(status) {
  if (status === "created" || status === "running") return t("tool.running");
  if (status === "failed") return t("task.failed");
  if (status === "stopped") return t("task.stopped");
  if (status === "done" || status === "completed") return t("task.done");
  return t("task.statusUnconfirmed");
}

function taskMetrics(note) {
  var summary = String(note.summary || "");
  var parts = [], steps = summary.match(/(\d+)\s+steps?/i);
  var tokens = summary.match(/(\d+)\s+in\/(\d+)\s+out\s+tok/i);
  var model = summary.match(/(?:^|\s·\s)model=([^·]+)/i);
  if (steps) {
    var n = Number(steps[1]);
    parts.push(fmtInteger(n) + " " + t(n === 1 ? "task.step" : "task.steps"));
  }
  if (tokens) {
    parts.push(fmtCompactNumber(Number(tokens[1])) + " " + t("task.input"));
    parts.push(fmtCompactNumber(Number(tokens[2])) + " " + t("task.output"));
  }
  if (model) parts.push(model[1].trim());
  if (!parts.length && (note.status === "running" || note.status === "created")) return t("tool.running");
  var agent = String(note.agent || "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  var status = String(note.status || "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return parts.join(" · ") || summary.replace(new RegExp("^" + agent + "\\s+" + status + "\\s*[·-]?\\s*", "i"), "");
}

function bindWorkerObservations(row, message) {
  if (!message || !/^[1-9][0-9]*$/.test(message.worker_observations_id || "") || !Number.isInteger(message.seq) || message.seq < 1) return;
  var session = transcriptSessionID || activeSessionID, previous = row._workerObservationsRef;
  if (!previous || previous.session !== session || previous.seq !== message.seq || previous.id !== message.worker_observations_id) {
    row._workerObservationsRef = {session:session, seq:message.seq, id:message.worker_observations_id};
    if (row._workerObservationsDetail) row._workerObservationsDetail.remove();
  }
  if (row._activitySummary && row._activity) {
    row._activity.title = t("task.activityPartial");
    row._activity.dataset.i18nTitle = "task.activityPartial";
    var empty = row._activity.querySelector('.task-activity-empty');
    if (empty) {
      empty.textContent = t("task.activityPartial");
      empty.dataset.i18nText = "task.activityPartial";
      empty.i18nTextNode = empty.firstChild;
    }
  }
  appendWorkerObservations(row);
}

function appendWorkerObservations(row) {
  var ref = row._workerObservationsRef;
  if (!ref || !ref.session || row._savedWorkerActivity || row._workerObservationsDetail && row._workerObservationsDetail.parentNode === row._body) return;
  var detail = el("details", "worker-observations");
  var summary = i18nEl("summary", "", "tool.read_output"), output = el("pre");
  detail.append(summary, output);
  row._workerObservationsDetail = detail;
  row._body.appendChild(detail);
  var loading = false, controller = null, generation = 0;
  detail.addEventListener("toggle", function () {
    if (!detail.open) {
      if (controller) { generation++; controller.abort(); controller = null; loading = false; }
      return;
    }
    if (row._workerObservationsCache && row._workerObservationsCache.id === ref.id) {
      output.textContent = row._workerObservationsCache.text;
      return;
    }
    if (loading) return;
    loading = true;
    controller = new AbortController();
    var version = ++generation;
    output.textContent = t("common.loading");
    row._workerObservationRequest = j("/api/workers/observations?session=" + encodeURIComponent(ref.session) + "&seq=" + ref.seq + "&message_id=" + encodeURIComponent(ref.id), {signal:controller.signal}).then(function (data) {
      if (version !== generation || !detail.isConnected || row._workerObservationsRef !== ref || row._workerObservationsDetail !== detail) return;
      if (!data || typeof data.text !== "string") throw Error("missing observations");
      row._workerObservationsCache = {id:ref.id,text:data.text};
      output.textContent = data.text;
    }).catch(function () {
      if (version === generation && detail.isConnected && row._workerObservationsDetail === detail) output.textContent = t("task.activityUnavailable");
    }).finally(function () { if (version === generation) { loading = false; controller = null; } });
  });
}

function renderTaskResult(row, note, elapsed, prompt, err, history) {
  if (workerStatusTerminal(row._taskStatus) && note.status === "running") return;
  row._taskStatus = err ? "failed" : note.status;
  row._taskID = note.id || row._taskID;
  row._taskAgent = note.agent || row._taskAgent;
  // A replacement result must release its previous unopened report callback.
  if (row._cancelTaskReport) row._cancelTaskReport();
  row._cancelTaskReport = null;
  if (row._cancelTaskActivity) row._cancelTaskActivity();
  row._cancelTaskActivity = null;
  var activity = row._activity;
  row.classList.add("task-row");
  row.classList.remove("running", "done", "failed");
  row.classList.add(err || note.status === "failed" ? "failed" : (note.status === "running" ? "running" : "done"));
  row._tname.textContent = taskRowTitle(row, note.agent, note.id);
  row._tname.title = note.id || "";
  row._modelLabel = null;
  applyWorkerModelInfo(row, note.model ? note : {model: row._workerModel, reasoning_effort: row._workerEffort});
  if (row._t0 != null) updateWorkerOverview(note.id, note.agent, err ? "failed" : note.status, row._taskActivity || row._taskPrompt, row);
  row._thint.textContent = taskMetrics(note);
  row._stat.textContent = taskStatusLabel(err ? "failed" : note.status) + (elapsed != null ? " · " + fmtDuration(elapsed) : "");
  row._stat.classList.toggle("err", !!err || note.status === "failed");
  row._taskSummary = note.summary || "";
  row._taskElapsed = elapsed;
  if (typeof registerLanguageRefresh === "function") registerLanguageRefresh(row, function () {
    row._tname.textContent = taskRowTitle(row, row._taskAgent, row._taskID);
    row._modelLabel = null;
    applyWorkerModelInfo(row, {model: row._workerModel, reasoning_effort: row._workerEffort});
    row._thint.textContent = taskMetrics({summary: row._taskSummary, agent: row._taskAgent, status: row._taskStatus});
    row._stat.textContent = taskStatusLabel(row._taskStatus) + (row._taskElapsed != null ? " · " + fmtDuration(row._taskElapsed) : "");
  });
  row._body.innerHTML = "";
  appendTaskBacklink(row);
  if (prompt) {
    row._body.appendChild(i18nEl("div", "lbl", "task.brief"));
    row._body.appendChild(el("div", "task-brief", prompt));
  }
  row._body.appendChild(i18nEl("div", "lbl", "task.activity"));
  var recorded = recordedWorkerActivity(row, note);
  if (recorded) {
    var expanded = Object.create(null);
    if (activity) activity.querySelectorAll('.task-activity-item').forEach(function (item) {
      var detail = item.querySelector('.activity-result');
      if (detail && detail.open && item.dataset.workerCallId) expanded[item.dataset.workerCallId] = true;
    });
    activity = el("div", "task-activity");
    row._activitySummary = false;
    var cancelActivity = renderToolPayloadWhenOpen(row, function () {
      row._cancelTaskActivity = null;
      fillRecordedWorkerActivity(activity, recorded, expanded);
    });
    if (!row.open) row._cancelTaskActivity = cancelActivity;
  } else if (!activity || row._activitySummary || !activity.querySelector(".task-activity-item")) {
    activity = el("div", "task-activity");
    row._activitySummary = true;
    if (String(note.tools || "").trim()) {
      String(note.tools).split(/,\s*/).map(function (name) { return name.trim(); }).filter(Boolean).forEach(function (name) {
        // Names-only history keeps the same tool-type colours as live calls.
        var item = el("div", "task-activity-item");
        applyActivityToolKind(item, name.replace(/\s*×\s*\d+$/, ""));
        item.appendChild(el("span", "activity-dot"));
        item.appendChild(workerToolNameNode(name));
        item.appendChild(el("span", "activity-hint", ""));
        item.appendChild(workerActivityStatus("task.statusUnconfirmed"));
        activity.appendChild(item);
      });
      var activityNote = i18nEl("div", "task-activity-note", "task.activitySummaryOnly");
      activityNote.hidden = true;
      activity.appendChild(activityNote);
      activity.title = t("task.activitySummaryOnly");
      activity.dataset.i18nTitle = "task.activitySummaryOnly";
    } else {
      activity.appendChild(i18nEl("div", "task-activity-empty", !history && (note.status === "running" || note.status === "created") ? "task.activityWaiting" : "task.activityUnavailable"));
    }
  }
  row._activity = activity;
  row._body.appendChild(activity);
  if (note.result) {
    row._body.appendChild(i18nEl("div", "lbl", "task.report"));
    var report = el("div", "task-report msg-assistant");
    row._body.appendChild(report);
    // Task rows start folded too; keep large reports out of the hidden DOM.
    var source = note.result;
    var cancel = renderToolPayloadWhenOpen(row, function () {
      row._cancelTaskReport = null;
      report.innerHTML = renderText(source);
      report.querySelectorAll("details[data-think-id]").forEach(function (d) { d.open = thinkingDisclosureOpen(history); });
    });
    if (!row.open) row._cancelTaskReport = cancel;
  }
  appendWorkerObservations(row);
}

function addHistoryTask(note, prompt, savedActivity) {
  var row = document.createElement("details");
  row.className = "tool-row task-row done";
  row.open = toolDisclosureOpen();
  var sum = el("summary");
  row._tname = el("span", "tname");
  row._thint = el("span", "thint");
  row._stat = el("span", "tstat");
  sum.appendChild(row._tname); sum.appendChild(row._thint); sum.appendChild(row._stat);
  row.appendChild(sum);
  row._body = el("div", "tbody");
  row.appendChild(row._body);
  if (note.id) {
    row._taskID = note.id;
    // Saved-history pages still select their latest row. A live invocation
    // (including a legacy run-zero one) keeps ownership over historical notes.
    if (!workerRows[note.id] || workerRows[note.id]._t0 == null) workerRows[note.id] = row;
  }
  row._taskPrompt = prompt || "";
  row._savedWorkerActivity = savedActivity || null;
  renderTaskResult(row, note, null, row._taskPrompt, note.status === "failed", true);
  appendStream(row);
  return row;
}
// Remote events keep server epoch times; local/legacy events retain the
// monotonic performance clock, including the originating client's behaviour.
function keepTaskClock(row) {
  clearInterval(row._clock);
  function update() { row._stat.textContent = taskStatusLabel("running") + " · " + fmtDuration(toolElapsed(row)); }
  update();
  row._clock = setInterval(update, 250);
}
function toolElapsed(row, at) {
  if (row._startedAtMS > 0) {
    var now = at > 0 ? at : (typeof syncServerNow === "function" ? syncServerNow() : Date.now());
    return Math.max(0, now - row._startedAtMS);
  }
  return Math.max(0, performance.now() - row._t0);
}

function addToolCall(name, args, id, at) {
  var info = toolHint(name, args);
  var row = document.createElement("details");
  row.className = "tool-row";
  applyToolKind(row, name);
  row.open = toolDisclosureOpen();
  var sum = el("summary");
  var title = el("span", "tname", info.name);
  title.title = name;
  var hint = el("span", "thint", info.hint);
  sum.appendChild(title);
  sum.appendChild(hint);
  var stat = el("span", "tstat");
  sum.appendChild(stat);
  row.appendChild(sum);
  var body = el("div", "tbody");
  body.hidden = !row.open;
  row._stat = stat; row._body = body; row._tname = title; row._thint = hint;
  row._toolName = name; row._toolArgs = args || "{}"; row._taskAgent = info.agent || ""; row._taskPrompt = info.prompt || "";
  // Like outputs, live inputs need no hidden DOM while the row is folded.
  // Task rows replace the input with their brief below; do not format it.
  if (!FILE_READ_TOOLS[name] && name !== "task" && name !== "send_message") {
    renderToolPayloadWhenOpen(row, function () {
      body.appendChild(i18nEl("div", "lbl", "tool.input"));
      body.appendChild(toolInputPre(args));
      // patch_file output still needs its path to pick the code language.
      if (!NUMBERED_CODE_TOOLS[name]) row._toolArgs = null;
      args = null;
    });
  }
  row.appendChild(body);
  row.addEventListener("toggle", function () { body.hidden = !row.open; });
  row._t0 = performance.now();
  row._startedAtMS = at > 0 ? at : 0;
  if (name === "task" || name === "send_message") {
    row.classList.add("task-row");
    if (info.workerID) {
      row._taskID = info.workerID;
      row._previousTaskRow = workerRows[info.workerID] || null;
      // Dispatch may only steer or be rejected. Transfer ownership only when
      // the worker's accepted started event binds this invocation below.
    }
    body.innerHTML = "";
    appendTaskBacklink(row);
    body.appendChild(i18nEl("div", "lbl", "task.brief"));
    body.appendChild(el("div", "task-brief", info.prompt || ""));
    body.appendChild(i18nEl("div", "lbl", "task.activity"));
    row._activity = el("div", "task-activity");
    row._activity.appendChild(i18nEl("div", "task-activity-empty", "task.activityWaiting"));
    row._workerCalls = {};
    body.appendChild(row._activity);
    // The full brief above is canonical; these unused JSON owners also share
    // the persistent visibility listener context. Release both after success.
    row._toolArgs = null;
    args = null;
  }
  row.classList.add("running");
  function updateElapsed() {
    stat.textContent = t("tool.running") + " · " + fmtDuration(toolElapsed(row));
  }
  updateElapsed();
  row._clock = setInterval(updateElapsed, 250);
  appendStream(row);
  var key = id || ("~" + openToolOrder.length);
  toolRows[key] = row;
  openToolOrder.push(key);
  runToolCount++;
  smartScroll();
}
function addToolResult(id, output, err, at) {
  var key = id && toolRows[id] ? id : openToolOrder[0];
  var row = toolRows[key];
  openToolOrder = openToolOrder.filter(function (k) { return k !== key; });
  if (!row) return;
  clearInterval(row._clock);
  row._clock = null;
  row.classList.remove("running");
  row.classList.add(err ? "failed" : "done");
  var ms = toolElapsed(row, at);
  var task = (row._toolName === "task" || row._toolName === "send_message") ? parseTaskNotification(output || err) : null;
  if (task) {
    if (task.id && !workerRows[task.id]) workerRows[task.id] = row;
    renderTaskResult(row, task, ms, row._taskPrompt, err);
    // A background start is only an acknowledgement: keep timing the worker
    // until its finished event, report or terminal snapshot clears the clock.
    if (!err && task.status === "running" && row._taskStatus === "running") keepTaskClock(row);
    smartScroll();
    return;
  }
  if (row._taskID) updateWorkerOverview(row._taskID, row._taskAgent, err ? "failed" : "done", row._taskPrompt, row);
  var payload = err || output || "";
  var changes = toolChangeStats(row._toolName, payload);
  var mutationLabel = mutationOutcomeLabel(row._toolName, payload, !!err);
  if (mutationLabel) row._tname.textContent = t(mutationLabel);
  if (changes.diff || mutationLabel) row.classList.add("has-changes");
  if (err) row._stat.classList.add("err");
  setToolResultStatus(row, ms, err, changes);
  appendToolMediaPreview(row, payload, row._toolName, !!err);
  // Live results start folded too. Large read/diff viewers should not occupy
  // the WebView DOM until the user actually expands this result.
  renderToolPayloadWhenOpen(row, function () {
    appendToolPayload(row._body, err ? t("tool.error") : t("tool.output"), payload, row._toolName, !!err, row._toolArgs);
  });
  smartScroll();
}

function addFileChanges(changes) {
  changes = superCliUI.normalizeFileChanges(changes);
  if (!changes.length) return;
  var kinds = ["created", "modified", "deleted"];
  var grouped = { created: [], modified: [], deleted: [] };
  changes.forEach(function (change) { grouped[change.kind].push(change); });

  var row = document.createElement("details");
  row.className = "file-change-summary";
  // A handful of changes is useful at a glance. Bulk operations stay compact
  // instead of adding hundreds of near-identical rows to the transcript.
  row.open = changes.length <= 8;
  var header = el("summary", "file-change-header");
  header.appendChild(el("span", "file-change-title", t("change.title") + " · " + changes.length));
  var counts = el("span", "file-change-counts");
  kinds.forEach(function (kind) {
    if (grouped[kind].length) {
      counts.appendChild(el("span", "file-change-count " + kind,
        t("change." + kind) + " " + grouped[kind].length));
    }
  });
  header.appendChild(counts);
  row.appendChild(header);

  var body = el("div", "file-change-body");
  if (changes.length <= 8) {
    var list = el("div", "file-change-list");
    changes.forEach(function (change) {
      var item = el("div", "file-change-item " + change.kind);
      item.appendChild(el("span", "file-change-kind", t("change." + change.kind)));
      item.appendChild(el("code", "file-change-path", String(change.path)));
      list.appendChild(item);
    });
    body.appendChild(list);
  } else {
    kinds.forEach(function (kind) {
      if (!grouped[kind].length) return;
      var group = document.createElement("details");
      group.className = "file-change-group " + kind;
      var groupHeader = el("summary", "file-change-group-header");
      groupHeader.appendChild(el("span", "file-change-group-kind",
        t("change." + kind) + " · " + grouped[kind].length));
      var root = commonFileChangeDirectory(grouped[kind]);
      if (root) groupHeader.appendChild(el("code", "file-change-root", root));
      group.appendChild(groupHeader);
      var groupList = el("div", "file-change-group-list");
      appendFileChangePathsWhenOpen(group, groupList, grouped[kind], root);
      group.appendChild(groupList);
      body.appendChild(group);
    });
  }
  row.appendChild(body);
  appendStream(row);
  smartScroll();
}

// Retain only this group's paths until it is opened. Large folded change
// summaries keep their counts and directory header without hidden path nodes.
function appendFileChangePathsWhenOpen(group, list, changes, root) {
  renderToolPayloadWhenOpen(group, function () {
    var paths = document.createDocumentFragment();
    changes.forEach(function (change) {
      var path = String(change.path);
      if (root && path.indexOf(root + "/") === 0) path = path.slice(root.length + 1);
      paths.appendChild(el("code", "file-change-path", path));
    });
    list.appendChild(paths);
    changes = null;
  });
}

function commonFileChangeDirectory(changes) {
  if (!changes.length) return "";
  var common = String(changes[0].path).replace(/\\/g, "/");
  var end = common.lastIndexOf("/");
  common = end < 0 ? "" : common.slice(0, end);
  for (var i = 1; i < changes.length && common.length; i++) {
    var path = String(changes[i].path).replace(/\\/g, "/");
    var dirEnd = path.lastIndexOf("/");
    if (dirEnd < 0) return "";
    var limit = Math.min(common.length, dirEnd);
    var keep = 0;
    while (keep < limit && common.charCodeAt(keep) === path.charCodeAt(keep)) keep++;
    if (keep === common.length && (keep === dirEnd || path.charAt(keep) === "/")) continue;
    if (keep === dirEnd && common.charAt(keep) === "/") common = common.slice(0, keep);
    else {
      var boundary = common.lastIndexOf("/", keep - 1);
      common = boundary < 0 ? "" : common.slice(0, boundary);
    }
  }
  return common;
}
function settleOpenTools() {
  openToolOrder.forEach(function (k) {
    var row = toolRows[k];
    // A parent stream ending says nothing about a detached worker's outcome.
    // Keep its verified lifecycle state until the terminal event/snapshot arrives.
    if (row && row._taskID && row._taskStatus) return;
    if (row) {
      clearInterval(row._clock);
      row._clock = null;
      row.classList.remove("running");
      row._stat.textContent = "—";
      if (row._taskID) updateWorkerOverview(row._taskID, row._taskAgent, "stopped", row._taskPrompt, row);
    }
  });
  openToolOrder = [];
}
function prettyJSON(s) {
  try { return JSON.stringify(JSON.parse(s), null, 2); } catch (e) { return s || ""; }
}
// highlightCodeHTML escapes everything it returns; null means plain text.
function highlightedPre(cls, text, lang) {
  var pre = el("pre", cls);
  var html = text && lang && typeof highlightCodeHTML === "function" ? highlightCodeHTML(text, lang) : null;
  if (html === null) pre.textContent = text;
  else pre.innerHTML = html;
  return pre;
}

// Argument names that carry file content; they use the target file's language.
var TOOL_CODE_FIELDS = {
  old: true, new: true, content: true, text: true, code: true, source: true,
  replacement: true, patch: true, diff: true, input: true,
};

// Tool input: one row per argument ("path  C:\x") instead of raw JSON, so
// braces/quotes disappear and multi-line strings show real lines. Display
// only; the model still receives JSON. Non-object or empty input stays JSON.
function toolInputPre(args) {
  var pretty = prettyJSON(args), parsed = null;
  try { parsed = JSON.parse(pretty || ""); } catch (e) {}
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed) || !Object.keys(parsed).length) {
    return highlightedPre("", pretty, "json");
  }
  var lang = toolPathLang(typeof parsed.file === "string" ? parsed.file : parsed.path);
  var view = el("div", "tool-input-view");
  appendToolInputFields(view, parsed, "", lang);
  return view;
}
function toolInputHasCode(obj) {
  return Object.keys(obj).some(function (key) {
    var value = obj[key];
    if (typeof value === "string") return value.indexOf("\n") >= 0 || !!TOOL_CODE_FIELDS[key];
    return Array.isArray(value) && value.some(function (v) {
      return v && typeof v === "object" && !Array.isArray(v) && toolInputHasCode(v);
    });
  });
}
function appendToolInputFields(view, obj, prefix, lang) {
  Object.keys(obj).forEach(function (key) {
    var value = obj[key], label = prefix + key;
    // patch_file "changes": list each replacement's fields, numbered from 1.
    if (Array.isArray(value) && value.length && value.every(function (v) { return v && typeof v === "object" && !Array.isArray(v); })) {
      value.forEach(function (item, i) { appendToolInputFields(view, item, label + "[" + (i + 1) + "].", lang); });
      return;
    }
    // Nested options (e.g. {"options":{"x":1}}) become "options.x" rows.
    if (value && typeof value === "object" && !Array.isArray(value) && Object.keys(value).length) {
      appendToolInputFields(view, value, label + ".", lang);
      return;
    }
    if (typeof value === "string" && (value.indexOf("\n") >= 0 || TOOL_CODE_FIELDS[key])) {
      // Replaced text red, replacement green, so old/new read like a diff.
      var side = /^old(_string|_text)?$/.test(key) ? " is-old" : /^new(_string|_text)?$/.test(key) ? " is-new" : "";
      view.appendChild(el("div", "tool-input-key" + side, label));
      var codeLang = key === "patch" || key === "diff" ? "diff" : TOOL_CODE_FIELDS[key] ? lang : "";
      view.appendChild(highlightedPre("tool-input-code" + side, value || " ", codeLang));
      return;
    }
    var row = el("div", "tool-input-field");
    row.appendChild(el("span", "tool-input-key", label));
    // Lists of plain values read as "a, b, c" rather than a JSON array.
    var plainList = Array.isArray(value) && value.length && value.every(function (v) { return v === null || typeof v !== "object"; });
    var shown = typeof value === "string" ? value : plainList ? value.map(String).join(", ") : JSON.stringify(value);
    var cls = typeof value === "string" || plainList ? "hl-str" : typeof value === "number" ? "hl-num" :
      typeof value === "boolean" || value === null ? "hl-lit" : "";
    if (cls) row.appendChild(el("span", "tool-input-val " + cls, shown));
    else row.appendChild(highlightedPre("tool-input-val", shown, "json"));
    view.appendChild(row);
  });
}

// Plain tool output: JSON gets JSON colours; logs get status-line colours
// (ok/PASS green, FAIL/panic/error red, warnings amber). Text never changes.
var OUT_OK_LINE = /^\s*(?:ok\s|PASS\b|--- PASS|✓)/;
var OUT_ERR_LINE = /^\s*(?:FAIL\b|--- FAIL|panic:|error:|ERROR\b|fatal:|✗)/i;
var OUT_WARN_LINE = /^\s*(?:warning:|WARN\b)/i;
function toolOutputPre(raw, isError) {
  var cls = "tool-output" + (isError ? " error" : "");
  if (!raw) return el("pre", cls, t("tool.empty"));
  // 03b-highlight.js may be absent (tests load single files); same limit then.
  var maxChars = typeof HL_MAX_CHARS === "number" ? HL_MAX_CHARS : 200000;
  if (isError || raw.length > maxChars) return el("pre", cls, raw);
  if (/^\s*[\[{]/.test(raw)) {
    try { JSON.parse(raw); return highlightedPre(cls, raw, "json"); } catch (e) {}
  }
  var lines = raw.split("\n"), classes = lines.map(function (line) {
    return OUT_ERR_LINE.test(line) ? "out-err" : OUT_OK_LINE.test(line) ? "out-ok" : OUT_WARN_LINE.test(line) ? "out-warn" : "";
  });
  var pre = el("pre", cls);
  if (!classes.some(Boolean)) { pre.textContent = raw; return pre; }
  pre.innerHTML = lines.map(function (line, i) {
    return classes[i] ? '<span class="' + classes[i] + '">' + escHtml(line) + "</span>" : escHtml(line);
  }).join("\n");
  return pre;
}

function appendTaskBacklink(row) {
  if (!row._previousTaskRow) return;
  var link = i18nEl("button", "task-backlink", "task.previous");
  link.type = "button";
  link.addEventListener("click", function () {
    row._previousTaskRow.open = true;
    row._previousTaskRow.scrollIntoView({ block: "center", behavior: "smooth" });
  });
  row._body.appendChild(link);
}

function findTaskRow(taskID, agentName, parentCallID, run) {
  if (parentCallID) return toolRows[parentCallID] || null;
  if (taskID && workerRows[taskID]) {
    var latest = workerRows[taskID];
    if (run == null || (latest._taskRun || 0) === run) return latest;
    // Versioned notifications can still finish the correct historical row.
    if (run > 0) {
      var rows = Object.values(toolRows);
      for (var j = rows.length - 1; j >= 0; j--) {
        if (rows[j]._taskID === taskID && rows[j]._taskRun === run) return rows[j];
      }
    }
    return null;
  }
  for (var i = openToolOrder.length - 1; i >= 0; i--) {
    var candidate = toolRows[openToolOrder[i]];
    if (candidate && !candidate._taskID && candidate._toolName === "task" &&
      (!agentName || candidate._taskAgent === agentName)) return candidate;
  }
  return null;
}

function workerModelInfo(model, effort) {
  if (!model) return "";
  return t("task.modelThinking").replace("{model}", model).replace("{effort}", effort || "—");
}
function applyWorkerModelInfo(row, ev) {
  if (!ev.model) return;
  row._workerModel = ev.model; row._workerEffort = ev.reasoning_effort;
  row._workerModelInfo = workerModelInfo(ev.model, ev.reasoning_effort);
  row.title = row._workerModelInfo;
  row._tname.title = row._workerModelInfo;
  if (!row._modelLabel) { row._modelLabel = el("small", "worker-model-info"); row._tname.appendChild(row._modelLabel); }
  row._modelLabel.textContent = row._workerModelInfo;
}
function addWorkerProgress(ev) {
  var row = findTaskRow(ev.id, ev.name, ev.parent_call_id, ev.run || undefined);
  if (!row) return false;
  if (ev.run && row._taskRun && ev.run < row._taskRun) return false;
  // Run-zero callbacks remain compatible when bound to their originating call;
  // an unknown terminal event cannot finish a known live invocation.
  if (!ev.run && ev.kind === "finished" && row._taskRun && !ev.parent_call_id) return false;
  if (workerStatusTerminal(row._taskStatus) && (!ev.run || !row._taskRun || ev.run === row._taskRun) &&
      (ev.kind === "started" || ev.kind === "tool_call")) return false;
  if (ev.id) {
    if (!workerRows[ev.id] || (ev.kind === "started" && !row._taskRun && (ev.run || !workerRows[ev.id]._taskRun))) workerRows[ev.id] = row;
    row._taskID = ev.id;
  }
  if (ev.kind === "started") {
    row._taskStatus = "running";
    row.classList.remove("done", "failed");
    row.classList.add("running");
    // Preserve the server-derived elapsed clock on ordinary/replayed starts.
    if (row._workerStatusUnconfirmed) row._stat.textContent = taskStatusLabel("running");
    row._workerStatusUnconfirmed = false;
    row._taskActivity = "";
    row._taskActivitySerial = (row._taskActivitySerial || 0) + 1;
    row._workerSnapshotMismatch = false;
    row._taskAgent = ev.name || row._taskAgent;
    row._taskRun = ev.run || row._taskRun || 0;
    row._tname.textContent = taskRowTitle(row, row._taskAgent, ev.id);
    row._tname.title = ev.id;
    // Reattach the label after the task title's textContent reset.
    row._modelLabel = null;
    applyWorkerModelInfo(row, ev);
    updateWorkerOverview(ev.id, row._taskAgent, "running", row._taskPrompt || ev.prompt, row);
    row._thint.textContent = clip(row._taskPrompt || ev.prompt || "", 90);
    return true;
  }
  if (ev.kind === "finished") {
    row._taskStatus = ev.status || "done";
    row._workerStatusUnconfirmed = false;
    row._taskActivitySerial = (row._taskActivitySerial || 0) + 1;
    row.classList.remove("running");
    row.classList.add(row._taskStatus === "failed" ? "failed" : "done");
    applyWorkerModelInfo(row, ev);
    clearInterval(row._clock);
    row._clock = null;
    row._stat.textContent = taskStatusLabel(ev.status) + (row._startedAtMS > 0 ? " · " + fmtDuration(toolElapsed(row, ev.at)) : "");
    updateWorkerOverview(ev.id, ev.name, ev.status, row._taskActivity || row._taskPrompt, row);
    return true;
  }
  if (!row._activity) row._activity = el("div", "task-activity");
  if (row._activitySummary) {
    row._activity.innerHTML = "";
    row._activitySummary = false;
    row._workerCalls = {};
  }
  var emptyActivity = row._activity.querySelector(".task-activity-empty");
  if (emptyActivity) emptyActivity.remove();
  if (!row._workerCalls) row._workerCalls = {};
  var item = ev.call_id ? row._workerCalls[ev.call_id] : null;
  if (ev.kind === "steering_delivered" || ev.kind === "steering_rejected") {
    var receiptKey = "steering:" + (ev.call_id || "");
    item = row._workerCalls[receiptKey];
    if (!item) {
      item = el("div", "task-activity-item");
      item.appendChild(el("span", "activity-dot"));
      item.appendChild(el("span", "activity-name", "send_message"));
      item.appendChild(el("span", "activity-hint"));
      item.appendChild(el("span", "activity-status"));
      row._activity.appendChild(item);
      if (ev.call_id) row._workerCalls[receiptKey] = item;
    }
    var rejected = ev.kind === "steering_rejected";
    item.classList.remove("done", "failed");
    item.classList.add(rejected ? "failed" : "done");
    item.querySelector(".activity-hint").textContent = clip(ev.prompt || "", 140);
    item.querySelector(".activity-status").textContent = t(rejected ? "task.failed" : "task.done");
    item.title = ev.err || ev.prompt || "";
  } else if (ev.kind === "tool_call") {
    if (row._workerStatusUnconfirmed) {
      row.classList.add("running");
      row._stat.textContent = taskStatusLabel("running");
      row._workerStatusUnconfirmed = false;
    }
    var info = toolHint(ev.tool || "tool", ev.args || "{}");
    row._taskActivity = workerActivityText({tool: ev.tool, args: ev.args});
    row._workerSnapshotMismatch = false;
    row._taskActivitySerial = (row._taskActivitySerial || 0) + 1;
    updateWorkerOverview(ev.id, ev.name, "running", row._taskActivity, row);
    item = el("div", "task-activity-item running");
    item.dataset.workerCallId = ev.call_id || "";
    applyActivityToolKind(item, ev.tool);
    item.appendChild(el("span", "activity-dot"));
    item.appendChild(workerToolNameNode(ev.tool));
    item.appendChild(el("span", "activity-hint", info.hint || ""));
    item.appendChild(i18nEl("span", "activity-status", "tool.running"));
    item._workerArgs = ev.args || "";
    setWorkerActivityDetail(item, item._workerArgs, "", "", ev.detail_handle, ev.partial);
    row._activity.appendChild(item);
    if (ev.call_id) row._workerCalls[ev.call_id] = item;
  } else if (ev.kind === "tool_result") {
    row._taskActivitySerial = (row._taskActivitySerial || 0) + 1;
    if (!item) {
      item = el("div", "task-activity-item");
      item.dataset.workerCallId = ev.call_id || "";
      applyActivityToolKind(item, ev.tool);
      item.appendChild(el("span", "activity-dot"));
      item.appendChild(workerToolNameNode(ev.tool));
      item.appendChild(el("span", "activity-hint", ""));
      item.appendChild(el("span", "activity-status"));
      row._activity.appendChild(item);
    }
    item.classList.remove("running");
    item.classList.add(ev.err ? "failed" : "done");
    var status = item.querySelector(".activity-status");
    var statusKey = ev.err ? "task.failed" : "task.done";
    status.textContent = t(statusKey);
    status.dataset.i18nText = statusKey;
    status.i18nTextNode = status.firstChild;
    setWorkerActivityDetail(item, item._workerArgs, ev.output || "", ev.err || "", ev.detail_handle, ev.partial);
    updateWorkerOverview(ev.id, ev.name, "running", null, row);
  }
  row._body.hidden = !row.open;
  smartScroll();
  return true;
}

function generationSpeedText(ev) {
  var rate = ev && ev.generation_tps;
  return typeof rate === "number" && Number.isFinite(rate) && rate > 0 ? rate.toFixed(1) + " tok/s" : "";
}

// Telemetry line: time · cache/eval/gen · cached% · think · tools
function addTurnMeta(ev, elapsed, toolCount, seq) {
	if (toolCount == null) toolCount = runToolCount;
  var parts = [fmtDuration(elapsed)];
  var evalTok = (ev.tok_in || 0) - (ev.tok_cached || 0);
  if (ev.tok_cached) {
    parts.push("cache " + fmtTok(ev.tok_cached) + " · eval " + fmtTok(evalTok) + " · gen " + fmtTok(ev.tok_out));
  } else if (ev.tok_total) {
    parts.push("in " + fmtTok(ev.tok_in) + " · gen " + fmtTok(ev.tok_out));
  }
  if (ev.cache_hit_pct) parts.push(ev.cache_hit_pct + "% " + t("run.cached"));
  if (ev.reasoning_tok) parts.push(t("run.think") + " " + fmtTok(ev.reasoning_tok));
	if (toolCount) parts.push(toolCount + " " + t("run.tools"));
  var line = el("div", "turn-meta");
	line.innerHTML = parts.map(function (p, i) { return i === 0 ? "<b>" + escHtml(p) + "</b>" : escHtml(p); }).join(" · ");
	var speed = generationSpeedText(ev);
  if (speed) {
    var speedNode = el("span", "generation-speed", " · " + speed);
    speedNode.dataset.i18nTitle = "generation.speed_hint";
    speedNode.title = t("generation.speed_hint");
    line.appendChild(speedNode);
  }
	appendStream(line);
  smartScroll();
}

// Native tool images use same-origin, session-scoped handles produced by the
// server. Treat all strings as data; never accept provider HTML or remote URLs.
function renderNativeToolPreview(row, preview) {
  if (preview._cancelNativeRender) preview._cancelNativeRender();
  preview._cancelNativeRender = null;
  var cancel = renderToolPayloadWhenOpen(row, function () {
    if (preview._nativePaths) {
      renderSentAttachments(preview, preview._nativePaths, "image");
      preview._nativePaths = null;
    }
    row._body.appendChild(preview);
    preview._cancelNativeRender = null;
  });
  if (!row.open) preview._cancelNativeRender = cancel;
}
function appendNativeToolImages(row, paths) {
  if (!row || !row._body || row._mediaPreview) return;
  paths = (paths || []).filter(function (path) {
    return typeof path === "string" && /^session:[A-Za-z0-9_-]+\/[a-f0-9]{64}\.(png|jpg|jpeg|gif|webp)$/.test(path);
  });
  if (!paths.length) return;
  var preview = el("div", "tool-media-preview tool-native-preview");
  preview._nativePaths = paths;
  row._mediaPreview = preview;
  // Keep folded media out of the body. Both image nodes and their src are
  // created only after opening, after the row's payload callback.
  renderNativeToolPreview(row, preview);
}
function moveHistoryToolImages(source, target) {
  var preview = source._mediaPreview;
  if (!preview) return;
  if (preview._cancelNativeRender) preview._cancelNativeRender();
  preview._cancelNativeRender = null;
  source._mediaPreview = null;
  if (!target._mediaPreview) {
    target._mediaPreview = preview;
    if (source.open) {
      target.open = true;
      target._body.hidden = false;
    }
    renderNativeToolPreview(target, preview);
  } else {
    // Canonical show/capture output is already visible; analysis adds no copy.
    preview._nativePaths = null;
    preview.remove();
  }
  source.remove();
}
