"use strict";

/* ═══ projects ═══ */
var projectDeleteCheckpointsOnRemove = false;
var projectCleanupPending = false;
// Unlike projectEpoch, this advances before a workspace change is acknowledged.
var projectTransitionSeq = 0;
var runningElsewhereProjectPath = "", runningElsewhereTimer = null, runningElsewhereOpening = false;
var runningElsewhereClocks = [];

function updateRunningElsewhereTimes() {
  runningElsewhereClocks.forEach(function (clock) {
    var seconds = clock.started > 0 ? Math.floor(Math.max(0, syncServerNow() - clock.started) / 1000) : 0;
    clock.node.textContent = clock.state + " · " + Math.floor(seconds / 60) + ":" + String(seconds % 60).padStart(2, "0");
  });
}

function runningElsewhereRuns() {
  if (typeof syncRuns === "undefined" || !runningElsewhereProjectPath) return [];
  return Object.keys(syncRuns).map(function (id) {
    var run = syncRuns[id];
    return {id: id, run: run};
  }).filter(function (item) {
    return item.run.project_path && item.run.project_path !== runningElsewhereProjectPath;
  }).sort(function (a, b) {
    return (a.run.startedAtMS || a.run.started_at_ms || 0) - (b.run.startedAtMS || b.run.started_at_ms || 0) || a.id.localeCompare(b.id);
  });
}

async function openRunningElsewhere(id) {
  if (runningElsewhereOpening || typeof syncRuns === "undefined" || !syncRuns[id]) return;
  runningElsewhereOpening = true;
  var run = syncRuns[id];
  try {
    // projectAction detaches the current run and resets only this view. Await
    // the switch before attaching through the ordinary history + sync path.
    if (run.project_path !== runningElsewhereProjectPath && !await projectAction("use", run.project_path)) return;
    await resumeSession(id, {id: id, title: run.title});
  } finally { runningElsewhereOpening = false; }
}

function renderRunningElsewhere() {
  var section = $("#running-elsewhere"), list = $("#running-elsewhere-list");
  if (!section || !list) return;
  var items = runningElsewhereRuns();
  section.hidden = !items.length;
  list.innerHTML = "";
  runningElsewhereClocks = [];
  items.forEach(function (item) {
    var run = item.run;
    var button = el("button", "side-item running-elsewhere-item");
    button.type = "button";
    var title = run.title || t("project.runningTitle"), project = run.project_name || run.project_path;
    button.title = title + " — " + project;
    button.setAttribute("aria-label", t("project.openRunning") + ": " + button.title);
    button.appendChild(el("span", "t", title));
    button.appendChild(el("span", "s running-elsewhere-project", project));
    var state = t(run.state === "waiting_answer" ? "project.waitingAnswer" : "project.runWorking");
    var started = run.startedAtMS || run.started_at_ms || 0;
    var clock = el("span", "s running-elsewhere-state");
    runningElsewhereClocks.push({node: clock, started: started, state: state});
    button.appendChild(clock);
    button.addEventListener("click", function () { openRunningElsewhere(item.id); });
    list.appendChild(button);
  });
  updateRunningElsewhereTimes();
  // Only update clock text on ticks: don't replace a focused/clicked row.
  if (items.length && runningElsewhereTimer === null) runningElsewhereTimer = setInterval(updateRunningElsewhereTimes, 1000);
  if (!items.length && runningElsewhereTimer !== null) { clearInterval(runningElsewhereTimer); runningElsewhereTimer = null; }
}

async function loadProjects() {
  var list = $("#project-list");
  try {
    var got = await j("/api/projects");
    projectDeleteCheckpointsOnRemove = got.delete_checkpoints_on_remove === true;
    list.innerHTML = "";
    var projects = got.projects || [];
    var current = projects.find(function (project) { return project.cwd; });
    runningElsewhereProjectPath = current ? current.path : "";
    renderRunningElsewhere();
    if (!projects.length) {
      list.appendChild(i18nEl("div", "side-empty", "side.noProjects"));
      return;
    }
    projects.forEach(function (p) {
      // running: a conversation of this project is still working, possibly
      // left going after switching here from that project.
      var b = el("div", "side-item project-item" + (p.cwd ? " active" : "") + (p.running > 0 ? " running" : ""));
      var select = el("button", "project-select");
      select.type = "button";
      var tspan = el("span", "t");
      if (p.cwd) tspan.appendChild(el("span", "dot", "●"));
      tspan.appendChild(document.createTextNode(p.name || p.path));
      select.appendChild(tspan);
      select.appendChild(el("span", "s", p.path));
      select.title = p.path;
      b.appendChild(select);
      var actions = el("span", "session-actions project-actions");
      var edit = el("button", "session-action rename", "✎");
      edit.type = "button";
      edit.title = t("project.changeFolder");
      edit.setAttribute("aria-label", t("project.changeFolder"));
      async function changeFolder(e) {
        e.stopPropagation();
        if (streaming) { toast(t("project.stopRun")); return; }
        try {
          var picked = await j("/api/folder-picker");
          if (picked && picked.path) await projectAction("relocate", p.path, "", picked.path);
        } catch (error) { toast(error.message); }
      }
      edit.addEventListener("click", changeFolder);
      actions.appendChild(edit);
      var clear = el("button", "session-action delete checkpoint-cleanup", "⌫");
      clear.type = "button";
      clear.title = t("project.clearCheckpoints");
      clear.setAttribute("aria-label", clear.title);
      clear.setAttribute("data-i18n-title", "project.clearCheckpoints");
      clear.setAttribute("data-i18n-aria", "project.clearCheckpoints");
      clear.addEventListener("click", function (e) {
        e.stopPropagation();
        projectAction("clear_checkpoints", p.path);
      });
      actions.appendChild(clear);
      var x = el("button", "session-action delete", "×");
      x.type = "button";
      x.title = t("common.remove");
      x.setAttribute("aria-label", x.title);
      x.addEventListener("click", function (e) {
        e.stopPropagation();
        projectAction("remove", p.path);
      });
      actions.appendChild(x);
      b.appendChild(actions);
      select.addEventListener("click", function () { projectAction("use", p.path); });
      list.appendChild(b);
    });
  } catch (e) {
    list.innerHTML = "";
    list.appendChild(i18nEl("div", "side-empty", "common.error"));
  }
}

