import {assert, test, readSource, readDir, sourcePath, testDir, createSandbox, runScript} from './harness.js';
const root = sourcePath(testDir, '../../internal/webgui/assets');
const chat = readSource(sourcePath(root, 'js/05-chat.js'), 'utf8');
const transcript = readSource(sourcePath(root, 'js/04-transcript.js'), 'utf8');
const en = JSON.parse(readSource(sourcePath(root, 'locales/en.json'), 'utf8'));
function node(tag, cls, text) {
  return {tag, className:cls || '', textContent:text || '', children:[], events:{}, attrs:{}, dataset:{}, hidden:false,
    classList:{add(){},remove(){},toggle(){}},
    appendChild(n){this.children.push(n); n.parentElement=this; return n;}, setAttribute(k,v){this.attrs[k]=v;},
    addEventListener(k,v){this.events[k]=v;}, focus(){},
    showModal(){this.open=true;}, close(){this.open=false;},
    remove(){if(this.parentElement)this.parentElement.children=this.parentElement.children.filter(n=>n!==this);this.parentElement=null;},
    contains(n){return n===this || this.children.some(child=>child.contains(n));},
    querySelectorAll(selector){return this.children.flatMap(n=>[...(selector==='.attachment-remove' && n.className==='attachment-remove' ? [n] : []),...n.querySelectorAll(selector)]);},
    set innerHTML(value){this.children=[];}, get innerHTML(){return '';},
    get isConnected(){return !!this.parentElement;},
  };
}
function harness() {
 const host=node('div'); const requests=[], sent=[], errors=[];
 const w={console, Promise, window: new EventTarget(), setTimeout, promptQueue:[], projectEpoch:0, promptQueueLoadSeq:0, runStopRevision:0, streaming:false, pauseQueue:true,
  activeSessionID:'s1',activeWorkspacePath:'fixture',composerDraftStore:{scope:()=> 'session:s1'},
  queueDispatching:false, queueDispatchItem:null, pendingImmediate:null,
  document:{createElement:node,body:node('body'),addEventListener(){},removeEventListener(){}}, activeAppDialog:null, promptEl:node('textarea'), FormData:class { constructor(){this.files=[];} append(key,file,name){this.files.push({key,file,name});} },
  fetch:async(url,options)=>{w.uploads.push(options.body.files);return {ok:true,json:async()=>({paths:options.body.files.map(f=>f.name)})};}, uploads:[], pendingAttachments:[],
  t:k=>en[k]||k, el:node, i18nEl:(tag,cls,key)=>node(tag,cls,en[key]), $:()=>host,
  isQueuedTaskTransition:()=>false, appendQueuedAttachmentBadge(){}, styleQueuedTaskTransition(){}, wireQueuedTaskDrag(){},
  queueDragHandle:()=>node('button'), queuePositionButton:()=>node('button'), updateAppBadge(){}, renderTaskCenter(){},
  removeQueuedTask(){}, toast:e=>errors.push(e), runQueuedTask:q=>sent.push(q),
  j:async (url,options)=>{requests.push({url,body:JSON.parse(options.body)});},
  jpost:async(url,body)=>{requests.push({url,body}); return w.batches;},
  loadPromptQueue:()=>w.promptQueueReady=Promise.resolve(true),
 };
 createSandbox(w);
 runScript(chat.slice(chat.indexOf('function attachmentName'),chat.indexOf('function attachmentTranscriptText')),w);
 runScript(chat.slice(chat.indexOf('function addAttachmentPaths'),chat.indexOf('function clearAttachmentPreviewContent')),w);
 runScript(chat.slice(chat.indexOf('function renderAttachments'),chat.indexOf('function clearAttachments')),w);
 runScript(readSource(sourcePath(root,'js/05a-lightbox.js'),'utf8'),w);
 runScript(chat.slice(chat.indexOf('var queueEdit ='), chat.indexOf('function clearQueueDropState')),w);
 runScript(chat.slice(chat.indexOf('function renderPromptQueue()'), chat.indexOf('// Queued attachments keep')),w);
 return {w,host,requests,sent,errors};
}
test('merge button uses ordered durable IDs and localized numbering; sends only first safe batch', async()=>{
 const h=harness(); h.w.promptQueue=[{id:'a',text:'one'},{id:'b',text:'two'}];
 h.w.batches=[{id:'merged-1',prompt:'Message 1/2\none',merged:true,attachments:['a.png']},{id:'merged-2',prompt:'Message 2/2\ntwo',merged:true,attachments:['b.png']}];
 h.w.renderPromptQueue();
 const button=h.host.children.find(n=>n.className.includes('queue-merge'));
 assert.equal(button.textContent,'Send together');
 button.events.click(); await new Promise(resolve => setTimeout(resolve, 0));
 assert.equal(h.requests.length,1); assert.equal(h.requests[0].url,'/api/tasks?merge=1');
 assert.deepEqual(Array.from(h.requests[0].body.ids),['a','b']);
 assert.equal(h.requests[0].body.label,'Message {index}/{count}');
 assert.equal(h.w.queuedMessageLabel(2,3),'Message 2/3');
 assert.equal(h.sent.length,1); assert.equal(h.sent[0].id,'merged-1');
 assert.equal(h.w.promptQueue.length,2); assert.equal(h.w.pauseQueue,false);
 assert.deepEqual(h.errors,[]);
});
test('merge while a reply is streaming waits for the normal handoff and duplicate clicks do not duplicate requests',async()=>{
 const h=harness();h.w.streaming=true;h.w.promptQueue=[{id:'a',text:'one'}];
 let release;h.w.jpost=async(url,body)=>{h.requests.push({url,body});return new Promise(r=>release=r);};
 const first=h.w.mergeQueuedMessages(true); const second=await h.w.mergeQueuedMessages(true);
 assert.equal(second,false);assert.equal(h.requests.length,1);
 release([{id:'merged-x',prompt:'Message 1/1\none',merged:true}]); await first;
 assert.equal(h.sent.length,0);assert.equal(h.w.promptQueue[0].merged,true);
 // Safe split rows stay separate instead of being recursively re-merged.
 h.w.streaming=false;await h.w.mergeQueuedMessages(true);
 assert.equal(h.requests.length,1);assert.equal(h.sent.length,1);
});
test('a failed unsafe split keeps the original queue and pauses instead of sending an oversized request',async()=>{
 const h=harness();const original=[{id:'a',text:'one',attachments:['huge.png']}];h.w.promptQueue=original;
 h.w.jpost=async()=>{throw new Error('too large');};
 assert.equal(await h.w.mergeQueuedMessages(true),false);
 assert.equal(h.w.promptQueue,original);assert.equal(h.w.pauseQueue,true);assert.equal(h.sent.length,0);
 assert.deepEqual(h.errors,['too large']);
});
test('a project switch during merge never replaces or sends the next project queue',async()=>{
 const h=harness();h.w.promptQueue=[{id:'old',text:'old'}];let release;
 h.w.jpost=async()=>new Promise(r=>release=r);
 const merging=h.w.mergeQueuedMessages(true);h.w.projectEpoch++;h.w.promptQueue=[{id:'new',text:'new'}];
 release([{id:'merged-old',prompt:'old'}]);await merging;
 assert.equal(h.w.promptQueue[0].id,'new');assert.equal(h.sent.length,0);
});
test('modal editor save PATCHes the durable text; cancel never writes',async()=>{
 const h=harness();const q={id:'q',text:'before'};h.w.promptQueue=[q];
 h.w.editQueuedTask(q);let editor=currentEditor(h);
 assert.ok(editor);const input=editor.children.find(n=>n.tag==='textarea');input.value='after\nline 2';input.events.input();
 await editorButton(editor,'common.save').events.click();
 assert.deepEqual(h.requests,[{url:'/api/tasks',body:{id:'q',prompt:'after\nline 2',attachments:[]}}]);
 assert.equal(h.w.queueEdit,null);
 h.w.editQueuedTask(q);editor=currentEditor(h);
 editorButton(editor,'common.cancel').events.click();assert.equal(h.w.queueEdit,null);assert.equal(h.requests.length,1);
});
function currentEditor(h) { return h.w.document.body.children.find(n=>n.className==='app-dialog queue-editor'); }
function editorButton(editor,key) { return editor.children.find(n=>n.className.includes('queue-edit-actions')).children.find(n=>n.tag==='button' && n.textContent===en[key]); }
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
test('queue editor removes and adds uploaded attachments, saves the complete list for live sync',async()=>{
 const h=harness();const q={id:'q',text:'before',attachments:['old.png','keep.pdf']};h.w.promptQueue=[q];
 h.w.editQueuedTask(q);let editor=currentEditor(h);
 let list=editor.children.find(n=>n.className.includes('queue-edit-attachments'));
 assert.equal(list.children.length,2);
 list.children[0].children.find(n=>n.className==='attachment-remove').events.click();
 assert.deepEqual(Array.from(h.w.queueEdit.attachments),['keep.pdf']);
 const picker=editor.children.find(n=>n.type==='file');picker.files=[{name:'new.png'}];picker.events.change();
 assert.equal(editorButton(editor,'common.save').disabled,true);
 await tick();editor=currentEditor(h);
 assert.equal(h.w.uploads.length,1);assert.equal(h.w.uploads[0][0].name,'new.png');
 assert.deepEqual(q.attachments,['old.png','keep.pdf'],'durable item stays unchanged before Save');
 await editorButton(editor,'common.save').events.click();
 assert.deepEqual(h.requests[0],{url:'/api/tasks',body:{id:'q',prompt:'before',attachments:['keep.pdf','new.png']}});
 assert.equal(h.w.queueEdit,null);
});
test('cancel discards attachment removals and uploads, including an upload completing after cancel',async()=>{
 const h=harness();const q={id:'q',text:'before',attachments:['old.png']};h.w.promptQueue=[q];h.w.editQueuedTask(q);
 let editor=currentEditor(h);const list=editor.children.find(n=>n.className.includes('queue-edit-attachments'));
 list.children[0].children.find(n=>n.className==='attachment-remove').events.click();
 let finish;h.w.fetch=async()=>new Promise(resolve=>finish=resolve);
 editor.events.paste({clipboardData:{files:[{name:'late.png'}],getData:()=>''},preventDefault(){},stopPropagation(){}});
 editorButton(editor,'common.cancel').events.click();finish({ok:true,json:async()=>({paths:['late.png']})});await tick();
 assert.equal(h.w.queueEdit,null);assert.deepEqual(q.attachments,['old.png']);assert.equal(h.requests.length,0);
 h.w.editQueuedTask(q);assert.deepEqual(Array.from(h.w.queueEdit.attachments),['old.png']);
});
test('queue editor picker, clipboard and drop share upload validation and the eight-file cap',async()=>{
 const h=harness();const q={id:'q',text:'message',attachments:Array.from({length:7},(_,i)=>i+'.png')};h.w.promptQueue=[q];h.w.editQueuedTask(q);
 let prevented=0,stopped=0;let editor=currentEditor(h);
 editor.events.drop({dataTransfer:{files:[{name:'eighth.png'},{name:'ninth.png'}]},preventDefault(){prevented++;},stopPropagation(){stopped++;}});await tick();
 assert.equal(prevented,1);assert.equal(stopped,1);assert.equal(h.w.uploads.length,0);assert.equal(h.w.queueEdit.attachments.length,7);
 assert.ok(h.errors.some(e=>e.includes('Too many attachments')),'reject the whole selection instead of silently dropping the ninth file');
 assert.deepEqual(q.attachments,Array.from({length:7},(_,i)=>i+'.png'));assert.equal(h.w.queueEdit.uploading,false);assert.equal(h.w.attachmentUpload,null);
 editor=currentEditor(h);assert.equal(editorButton(editor,'composer.attach').disabled,false);
 editor.events.drop({dataTransfer:{files:[{name:'eighth.png'}]},preventDefault(){prevented++;},stopPropagation(){stopped++;}});await tick();
 assert.equal(prevented,2);assert.equal(stopped,2);assert.equal(h.w.uploads.length,1);assert.equal(h.w.uploads[0].length,1);assert.equal(h.w.uploads[0][0].name,'eighth.png');assert.equal(h.w.queueEdit.attachments.length,8);
 editor=currentEditor(h);assert.equal(editorButton(editor,'composer.attach').disabled,true);
 editor.events.paste({clipboardData:{files:[{name:'extra.png'}],getData:()=>''},preventDefault(){},stopPropagation(){}});await tick();
 assert.equal(h.w.uploads.length,1);assert.equal(h.w.pendingAttachments.length,0);
 editor=currentEditor(h);editor.children.find(n=>n.className.includes('queue-edit-attachments')).children[0].children.find(n=>n.className==='attachment-remove').events.click();
 h.w.fetch=async()=>({ok:false,text:async()=> 'invalid type or size'});
 editor.events.paste({clipboardData:{items:[{kind:'file',getAsFile:()=>({name:'bad.bin'})}],getData:()=>''},preventDefault(){},stopPropagation(){}});await tick();
 assert.equal(h.w.queueEdit.attachments.length,7);assert.ok(h.errors.some(e=>e.includes('invalid type or size')));
});
test('claimed or removed queued items retain the draft as a conflict; project changes still close it',async()=>{
 const h=harness();const q={id:'q',text:'old',attachments:['a.png']};h.w.promptQueue=[q];h.w.editQueuedTask(q);
 h.w.j=async()=>{throw Error('task is being dispatched');};h.w.loadPromptQueue=async()=>{h.w.promptQueue=[];h.w.renderPromptQueue();};
 await h.w.saveQueuedTaskEdit();assert.equal(h.w.queueEdit.text,'old');assert.equal(h.w.queueEdit.conflict,true);assert.ok(h.errors.includes('task is being dispatched'));
 h.w.closeQueueEditor(h.w.queueEdit);
 h.w.promptQueue=[q];h.w.editQueuedTask(q);let finish;h.w.j=async()=>new Promise(r=>finish=r);
 const saving=h.w.saveQueuedTaskEdit();h.w.projectEpoch++;h.w.renderPromptQueue();finish({});await saving;assert.equal(h.w.queueEdit,null);
});
test('worker and advisor tooltip/label show model plus actual effort, including restored notifications',()=>{
 const w={t:k=>en[k]||k,el:node};createSandbox(w);
 runScript(transcript.slice(transcript.indexOf('function workerModelInfo'),transcript.indexOf('function addWorkerProgress')),w);
 runScript(transcript.slice(transcript.indexOf('function parseTaskNotification'),transcript.indexOf('function taskStatusLabel')),w);
 for(const kind of ['worker','advisor']){
  const row={_tname:node('span')};w.applyWorkerModelInfo(row,{model:'gpt-5',reasoning_effort:'high',name:kind});
  assert.equal(row.title,'gpt-5 · thinking: high');assert.equal(row._tname.title,row.title);
  assert.equal(row._modelLabel.textContent,row.title);
  w.applyWorkerModelInfo(row,{model:'gpt-5',reasoning_effort:'low'});assert.equal(row._tname.children.length,1);
  assert.equal(row._modelLabel.textContent,'gpt-5 · thinking: low');
 }
 const note=w.parseTaskNotification('<task-notification><task-id>worker-1</task-id><agent>code</agent><worker-model>gpt-5-mini</worker-model><reasoning-effort>medium</reasoning-effort><status>done</status></task-notification>');
 assert.equal(note.model,'gpt-5-mini');assert.equal(note.reasoning_effort,'medium');
});
test('automatic merge dispatches safe batches sequentially via the normal queue path', async()=>{
 const h=harness(); const w=h.w; w.promptQueue=[{id:'a',text:'one'},{id:'b',text:'two'}];w.pauseQueue=false;w.queueMergeMode='auto';
 w.batches=[{id:'merged-1',prompt:'Message 1/2\none',merged:true,attachments:['one.png']},{id:'merged-2',prompt:'Message 2/2\ntwo',merged:true,attachments:['two.png']}];
 w.prepareQueuedTask=async()=>true;w.isAfterRunPrompt=()=>false;
 w.showQueueTransition=q=>{w.queueDispatchItem=q;};
 w.sendPrompt=(text,attachments,draft,item)=>{h.sent.push({text,attachments,id:item.id});};
 runScript(chat.slice(chat.indexOf('async function dispatchQueuedTask('),chat.indexOf('async function runQueuedTask(')),w);
 await w.dispatchQueuedTask(w.promptQueue[0]);
 assert.equal(h.requests.length,1);assert.equal(h.sent.length,1);assert.equal(h.sent[0].id,'merged-1');
 w.promptQueue.shift();w.queueDispatching=false;w.queueDispatchItem=null;
 await w.dispatchQueuedTask(w.promptQueue[0]);
 assert.equal(h.requests.length,1);assert.equal(h.sent.length,2);assert.equal(h.sent[1].id,'merged-2');
 assert.deepEqual(Array.from(h.sent[1].attachments),['two.png']);
});
test('all 27 catalogs translate queue controls and preserve placeholders',()=>{
 const files=readDir(sourcePath(root,'locales')).filter(f=>f.endsWith('.json'));assert.equal(files.length,27);
 for(const file of files){const c=JSON.parse(readSource(sourcePath(root,'locales',file),'utf8'));
  for(const key of ['composer.sendTogether','composer.messageNumber','task.modelThinking','setting.queue_merge.label','setting.queue_merge.desc','modes.desc.queue_merge']){
   assert.equal(typeof c[key],'string',file+': '+key);
   assert.deepEqual((c[key].match(/\{\w+\}/g)||[]).sort(),(en[key].match(/\{\w+\}/g)||[]).sort());
  }
 }
});
