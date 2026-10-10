"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const assets = path.resolve(__dirname, "../../internal/webgui/assets");
const english = JSON.parse(fs.readFileSync(path.join(assets, "locales/en.json"), "utf8"));
const workspace = "C:/synthetic/project";

function node(tag, className = "", textContent = "") {
  return {
    tag, className, textContent, children: [], listeners: {}, attributes: {},
    appendChild(child) { this.children.push(child); return child; },
    setAttribute(name, value) { this.attributes[name] = value; },
    addEventListener(name, fn) { this.listeners[name] = fn; },
    set innerHTML(value) { this.children = []; },
    get innerHTML() { return ""; }
  };
}

function harness(options = {}) {
  const list = node("div");
  const add = node("button");
  const requests = [], dialogs = [], toasts = [], lifecycle = [];
  const answers = [...(options.answers || [])];
  const state = { policy: options.policy === true };
  const context = {
    window: { SuperCliUI: {} },
    document: { createTextNode: text => node("text", "", text) },
    streaming: options.streaming === true,
    projectEpoch: 0,
    $: selector => selector === "#project-list" ? list : selector === "#add-project" ? add : null,
    t: key => english[key] || key,
    checkHealth: async () => { lifecycle.push("health"); },
    newSession: () => { lifecycle.push("new-session"); },
    loadPromptQueue: () => { lifecycle.push("queue"); }
  };
  vm.createContext(context);
  vm.runInContext(fs.readFileSync(path.join(assets, "js/00-helpers.js"), "utf8"), context);
  Object.assign(context, {
    $: selector => selector === "#project-list" ? list : selector === "#add-project" ? add : null,
    el: node,
    i18nEl: (tag, cls, key) => node(tag, cls, english[key]),
    toast: message => toasts.push(message),
    j: async url => {
      requests.push({ method: "GET", url });
      if (url === "/api/projects") return { projects: [{ name: "Synthetic project", path: workspace, cwd: true }], delete_checkpoints_on_remove: state.policy };
      if (url === "/api/folder-picker") return { path: "C:/synthetic/moved" };
      throw new Error("Unexpected GET " + url);
    },
    jpost: async (url, body) => {
      const exact = JSON.parse(JSON.stringify(body));
      requests.push({ method: "POST", url, body: exact });
      if (body.action === "checkpoint_preview") {
        if (options.preview) return options.preview(context);
        return { workspace, bytes: 2048, files: 2, stores: 1 };
      }
      if (options.writeError) throw new Error(options.writeError);
      if (body.remember_cleanup) state.policy = body.delete_checkpoints;
      return { delete_checkpoints_on_remove: state.policy };
    },
    showAppDialog: async dialog => {
      dialogs.push(dialog);
      if (options.dialog) return options.dialog(context, dialog);
      return answers.length ? answers.shift() : null;
    }
  });
  vm.runInContext(fs.readFileSync(path.join(assets, "js/09-projects.js"), "utf8"), context);
  return { c: context, list, add, requests, dialogs, toasts, lifecycle, state };
}

function posts(h) { return h.requests.filter(request => request.method === "POST"); }
function deferred() {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
}

test("project listing adds an accessible cleanup action without automatic footprint requests", async () => {
  const h = harness({ policy: true });
  await h.c.loadProjects();
  assert.equal(h.c.projectDeleteCheckpointsOnRemove, true);
  assert.deepEqual(h.requests, [{ method: "GET", url: "/api/projects" }]);
  const actions = h.list.children[0].children[1].children;
  const clear = actions.find(action => action.className.includes("checkpoint-cleanup"));
  assert.equal(clear.title, "Clear checkpoints");
  assert.equal(clear.attributes["aria-label"], clear.title);
  assert.equal(clear.attributes["data-i18n-title"], "project.clearCheckpoints");
  assert.equal(clear.type, "button");
  const clicks = [];
  h.c.projectAction = (action, target) => clicks.push({ action, target });
  let stopped = false;
  clear.listeners.click({ stopPropagation() { stopped = true; } });
  assert.equal(stopped, true);
  assert.deepEqual(clicks, [{ action: "clear_checkpoints", target: workspace }]);
});

