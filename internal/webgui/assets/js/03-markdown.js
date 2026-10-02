"use strict";

/* ═══ markdown-ish renderer (safe: escape first, then decorate) ═══ */

function codeCopyButtonHTML() {
  return '<button class="code-copy" type="button" data-i18n="code.copy" data-i18n-title="code.copy" data-i18n-aria="code.copy" title="' +
    escAttr(t("code.copy")) + '" aria-label="' + escAttr(t("code.copy")) + '">' + escHtml(t("code.copy")) + "</button>";
}

function copyTextFallback(text) {
  var active = document.activeElement, selection = window.getSelection && window.getSelection(), ranges = [];
  if (selection) for (var i = 0; i < selection.rangeCount; i++) ranges.push(selection.getRangeAt(i).cloneRange());
  var input = document.createElement("textarea");
  input.value = text;
  input.style.position = "fixed"; input.style.opacity = "0"; input.style.pointerEvents = "none";
  document.body.appendChild(input);
  try {
    input.select();
    if (!document.execCommand("copy")) throw new Error("Clipboard copy failed");
  } finally {
    input.remove();
    if (active && active.focus) active.focus({preventScroll: true});
    if (selection) { selection.removeAllRanges(); ranges.forEach(function (range) { selection.addRange(range); }); }
  }
}

async function copyCodeBlock(button) {
  if (!button || button.disabled) return;
  var pre = button.closest("pre"), code = pre && pre.querySelector("code");
  if (!code) return;
  // Capture this exact block when clicked, including newlines and indentation.
  // No per-frame DOM scans, inline handlers or extra backend/model requests.
  var text = code.textContent;
  button.disabled = true;
  try {
    try {
      if (typeof navigator === "undefined" || !navigator.clipboard || !navigator.clipboard.writeText) throw new Error("Clipboard API unavailable");
      await navigator.clipboard.writeText(text);
    } catch (error) { copyTextFallback(text); }
    toast(t("code.copied"));
  } catch (error) { toast(t("code.copyFailed")); }
  finally { button.disabled = false; }
}

function mdInline(s) {
  s = s.replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/__(.+?)__/g, "<strong>$1</strong>");
  s = s.replace(/(^|\s)\*([^*\s][^*]*?)\*(\s|$)/g, "$1<em>$2</em>$3");
  s = s.replace(/(^|\s)_([^_\s][^_]*?)_(\s|$)/g, "$1<em>$2</em>$3");
  s = s.replace(/`([^`]+)`/g, "<code>$1</code>");
  s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
  s = s.replace(/~~(.+?)~~/g, "<del>$1</del>");
  return s;
}

function renderMarkdownish(text) {
  var parts = String(text || "").split(/```/);
  var html = "";
  for (var i = 0; i < parts.length; i++) {
    if (i % 2 === 1) {
      var lang = "", code = parts[i], nl = code.indexOf("\n");
      if (nl > 0) { lang = code.slice(0, nl).trim(); code = code.slice(nl + 1); }
      html += '<pre data-lang="' + escAttr(lang) + '">' + codeCopyButtonHTML() + '<code>' + escHtml(code.replace(/\s+$/, "")) + "</code></pre>";
    } else {
      // Some providers emit empty HTML comment markers while transitioning
      // between reasoning and visible text. They carry no content and should
      // not appear as literal "<!-- -->" paragraphs. Fenced code is handled
      // by the branch above and remains byte-for-byte visible.
      html += mdBlocks(parts[i].replace(/^[ \t]*<!--\s*-->\s*$/gm, ""));
    }
  }
  return html;
}

