const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const test=require('node:test');

function element(_tag,className='',text='') {
 const classes=new Set(className.split(' ').filter(Boolean));
 return {children:[],textContent:text,hidden:false,title:'',classList:{add(...names){names.forEach(n=>classes.add(n));},remove(...names){names.forEach(n=>classes.delete(n));},contains(name){return classes.has(name);}},appendChild(child){this.children.push(child);return child;},querySelector(selector){const name=selector.slice(1);for(const child of this.children){if(child.classList.contains(name))return child;const nested=child.querySelector(selector);if(nested)return nested;}return null;}};
}

for(const language of ['en','pl']) {
 test('worker steering receipts remain visible and localized: '+language,()=>{
  const catalog=JSON.parse(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/locales/'+language+'.json'),'utf8'));
  const context={window:{},superCliUI:{},$(selector){return {addEventListener(){}};}};
  vm.createContext(context);
  vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js/04-transcript.js'),'utf8'),context);
  context.el=element;
  context.t=key=>{assert.ok(catalog[key],'missing translation '+key);return catalog[key];};
  context.clip=(value,length)=>String(value).slice(0,length);
  context.smartScroll=()=>{};
  const existingTool=element('div','task-activity-item running');
  const row={_body:element('div'),_activity:element('div'),_stat:{textContent:'running'},_workerCalls:{'steer-1':existingTool},open:false};
  context.toolRows={'task-call':row};context.workerRows={};context.openToolOrder=[];
  const delivered={id:'worker-1',name:'code',parent_call_id:'task-call',kind:'steering_delivered',call_id:'steer-1',prompt:'Keep the existing findings'};
  assert.equal(context.addWorkerProgress(delivered),true);
  assert.equal(row._activity.children.length,1);
  const receipt=row._activity.children[0];
  assert.equal(receipt.querySelector('.activity-name').textContent,'send_message');
  assert.equal(receipt.querySelector('.activity-status').textContent,catalog['task.done']);
  assert.equal(receipt.querySelector('.activity-hint').textContent,delivered.prompt);
  assert.equal(receipt.classList.contains('done'),true);
  assert.equal(existingTool.classList.contains('running'),true);
  assert.equal(row._stat.textContent,'running');
  context.addWorkerProgress(delivered);
  assert.equal(row._activity.children.length,1,'replayed receipt duplicated');
  context.addWorkerProgress({...delivered,kind:'steering_rejected',call_id:'steer-2',err:'Stopped before delivery',prompt:'x'.repeat(300)});
  assert.equal(row._activity.children.length,2);
  const rejected=row._activity.children[1];
  assert.equal(rejected.classList.contains('failed'),true);
  assert.equal(rejected.querySelector('.activity-status').textContent,catalog['task.failed']);
  assert.equal(rejected.querySelector('.activity-hint').textContent.length,140);
  assert.equal(rejected.title,'Stopped before delivery');
  assert.equal(row._stat.textContent,'running');
 });
}