test("cancelling checkpoint cleanup sends only the preview and preserves browser conversation", async () => {
  const h = harness({ answers: [null] });
  await h.c.projectAction("clear_checkpoints", workspace);
  assert.deepEqual(posts(h), [{ method: "POST", url: "/api/projects", body: { action: "checkpoint_preview", target: workspace } }]);
  assert.equal(h.dialogs.length, 1);
  assert.equal(h.dialogs[0].danger, true);
  assert.match(h.dialogs[0].message, /2\.0 KB/);
  assert.ok(h.dialogs[0].message.includes(workspace));
  assert.match(h.dialogs[0].message, /File undo will be lost/);
  assert.match(h.dialogs[0].message, /Project files and conversations stay/);
  assert.deepEqual(h.lifecycle, []);
  assert.equal(h.c.projectEpoch, 0);
  assert.equal(h.c.projectCleanupPending, false);
});

test("confirmed cleanup posts the exact target once and keeps session identity", async () => {
  const h = harness({ answers: [true] });
  await h.c.projectAction("clear_checkpoints", workspace);
  assert.deepEqual(posts(h).map(request => request.body), [
    { action: "checkpoint_preview", target: workspace },
    { action: "clear_checkpoints", target: workspace }
  ]);
  assert.ok(h.toasts.includes("Checkpoints cleared."));
  assert.deepEqual(h.lifecycle, ["health"]);
  assert.equal(h.c.projectEpoch, 0);
});

test("project removal requires a choice and exposes footprint plus all three scopes", async () => {
  for (const answer of [null, "unexpected"]) {
    const h = harness({ answers: [answer] });
    await h.c.projectAction("remove", workspace);
    assert.equal(posts(h).length, 1);
    assert.equal(posts(h)[0].body.action, "checkpoint_preview");
    assert.deepEqual(Array.from(h.dialogs[0].choices, choice => choice.value), ["keep", "cleanup", "always"]);
    assert.match(h.dialogs[0].message, /Checkpoints use 2\.0 KB/);
    assert.match(h.dialogs[0].message, /loses file undo/);
    assert.deepEqual(h.lifecycle, []);
  }
});

test("removal choices send explicit cleanup and portable-policy flags", async () => {
  for (const [choice, cleanup, remember] of [["keep", false, false], ["cleanup", true, false], ["always", true, true]]) {
    const h = harness({ answers: [choice] });
    await h.c.projectAction("remove", workspace);
    assert.deepEqual(posts(h).map(request => request.body), [
      { action: "checkpoint_preview", target: workspace },
      { action: "remove", target: workspace, delete_checkpoints: cleanup, remember_cleanup: remember }
    ]);
    assert.deepEqual(h.lifecycle, ["health"]);
    assert.equal(h.c.projectEpoch, 0);
    assert.equal(h.state.policy, remember);
  }
});

test("saved cleanup policy offers separate one-off and persistent keep choices", async () => {
  const h = harness({ policy: true, answers: ["always_keep"] });
  await h.c.loadProjects();
  await h.c.projectAction("remove", workspace);
  const choices = h.dialogs[0].choices;
  assert.equal(choices[0].value, "cleanup");
  assert.equal(choices[0].primary, true);
  assert.equal(choices.find(choice => choice.value === "keep").primary, false);
  assert.equal(choices.find(choice => choice.value === "keep").label, english["project.removeOnly"]);
  assert.equal(choices.find(choice => choice.value === "always_keep").label, "Always keep checkpoints when removing projects");
  assert.equal(posts(h).at(-1).body.delete_checkpoints, false);
  assert.equal(posts(h).at(-1).body.remember_cleanup, true);
  assert.equal(h.state.policy, false);
  const once = harness({ policy: true, answers: ["keep"] });
  await once.c.loadProjects();
  await once.c.projectAction("remove", workspace);
  assert.equal(posts(once).at(-1).body.delete_checkpoints, false);
  assert.equal(posts(once).at(-1).body.remember_cleanup, false);
  assert.equal(once.state.policy, true);
});

