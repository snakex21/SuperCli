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

stream.addEventListener("click", function (event) {
  var button = event.target && event.target.closest && event.target.closest(".code-copy");
  if (!button || !stream.contains(button)) return;
  event.preventDefault();
  copyCodeBlock(button);
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
  transcriptFollowTail = nearBottom();
}, { passive: true });
function flushSmartScroll() {
  if (smartScrollFrame !== null) {
    cancelAnimationFrame(smartScrollFrame);
    smartScrollFrame = null;
  }
  if (!smartScrollPending) return;
  // Read geometry after the frame's text writes. Reading before a paced paint
  // followed the preceding line height and made the transcript jump next frame.
  if (!streamAppendTarget && (smartScrollForced || transcriptFollowTail)) stage.scrollTop = stage.scrollHeight;
  smartScrollPending = false;
  smartScrollForced = false;
}
function smartScroll(force) {
  if (streamAppendTarget) return;
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

function addUserMsg(text, seq, attachments) {
  hideWelcome();
  attachments = (attachments || []).slice();
  var m = el("div", "msg-user");
  var displayText = userMessageDisplayText(text, attachments);
  if (displayText) m.appendChild(el("div", "msg-user-text", displayText));
  renderSentAttachments(m, attachments);
  if (seq) addMessageRewind(m, seq, text);
  appendStream(m);
  smartScroll(true);
  return m;
}

function addMessageRewind(node, seq, text) {
  if (!node || !seq || node.querySelector(".msg-rewind")) return;
  var button = el("button", "msg-rewind", "↶ " + t("workflow.rewind"));
  button.type = "button";
  button.title = t("workflow.rewindHint");
  button.setAttribute("aria-label", t("workflow.rewind"));
  button.addEventListener("click", function (event) {
    event.preventDefault();
    event.stopPropagation();
    showRewindDialog(activeSessionID, seq, text, button);
  });
  node.appendChild(button);
}

async function showRewindDialog(sessionID, seq, text, trigger) {
  if (trigger) trigger.disabled = true;
  var preview = null;
  try {
    preview = await j("/api/checkpoint/rewind?session=" + encodeURIComponent(sessionID) + "&from_seq=" + seq);
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
    var ok = await rewindSession(sessionID, seq, text, input.value.trim(), !!(rewindFiles && rewindFiles.checked), trigger);
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

async function addLatestMessageRewind(node, text, attempts) {
  if (!node || !node.isConnected) return 0;
  attempts = Math.max(1, Number(attempts) || 1);
  for (var attempt = 0; attempt < attempts; attempt++) {
    var candidates = activeSessionID ? [activeSessionID] : [];
    // A very fast Stop can abort the SSE response before its initial session
    // event reaches the browser. Recover the newly created session from the
    // recent list, then confirm it by exact user-message content.
    if (!candidates.length) {
      try {
        var recent = await j("/api/sessions?limit=6");
        candidates = (recent || []).slice(0, 3).map(function (session) { return session.id; });
      } catch (listError) {}
    }
    for (var candidateIndex = 0; candidateIndex < candidates.length; candidateIndex++) {
      var sessionID = candidates[candidateIndex];
      try {
        var page = await j("/api/transcript?id=" + encodeURIComponent(sessionID) + "&limit=24");
        if (!node.isConnected) return 0;
        var messages = page.messages || [];
        for (var i = messages.length - 1; i >= 0; i--) {
          if (messages[i].role === "user" && messages[i].content === text) {
            activeSessionID = sessionID;
            transcriptSessionID = sessionID;
            addMessageRewind(node, messages[i].seq, text);
            return messages[i].seq;
          }
        }
      } catch (transcriptError) {}
    }
    if (attempt + 1 < attempts) {
      await new Promise(function (resolve) { setTimeout(resolve, 100 + attempt * 75); });
    }
  }
  return 0;
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
        var first = template.content.firstChild, last = block.node.lastChild;
        if (first && last && first.nodeType === 3 && last.nodeType === 3) {
          last.data += first.data;
          first.remove();
        }
        block.node.appendChild(template.content);
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
    // renderText. A persisted, folded thought does not need a Markdown DOM.
    var template = document.createElement("template");
    _thinkId = 0;
    template.innerHTML = renderThinkBlock("");
    container = template.content.firstElementChild;
    target = container.querySelector(".think-content");
    if (history) container.open = false;
  } else {
    container = target = document.createDocumentFragment();
  }
  var state = {kind: part.kind, node: container, text: part.text, markdown: null};
  function paint() {
    if (!state.markdown) state.markdown = markdownStream(target);
    updateMarkdownStream(state.markdown, state.text);
  }
  state.paint = paint;
  state.remove = function () {
    if (state.kind === "thinking") container.remove();
    else removeMarkdownSection(state.markdown);
  };
  if (history && part.kind === "thinking") {
    container.addEventListener("toggle", function reveal() {
      if (!container.open) return;
      paint();
      container.removeEventListener("toggle", reveal);
    });
  } else paint();
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
      if (!state || state.kind !== part.kind) {
        while (states.length > i) states.pop().remove();
        state = assistantPart(part, !!node._history);
        states.push(state);
        node.appendChild(state.node);
      } else {
        state.text = part.text;
        // Keep history thinking unmaterialized until the user opens it.
        if (state.markdown) state.paint();
      }
    }
    while (states.length > parts.length) states.pop().remove();
    // Only a complete paint may satisfy repeated done/EOF/seal flushes.
    node._renderedSource = node._displayParts ? null : node._raw;
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
  if (gap >= 1) {
    state.gaps.push(Math.min(600, gap));
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

function closeAssistantReasoning(node) {
  if (!node || !node._reasoningOpen) return;
  appendAssistantSource(node, "</thinking>\n");
  node._reasoningOpen = false;
}

function appendAssistantReasoning(node, text) {
  if (!node) return;
  if (!node._reasoningOpen) {
    appendAssistantSource(node, "<thinking>");
    node._reasoningOpen = true;
  }
  appendAssistantSource(node, text || "");
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
function workerLabel(id) { return String(id || "Worker").replace(/^worker-(\d+)$/, "Worker $1"); }
function resetWorkerOverview() {
  workerOverview = {};
  var panel = $("#worker-overview");
  if (panel) panel.hidden = true;
  var list = $("#worker-overview-list");
  if (list) list.replaceChildren();
}
function updateWorkerOverview(id, agentName, status, activity, row) {
  if (!id) return;
  var item = workerOverview[id];
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
  item.agent = agentName || item.agent || "";
  item.status = status || item.status || "running";
  if (activity != null) item.activity = activity;
  if (row) item.row = row;
  item.button.dataset.state = item.status;
  item.label.textContent = workerLabel(id) + (item.agent ? " · " + item.agent : "");
  item.detail.textContent = item.activity || "";
  item.state.textContent = taskStatusLabel(item.status);
  item.button.title = [id, item.activity].filter(Boolean).join(" · ");
  var items = Object.values(workerOverview).sort(function (a, b) {
    return a.id.localeCompare(b.id, undefined, { numeric: true });
  });
  items.forEach(function (w) { $("#worker-overview-list").appendChild(w.button); });
  var active = items.filter(function (w) { return w.status === "running"; }).length;
  $("#worker-overview-summary").textContent = t("task.workers") + " · " +
    t("task.active") + ": " + active + " / " + items.length;
  $("#worker-overview").hidden = false;
}
function taskRowTitle(row, agentName, id) {
  return (id ? workerLabel(id) : t("task.delegation")) +
    (agentName ? " · " + agentName : "") +
    (row._toolName === "send_message" ? " · " + t("task.continue") : "");
}

var openToolOrder = []; // ids without results yet

function toolDisplayName(name) {
  var key = "tool." + name;
  var translated = t(key);
  return translated === key ? name : translated;
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

function appendFileReadPayload(body, text) {
  var viewer = el("div", "tool-file-view");
  String(text == null ? "" : text).split(/\r?\n/).forEach(function (line) {
    var numbered = line.match(/^\s*(\d+)\s*\|\s?(.*)$/);
    if (numbered) {
      var row = el("div", "file-code-line");
      row.appendChild(el("span", "file-line-no", numbered[1]));
      row.appendChild(el("code", "file-line-code", numbered[2] || " "));
      viewer.appendChild(row);
      return;
    }
    var section = line.match(/^==\s*(.*?)\s*==$/);
    if (section) {
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
  String(text || "").split(/\r?\n/).forEach(function (line) {
    if (/^\+(?!\+\+)/.test(line)) { stats.added++; stats.diff = true; }
    else if (/^-(?!---)/.test(line)) { stats.removed++; stats.diff = true; }
  });
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
    render();
  }
  row.addEventListener("toggle", renderIfOpen);
  renderIfOpen();
  return function () {
    rendered = true;
    row.removeEventListener("toggle", renderIfOpen);
  };
}

function appendHistoryToolPayload(row, body, args, text, name) {
  return renderToolPayloadWhenOpen(row, function () {
    if (args && !FILE_READ_TOOLS[name]) {
      body.appendChild(i18nEl("div", "lbl", "tool.input"));
      body.appendChild(el("pre", "", prettyJSON(args)));
    }
    appendToolPayload(body, t("tool.output"), text || "", name, false);
  });
}

// Media is user-visible output, not hidden diagnostic JSON. Its thumbnail stays
// outside folded tool details in both live and restored conversations.
function toolMediaDescriptor(text, name, isError) {
  if ((name === "send_screenshot" || name === "show_media" || name === "generate_image" || name === "generate_video" || name === "headless_control") && !isError) {
    try {
      var media = JSON.parse(String(text == null ? "" : text));
      var mediaPath = media && media.path;
      // Accept only recognized local file metadata; never arbitrary model HTML.
      if (typeof mediaPath === "string" && !/[\x00-\x1f]/.test(mediaPath) &&
          /^(?:[a-z]:[\\/]|\/|\\\\)/i.test(mediaPath) &&
          !media.save_error && !media.error &&
          ((name !== "send_screenshot" && name !== "headless_control") || media.type === "image") &&
          (name !== "headless_control" || media.source === "qmp" || media.source === "browser") &&
          media.type === attachmentMimeKind(media.media_type) &&
          ["image", "video", "audio"].indexOf(media.type) >= 0) {
        var previewPath = (name === "send_screenshot" || name === "headless_control") && typeof media.preview_path === "string" &&
          /^snapshot:[A-Za-z0-9._-]+$/.test(media.preview_path) ? media.preview_path : "";
        return {path: mediaPath, kind: media.type, previewPath: previewPath};
      }
    } catch (e) {}
  }
  return null;
}

function appendToolMediaPreview(row, text, name, isError) {
  if (!row || !row.parentNode || row._mediaPreview) return;
  var media = toolMediaDescriptor(text, name, isError);
  if (!media) return;
  var preview = el("div", "tool-media-preview");
  if (row.classList.contains("transcript-live")) preview.classList.add("transcript-live");
  renderSentAttachments(preview, [media.path], media.kind, media.previewPath);
  preview.querySelectorAll("img").forEach(function (image) {
    image.addEventListener("load", function () { smartScroll(); }, {once: true});
  });
  row.parentNode.insertBefore(preview, row.nextSibling);
  row._mediaPreview = preview;
}

function appendToolPayload(body, label, text, name, isError) {
  var raw = String(text == null ? "" : text);
  if (FILE_READ_TOOLS[name] && !isError) {
    appendFileReadPayload(body, raw);
    return;
  }
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
    body.appendChild(el("pre", "tool-output" + (isError ? " error" : ""), raw || t("tool.empty")));
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
  var src = String(text || "");
  if (src.indexOf("<task-notification>") < 0) return null;
  function field(name) {
    var open = "<" + name + ">", close = "</" + name + ">";
    var from = src.indexOf(open);
    var to = name === "result" ? src.lastIndexOf(close) : src.indexOf(close, from + open.length);
    if (from < 0 || to < from) return "";
    return src.slice(from + open.length, to).trim();
  }
  return {
    id: field("task-id"), agent: field("agent") || "worker",
    status: field("status") || "done", summary: field("summary"),
    tools: field("tools"), result: field("result"),
  };
}

function taskStatusLabel(status) {
  if (status === "running") return t("tool.running");
  if (status === "failed") return t("task.failed");
  if (status === "stopped") return t("task.stopped");
  return t("task.done");
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
  var agent = String(note.agent || "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  var status = String(note.status || "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return parts.join(" · ") || summary.replace(new RegExp("^" + agent + "\\s+" + status + "\\s*[·-]?\\s*", "i"), "");
}

function renderTaskResult(row, note, elapsed, prompt, err, history) {
  // A replacement result must release its previous unopened report callback.
  if (row._cancelTaskReport) row._cancelTaskReport();
  row._cancelTaskReport = null;
  var activity = row._activity;
  row.classList.add("task-row");
  row.classList.remove("running", "done", "failed");
  row.classList.add(err || note.status === "failed" ? "failed" : (note.status === "running" ? "running" : "done"));
  row._tname.textContent = taskRowTitle(row, note.agent, note.id);
  row._tname.title = note.id || "";
  if (row._t0 != null) updateWorkerOverview(note.id, note.agent, err ? "failed" : note.status, row._taskPrompt, row);
  row._thint.textContent = taskMetrics(note);
  row._stat.textContent = taskStatusLabel(err ? "failed" : note.status) + (elapsed != null ? " · " + fmtDuration(elapsed) : "");
  row._stat.classList.toggle("err", !!err || note.status === "failed");
  row._body.innerHTML = "";
  appendTaskBacklink(row);
  if (prompt) {
    row._body.appendChild(i18nEl("div", "lbl", "task.brief"));
    row._body.appendChild(el("div", "task-brief", prompt));
  }
  if (activity && activity.childNodes.length) {
    row._body.appendChild(i18nEl("div", "lbl", "task.activity"));
    row._body.appendChild(activity);
  } else if (note.tools) {
    row._body.appendChild(i18nEl("div", "lbl", "task.activity"));
    activity = el("div", "task-activity");
    String(note.tools).split(/,\s*/).filter(Boolean).forEach(function (name) {
      var item = el("div", "task-activity-item done");
      item.appendChild(el("span", "activity-dot"));
      item.appendChild(el("span", "activity-name", name));
      item.appendChild(el("span", "activity-hint", ""));
      item.appendChild(i18nEl("span", "activity-status", "task.done"));
      activity.appendChild(item);
    });
    row._activity = activity;
    row._body.appendChild(activity);
  }
  if (note.result) {
    row._body.appendChild(i18nEl("div", "lbl", "task.report"));
    var report = el("div", "task-report msg-assistant");
    row._body.appendChild(report);
    // Task rows start folded too; keep large reports out of the hidden DOM.
    var source = note.result;
    var cancel = renderToolPayloadWhenOpen(row, function () {
      row._cancelTaskReport = null;
      report.innerHTML = renderText(source);
      if (history) report.querySelectorAll("details[data-think-id]").forEach(function (d) { d.open = false; });
    });
    if (!row.open) row._cancelTaskReport = cancel;
  }
}

function addHistoryTask(note) {
  var row = document.createElement("details");
  row.className = "tool-row task-row done";
  var sum = el("summary");
  row._tname = el("span", "tname");
  row._thint = el("span", "thint");
  row._stat = el("span", "tstat");
  sum.appendChild(row._tname); sum.appendChild(row._thint); sum.appendChild(row._stat);
  row.appendChild(sum);
  row._body = el("div", "tbody");
  row.appendChild(row._body);
  if (note.id) { row._taskID = note.id; workerRows[note.id] = row; }
  renderTaskResult(row, note, null, "", note.status === "failed", true);
  appendStream(row);
  return row;
}
function addToolCall(name, args, id) {
  var info = toolHint(name, args);
  var row = document.createElement("details");
  row.className = "tool-row";
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
  body.hidden = true;
  if (!FILE_READ_TOOLS[name]) {
    var lblA = i18nEl("div", "lbl", "tool.input");
    body.appendChild(lblA);
    body.appendChild(el("pre", "", prettyJSON(args)));
  }
  row.appendChild(body);
  row.addEventListener("toggle", function () { body.hidden = !row.open; });
  row._stat = stat; row._body = body; row._tname = title; row._thint = hint;
  row._toolName = name; row._toolArgs = args || "{}"; row._taskAgent = info.agent || ""; row._taskPrompt = info.prompt || "";
  row._t0 = performance.now();
  if (name === "task" || name === "send_message") {
    row.classList.add("task-row");
    if (info.workerID) {
      row._taskID = info.workerID;
      row._previousTaskRow = workerRows[info.workerID] || null;
      workerRows[info.workerID] = row;
    }
    body.innerHTML = "";
    appendTaskBacklink(row);
    body.appendChild(i18nEl("div", "lbl", "task.brief"));
    body.appendChild(el("div", "task-brief", info.prompt || ""));
    body.appendChild(i18nEl("div", "lbl", "task.activity"));
    row._activity = el("div", "task-activity");
    row._workerCalls = {};
    body.appendChild(row._activity);
  }
  row.classList.add("running");
  function updateElapsed() {
    stat.textContent = t("tool.running") + " · " + fmtDuration(performance.now() - row._t0);
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
function addToolResult(id, output, err) {
  var key = id && toolRows[id] ? id : openToolOrder[0];
  var row = toolRows[key];
  openToolOrder = openToolOrder.filter(function (k) { return k !== key; });
  if (!row) return;
  clearInterval(row._clock);
  row._clock = null;
  row.classList.remove("running");
  row.classList.add(err ? "failed" : "done");
  var ms = performance.now() - row._t0;
  var task = (row._toolName === "task" || row._toolName === "send_message") ? parseTaskNotification(output || err) : null;
  if (task) {
    if (task.id) workerRows[task.id] = row;
    renderTaskResult(row, task, ms, row._taskPrompt, err);
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
    appendToolPayload(row._body, err ? t("tool.error") : t("tool.output"), payload, row._toolName, !!err);
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
      grouped[kind].forEach(function (change) {
        var path = String(change.path);
        if (root && path.indexOf(root + "/") === 0) path = path.slice(root.length + 1);
        groupList.appendChild(el("code", "file-change-path", path));
      });
      group.appendChild(groupList);
      body.appendChild(group);
    });
  }
  row.appendChild(body);
  appendStream(row);
  smartScroll();
}

function commonFileChangeDirectory(changes) {
  if (!changes.length) return "";
  var common = String(changes[0].path).replace(/\\/g, "/").split("/").slice(0, -1);
  for (var i = 1; i < changes.length && common.length; i++) {
    var parts = String(changes[i].path).replace(/\\/g, "/").split("/").slice(0, -1);
    var keep = 0;
    while (keep < common.length && keep < parts.length && common[keep] === parts[keep]) keep++;
    common.length = keep;
  }
  return common.join("/");
}
function settleOpenTools() {
  openToolOrder.forEach(function (k) {
    var row = toolRows[k];
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

function findTaskRow(taskID, agentName, parentCallID) {
  if (parentCallID) return toolRows[parentCallID] || null;
  if (taskID && workerRows[taskID]) return workerRows[taskID];
  for (var i = openToolOrder.length - 1; i >= 0; i--) {
    var candidate = toolRows[openToolOrder[i]];
    if (candidate && !candidate._taskID && candidate._toolName === "task" &&
      (!agentName || candidate._taskAgent === agentName)) return candidate;
  }
  return null;
}

function addWorkerProgress(ev) {
  var row = findTaskRow(ev.id, ev.name, ev.parent_call_id);
  if (!row) return false;
  if (ev.id) { workerRows[ev.id] = row; row._taskID = ev.id; }
  if (ev.kind === "started") {
    row._taskAgent = ev.name || row._taskAgent;
    row._taskRun = ev.run || 1;
    row._tname.textContent = taskRowTitle(row, row._taskAgent, ev.id);
    row._tname.title = ev.id;
    updateWorkerOverview(ev.id, row._taskAgent, "running", row._taskPrompt || ev.prompt, row);
    row._thint.textContent = clip(row._taskPrompt || ev.prompt || "", 90);
    return true;
  }
  if (ev.kind === "finished") {
    clearInterval(row._clock);
    row._clock = null;
    row._stat.textContent = taskStatusLabel(ev.status);
    updateWorkerOverview(ev.id, ev.name, ev.status, row._taskPrompt, row);
    return true;
  }
  if (!row._activity) row._activity = el("div", "task-activity");
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
    var info = toolHint(ev.tool || "tool", ev.args || "{}");
    updateWorkerOverview(ev.id, ev.name, "running", (info.name || ev.tool) + (info.hint ? " · " + info.hint : ""), row);
    item = el("div", "task-activity-item running");
    item.appendChild(el("span", "activity-dot"));
    item.appendChild(el("span", "activity-name", ev.tool || "tool"));
    item.appendChild(el("span", "activity-hint", info.hint || ""));
    item.appendChild(i18nEl("span", "activity-status", "tool.running"));
    row._activity.appendChild(item);
    if (ev.call_id) row._workerCalls[ev.call_id] = item;
  } else if (ev.kind === "tool_result") {
    if (!item) {
      item = el("div", "task-activity-item");
      item.appendChild(el("span", "activity-dot"));
      item.appendChild(el("span", "activity-name", ev.tool || "tool"));
      item.appendChild(el("span", "activity-hint", ""));
      item.appendChild(el("span", "activity-status"));
      row._activity.appendChild(item);
    }
    item.classList.remove("running");
    item.classList.add(ev.err ? "failed" : "done");
    item.querySelector(".activity-status").textContent = ev.err ? t("task.failed") : t("task.done");
    if (ev.err || ev.output) item.title = ev.err || ev.output;
    updateWorkerOverview(ev.id, ev.name, "running", row._taskPrompt, row);
  }
  row._body.hidden = !row.open;
  smartScroll();
  return true;
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
	appendStream(line);
  smartScroll();
}

// Native tool images use same-origin, session-scoped handles produced by the
// server. Treat all strings as data; never accept provider HTML or remote URLs.
function appendNativeToolImages(row, paths) {
  if (!row || !row.parentNode || row._mediaPreview) return;
  paths = (paths || []).filter(function (path) {
    return typeof path === "string" && /^session:[A-Za-z0-9_-]+\/[a-f0-9]{64}\.(png|jpg|jpeg|gif|webp)$/.test(path);
  });
  if (!paths.length) return;
  var preview = el("div", "tool-media-preview");
  renderSentAttachments(preview, paths, "image");
  row.parentNode.insertBefore(preview, row.nextSibling);
  row._mediaPreview = preview;
}
