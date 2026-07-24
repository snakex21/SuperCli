"use strict";

/* ═══ markdown-ish renderer (safe: escape first, then decorate) ═══ */

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
      html += '<pre data-lang="' + escAttr(lang) + '"><code>' + escHtml(code.replace(/\s+$/, "")) + "</code></pre>";
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
      for (var tj = tbl.length - 1; tj >= 1; tj--) lines.splice(i - (tbl.length - tj), 0, tbl[tj]);
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

function renderTable(lines) {
  function cells(line) {
    return line.replace(/^\||\|$/g, "").split("|").map(function (c) { return c.trim(); });
  }
  var head = cells(lines[0]), align = [];
  cells(lines[1]).forEach(function (c) {
    if (/^:.*:$/.test(c)) align.push("center");
    else if (/:$/.test(c)) align.push("right");
    else align.push("left");
  });
  var html = '<div class="md-table-wrap"><table><thead><tr>';
  head.forEach(function (c, idx) {
    html += '<th style="text-align:' + (align[idx] || "left") + '">' + mdInline(escHtml(c)) + "</th>";
  });
  html += "</tr></thead><tbody>";
  for (var r = 2; r < lines.length; r++) {
    var row = cells(lines[r]);
    html += "<tr>";
    for (var c = 0; c < head.length; c++) {
      html += '<td style="text-align:' + (align[c] || "left") + '">' + mdInline(escHtml(row[c] || "")) + "</td>";
    }
    html += "</tr>";
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
function renderText(text) {
  _thinkId = 0;
  var src = String(text || ""), html = "", outside = 0, inside = 0, depth = 0, thought = "", m;
  var renderedThinking = false;
  // Local servers are inconsistent: some use <think>, others <thinking>,
  // and a few emit a second opening marker or an orphan closing marker when
  // native reasoning_content switches back to visible content. A depth-aware
  // parser keeps nested/split streams renderable and never shows protocol tags
  // as assistant prose.
  var tags = /<\/?(?:thinking|think|reasoning|reflection)>/gi;
  while ((m = tags.exec(src)) !== null) {
    var closing = m[0].charAt(1) === "/";
    if (depth === 0) {
      if (m.index > outside) html += renderMarkdownish(src.slice(outside, m.index));
      if (closing) {
        // Orphan close: provider/model both closed the same native channel.
        outside = tags.lastIndex;
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
        html += renderedThinking ? renderMarkdownish(thought) : renderThinkBlock(thought);
        renderedThinking = true;
      }
      thought = "";
      outside = tags.lastIndex;
    }
  }
  if (depth > 0) {
    thought += src.slice(inside);
    if (thought.trim()) html += renderedThinking ? renderMarkdownish(thought) : renderThinkBlock(thought);
  } else if (outside < src.length) {
    html += renderMarkdownish(src.slice(outside));
  }
  return html;
}

