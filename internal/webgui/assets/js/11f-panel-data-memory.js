"use strict";

sections.data = async function () {
  panelContent.innerHTML = "";
  panelContent.appendChild(el("div", "note", t("data.hint")));
  var status;
  try { status = await j("/api/data/status"); }
  catch (e) { panelContent.appendChild(el("div", "note", e.message)); return; }
  var facts = el("div", "data-facts");
  [["data.sessions", status.sessions], ["data.memories", status.memory_entries], ["data.goals", status.goals]].forEach(function (item) {
    var fact = el("div", "data-fact");
    fact.appendChild(el("span", "", t(item[0])));
    fact.appendChild(el("strong", "", fmtCompactNumber(item[1] || 0)));
    facts.appendChild(fact);
  });
  panelContent.appendChild(facts);
  if (status.import_pending) panelContent.appendChild(el("div", "data-pending", t("data.importPending")));

  function dataAction(title, hint, button) {
    var row = el("div", "data-action"), copy = el("div", "data-action-copy");
    copy.appendChild(el("strong", "", title)); copy.appendChild(el("span", "", hint));
    row.appendChild(copy); row.appendChild(button); return row;
  }
  var backup = el("div", "group data-group"); backup.appendChild(el("div", "g-label", t("data.backup")));
  var exportBtn = el("button", "btn", t("data.export")); exportBtn.type = "button";
  exportBtn.addEventListener("click", function () {
    var link = document.createElement("a"); link.href = "/api/data/export"; link.download = "";
    document.body.appendChild(link); link.click(); link.remove();
  });
  backup.appendChild(dataAction(t("data.export"), t("data.exportHint"), exportBtn));
  var fullExportBtn = el("button", "btn", t("data.exportFull")); fullExportBtn.type = "button";
  fullExportBtn.addEventListener("click", function () {
    var link = document.createElement("a"); link.href = "/api/data/export/full"; link.download = "";
    document.body.appendChild(link); link.click(); link.remove();
  });
  backup.appendChild(dataAction(t("data.exportFull"), t("data.exportFullHint"), fullExportBtn));
  var importBtn = el("button", "btn", t("data.import")); importBtn.type = "button";
  var importInput = document.createElement("input"); importInput.type = "file"; importInput.accept = ".zip,application/zip"; importInput.hidden = true;
  importBtn.addEventListener("click", function () { importInput.click(); });
  importInput.addEventListener("change", async function () {
    if (!importInput.files || !importInput.files[0]) return;
    var selectedFile = importInput.files[0];
    importBtn.disabled = true;
    var form = new FormData(); form.append("backup", selectedFile);
    try {
      var response = await fetch("/api/data/import", { method:"POST", body:form });
      if (!response.ok) throw new Error((await response.text()).trim() || String(response.status));
      toast(t("data.importReady")); await sections.data();
    } catch (e) { toast(e.message); importBtn.disabled = false; }
    finally { importInput.value = ""; }
  });
  var importRow = dataAction(t("data.import"), t("data.importHint"), importBtn); importRow.appendChild(importInput); backup.appendChild(importRow);
  panelContent.appendChild(backup);

  var danger = el("div", "group data-group danger-zone"); danger.appendChild(el("div", "g-label", t("data.danger")));
  function destructiveAction(labelKey, hintKey, action, after) {
    var button = el("button", "btn danger", t(labelKey)); button.type = "button"; button.disabled = streaming;
    button.addEventListener("click", async function () {
      if (!await appConfirm(t(hintKey), {
        title: t(labelKey), danger: true, confirmLabel: t("common.remove"),
      })) return;
      button.disabled = true;
      try { await jpost("/api/data/clear", { action:action }); toast(t("data.cleared")); if (after) after(); await sections.data(); }
      catch (e) { toast(e.message); button.disabled = false; }
    });
    danger.appendChild(dataAction(t(labelKey), t(hintKey), button));
  }
  destructiveAction("data.clearSessions", "data.confirmSessions", "sessions", function () { newSession(); loadSessions(); renderStats(); });
  destructiveAction("data.clearMemory", "data.confirmMemory", "memory", function () {});
  panelContent.appendChild(danger);
};

sections.memory = async function () {
  var got;
  try { got = await j("/api/memory?limit=100"); } catch (e) {
    panelContent.innerHTML = '<div class="note">' + escHtml(e.message) + "</div>";
    return;
  }
  panelContent.innerHTML = "";
  var g = el("div", "group");
  g.appendChild(el("div", "g-label", t("panel.memory")));
  if (!got || !got.length) g.appendChild(el("div", "note", t("mem.empty")));
  (got || []).forEach(function (m) {
    var row = el("div", "list-row");
    var main = el("div", "lr-main");
    var title = el("div", "lr-title", m.content);
    title.style.whiteSpace = "normal";
    main.appendChild(title);
    main.appendChild(el("div", "lr-sub", [m.scope, (m.tags || []).join(","), fmtWhen(m.updated_at)].filter(Boolean).join(" · ")));
    row.appendChild(main);
    g.appendChild(row);
  });
  panelContent.appendChild(g);
};

