/* 04c-tool-views.js — readable views for common tool outputs.
   Each renderer parses the tool's stable text format and returns null when the
   text does not match, so the caller keeps the raw output. Display only: the
   model still receives the original text. Everything is built with
   textContent; links are created only for http(s) URLs. */

var TOOL_VIEWS = {
  list_dir: tvListDir,
  search_code: tvSearchLocations,
  web_search: tvWebResults,
  web_lookup: tvWebResults,
  web_fetch: tvWebFetch,
  goal: tvGoal,
};

// Returns true when a readable view replaced the raw output.
function renderRichToolPayload(body, label, raw, name, args) {
  var view = TOOL_VIEWS[name];
  if (!view || !raw) return false;
  var node = null;
  try { node = view(String(raw), args); } catch (e) { node = null; }
  if (!node) return false;
  body.appendChild(el("div", "lbl", label));
  body.appendChild(node);
  return true;
}

function tvLines(raw) { return String(raw).replace(/\r\n/g, "\n").split("\n"); }
function tvLang(path) {
  if (typeof toolPathLang === "function") return toolPathLang(path);
  var m = /\.([A-Za-z0-9+#_-]{1,12})(?::\d+(?:-\d+)?)?\s*$/.exec(String(path || ""));
  return m ? m[1].toLowerCase() : "";
}
function tvSize(bytes) { return typeof fmtSize === "function" ? fmtSize(bytes) : bytes + " B"; }
function tvPre(raw) {
  return typeof toolOutputPre === "function" ? toolOutputPre(raw, false) : el("pre", "tool-output", raw);
}
function tvLink(url, text, cls) {
  if (!/^https?:\/\//i.test(url || "")) return el("span", cls, text || url);
  var a = el("a", cls, text || url);
  a.href = url;
  a.target = "_blank";
  a.rel = "noopener noreferrer";
  return a;
}
function tvHead(title, count, note) {
  var head = el("div", "tv-head");
  head.appendChild(el("span", "tv-head-title", title));
  if (count != null) head.appendChild(el("span", "tv-count", String(count)));
  if (note) head.appendChild(el("span", "tv-head-note", note));
  return head;
}
function tvCodeRow(no, src, lang, hit) {
  var row = el("div", hit ? "file-code-line hit" : "file-code-line");
  row.appendChild(el("span", "file-line-no", no));
  var code = el("code", "file-line-code");
  // highlightCodeHTML escapes everything it returns, or null for plain text.
  var html = src && lang && typeof highlightCodeHTML === "function" ? highlightCodeHTML(src, lang) : null;
  if (html === null) code.textContent = src || " ";
  else code.innerHTML = html;
  row.appendChild(code);
  return row;
}

/* list_dir: "<root> contains N item(s)[ (scope)]:" then one entry per line:
   "dir/", "dir/ (depth limit)", "name (N bytes)", "x (symlink; not expanded)".
   Tree mode lists paths relative to root; nesting follows the path. */
var LIST_HEAD = /^(.*) contains (\d+) item\(s\)(?: \((.*)\))?:$/;
function tvListDir(raw) {
  var lines = tvLines(raw), head = LIST_HEAD.exec(lines[0] || "");
  if (!head) return null;
  var dirs = {}, roots = [], notes = [];
  for (var i = 1; i < lines.length; i++) {
    if (!lines[i]) continue;
    var entry = tvListEntry(lines[i]);
    if (!entry) { notes.push(lines[i]); continue; }
    var cut = entry.path.lastIndexOf("/");
    var parent = cut > 0 ? dirs[entry.path.slice(0, cut)] : null;
    (parent ? parent.children : roots).push(entry);
    if (entry.dir) dirs[entry.path] = entry;
  }
  if (!roots.length) return null;
  var view = el("div", "tv-tree");
  view.appendChild(tvHead(head[1], head[2], head[3]));
  (function walk(list, depth) {
    list.forEach(function (e) {
      var row = el("div", "tv-tree-row " + (e.dir ? "dir" : "file"));
      row.style.paddingLeft = (10 + depth * 16) + "px";
      row.appendChild(el("span", "tv-icon", e.dir ? "📁" : e.link ? "🔗" : "📄"));
      row.appendChild(el("span", "tv-name", e.name));
      if (e.note) row.appendChild(el("span", "tv-note", e.note));
      if (e.size != null) row.appendChild(el("span", "tv-size", tvSize(e.size)));
      view.appendChild(row);
      walk(e.children, depth + 1);
    });
  })(roots, 0);
  notes.forEach(function (n) { view.appendChild(el("div", "tv-foot", n)); });
  return view;
}
function tvListEntry(line) {
  var m;
  if ((m = /^(.+?) \(symlink; not expanded\)$/.exec(line))) return tvEntry(m[1], false, null, "symlink", true);
  if ((m = /^(.+?)\/(?: \((depth limit|not expanded)\))?$/.exec(line))) return tvEntry(m[1], true, null, m[2] || "");
  if ((m = /^(.+) \((\d+) bytes\)$/.exec(line))) return tvEntry(m[1], false, Number(m[2]), "");
  return null;
}
function tvEntry(path, dir, size, note, link) {
  return { path: path, name: path.slice(path.lastIndexOf("/") + 1) + (dir ? "/" : ""),
    dir: dir, size: size, note: note, link: !!link, children: [] };
}

/* search_code location mode: "path:line:text" per hit. Context mode (numbered
   lines under "== file ==" headers) is rendered by appendFileReadPayload. */
var SEARCH_LOC = /^(\S.*?):(\d+):(.*)$/;
function tvSearchLocations(raw) {
  if (/^>?\s*\d+ \|/m.test(raw) || /^==\s.*\s==$/m.test(raw)) return null;
  var groups = [], byFile = {}, notes = [], hits = 0, other = 0;
  tvLines(raw).forEach(function (line) {
    if (!line) return;
    var m = SEARCH_LOC.exec(line);
    if (m) {
      var g = byFile[m[1]];
      if (!g) { g = byFile[m[1]] = { file: m[1], rows: [] }; groups.push(g); }
      g.rows.push({ no: m[2], code: m[3] });
      hits++;
      return;
    }
    if (/^\[/.test(line)) notes.push(line);
    else other++;
  });
  if (!hits || other > hits) return null;
  var view = el("div", "tool-file-view tv-search");
  groups.forEach(function (g) {
    var section = el("div", "file-code-section");
    section.appendChild(el("span", "", g.file + " "));
    section.appendChild(el("span", "tv-count", String(g.rows.length)));
    view.appendChild(section);
    var lang = tvLang(g.file);
    g.rows.forEach(function (r) { view.appendChild(tvCodeRow(r.no, r.code, lang, true)); });
  });
  notes.forEach(function (n) { view.appendChild(el("div", "file-code-summary", n)); });
  return view;
}

/* web_search: "Results for "q" (engine: e):" then "N. Title", "   URL",
   "   snippet" blocks, followed by plain usage notes. */
function tvWebResults(raw) {
  var lines = tvLines(raw);
  if (!/^(?:Results|No results) for /.test(lines[0] || "")) return null;
  var cards = [], notes = [], card = null;
  for (var i = 1; i < lines.length; i++) {
    var line = lines[i], m = /^(\d+)\. (.*)$/.exec(line);
    if (m) { card = { title: m[2], url: "", snippet: [] }; cards.push(card); continue; }
    if (card && /^ {2,}\S/.test(line)) {
      if (!card.url) card.url = line.trim();
      else card.snippet.push(line.trim());
      continue;
    }
    if (line.trim()) { card = null; notes.push(line.trim()); }
  }
  var view = el("div", "tv-web");
  view.appendChild(tvHead(lines[0].replace(/:$/, ""), cards.length || null));
  cards.forEach(function (c) {
    var box = el("div", "tv-card");
    box.appendChild(tvLink(c.url, c.title, "tv-card-title"));
    if (c.url) box.appendChild(el("div", "tv-card-url", c.url));
    if (c.snippet.length) box.appendChild(el("div", "tv-card-snippet", c.snippet.join(" ")));
    view.appendChild(box);
  });
  notes.forEach(function (n) { view.appendChild(el("div", "tv-foot", n)); });
  return view;
}

// web_fetch: "URL: …", optional "Title: …", blank line, page text.
function tvWebFetch(raw) {
  var text = String(raw).replace(/\r\n/g, "\n");
  var m = /^URL: (\S+)\n(?:Title: (.*)\n)?\n?/.exec(text);
  if (!m) return null;
  var view = el("div", "tv-web");
  var head = el("div", "tv-card tv-fetch");
  head.appendChild(tvLink(m[1], m[2] || m[1], "tv-card-title"));
  if (m[2]) head.appendChild(el("div", "tv-card-url", m[1]));
  view.appendChild(head);
  var rest = text.slice(m[0].length);
  if (rest.trim()) view.appendChild(tvPre(rest));
  return view;
}

/* goal: task lines "[x] 1. title" ([x] done, [>] in progress, [~] skipped,
   [ ] pending), optionally prefixed with "- "; "added N tasks:" lists plain
   numbered titles. Other lines stay as notes; Markdown headings are bold. */
var GOAL_TASK = /^\s*(?:-\s*)?\[([ x>~])\]\s*(\d+)\.\s*(.*)$/;
var GOAL_STATES = { "x": ["done", "✓"], ">": ["active", "●"], "~": ["skipped", "–"], " ": ["pending", "○"] };
function tvGoal(raw) {
  var parts = [], tasks = 0, added = false;
  tvLines(raw).forEach(function (line) {
    var m = GOAL_TASK.exec(line);
    if (m) { parts.push({ mark: m[1], seq: m[2], title: m[3] }); tasks++; return; }
    var plain = added ? /^\s+(\d+)\.\s+(.*)$/.exec(line) : null;
    if (plain) { parts.push({ mark: " ", seq: plain[1], title: plain[2] }); tasks++; return; }
    if (!line.trim()) return;
    if (/^(?:added|proposed) /.test(line.trim())) added = true;
    parts.push({ note: line.trim() });
  });
  if (!tasks) return null;
  var view = el("div", "tv-goal");
  parts.forEach(function (p) {
    if (p.note != null) {
      var heading = /^#+\s/.test(p.note);
      view.appendChild(el("div", "tv-goal-note" + (heading ? " heading" : ""), p.note.replace(/^#+\s*/, "")));
      return;
    }
    var state = GOAL_STATES[p.mark] || GOAL_STATES[" "];
    var row = el("div", "tv-task " + state[0]);
    row.appendChild(el("span", "tv-task-mark", state[1]));
    row.appendChild(el("span", "tv-task-seq", p.seq + "."));
    row.appendChild(el("span", "tv-task-title", p.title));
    view.appendChild(row);
  });
  return view;
}
