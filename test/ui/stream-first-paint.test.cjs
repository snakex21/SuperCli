const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const test=require('node:test');

test('first assistant fragment paints immediately and later chunks remain batched',()=>{
 const timers=[];const paints=[];
 const context={setTimeout(fn){timers.push(fn);return timers.length;},clearTimeout(){},window:{},superCliUI:{},$(selector){return {addEventListener(){}};}};
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
