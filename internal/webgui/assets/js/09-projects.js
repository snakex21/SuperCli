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
      var b = el("button", "side-item" + (p.cwd ? " active" : ""));
      b.type = "button";
      var tspan = el("span", "t");
      if (p.cwd) tspan.appendChild(el("span", "dot", "●"));
      tspan.appendChild(document.createTextNode(p.name || p.path));
      b.appendChild(tspan);
      b.appendChild(el("span", "s", p.path));
      b.title = p.path;
      var x = el("span", "x", "×");
      x.title = t("common.remove");
      x.addEventListener("click", function (e) {
        e.stopPropagation();
        projectAction("remove", p.path);
      });
      b.appendChild(x);
      b.addEventListener("click", function () { projectAction("use", p.path); });
      list.appendChild(b);
    });
  } catch (e) {
    list.innerHTML = "";
    list.appendChild(el("div", "side-empty", t("common.error")));
  }
}
async function projectAction(action, target, name) {
  if (streaming && (action === "use" || action === "add")) {
    toast(t("project.stopRun"));
    return;
  }
  try {
    await jpost("/api/projects", { action: action, target: target, name: name || "" });
    if (action === "use" || action === "add") projectEpoch++;
    await checkHealth();
    loadProjects();
    // A conversation belongs to the workspace where it was created. Switching
    // projects starts a clean browser conversation and reloads only that
    // project's history; the old session remains stored under its project.
    if (action === "use" || action === "add") { newSession(); loadPromptQueue(); }
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

