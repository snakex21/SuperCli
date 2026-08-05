"use strict";

/* ═══ projects ═══ */

async function loadProjects() {
  var list = $("#project-list");
  try {
    var got = await j("/api/projects");
    list.innerHTML = "";
    var projects = got.projects || [];
    if (!projects.length) {
      list.appendChild(el("div", "side-empty", t("side.noProjects")));
      return;
    }
    projects.forEach(function (p) {
      var b = el("div", "side-item project-item" + (p.cwd ? " active" : ""));
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
      var x = el("button", "session-action delete", "×");
      x.type = "button";
      x.title = t("common.remove");
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
    list.appendChild(el("div", "side-empty", t("common.error")));
  }
}
async function projectAction(action, target, name, newPath) {
  if (streaming && (action === "use" || action === "add" || action === "relocate")) {
    toast(t("project.stopRun"));
    return;
  }
  try {
    await jpost("/api/projects", { action: action, target: target, name: name || "", new_path: newPath || "" });
    if (action === "use" || action === "add" || action === "relocate") projectEpoch++;
    await checkHealth();
    loadProjects();
    // A conversation belongs to the workspace where it was created. Switching
    // projects starts a clean browser conversation and reloads only that
    // project's history; the old session remains stored under its project.
    if (action === "use" || action === "add" || action === "relocate") { newSession(); loadPromptQueue(); }
    if (action === "relocate") toast(t("project.changed"));
  } catch (e) {
    toast(e.message);
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