function mdBlocks(text) {
  var lines = text.split("\n");
  var out = "", i = 0, inList = false, listType = "";
  function closeList() { if (inList) { out += "</" + listType + ">"; inList = false; listType = ""; } }
  while (i < lines.length) {
    var trimmed = lines[i].trim();
    i++;
    if (trimmed === "") { closeList(); continue; }
    if (/^(-{3,}|\*{3,}|_{3,})\s*$/.test(trimmed)) { closeList(); out += "<hr>"; continue; }
    var h = trimmed.match(/^(#{1,6})\s+(.+)/);
    if (h) { closeList(); out += "<h" + h[1].length + ">" + mdInline(escHtml(h[2])) + "</h" + h[1].length + ">"; continue; }
    if (trimmed.indexOf("> ") === 0 || trimmed === ">") {
      closeList();
      var bq = [trimmed.replace(/^>\s?/, "")];
      while (i < lines.length && lines[i].trim().indexOf(">") === 0) { bq.push(lines[i].trim().replace(/^>\s?/, "")); i++; }
      out += "<blockquote>" + bq.map(function (l) { return mdInline(escHtml(l)); }).join("<br>") + "</blockquote>";
      continue;
    }
    var ul = trimmed.match(/^[-*+]\s+(.+)/);
    if (ul) {
      if (!inList || listType !== "ul") { closeList(); out += "<ul>"; inList = true; listType = "ul"; }
      out += "<li>" + mdInline(escHtml(ul[1])) + "</li>";
      continue;
    }
    var ol = trimmed.match(/^\d+[.)]\s+(.+)/);
    if (ol) {
      if (!inList || listType !== "ol") { closeList(); out += "<ol>"; inList = true; listType = "ol"; }
      out += "<li>" + mdInline(escHtml(ol[1])) + "</li>";
      continue;
    }
    if (trimmed.indexOf("|") >= 0) {
      var tbl = [trimmed];
      while (i < lines.length && lines[i].trim().indexOf("|") >= 0) { tbl.push(lines[i].trim()); i++; }
      var sep = tbl.length >= 2 && /^\|[\s\-:|]+\|$/.test(tbl[1]);
      if (sep) { closeList(); out += renderTable(tbl); continue; }
      // Not a table: hand the run back to the normal block rules by rewinding
      // only. Re-inserting the lines with splice() duplicated every one of
      // them AND grew `lines` faster than `i` advanced, so a run of "|" lines
      // whose second line is not a separator row looped forever and built a
      // string until the engine threw. Every streamed table passes through
      // that state while its separator row is still arriving, which froze the
      // whole tab mid-answer. Rewinding leaves `i` past `trimmed`, so the
      // outer loop always makes progress.
      i -= tbl.length - 1;
    }
    closeList();
    var para = [trimmed];
    while (i < lines.length && lines[i].trim() !== "" &&
      !/^(#{1,6}\s|>\s|[-*+]\s|\d+[.)]\s|-{3,}|\|)/.test(lines[i].trim())) {
      para.push(lines[i].trim());
      i++;
    }
    out += "<p>" + mdInline(escHtml(para.join(" "))) + "</p>";
  }
  closeList();
  return out;
}

function markdownTableCells(line) {
  return line.replace(/^\||\|$/g, "").split("|").map(function (cell) { return cell.trim(); });
}

function markdownTableAlign(line) {
  return markdownTableCells(line).map(function (cell) {
    return /^:.*:$/.test(cell) ? "center" : /:$/.test(cell) ? "right" : "left";
  });
}

function markdownTableCellHTML(line, align, width, heading) {
  var cells = markdownTableCells(line), html = "", tag = heading ? "th" : "td";
  for (var i = 0; i < width; i++) {
    html += '<' + tag + ' style="text-align:' + (align[i] || "left") + '">' +
      mdInline(escHtml(cells[i] || "")) + '</' + tag + '>';
  }
  return html;
}

function renderTable(lines) {
  var width = markdownTableCells(lines[0]).length, align = markdownTableAlign(lines[1]);
  var html = '<div class="md-table-wrap"><table><thead><tr>' +
    markdownTableCellHTML(lines[0], align, width, true) + '</tr></thead><tbody>';
  for (var row = 2; row < lines.length; row++) {
    html += '<tr>' + markdownTableCellHTML(lines[row], align, width, false) + '</tr>';
  }
  return html + "</tbody></table></div>";
}

// renderText splits <thinking>/<think> reasoning into quiet blocks.
// Thinking is OPEN by default (it has value — project principle);
// the user can fold a block and the fold survives re-renders.
var _thinkId = 0;
function renderThinkBlock(text) {
  var id = "think-" + (++_thinkId);
  return '<details class="think-block" open data-think-id="' + id + '">' +
    '<summary><span>' + escHtml(t("role.thinking")) + '</span><span class="think-line"></span></summary>' +
    '<div class="think-content">' + renderMarkdownish(String(text).trim()) + "</div></details>";
}
function assistantTextParts(text, checkpoint) {
  var src = String(text || "");
  var parts = checkpoint && checkpoint.parts.length ? checkpoint.parts.slice() : [];
  function markdown(value) { if (value) parts.push({kind: "markdown", text: value}); }
  var outside = checkpoint ? checkpoint.offset : 0, inside = outside, depth = 0, thought = "", m;
  var renderedThinking = checkpoint ? checkpoint.renderedThinking : false;
  // Only a balanced closing boundary is immutable. Reparse the complete open
  // suffix so partial/nested/orphan/repeated protocol tags keep their semantics.
  var stableEnd = outside, stableCount = parts.length, stableThinking = renderedThinking;
  // Local servers are inconsistent: some use <think>, others <thinking>,
  // and a few emit a second opening marker or an orphan closing marker when
  // native reasoning_content switches back to visible content. A depth-aware
  // parser keeps nested/split streams renderable and never shows protocol tags
  // as assistant prose.
  var tags = /<\/?(?:thinking|think|reasoning|reflection)>/gi;
  tags.lastIndex = outside;
  while ((m = tags.exec(src)) !== null) {
    var closing = m[0].charAt(1) === "/";
    if (depth === 0) {
      if (m.index > outside) markdown(src.slice(outside, m.index));
      if (closing) {
        // Orphan close: provider/model both closed the same native channel.
        outside = tags.lastIndex;
        stableEnd = outside; stableCount = parts.length; stableThinking = renderedThinking;
        continue;
      }
      depth = 1;
      thought = "";
      inside = tags.lastIndex;
      outside = tags.lastIndex;
      continue;
    }
    thought += src.slice(inside, m.index);
    if (closing) depth--;
    else depth++;
    inside = tags.lastIndex;
    if (depth === 0) {
      if (thought.trim()) {
        // One assistant segment has one reasoning phase. Some local servers
        // incorrectly open the native channel again around the final answer;
        // keep the first block as reasoning and recover later blocks as prose.
        if (renderedThinking) markdown(thought);
        else parts.push({kind: "thinking", text: thought.trim()});
        renderedThinking = true;
      }
      thought = "";
      outside = tags.lastIndex;
      stableEnd = outside; stableCount = parts.length; stableThinking = renderedThinking;
    }
  }
  if (checkpoint && stableEnd !== checkpoint.offset) {
    checkpoint.offset = stableEnd;
    checkpoint.parts = parts.slice(0, stableCount);
    checkpoint.renderedThinking = stableThinking;
  }
  if (depth > 0) {
    thought += src.slice(inside);
    if (thought.trim()) {
      if (renderedThinking) markdown(thought);
      else parts.push({kind: "thinking", text: thought.trim()});
    }
  } else if (outside < src.length) {
    markdown(src.slice(outside));
  }
  return parts;
}

function renderText(text) {
  _thinkId = 0;
  return assistantTextParts(text).map(function (part) {
    return part.kind === "thinking" ? renderThinkBlock(part.text) : renderMarkdownish(part.text);
  }).join("");
}

