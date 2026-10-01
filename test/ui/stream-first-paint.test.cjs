const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const test=require('node:test');

test('first assistant fragment paints immediately and later chunks remain batched',()=>{
 const timers=[];const paints=[];
 const context={requestAnimationFrame(fn){timers.push(fn);return timers.length;},cancelAnimationFrame(){},window:{},superCliUI:{},$(selector){return {addEventListener(){}};}};
 vm.createContext(context);
 vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js/04-transcript.js'),'utf8'),context);
 context.renderAssistant=n=>paints.push(n._raw);
 context.smartScroll=()=>{};
 const node={_raw:'first',_renderTimer:null};
 context.scheduleAssistantRender(node);
 assert.deepEqual(paints,['first']);
 assert.equal(timers.length,0);
 node._raw+=' second';context.scheduleAssistantRender(node);
 node._raw+=' third';context.scheduleAssistantRender(node);
 assert.equal(timers.length,1);
 assert.equal(paints.length,1);
 timers.shift()();
 assert.deepEqual(paints,['first','first second third']);
});

test('long answers use one frame without a length delay; flush and navigation cancel stale work',()=>{
 const frames=new Map(),paints=[];let id=0,scrolls=0;
 const context={requestAnimationFrame(fn){frames.set(++id,fn);return id},cancelAnimationFrame(id){frames.delete(id)},window:{},superCliUI:{},$(){return {addEventListener(){}}}};
 vm.createContext(context);
 vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js/04-transcript.js'),'utf8'),context);
 context.renderAssistant=n=>paints.push(n._raw);context.smartScroll=()=>scrolls++;
 const node={_raw:'x'.repeat(64000),_renderTimer:null,isConnected:true};
 context.scheduleAssistantRender(node);
 assert.equal(paints.length,1);
 node._raw+=' A';context.scheduleAssistantRender(node);
 node._raw+=' B';context.scheduleAssistantRender(node);
 assert.equal(frames.size,1);
 context.flushAssistantRender(node);
 assert.equal(frames.size,0);assert.equal(paints.at(-1),node._raw);
 node._raw+=' C';context.scheduleAssistantRender(node);
 const stale=frames.values().next().value;node.isConnected=false;
 const before=scrolls;stale();
 assert.equal(paints.length,2);assert.equal(scrolls,before);assert.equal(node._renderTimer,null);
});