/* ── Goal ── */

function goalStatus(status) { return t("goal.status." + status); }

async function goalMutate(button, body) {
  if (button) button.disabled = true;
  try {
    var got = await jpost("/api/goal", body);
    renderGoalPanel(got);
    if ($("#side-tab-goal").classList.contains("active")) renderSideGoal(got);
    toast(t("goal.updated"));
  } catch (e) {
    toast(e.message);
    if (button) button.disabled = false;
  }
}

function goalInput(placeholder, multiline) {
  var input = el(multiline ? "textarea" : "input", "field-input goal-input");
  input.placeholder = placeholder;
  if (!multiline) input.type = "text";
  return input;
}

function renderGoalCreate() {
  var empty = el("div", "goal-empty");
  empty.appendChild(el("div", "goal-empty-title", t("goal.none")));
  empty.appendChild(el("div", "note", t("goal.noneHint")));
  var form = el("form", "goal-create-form");
  var title = goalInput(t("goal.title"), false);
  title.required = true;
  var description = goalInput(t("goal.description"), true);
  var criteria = goalInput(t("goal.criteria"), true);
  var submit = el("button", "btn primary", t("goal.create"));
  submit.type = "submit";
  form.appendChild(title);
  form.appendChild(description);
  form.appendChild(criteria);
  form.appendChild(submit);
  form.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var value = title.value.trim();
    if (!value) { title.focus(); return; }
    goalMutate(submit, { action: "set", title: value, description: description.value.trim(),
      success_criteria: criteria.value.trim(), parent_session_id: activeSessionID });
  });
  empty.appendChild(form);
  panelContent.appendChild(empty);
  title.focus();
}

function goalTaskAction(label, task, status) {
  var button = el("button", "goal-task-action", label);
  button.type = "button";
  button.addEventListener("click", function () {
    goalMutate(button, { action: "set_task_status", task_seq: task.seq, status: status });
  });
  return button;
}

function renderGoalVerification(got) {
  var section = el("section", "group goal-verification status-" + (got.verification_status || "pending"));
  section.appendChild(el("div", "g-label", t("goal.verification")));
  var state = el("div", "goal-verification-state");
  var mark = el("span", "goal-verification-mark");
  state.appendChild(mark);
  var copy = el("div", "goal-verification-copy");
  if (!got.ready_for_verification) {
    copy.appendChild(el("div", "goal-verification-title", t("goal.verificationWaiting")));
    copy.appendChild(el("div", "goal-verification-hint", t("goal.finishBlocked")));
  } else if (got.verification_status === "passed") {
    copy.appendChild(el("div", "goal-verification-title", t("goal.verificationPassed")));
    if (got.verification_evidence) {
      copy.appendChild(el("div", "goal-verification-evidence", got.verification_evidence));
    }
  } else {
    copy.appendChild(el("div", "goal-verification-title",
      got.verification_status === "failed" ? t("goal.verificationFailed") : t("goal.awaitingVerification")));
    copy.appendChild(el("div", "goal-verification-hint", t("goal.verificationReady")));
    if (got.verification_evidence) {
      copy.appendChild(el("div", "goal-verification-evidence", got.verification_evidence));
    }
  }
  state.appendChild(copy);
  section.appendChild(state);

  if (got.ready_for_verification && got.verification_status !== "passed") {
    var form = el("form", "goal-verify-form");
    var evidence = goalInput(t("goal.verifyPlaceholder"), true);
    evidence.required = true;
    var actions = el("div", "goal-verify-actions");
    var pass = el("button", "btn primary", t("goal.verifyPass"));
    pass.type = "submit";
    var fail = el("button", "btn", t("goal.verifyFail"));
    fail.type = "button";
    actions.appendChild(pass);
    actions.appendChild(fail);
    form.appendChild(evidence);
    form.appendChild(actions);
    function verify(passed, button) {
      var value = evidence.value.trim();
      if (!value) { evidence.focus(); return; }
      goalMutate(button, { action: "verify", passed: passed, text: value });
    }
    form.addEventListener("submit", function (ev) { ev.preventDefault(); verify(true, pass); });
    fail.addEventListener("click", function () { verify(false, fail); });
    section.appendChild(form);
  }
  return section;
}

