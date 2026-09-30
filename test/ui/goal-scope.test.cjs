const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');
function ui() {
 const requests=[];
 function node(tag,cls,text='') { return {tag,className:cls,textContent:text,value:'',children:[],listeners:{},classList:{contains:()=>false},appendChild(child){this.children.push(child);return child;},setAttribute(){},addEventListener(kind,fn){this.listeners[kind]=fn;},focus(){},set innerHTML(value){this.children=[];},get innerHTML(){return ''}}; }
 const panel=node('main');
 const context={sections:{},panelContent:panel,el:node,t:key=>key,i18nEl:(tag,cls,key)=>node(tag,cls,key),escHtml:value=>value,activeSessionID:'synthetic-session',toast(){},$:()=>node('div'),document:{createTextNode:text=>node('text','',text)},requests,
 j:async url=>{requests.push({url});return url.includes('catalog=1') ? {unassigned:[],paused:[]} : null;},
 jpost:async (url,body)=>{requests.push({url,body:{...body}});return null;}
 };
 vm.createContext(context);
 for(const file of ['11f-panel-data-memory.js','11g-panel-goal-usage.js'])vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js',file),'utf8'),context);
 return context;
}
test('goal form submits the selected global scope and project switching is available',async()=>{
 const c=ui();c.renderGoalPanel(null);
 const select=c.panelContent.children[0].children[0];
 assert.deepEqual(select.children.map(n=>n.value),['project','global']);
 select.value='global';select.listeners.change();
 await new Promise(setImmediate);
 assert.equal(c.goalPanelScope,'global');
 assert.ok(c.requests.some(r=>r.url==='/api/goal?scope=global'));
 const empty=c.panelContent.children.at(-1);const form=empty.children.find(n=>n.tag==='form');
 form.children[0].value='General goal';form.listeners.submit({preventDefault(){}});
 await new Promise(setImmediate);
 const sent=c.requests.find(r=>r.body && r.body.action==='set');
 assert.equal(sent.body.scope,'global');assert.equal(sent.body.title,'General goal');
});
test('legacy assignment sends the selected goal id and preserves the target',async()=>{
 const c=ui();c.goalUnassigned=[{id:'legacy-goal',title:'Windows work'}];
 c.renderGoalPanel(null);
 const legacyGroup=c.panelContent.children[1];const row=legacyGroup.children[1];
 row.children[1].listeners.click();
 await new Promise(setImmediate);
 const sent=c.requests.find(r=>r.body && r.body.action==='assign');
 assert.equal(sent.body.goal_id,'legacy-goal');assert.equal(sent.body.target,'project');assert.equal(sent.body.scope,'project');
});
test('paused goals can be resumed without mutating an unrelated active goal',async()=>{
 const c=ui();c.goalPanelScope='global';c.goalPaused=[{id:'paused-global',title:'General work',scope:'global'}];
 c.renderGoalPanel(null);
 const group=c.panelContent.children[1];group.children[1].children[1].listeners.click();
 await new Promise(setImmediate);
 const sent=c.requests.find(r=>r.body && r.body.action==='resume');
 assert.equal(sent.body.goal_id,'paused-global');assert.equal(sent.body.scope,'global');
});

test('task buttons submit the displayed goal id instead of whichever goal is active later',async()=>{
 const c=ui();const button=c.goalTaskAction('Done',{seq:1},'done','displayed-goal');
 button.listeners.click();await new Promise(setImmediate);
 const sent=c.requests.find(r=>r.body && r.body.action==='set_task_status');
 assert.equal(sent.body.goal_id,'displayed-goal');
});
