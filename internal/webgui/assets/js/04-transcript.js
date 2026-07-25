"use strict";

/* ═══ transcript ═══ */

var stage = $("#stage"), stream = $("#stream"), welcome = $("#welcome");
var streamAppendTarget = null;

function appendStream(node) {
  (streamAppendTarget || stream).appendChild(node);
}

function nearBottom() { return stage.scrollHeight - stage.scrollTop - stage.clientHeight < 120; }
function smartScroll(force) {
  if (streamAppendTarget) return;
  if (force || nearBottom()) stage.scrollTop = stage.scrollHeight;
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
  panel.appendChild(el("div", "question-kicker", t("workflow.rewindWhy")));
  panel.appendChild(el("div", "question-option-desc", t("workflow.rewindWhyHint")));
	panel.appendChild(el("div", "question-option-desc rewind-warning", t("workflow.rewindPermanent")));
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
    fileCopy.appendChild(el("strong", "", t("workflow.rewindFiles")));
    fileCopy.appendChild(el("span", "question-option-desc",
      preview.checkpoints + " " + t("workflow.rewindFilesHint").replace("file(s)", (preview.files || []).length + " file(s)").replace("plików", (preview.files || []).length + " plików")));
    fileOption.appendChild(fileCopy);
    panel.appendChild(fileOption);
  }
  var actions = el("div", "question-actions");
  var cancel = el("button", "btn", t("common.cancel"));
  var confirm = el("button", "btn primary", t("workflow.rewindContinue"));
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

async function addLatestMessageRewind(node, text) {
  if (!node || !node.isConnected || !activeSessionID) return 0;
  var sessionID = activeSessionID;
  try {
    var page = await j("/api/transcript?id=" + encodeURIComponent(sessionID) + "&limit=24");
    if (sessionID !== activeSessionID || !node.isConnected) return 0;
    var messages = page.messages || [];
    for (var i = messages.length - 1; i >= 0; i--) {
      if (messages[i].role === "user" && messages[i].content === text) {
        addMessageRewind(node, messages[i].seq, text);
        return messages[i].seq;
      }
    }
  } catch (e) {}
  return 0;
}
function addAssistantMsg() {
  var m = el("div", "msg-assistant");
  m._raw = "";
  m._renderTimer = null;
  appendStream(m);
  return m;
}
function renderAssistant(node) {
  // Thinking is open by default; preserve the blocks the user FOLDED
  // across streaming re-renders (ids are positional and stable).
  var closed = {};
  node.querySelectorAll("details[data-think-id]:not([open])").forEach(function (d) {
    closed[d.dataset.thinkId] = true;
  });
  // A renderer failure must never end the stream. renderAssistant runs from a
  // timer and from the SSE handler; letting it throw there stopped every later
  // chunk from reaching the screen for the rest of the session. Falling back to
  // plain text keeps the answer readable and lets the next chunk try again.
  var html;
  try {
    html = renderText(node._raw);
  } catch (renderErr) {
    node.textContent = node._raw;
    if (window.console && console.error) console.error("renderAssistant", renderErr);
    return;
  }
  node.innerHTML = html;
  node.querySelectorAll("details[data-think-id]").forEach(function (d) {
    if (closed[d.dataset.thinkId]) d.open = false;
  });
}

// Providers often send a token in each SSE frame. Rebuilding all accumulated
// Markdown for every token makes long answers progressively more expensive and
// can freeze the WebView UI. Batch chunks and render at an adaptive cadence.
function scheduleAssistantRender(node) {
  if (!node || node._renderTimer !== null) return;
  var n = node._raw.length;
  var delay = n > 48000 ? 250 : (n > 16000 ? 120 : (n > 4000 ? 60 : 40));
  node._renderTimer = setTimeout(function () {
    node._renderTimer = null;
    renderAssistant(node);
    smartScroll();
  }, delay);
}

