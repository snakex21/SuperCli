"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const http = require("node:http");
const path = require("node:path");
const { chromium } = require("playwright");

(async () => {
 const root=path.resolve(__dirname,"..");
 const temp=path.join(root,".tmp","session-activity");
 await fs.mkdir(temp,{recursive:true});
 const now=new Date().toISOString();
 const old=new Date(Date.now()-10*86400000).toISOString();
 const rowsA=[
  {id:"new",first_user_msg:"Nowsza rozmowa",started_at:now,updated_at:now,message_count:2,model:"fixture"},
  {id:"old",first_user_msg:"Starsza rozmowa — wracam do projektu",started_at:old,updated_at:old,message_count:2,model:"fixture"},
 ];
 const rowsB=[{id:"other",first_user_msg:"Inny projekt",started_at:now,updated_at:now,message_count:1}];
 let project="A",holdList=null,listCount=0;
 const gates=[];
 function gate() {
  let started,release,timer;
  const value={started:new Promise((r,j)=>{started=()=>{clearTimeout(timer);r();};timer=setTimeout(()=>j(Error("Fixture request did not arrive")),10000);}),wait:new Promise(r=>release=r),start:()=>started(),release:()=>{clearTimeout(timer);release();}};
  gates.push(value);return value;
 }
 const chat=gate(),activityList=gate();
 let awaitActivityList=false;
 const server=http.createServer(async(req,res)=>{
  try {
   const url=new URL(req.url,"http://localhost"),pathname=url.pathname;
   if(pathname.startsWith("/api/")) {
    res.setHeader("Content-Type","application/json");
    let result={sessions:[],projects:[],providers:[],models:[],workers:[],tasks:[],settings:{},ui:{lang:"pl",rememberSessionRuntime:false}};
    if(pathname==="/api/health")result={ok:true,chat_ready:true,model:"fixture",home:project};
    if(pathname==="/api/sessions") {
     listCount++;
     const snapshot=JSON.parse(JSON.stringify(project==="A"?rowsA:rowsB));
     result=snapshot.sort((a,b)=>b.updated_at.localeCompare(a.updated_at));
     if(holdList) {const held=holdList;holdList=null;held.start();await held.wait;if(held.error) {res.statusCode=500;res.end("old request failed");return;}}
     if(awaitActivityList) {awaitActivityList=false;activityList.start();}
    }
    if(pathname==="/api/transcript")result={messages:[],has_more:false};
    if(pathname==="/api/chat") {
     let raw="";for await(const part of req)raw+=part;
     const input=JSON.parse(raw);
     assert.equal(input.session_id,"old");
     rowsA[1].updated_at=new Date().toISOString();rowsA[1].message_count++;
     res.setHeader("Content-Type","text/event-stream");
     const emit=ev=>res.write("data: "+JSON.stringify(ev)+"\n\n");
     emit({type:"session",session_id:"old"});
     awaitActivityList=true;
     emit({type:"session_activity",session_id:"old"});
     chat.start();await chat.wait;
     emit({type:"message",text:"Gotowe."});
     emit({type:"done",tok_in:1,tok_out:1,tok_total:2});
     res.end();return;
    }
    res.end(JSON.stringify(result));return;
   }
   const filename=pathname==="/"?"assets/index.html":pathname.startsWith("/.__supercli/ui/")?"shared-ui/"+path.basename(pathname):"assets"+pathname;
   const assets=path.join(root,"internal","webgui"),file=path.resolve(assets,filename);
   if(!file.startsWith(assets+path.sep)) {res.statusCode=404;res.end();return;}
   const body=await fs.readFile(file);
   res.setHeader("Content-Type",pathname.endsWith(".css")?"text/css":pathname.endsWith(".js")?"text/javascript":"text/html");
   res.end(body);
  } catch(error) {res.statusCode=500;res.end(String(error));}
 });
 await new Promise((resolve,reject)=>{server.once("error",reject);server.listen(0,"127.0.0.1",resolve);});
 let browser;
 try {
  const profile=await fs.mkdtemp(path.join(temp,"browser-"));
  browser=await chromium.launchPersistentContext(profile,{
   executablePath:process.env.PLAYWRIGHT_BROWSER_PATH || "C:/Program Files/Google/Chrome/Application/chrome.exe",
   headless:true,timeout:15000,viewport:{width:1280,height:850},
  });
  const page=await browser.newPage();page.setDefaultTimeout(10000);
  const errors=[];page.on("pageerror",e=>errors.push(e.message));
  await page.goto("http://127.0.0.1:"+server.address().port,{waitUntil:"load"});
  await page.evaluate(async()=>{
   ui.lang="pl";ui.rememberSessionRuntime=false;ui.sidebarHidden=false;applyUI();applyI18n();
   activateSideTab(document.querySelector('#side-tabs button[data-tab="sessions"]'));
   const originalLoad=loadSessions;
   loadSessions=function(){window.latestList=originalLoad();return window.latestList;};
   await loadSessions();
  });
  const ids=()=>page.evaluate(()=>Array.from(document.querySelectorAll("#session-list .side-item")).map(n=>n.dataset.sessionId));
  assert.deepEqual(await ids(),["new","old"]);
  await page.evaluate(async()=>{await resumeSession("old",sessionByID.old);await sessionRuntimeReady;await window.latestList;});
  assert.deepEqual(await ids(),["new","old"],"opening without writing must not promote a chat");
  const requestsBefore=listCount;
  await page.evaluate(()=>{window.turn=sendPrompt("Wracam dzisiaj do tej rozmowy.");});
  await chat.started;await activityList.started;
  await page.evaluate(async()=>{await window.latestList;});
  assert.deepEqual(await ids(),["old","new"],"old chat must move before the model finishes");
  assert.equal(await page.evaluate(()=>streaming),true);
  const groups=await page.locator("#session-list .session-date").allTextContents();
  assert.deepEqual(groups,["Dzisiaj"]);
  assert.equal(await page.locator("#session-list .side-item.active").getAttribute("data-session-id"),"old");
  assert.equal(listCount-requestsBefore,1,"activity should cause one refresh, not polling");
  assert.equal(await page.locator("#session-list").isVisible(),true);
   await page.screenshot({path:path.join(temp,"promoted-while-streaming.png")});
  chat.release();
  await page.evaluate(async()=>{await window.turn;});
  assert.deepEqual(await ids(),["old","new"]);
  await page.reload({waitUntil:"load"});
  await page.evaluate(async()=>{await loadSessions();});
  assert.deepEqual(await ids(),["old","new"],"ordering survives reload");

  // Exercise stale completion even when a network adapter ignores abort.
  await page.evaluate(()=>{
   const originalJ=j;
   j=function(url,opts){const next=Object.assign({},opts);delete next.signal;return originalJ(url,next);};
  });
  for(const fail of [false,true]) {
   project="A";
   const stale=gate();stale.error=fail;holdList=stale;
   await page.evaluate(()=>{window.staleList=loadSessions();});
   await stale.started;
   project="B";
   await page.evaluate(async()=>{projectEpoch++;await loadSessions();});
   stale.release();
   await page.evaluate(async()=>{await window.staleList;});
   assert.deepEqual(await ids(),["other"],"stale "+(fail?"failure":"response")+" overwrote another project");
  }
  assert.deepEqual(errors,[]);
  await fs.writeFile(path.join(temp,"ui-result.json"),JSON.stringify({passed:true,groups,listCount,checks:["promote before reply","activity grouping","opening does not promote","reload","stale success","stale failure"]},null,2));
  console.log("PASS: promotion before reply, activity date, reload, stale responses, no session polling");
 } finally {
  gates.forEach(g=>g.release());
  if(browser)await browser.close();
  server.closeAllConnections();await new Promise(resolve=>server.close(resolve));
 }
})().catch(error=>{console.error(error);process.exitCode=1;});