async function projectCheckpointPreview(target) {
  var preview = await jpost("/api/projects", { action: "checkpoint_preview", target: target });
  if (!preview || !Number.isSafeInteger(preview.bytes) || preview.bytes < 0 ||
      typeof preview.workspace !== "string" || !preview.workspace) {
    throw new Error(t("project.checkpointPreviewInvalid"));
  }
  return preview;
}
function projectCheckpointMessage(key, target, preview) {
  return t(key).replace(/\{(path|size)\}/g, function (_, field) {
    return field === "path" ? (preview ? preview.workspace : target) : (preview ? fmtSize(preview.bytes) : "");
  });
}
async function confirmProjectCleanup(action, target) {
  if (streaming) {
    toast(t("project.stopCheckpointCleanup"));
    return null;
  }
  var preview = await projectCheckpointPreview(target);
  if (streaming) { toast(t("project.stopCheckpointCleanup")); return null; }
  if (action === "clear_checkpoints") {
    var confirmed = await showAppDialog({
      title: t("project.clearCheckpoints"), danger: true,
      message: projectCheckpointMessage("project.confirmClearCheckpoints", target, preview),
      confirmLabel: t("project.clearCheckpoints")
    });
    return confirmed === true ? {} : null;
  }
  var clearByDefault = projectDeleteCheckpointsOnRemove;
  var keep = { value: "keep", label: t("project.removeOnly"), primary: !clearByDefault };
  var choices = [keep];
  var cleanup = { value: "cleanup", label: t("project.removeWithCheckpoints"), primary: clearByDefault };
  if (clearByDefault) choices.unshift(cleanup);
  else choices.push(cleanup);
  choices.push(clearByDefault ?
    { value: "always_keep", label: t("project.alwaysKeepCheckpoints") } :
    { value: "always", label: t("project.alwaysClearCheckpoints") });
  var choice = await showAppDialog({
    title: t("common.remove"), danger: true,
    message: projectCheckpointMessage("project.confirmRemove", target, preview),
    choices: choices
  });
  if (!choices.some(function (option) { return option.value === choice; })) return null;
  return { delete_checkpoints: choice === "cleanup" || choice === "always", remember_cleanup: choice === "always" || choice === "always_keep" };
}
async function projectAction(action, target, name, newPath) {
  // Switching keeps a running conversation going in its own project; moving
  // or removing a project still waits for the run.
  var switching = action === "use" || action === "add";
  if (streaming && (action === "relocate" || action === "remove" || (switching && !canDetachActiveRun()))) {
    toast(t("project.stopRun"));
    return;
  }
  var cleanupAction = action === "clear_checkpoints" || action === "remove";
  if (cleanupAction && projectCleanupPending) return;
  if (cleanupAction) projectCleanupPending = true;
  try {
    var body = cleanupAction ? { action: action, target: target } :
      { action: action, target: target, name: name || "", new_path: newPath || "" };
    if (cleanupAction) {
      var choice = await confirmProjectCleanup(action, target);
      if (choice === null) return;
      Object.assign(body, choice);
      if (streaming) {
        toast(t("project.stopCheckpointCleanup"));
        return;
      }
    }
    if (switching || action === "relocate") projectTransitionSeq++;
    var result = await jpost("/api/projects", body);
    if (result && typeof result.delete_checkpoints_on_remove === "boolean") {
      projectDeleteCheckpointsOnRemove = result.delete_checkpoints_on_remove;
    }
    if (switching && streaming) detachActiveRun();
    if (action === "use" || action === "add" || action === "relocate") projectEpoch++;
    await checkHealth();
    await loadProjects();
    // A conversation belongs to the workspace where it was created. Switching
    // projects starts a clean browser conversation and reloads only that
    // project's history; the old session remains stored under its project.
    if (action === "use" || action === "add" || action === "relocate") { newSession(); loadPromptQueue(); }
    if (action === "relocate") toast(t("project.changed"));
    if (action === "clear_checkpoints") toast(t("project.checkpointsCleared"));
    return true;
  } catch (e) {
    toast(e.message);
  } finally {
    if (cleanupAction) projectCleanupPending = false;
  }
}
$("#add-project").addEventListener("click", async function () {
  try {
    var got = await j("/api/folder-picker");
    if (got && got.path) projectAction("add", got.path);
  } catch (e) {
    // Headless / non-Windows fallback: register current workspace.
    projectAction("add", "");
  }
});