function renderGoalPanel(got) {
  panelContent.innerHTML = "";
  if (!got) {
    renderGoalCreate();
    return;
  }

  var head = el("section", "goal-overview");
  var copy = el("div", "goal-overview-copy");
  var eyebrow = el("div", "goal-eyebrow");
  eyebrow.appendChild(el("span", "goal-live-dot"));
  eyebrow.appendChild(document.createTextNode(goalStatus(got.status)));
  copy.appendChild(eyebrow);
  copy.appendChild(el("h3", "goal-title", got.title));
  if (got.description) {
    var desc = el("div", "goal-detail");
    desc.appendChild(el("span", "goal-detail-label", t("goal.descriptionLabel")));
    desc.appendChild(el("span", "", got.description));
    copy.appendChild(desc);
  }
  if (got.success_criteria) {
    var criteria = el("div", "goal-detail");
    criteria.appendChild(el("span", "goal-detail-label", t("goal.criteriaLabel")));
    criteria.appendChild(el("span", "", got.success_criteria));
    copy.appendChild(criteria);
  }
  head.appendChild(copy);
  var goalActions = el("div", "goal-head-actions");
  var finish = el("button", "btn primary", t("goal.finish"));
	finish.disabled = !got.can_finish;
	if (!got.can_finish) finish.title = t("goal.finishBlocked");
  finish.addEventListener("click", async function () {
    if (await appConfirm(t("goal.finishConfirm"), {
      title: t("goal.finish"), confirmLabel: t("goal.finish"),
    })) goalMutate(finish, { action: "set_status", status: "done" });
  });
  var abandon = el("button", "btn danger", t("goal.abandon"));
  abandon.addEventListener("click", async function () {
    if (await appConfirm(t("goal.abandonConfirm"), {
      title: t("goal.abandon"), danger: true, confirmLabel: t("goal.abandon"),
    })) goalMutate(abandon, { action: "set_status", status: "abandoned" });
  });
  goalActions.appendChild(finish);
  goalActions.appendChild(abandon);
  head.appendChild(goalActions);
  panelContent.appendChild(head);

  var tasks = got.tasks || [];
  var terminal = tasks.filter(function (task) { return task.status === "done" || task.status === "skipped"; }).length;
  var group = el("section", "group goal-task-group");
  var label = el("div", "g-label", t("goal.tasks"));
  label.appendChild(el("span", "g-act", tasks.length ? terminal + " / " + tasks.length : ""));
  group.appendChild(label);
  if (tasks.length) {
    var progress = el("div", "goal-progress");
    var bar = el("span");
    bar.style.width = (terminal * 100 / tasks.length) + "%";
    progress.appendChild(bar);
    progress.setAttribute("aria-label", t("goal.progress") + ": " + terminal + " / " + tasks.length);
    group.appendChild(progress);
  }
  if (!tasks.length) group.appendChild(el("div", "goal-no-tasks", t("goal.noTasks")));
  tasks.forEach(function (task) {
    var row = el("div", "goal-task status-" + task.status);
    var mark = task.status === "done" ? "✓" : (task.status === "skipped" ? "—" : (task.status === "in_progress" ? "●" : "○"));
    row.appendChild(el("span", "goal-task-mark", mark));
    var taskCopy = el("div", "goal-task-copy");
    taskCopy.appendChild(el("div", "goal-task-title", task.title));
    taskCopy.appendChild(el("div", "goal-task-status", goalStatus(task.status)));
    row.appendChild(taskCopy);
    var actions = el("div", "goal-task-actions");
    if (task.status === "pending") actions.appendChild(goalTaskAction(t("goal.start"), task, "in_progress"));
    if (task.status === "pending" || task.status === "in_progress") {
      actions.appendChild(goalTaskAction(t("goal.complete"), task, "done"));
      actions.appendChild(goalTaskAction(t("goal.skip"), task, "skipped"));
    } else {
      actions.appendChild(goalTaskAction(t("goal.reopen"), task, "pending"));
    }
    row.appendChild(actions);
    group.appendChild(row);
  });
  var addForm = el("form", "goal-inline-form");
  var taskInput = goalInput(t("goal.taskPlaceholder"), false);
  var add = el("button", "btn", t("goal.addTask"));
  add.type = "submit";
  addForm.appendChild(taskInput);
  addForm.appendChild(add);
  addForm.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var value = taskInput.value.trim();
    if (!value) { taskInput.focus(); return; }
    goalMutate(add, { action: "add_task", title: value });
  });
  group.appendChild(addForm);
  panelContent.appendChild(group);
	panelContent.appendChild(renderGoalVerification(got));

  var notes = el("section", "group goal-notes");
  notes.appendChild(el("div", "g-label", t("goal.notes")));
  if (got.notes) notes.appendChild(el("div", "goal-notes-history", got.notes));
  var noteForm = el("form", "goal-inline-form");
  var noteInput = goalInput(t("goal.notePlaceholder"), false);
  var addNote = el("button", "btn", t("goal.addNote"));
  addNote.type = "submit";
  noteForm.appendChild(noteInput);
  noteForm.appendChild(addNote);
  noteForm.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var value = noteInput.value.trim();
    if (!value) { noteInput.focus(); return; }
    goalMutate(addNote, { action: "add_note", text: value });
  });
  notes.appendChild(noteForm);
  panelContent.appendChild(notes);
}