function flushAssistantRender(node) {
  if (!node) return;
  if (node._renderTimer !== null) {
    clearTimeout(node._renderTimer);
    node._renderTimer = null;
  }
  renderAssistant(node);
  smartScroll();
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
var workerRows = {}; // worker task id -> delegated-task row
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
        name: t("task.delegation") + " · " + kind + (a.advise ? " (advise)" : ""),
        hint: clip(a.prompt || "", 90), agent: kind, prompt: a.prompt || "",
      };
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
        meta.appendChild(el("span", execution.exit_code === 0 ? "ok" : "err", "exit " + execution.exit_code));
        if (execution.duration_ms != null) meta.appendChild(el("span", "", fmtDuration(Number(execution.duration_ms))));
        if (execution.workdir) meta.appendChild(el("span", "", execution.workdir));
        body.appendChild(meta);
        if (execution.stdout) appendToolPayload(body, t("tool.stdout"), execution.stdout, "", false);
        if (execution.stderr) appendToolPayload(body, t("tool.stderr"), execution.stderr, "", true);
        if (!execution.stdout && !execution.stderr) body.appendChild(el("pre", "tool-output", "(empty)"));
        return;
      }
    } catch (e) {}
  }
  var changes = toolChangeStats(name, raw);
  if (!changes.diff) {
    body.appendChild(el("pre", "tool-output" + (isError ? " error" : ""), raw || "(empty)"));
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

function renderTaskResult(row, note, elapsed, prompt, err) {
  var activity = row._activity;
  row.classList.add("task-row");
  row.classList.remove("running", "done", "failed");
  row.classList.add(err || note.status === "failed" ? "failed" : (note.status === "running" ? "running" : "done"));
  row._tname.textContent = t("task.delegation") + " · " + note.agent;
  row._thint.textContent = taskMetrics(note);
  row._stat.textContent = taskStatusLabel(err ? "failed" : note.status) + (elapsed != null ? " · " + fmtDuration(elapsed) : "");
  row._stat.classList.toggle("err", !!err || note.status === "failed");
  row._body.innerHTML = "";
  if (prompt) {
    row._body.appendChild(el("div", "lbl", t("task.brief")));
    row._body.appendChild(el("div", "task-brief", prompt));
  }
  if (activity && activity.childNodes.length) {
    row._body.appendChild(el("div", "lbl", t("task.activity")));
    row._body.appendChild(activity);
  } else if (note.tools) {
    row._body.appendChild(el("div", "lbl", t("task.activity")));
    activity = el("div", "task-activity");
    String(note.tools).split(/,\s*/).filter(Boolean).forEach(function (name) {
      var item = el("div", "task-activity-item done");
      item.appendChild(el("span", "activity-dot"));
      item.appendChild(el("span", "activity-name", name));
      item.appendChild(el("span", "activity-hint", ""));
      item.appendChild(el("span", "activity-status", t("task.done")));
      activity.appendChild(item);
    });
    row._activity = activity;
    row._body.appendChild(activity);
  }
  if (note.result) {
    row._body.appendChild(el("div", "lbl", t("task.report")));
    var report = el("div", "task-report msg-assistant");
    report.innerHTML = renderText(note.result);
    row._body.appendChild(report);
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
  renderTaskResult(row, note, null, "", note.status === "failed");
  row.querySelectorAll("details[data-think-id]").forEach(function (d) { d.open = false; });
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
    var lblA = el("div", "lbl", t("tool.input"));
    body.appendChild(lblA);
    body.appendChild(el("pre", "", prettyJSON(args)));
  }
  row.appendChild(body);
  row.addEventListener("toggle", function () { body.hidden = !row.open; });
  row._stat = stat; row._body = body; row._tname = title; row._thint = hint;
  row._toolName = name; row._toolArgs = args || "{}"; row._taskAgent = info.agent || ""; row._taskPrompt = info.prompt || "";
  row._t0 = performance.now();
  if (name === "task") {
    row.classList.add("task-row");
    body.innerHTML = "";
    body.appendChild(el("div", "lbl", t("task.brief")));
    body.appendChild(el("div", "task-brief", info.prompt || ""));
    body.appendChild(el("div", "lbl", t("task.activity")));
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
  var task = row._toolName === "task" ? parseTaskNotification(output || err) : null;
  if (task) {
    if (task.id) workerRows[task.id] = row;
    renderTaskResult(row, task, ms, row._taskPrompt, err);
    smartScroll();
    return;
  }
  var payload = err || output || "";
  var changes = toolChangeStats(row._toolName, payload);
  var mutationLabel = mutationOutcomeLabel(row._toolName, payload, !!err);
  if (mutationLabel) row._tname.textContent = t(mutationLabel);
  if (changes.diff || mutationLabel) row.classList.add("has-changes");
  if (err) row._stat.classList.add("err");
  setToolResultStatus(row, ms, err, changes);
  appendToolPayload(row._body, err ? t("tool.error") : t("tool.output"), payload, row._toolName, !!err);
  smartScroll();
}

function addFileChanges(changes) {
  changes = superCliUI.normalizeFileChanges(changes);
  if (!changes.length) return;
  var row = el("div", "file-change-summary");
  row.appendChild(el("div", "file-change-title", t("change.title") + " · " + changes.length));
  var list = el("div", "file-change-list");
  changes.forEach(function (change) {
    var kind = ["created", "modified", "deleted"].indexOf(change.kind) >= 0 ? change.kind : "modified";
    var item = el("div", "file-change-item " + kind);
    item.appendChild(el("span", "file-change-kind", t("change." + kind)));
    item.appendChild(el("code", "file-change-path", String(change.path)));
    list.appendChild(item);
  });
  row.appendChild(list);
  appendStream(row);
  smartScroll();
}
function settleOpenTools() {
  openToolOrder.forEach(function (k) {
    var row = toolRows[k];
    if (row) {
      clearInterval(row._clock);
      row._clock = null;
      row.classList.remove("running");
      row._stat.textContent = "—";
    }
  });
  openToolOrder = [];
}
function prettyJSON(s) {
  try { return JSON.stringify(JSON.parse(s), null, 2); } catch (e) { return s || ""; }
}

function findTaskRow(taskID, agentName) {
  if (taskID && workerRows[taskID]) return workerRows[taskID];
  for (var i = openToolOrder.length - 1; i >= 0; i--) {
    var candidate = toolRows[openToolOrder[i]];
    if (candidate && candidate._toolName === "task" &&
      (!agentName || candidate._taskAgent === agentName)) return candidate;
  }
  return null;
}

function addWorkerProgress(ev) {
  var row = findTaskRow(ev.id, ev.name);
  if (!row) return false;
  if (ev.id) workerRows[ev.id] = row;
  if (!row._activity) row._activity = el("div", "task-activity");
  if (!row._workerCalls) row._workerCalls = {};
  var item = ev.call_id ? row._workerCalls[ev.call_id] : null;
  if (ev.kind === "tool_call") {
    var info = toolHint(ev.tool || "tool", ev.args || "{}");
    item = el("div", "task-activity-item running");
    item.appendChild(el("span", "activity-dot"));
    item.appendChild(el("span", "activity-name", ev.tool || "tool"));
    item.appendChild(el("span", "activity-hint", info.hint || ""));
    item.appendChild(el("span", "activity-status", t("tool.running")));
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
  }
  if (!row.open) row.open = true;
  row._body.hidden = false;
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

