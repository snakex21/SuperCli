'use strict';
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const test=require('node:test');
function harness(){
  const frames=new Map();let id=0,now=0,height=1000,reads=0,paints=0,scrollListener;
  const stage={scrollTop:0,clientHeight:100,addEventListener(name,fn){scrollListener=fn;}};
  Object.defineProperty(stage,'scrollHeight',{get(){reads++;return height;}});
  const c={window:{},superCliUI:{},performance:{now:()=>now},$(){return stage;},requestAnimationFrame(fn){frames.set(++id,fn);return id;},cancelAnimationFrame(id){frames.delete(id);}};
  vm.createContext(c);
  for(const file of ['03-markdown.js','04-transcript.js'])vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js',file),'utf8'),c);
  c.renderAssistant=n=>{paints++;height=1000+(n._displayParts||c.assistantTextParts(n._raw)).reduce((v,p)=>v+p.text.length,0);};
  const n={_raw:'First.',_renderTimer:null,isConnected:true};
  function frame(ms=16){now+=ms;for(const key of [...frames.keys()]){const fn=frames.get(key);if(fn){frames.delete(key);fn(now);}}}
  return {c,n,stage,frames,frame,elapse(ms){now+=ms;},reads:()=>reads,height:()=>height,paints:()=>paints,scroll(){scrollListener();}};
}
test('pending paced text paints before the single tail geometry read',()=>{
  const h=harness();h.c.scheduleAssistantRender(h.n);h.elapse(40);
  h.n._raw+=' more received text '.repeat(20);h.c.scheduleAssistantRender(h.n);
  assert.equal(h.frames.size,1);h.frame();assert.equal(h.reads(),1);
  assert.equal(h.stage.scrollTop,h.height());assert.equal(h.paints(),2);
  for(let i=0;i<6;i++){h.frame();assert.equal(h.stage.scrollTop,h.height());}
  assert.equal(h.frames.size,0);
});
test('first sparse content and tool bursts scroll on the next frame without extra paints',()=>{
  const h=harness();h.c.scheduleAssistantRender(h.n);
  for(let i=0;i<10;i++)h.c.smartScroll();
  assert.equal(h.frames.size,1);h.frame();assert.equal(h.reads(),1);assert.equal(h.paints(),1);
  assert.equal(h.stage.scrollTop,h.height());assert.equal(h.frames.size,0);
});
test('manually scrolling away from the tail remains respected during paced paints',()=>{
  const h=harness();h.c.scheduleAssistantRender(h.n);h.frame();h.elapse(40);
  h.n._raw+=' more received text '.repeat(20);h.c.scheduleAssistantRender(h.n);
  h.stage.scrollTop=0;h.scroll();assert.equal(h.c.transcriptFollowTail,false);const reads=h.reads();
  for(let i=0;i<8;i++)h.frame();
  assert.equal(h.stage.scrollTop,0);assert.equal(h.reads(),reads);
  h.c.smartScroll(true);h.frame();assert.equal(h.stage.scrollTop,h.height());
});
test('a detached message does not lose an already pending scroll from another block',()=>{
  const h=harness();h.c.scheduleAssistantRender(h.n);h.elapse(40);
  h.n._raw+=' queued text';h.c.scheduleAssistantRender(h.n);h.n.isConnected=false;h.c.smartScroll();
  h.frame();assert.equal(h.frames.size,0);assert.equal(h.stage.scrollTop,h.height());assert.equal(h.paints(),1);
});