test("preview failure or invalid footprint never permits destructive requests", async () => {
  for (const preview of [
    async () => { throw new Error("preview rejected"); },
    async () => ({ workspace, bytes: -1 }),
    async () => ({ workspace, bytes: "2048" }),
    async () => ({ workspace, bytes: null }),
    async () => ({ bytes: 2048 }),
    async () => ({ workspace, bytes: Number.MAX_SAFE_INTEGER + 1 })
  ]) {
    for (const action of ["remove", "clear_checkpoints"]) {
      const h = harness({ preview, answers: [action === "remove" ? "cleanup" : true] });
      await h.c.projectAction(action, workspace);
      assert.equal(posts(h).length, 1);
      assert.equal(h.dialogs.length, 0);
      assert.equal(h.toasts.length, 1);
      assert.equal(h.c.projectCleanupPending, false);
    }
  }
});

test("zero footprint remains explicit and can be cleared without changing project files", async () => {
  const h = harness({ answers: [true], preview: async () => ({ workspace, bytes: 0, files: 0, stores: 0 }) });
  await h.c.projectAction("clear_checkpoints", workspace);
  assert.match(h.dialogs[0].message, /0 B/);
  assert.deepEqual(posts(h).at(-1).body, { action: "clear_checkpoints", target: workspace });
});

test("confirmation preserves literal path characters instead of treating them as template substitutions", async () => {
  const target = "C:/synthetic/$&-{size}-$`-$'/project";
  const h = harness({ answers: [null], preview: async () => ({ workspace: target, bytes: 2048 }) });
  await h.c.projectAction("clear_checkpoints", target);
  assert.ok(h.dialogs[0].message.includes(target));
  assert.match(h.dialogs[0].message, /Delete 2\.0 KB of checkpoints/);
  assert.deepEqual(posts(h)[0].body, { action: "checkpoint_preview", target });
});

test("active streaming blocks cleanup and project removal before any request", async () => {
  for (const action of ["remove", "clear_checkpoints"]) {
    const h = harness({ streaming: true, answers: ["always"] });
    await h.c.projectAction(action, workspace);
    assert.deepEqual(h.requests, []);
    assert.deepEqual(h.dialogs, []);
    assert.equal(h.toasts.length, 1);
  }
});

test("a run beginning while preview or confirmation is open blocks deletion", async () => {
  for (const stage of ["preview", "dialog"]) {
    for (const action of ["remove", "clear_checkpoints"]) {
      const h = harness({
        preview: async c => { if (stage === "preview") c.streaming = true; return { workspace, bytes: 2048 }; },
        dialog: async c => { if (stage === "dialog") c.streaming = true; return action === "remove" ? "keep" : true; }
      });
      await h.c.projectAction(action, workspace);
      assert.equal(posts(h).length, 1);
      assert.equal(h.dialogs.length, stage === "dialog" ? 1 : 0);
      assert.deepEqual(h.lifecycle, []);
    }
  }
});

test("an open cleanup flow suppresses duplicate clicks and releases after cancellation", async () => {
  const entered = deferred(), answer = deferred();
  const h = harness({ dialog: async () => { entered.resolve(); return answer.promise; } });
  const first = h.c.projectAction("clear_checkpoints", workspace);
  await entered.promise;
  await h.c.projectAction("clear_checkpoints", workspace);
  await h.c.projectAction("remove", workspace);
  assert.equal(posts(h).length, 1);
  answer.resolve(null);
  await first;
  assert.equal(h.c.projectCleanupPending, false);
});

test("backend busy rejection preserves project and session UI and permits retry", async () => {
  const h = harness({ answers: ["cleanup"], writeError: "busy" });
  await h.c.projectAction("remove", workspace);
  assert.deepEqual(h.lifecycle, []);
  assert.deepEqual(h.toasts, ["busy"]);
  assert.equal(h.c.projectCleanupPending, false);
});

test("ordinary project switching keeps its existing request and conversation lifecycle", async () => {
  const h = harness();
  await h.c.projectAction("relocate", workspace, "", "C:/synthetic/moved");
  assert.deepEqual(posts(h).map(request => request.body), [{ action: "relocate", target: workspace, name: "", new_path: "C:/synthetic/moved" }]);
  assert.deepEqual(h.dialogs, []);
  assert.deepEqual(h.lifecycle, ["health", "new-session", "queue"]);
  assert.equal(h.c.projectEpoch, 1);
});
