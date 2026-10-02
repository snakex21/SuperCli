const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');

function harness() {
  const frames = new Map(), paints = [];
  let id = 0, now = 0, cost = 0;
  const c = {performance:{now:()=>now}, requestAnimationFrame(fn){frames.set(++id,fn);return id;}, cancelAnimationFrame(id){frames.delete(id);},
    setTimeout(){throw Error('No second timed batch');},window:{},superCliUI:{},$(){return {addEventListener(){}}}};
  vm.createContext(c);
  for (const file of ['03-markdown.js','04-transcript.js']) vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js',file),'utf8'),c);
  function parts(n){return JSON.parse(JSON.stringify(n._displayParts || c.assistantTextParts(n._raw))).map(p=>({kind:p.kind,text:p.text}));}
  c.renderAssistant=n=>{paints.push({at:now,parts:parts(n)});now+=cost;}; c.smartScroll=()=>{};
  function frame(ms=16){now+=ms;const pending=Array.from(frames.values());frames.clear();pending.forEach(fn=>fn());}
  return {c,frames,paints,frame,parts,elapse(ms){now+=ms;},cost(ms){cost=ms;},now(){return now;}};
}
const node=raw=>({_raw:raw,_renderTimer:null,isConnected:true,classList:{add(){}}});

test('first content is immediate; subsequent received prose is paced without changing source',()=>{
  const h=harness(),n=node('First.');h.c.scheduleAssistantRender(n);
  assert.equal(h.paints[0].parts[0].text,'First.');assert.equal(h.frames.size,0);
  h.elapse(280);n._raw+=' A larger packet with several words.';const exact=n._raw;h.c.scheduleAssistantRender(n);
  assert.equal(h.frames.size,1);h.frame();assert.equal(n._raw,exact);
  assert.ok(h.parts(n)[0].text.length>6 && h.parts(n)[0].text.length<exact.length);
  for(let i=0;i<21;i++)h.frame();
  assert.equal(h.parts(n)[0].text,exact);assert.equal(h.frames.size,0);
});

test('the first answer finishes queued reasoning synchronously and stays below a stable thought',()=>{
  const h=harness(),n=node('<thinking>First thought');n._reasoningOpen=true;
  h.c.scheduleAssistantRender(n);h.elapse(280);
  h.c.appendAssistantReasoning(n,' and a much longer reasoning packet with several words.');
  h.c.closeAssistantReasoning(n);n._raw+='Answer starts now.';h.c.scheduleAssistantRender(n);
  assert.deepEqual(h.parts(n),[{kind:'thinking',text:'First thought and a much longer reasoning packet with several words.'},{kind:'markdown',text:'\nAnswer starts now.'}]);
  assert.equal(h.frames.size,0); const paintsAtBoundary=h.paints.length;
  const original=n._raw;h.frame();
  assert.equal(h.parts(n)[0].text,h.c.assistantTextParts(n._raw)[0].text);
  assert.equal(h.paints.length,paintsAtBoundary);
  assert.equal(h.parts(n)[1].text,'\nAnswer starts now.');assert.equal(n._raw,original);
  for(let i=0;i<21;i++)h.frame();
  assert.equal(h.parts(n)[0].text,h.c.assistantTextParts(original)[0].text);assert.equal(h.frames.size,0);
});

test('steady Zen-sized packets stop producing long visible gaps after the initial wait',()=>{
  const h=harness(),n=node('Start.'),packets=[];h.c.scheduleAssistantRender(n);
  for(let k=0;k<18;k++){
    const gap=[280,250,300,330][k%4];let rest=gap;
    while(rest>=16){h.frame();rest-=16;}h.elapse(rest);
    n._raw+=' Another packet with several words.';packets.push({at:h.now(),length:n._raw.length});h.c.scheduleAssistantRender(n);
  }
  for(let i=0;i<22;i++)h.frame();
  for(const packet of packets){const paint=h.paints.find(p=>p.at>=packet.at&&p.parts[0].text.length>=packet.length);assert.ok(paint);assert.ok(paint.at-packet.at<=336);}
  const gaps=h.paints.slice(3).map((p,i)=>p.at-h.paints[i+2].at);
  assert.ok(Math.max(...gaps)<=64,'largest steady-stream display gap '+Math.max(...gaps));
  assert.equal(h.parts(n)[0].text,n._raw);assert.equal(h.frames.size,0);
});

test('cheap formatting follows high-refresh frames while retaining a short fast-provider buffer',()=>{
  for(const hz of [120,144,165,240]){
    const h=harness(),n=node('Start.');h.cost(.2);h.c.scheduleAssistantRender(n);h.elapse(40);
    n._raw+='x'.repeat(480);const arrival=h.now();h.c.scheduleAssistantRender(n);
    const interval=1000/hz;
    for(let i=0;i<Math.ceil(64/interval);i++)h.frame(interval);
    assert.equal(h.parts(n)[0].text,n._raw);assert.equal(h.frames.size,0);
    assert.ok(h.paints.at(-1).at-arrival<=48+interval+.2);
    const gaps=h.paints.slice(2).map((paint,index)=>paint.at-h.paints[index+1].at);
    assert.ok(gaps.length>=4,'enough actual text updates at '+hz+' Hz');
    assert.ok(Math.max(...gaps)<10,'display interval at '+hz+' Hz: '+Math.max(...gaps));
    assert.ok(Math.abs(h.paints[1].at-arrival-interval)<1e-9,'first pending update uses the next native frame');
  }
});

