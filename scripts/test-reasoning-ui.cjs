"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const http = require("node:http");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
  const root = path.resolve(__dirname, "..");
  const temp = path.join(root, ".tmp", "reasoning-ui");
  await fs.mkdir(temp, { recursive: true });
  const sessions = [
    { id:"a", model:"qwen", provider:"local", reasoning_effort:"low", runtime_known:true, first_user_msg:"Lokalny model", started_at:new Date().toISOString(), message_count:0 },
    { id:"b", model:"gpt-5.6-sol", provider:"cloud", reasoning_effort:"medium", runtime_known:true, first_user_msg:"Model chmurowy", started_at:new Date().toISOString(), message_count:0 },
  ];
  let model = "qwen", configured = "low";
  const calls = [];
  let holdReasoning, holdModels;
  const gates=[];
  function gate() {
    let start, release;
    let timer;
    const g={ started:new Promise((r,j)=>{start=()=>{clearTimeout(timer);r();};timer=setTimeout(()=>j(Error("Expected fixture request did not arrive")),10000);}), wait:new Promise(r=>{release=r}), start:()=>start(), release:()=>{clearTimeout(timer);release();} };
    gates.push(g);return g;
  }
  function state() {
    const toggle = model === "qwen";
    const effective = toggle && configured && configured !== "none" ? "on" : configured;
    return { configured, effective, selected:effective==="on"?"high":effective,
      adjusted:effective!==configured, supported:true, toggle_only:toggle,
      levels:toggle?["none","high"]:["none","minimal","low","medium","high","xhigh"] };
  }
  const server = http.createServer(async (req,res) => {
    try {
      const pathname = new URL(req.url,"http://localhost").pathname;
      if (pathname.startsWith("/api/")) {
        res.setHeader("Content-Type","application/json");
        let result = { sessions:[], projects:[], providers:[], models:[], workers:[], tasks:[], settings:{}, ui:{lang:"pl"} };
        if (pathname === "/api/sessions") result=sessions;
        if (pathname === "/api/transcript") result={messages:[],has_more:false};
        if (pathname === "/api/models") {
          result={active:model,provider:model==="qwen"?"local":"cloud",reasoning:state(),models:[]};
          if (holdModels) { const held=holdModels;holdModels=null;held.start();await held.wait; }
        }
        if (pathname === "/api/reasoning" && req.method === "GET") result=state();
        if ((pathname === "/api/reasoning" || pathname === "/api/model") && req.method === "POST") {
          let raw=""; for await (const data of req) raw+=data;
          const body=JSON.parse(raw);
          calls.push({path:pathname,...body});
          if (pathname === "/api/reasoning") {
            if (holdReasoning) { const held=holdReasoning;holdReasoning=null;held.start();await held.wait; }
            configured=body.level==="default"?"":body.level;
            result=state();
            const saved=sessions.find(s=>s.id===body.session_id);
            if (saved) {
              Object.assign(saved,{reasoning_effort:configured,model,provider:model==="qwen"?"local":"cloud"});
              result.session={...saved};
            }
          } else { model=body.model; result={}; }
        }
        res.end(JSON.stringify(result)); return;
      }
      const filename=pathname==="/"?"assets/index.html":
        pathname.startsWith("/.__supercli/ui/")?"shared-ui/"+path.basename(pathname):"assets"+pathname;
      const assets=path.join(root,"internal","webgui");
      const file=path.resolve(assets,filename);
      if (!file.startsWith(assets+path.sep)) {res.statusCode=404;res.end();return;}
      const body=await fs.readFile(file);
      res.setHeader("Content-Type",pathname.endsWith(".css")?"text/css":pathname.endsWith(".js")?"text/javascript":"text/html");
      res.end(body);
    } catch {res.statusCode=404;res.end();}
  });
  await new Promise((resolve,reject)=>{server.once("error",reject);server.listen(0,"127.0.0.1",resolve);});
  let browser;
  console.log("Fixture listening");
  try {
    const profile=await fs.mkdtemp(path.join(temp,"browser-"));
    browser=await chromium.launchPersistentContext(profile,{
      executablePath:process.env.PLAYWRIGHT_BROWSER_PATH,
      headless:true,timeout:15000,viewport:{width:1250,height:900},
    });
    console.log("Browser launched");
    const page=await browser.newPage();
    page.setDefaultTimeout(10000);
    const errors=[];
    page.on("pageerror",e=>errors.push(e.message));
    await page.goto("http://127.0.0.1:"+server.address().port,{waitUntil:"load"});
    console.log("Page loaded");
    await page.evaluate(async()=>{
      await sessionRuntimeReady;
      ui.lang="pl";ui.rememberSessionRuntime=true;
      await loadSessions();
      activeSessionID="a";
      await loadReasoning();
      toggleReasoningMenu(true);
    });
    const options=()=>page.evaluate(()=>Array.from(document.querySelectorAll("#reasoning-options button")).map(b=>({text:b.textContent,value:b.dataset.level,checked:b.getAttribute("aria-checked")})));
    let got=await options();
    assert.deepEqual(got.map(x=>x.value),["","none","high"]);
    assert.ok(got[1].text.includes("Wyłączone") && got[2].text.includes("Włączone"));
    assert.equal(got[2].checked,"true","legacy low must select On");
    await page.screenshot({path:path.join(temp,"toggle-desktop.png")});
    await page.setViewportSize({width:520,height:900});
    await page.screenshot({path:path.join(temp,"toggle-narrow.png")});
    const geometry=await page.locator("#reasoning-menu").boundingBox();
    assert.ok(geometry.x>=0 && geometry.x+geometry.width<=520);
    await page.setViewportSize({width:1250,height:900});
    await page.locator('#reasoning-options button[data-level="high"]').focus();
    await page.keyboard.press("ArrowUp");
    assert.equal(await page.evaluate(()=>document.activeElement.dataset.level),"none");
    await page.keyboard.press("Enter");
    await page.evaluate(async()=>{await sessionRuntimeReady;});
    assert.equal(sessions[0].reasoning_effort,"none");
    assert.equal(await page.locator("#reasoning-level").textContent(),"Wyłączone");
    await page.evaluate(async()=>{await resumeSession("b",sessionByID.b);await sessionRuntimeReady;await resumeSession("a",sessionByID.a);await sessionRuntimeReady;await loadReasoning();});
    assert.equal(configured,"none","reopening without a new message must keep choice");

    console.log("PASS: toggle, keyboard and saved session");
    // A catalog request started before the click must not repaint stale state.
    const stale=gate();holdModels=stale;
    await page.evaluate(()=>{window.oldModelRead=loadModels();});
    await stale.started;
    await page.evaluate(()=>toggleReasoningMenu(true));
    await page.locator('#reasoning-options button[data-level="high"]').click();
    await page.evaluate(async()=>{await sessionRuntimeReady;});
    stale.release();
    await page.evaluate(async()=>{await window.oldModelRead;});
    assert.equal(await page.locator("#reasoning-level").textContent(),"Włączone");

    console.log("PASS: stale catalog response");
    // Restoring B must wait for an in-flight change to A.
    const pending=gate();holdReasoning=pending;
    await page.evaluate(()=>toggleReasoningMenu(true));
    await page.locator('#reasoning-options button[data-level="none"]').click();
    await pending.started;
    await page.evaluate(async()=>{await resumeSession("b",sessionByID.b);});
    pending.release();
    await page.evaluate(async()=>{await sessionRuntimeReady;await loadReasoning();});
    assert.equal(sessions[0].reasoning_effort,"none");
    assert.equal(configured,"medium","pending A save overwrote B restoration");

    await page.evaluate(()=>{
      renderReasoning({configured:"xhigh",effective:"high",selected:"high",adjusted:true,supported:true,levels:["low","medium","high"]});
      toggleReasoningMenu(true);
    });
    got=await options();
    assert.deepEqual(got.map(x=>x.value),["","low","medium","high"]);
    assert.equal(got[3].checked,"true");
    await page.screenshot({path:path.join(temp,"graded-desktop.png")});
    assert.deepEqual(errors,[]);
    await fs.writeFile(path.join(temp,"result.json"),JSON.stringify({passed:true,calls,geometry},null,2));
    console.log("PASS: adaptive choices, legacy selection, keyboard, session persistence, in-flight save ordering, stale refresh, desktop/narrow geometry");
  } catch (e) {console.error("UI failure:",e);throw e;} finally {
    gates.forEach(g=>g.release());
    if (browser) await browser.close();
    server.closeAllConnections();
    await new Promise(resolve=>server.close(resolve));
  }
})().catch(e=>{console.error(e);process.exitCode=1;});
