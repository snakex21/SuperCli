"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const http = require("node:http");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const root = path.resolve(__dirname, "..");
  const temp = path.join(root, ".tmp", "delegation-ui");
  await fs.mkdir(temp, { recursive: true });
  // UI fixtures use real assets and deterministic read-only API responses.
  // Worker/backend routing is covered separately by worker_resume_test.go.
  const server = http.createServer(async (req, res) => {
    const pathname = new URL(req.url, "http://localhost").pathname;
    if (pathname.startsWith("/api/")) {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify({ sessions: [], projects: [], providers: [], models: [],
        workers: [], tasks: [], settings: {}, ui: { lang: "pl" } }));
      return;
    }
    const filename = pathname === "/" ? "assets/index.html" :
      pathname.startsWith("/.__supercli/ui/") ? "shared-ui/" + path.basename(pathname) : "assets" + pathname;
    try {
      const body = await fs.readFile(path.join(root, "internal/webgui", filename));
      res.setHeader("Content-Type", pathname.endsWith(".css") ? "text/css" : pathname.endsWith(".js") ? "text/javascript" : "text/html");
      res.end(body);
    } catch { res.statusCode = 404; res.end(); }
  });
  await new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
  const url = "http://127.0.0.1:" + server.address().port;
  let browser;
  try {
    browser = await chromium.launchPersistentContext(path.join(temp, "browser"), {
      executablePath: process.env.PLAYWRIGHT_BROWSER_PATH || undefined,
      headless: true, viewport: { width: 1250, height: 1000 },
    });
    const page = await browser.newPage();
    const errors = [];
    page.on("pageerror", (error) => { errors.push(error.message); console.error("browser:", error.message); });
    await page.goto(url, { waitUntil: "load" });
    await page.evaluate(async () => { await sessionRuntimeReady; });
    const result = await page.evaluate(() => {
      ui.lang = "pl";
      stream.innerHTML = "";
      toolRows = {}; workerRows = {}; openToolOrder = [];
      resetWorkerOverview();
      hideWelcome();
      addUserMsg("Sprawdź delegację i kontynuuj poprawki po błędzie.");
      function note(id, agent, report) {
        return "<task-notification><task-id>" + id + "</task-id><agent>" + agent +
          "</agent><status>done</status><summary>" + agent +
          " done · 2 steps · 200 in/40 out tok</summary><result>" + report +
          "</result></task-notification>";
      }
      addToolCall("task", JSON.stringify({ agent: "code", prompt: "Sprawdź pierwszą część projektu." }), "spawn-a");
      addToolCall("task", JSON.stringify({ agent: "code", prompt: "Sprawdź drugą część projektu." }), "spawn-b");
      addWorkerProgress({ id: "worker-2", name: "code", kind: "started", parent_call_id: "spawn-b", run: 1 });
      addWorkerProgress({ id: "worker-1", name: "code", kind: "started", parent_call_id: "spawn-a", run: 1 });
      addWorkerProgress({ id: "worker-2", name: "code", kind: "tool_call", parent_call_id: "spawn-b", call_id: "b-read", tool: "read_lines", args: '{"file":"second.go"}' });
      const parallelMapped = workerRows["worker-2"] === toolRows["spawn-b"] &&
        toolRows["spawn-a"]._activity.childNodes.length === 0 &&
        toolRows["spawn-b"]._activity.childNodes.length === 1;
      addToolResult("spawn-a", note("worker-1", "code", "Pierwsze sprawdzenie zakończone."));
      addToolResult("spawn-b", note("worker-2", "code", "Druga część sprawdzona."));
      const old = toolRows["spawn-a"];
      const before = old._body.textContent;
      const second = toolRows["spawn-b"];
      // A continuation usually arrives in a later coordinator turn.
      toolRows = {}; openToolOrder = [];
      addToolCall("send_message", JSON.stringify({ to: "worker-1", message: "Kontynuuj po błędzie bramy. Zachowaj wykonane zmiany i uruchom testy." }), "resume-a");
      addWorkerProgress({ id: "worker-1", name: "code", kind: "started", parent_call_id: "resume-a", run: 2 });
      const resumed = toolRows["resume-a"];
      resumed.open = false;
      addWorkerProgress({ id: "worker-1", name: "code", kind: "tool_call", parent_call_id: "resume-a", call_id: "check", tool: "ctx_execute", args: '{"command":["go","test","./..."]}' });
      const collapsePreserved = !resumed.open;
      addWorkerProgress({ id: "worker-1", name: "code", kind: "tool_result", parent_call_id: "resume-a", call_id: "check", output: "ok" });
      addToolResult("resume-a", note("worker-1", "code", "Zmiany poprawione. Testy przeszły."));
      const isolated = old._body.textContent === before && resumed._activity.childNodes.length === 1;
      old.open = false;
      resumed.querySelector(".task-backlink").click();
      const backlink = old.open && resumed._body.textContent.includes("Kontynuuj po błędzie");
      resumed.open = true;
      const question = (id) => ({ id, question: "Który wariant wybrać?", header: id,
        options: [{ label: "Zachowaj" }, { label: "Zmień" }], allow_custom: true });
      showQuestion(question("worker-question-1"));
      showQuestion(question("worker-question-2"));
      const bothQuestions = document.querySelectorAll(".question-inline").length === 2;
      closeQuestionOverlay("worker-question-1");
      const questionRetained = document.querySelectorAll(".question-inline").length === 1 &&
        !!questionOverlays["worker-question-2"];
      closeQuestionOverlay();
      old.open = false;
      second.open = false;
      const overviewStable = document.querySelectorAll(".worker-overview-item").length === 2 &&
        workerOverview["worker-1"].row === resumed &&
        workerOverview["worker-1"].label.textContent === "Worker 1 · code" &&
        workerOverview["worker-1"].status === "done";
      resumed.open = false;
      workerOverview["worker-1"].button.click();
      const overviewOpensLatest = resumed.open;
      stage.scrollTop = 0;
      return { parallelMapped, isolated, backlink, collapsePreserved, bothQuestions, questionRetained, overviewStable, overviewOpensLatest };
    });
    for (const [name, value] of Object.entries(result)) assert.equal(value, true, name);
    assert.deepEqual(errors, [], "browser JavaScript errors");
    await page.screenshot({ path: path.join(temp, "delegation-desktop.png"), fullPage: true });
    await page.setViewportSize({ width: 700, height: 850 });
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const geometry = await page.evaluate(() => {
      const row = toolRows["resume-a"].getBoundingClientRect();
      return { left: row.left, right: row.right, width: innerWidth };
    });
    assert.ok(geometry.left >= -1 && geometry.right <= geometry.width + 1, JSON.stringify(geometry));
    await page.screenshot({ path: path.join(temp, "delegation-narrow.png"), fullPage: true });

    await page.setViewportSize({ width: 1250, height: 1000 });
    await page.evaluate(() => {
      addToolCall("task", JSON.stringify({ agent: "review", prompt: "Sprawdź zapis plików i przenośność aplikacji." }), "spawn-c");
      addWorkerProgress({ id: "worker-3", name: "review", kind: "started", parent_call_id: "spawn-c", run: 1 });
      addWorkerProgress({ id: "worker-3", name: "review", kind: "tool_call", parent_call_id: "spawn-c",
        call_id: "c-read", tool: "read_lines", args: '{"file":"internal/config.go"}' });
      if (workerOverview["worker-3"].status !== "running") throw Error("active worker state");
      if (!$("#worker-overview-summary").textContent.includes("1 / 3")) throw Error("active worker count");
      toolRows["resume-a"].open = false;
      stage.scrollTop = 0;
    });
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await page.screenshot({ path: path.join(temp, "delegation-active.png"), fullPage: true });
    await page.evaluate(() => {
      settleOpenTools();
      if (workerOverview["worker-3"].status !== "stopped") throw Error("cancelled worker still running");
      newSession();
      if (!$("#worker-overview").hidden || Object.keys(workerOverview).length) throw Error("previous session workers retained");
    });
    console.log("PASS delegation UI:", JSON.stringify(result), "desktop + narrow layout + active/cancel/reset");
  } finally {
    if (browser) await browser.close();
    await new Promise((resolve) => server.close(resolve));
  }
})().catch((error) => { console.error(error); process.exitCode = 1; });