test('a 60 Hz display stays on native frames without requesting faster timer paints',()=>{
  const h=harness(),n=node('Start.');h.cost(.2);h.c.scheduleAssistantRender(n);h.elapse(40);
  n._raw+='x'.repeat(480);const arrival=h.now();h.c.scheduleAssistantRender(n);
  assert.equal(h.paints.length,1);
  h.frame(1000/60);
  assert.ok(Math.abs(h.paints[1].at-arrival-1000/60)<1e-9);
  for(let i=0;i<4;i++)h.frame(1000/60);
  assert.equal(h.parts(n)[0].text,n._raw);assert.equal(h.frames.size,0);
  for(let i=2;i<h.paints.length;i++)assert.ok(h.paints[i].at-h.paints[i-1].at>=1000/60);
});

test('moderately expensive formatting retains the work budget at high refresh',()=>{
  const h=harness(),n=node('Start.');h.cost(2);h.c.scheduleAssistantRender(n);h.elapse(40);
  n._raw+='x'.repeat(480);const arrival=h.now();h.c.scheduleAssistantRender(n);
  for(let i=0;i<30;i++)h.frame(2);
  assert.equal(h.parts(n)[0].text,n._raw);assert.equal(h.frames.size,0);
  assert.ok(h.paints.at(-1).at-arrival<=64);
  for(let i=1;i<h.paints.length;i++)assert.ok(h.paints[i].at-h.paints[i-1].at>=16);
});

test('sparse characters are not invented or repainted just to fill display frames',()=>{
  const h=harness(),n=node('Start.');h.cost(.2);h.c.scheduleAssistantRender(n);h.elapse(280);
  n._raw+='ab';h.c.scheduleAssistantRender(n);
  for(let i=0;i<80;i++)h.frame(1000/240);
  assert.equal(h.parts(n)[0].text,n._raw);assert.equal(h.frames.size,0);
  assert.deepEqual(h.paints.map(p=>p.parts[0].text),['Start.','Start.a','Start.ab']);
});

test('completion and tool boundaries synchronously drain exact received text',()=>{
  const h=harness(),n=node('<thinking>First');n._reasoningOpen=true;
  h.c.scheduleAssistantRender(n);h.elapse(280);h.c.appendAssistantReasoning(n,' longer thought');
  h.c.closeAssistantReasoning(n);n._raw+='Answer';h.c.scheduleAssistantRender(n);h.frame();
  assert.equal(h.parts(n)[0].text,h.c.assistantTextParts(n._raw)[0].text);
  h.elapse(280); n._raw+=' continues'; h.c.scheduleAssistantRender(n);
  assert.notEqual(h.parts(n)[1].text,h.c.assistantTextParts(n._raw)[1].text);
  h.c.sealAssistantSegment(n);
  assert.equal(h.frames.size,0);assert.equal(n._renderTimer,null);assert.equal(n._displayParts,null);assert.equal(n._pacedParts,null);
  assert.equal(n._sealed,true);assert.deepEqual(h.parts(n),JSON.parse(JSON.stringify(h.c.assistantTextParts(n._raw))));
});

test('large and expensive updates bypass animation instead of multiplying formatting work',()=>{
  const h=harness(),n=node('Large table.');h.cost(10);h.c.scheduleAssistantRender(n);h.elapse(280);
  n._raw+=' another packet with multiple words';h.c.scheduleAssistantRender(n);
  assert.equal(h.parts(n)[0].text,n._raw);assert.equal(h.frames.size,0);
  h.cost(0);h.c.flushAssistantRender(n);n._raw+='x'.repeat(3000);h.c.scheduleAssistantRender(n);
  assert.equal(h.parts(n)[0].text,n._raw);assert.equal(h.frames.size,0);
});

test('Unicode stays well formed and protocol markers remain outside displayed parts',()=>{
  const h=harness(),n=node('<thinking>Start');n._reasoningOpen=true;h.c.scheduleAssistantRender(n);h.elapse(280);
  h.c.appendAssistantReasoning(n,'😀'.repeat(15));
  for(let i=0;i<22;i++)h.frame();
  for(const paint of h.paints)for(const part of paint.parts){assert.equal(part.text.isWellFormed(),true);assert.ok(!part.text.includes('<thinking>'));}
  assert.equal(h.parts(n)[0].text,'Start'+'😀'.repeat(15));
});

test('detached messages cancel pending display work; suspended frames catch up on resume',()=>{
  const h=harness(),n=node('Start.');h.c.scheduleAssistantRender(n);h.elapse(280);
  n._raw+=' a long packet';h.c.scheduleAssistantRender(n);n.isConnected=false;const count=h.paints.length;h.frame();
  assert.equal(h.paints.length,count);assert.equal(h.frames.size,0);assert.equal(n._renderTimer,null);
  const resumed=node('Ready.');h.c.scheduleAssistantRender(resumed);h.elapse(280);resumed._raw+=' remaining text';h.c.scheduleAssistantRender(resumed);
  h.frame(1000);assert.equal(h.parts(resumed)[0].text,resumed._raw);assert.equal(h.frames.size,0);
});

test('replacement snapshots immediately correct source and cancel stale animation',()=>{
  const h=harness(),n=node('Original.');h.c.scheduleAssistantRender(n);h.elapse(280);n._raw+=' text pending';h.c.scheduleAssistantRender(n);
  n._raw='Corrected.';h.c.scheduleAssistantRender(n);
  assert.equal(h.parts(n)[0].text,'Corrected.');assert.equal(h.frames.size,0);assert.equal(n._renderTimer,null);
});
