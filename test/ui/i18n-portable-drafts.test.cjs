const assert=require('node:assert/strict');const fs=require('node:fs');const path=require('node:path');const vm=require('node:vm');const test=require('node:test');
const runtime=fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/shared-ui/runtime.js'),'utf8');
function harness(settings,legacy) {
 const reads=[];const writes=[];const input={value:'',listeners:{},addEventListener(k,f){this.listeners[k]=f;}};
 const window={addEventListener(){}};
 const context={window,Date,Blob,setTimeout,clearTimeout,navigator:{},localStorage:{getItem(){return JSON.stringify(legacy);},setItem(){throw Error('Browser storage write is forbidden');}},fetch:async(url,options)=>{
  if(options&&options.method==='POST'){const patch=JSON.parse(options.body);writes.push(patch);Object.assign(settings,patch);return {ok:true,json:async()=>({ok:true})};}
  reads.push(url);return {ok:true,json:async()=>({settings})};
 }};
 vm.createContext(context);vm.runInContext(runtime,context);
 return {reads,writes,input,store:window.SuperCliUI.createComposerDraftStore({storageKey:'drafts',input,getScope:()=> 'session:test'})};
}
test('legacy drafts migrate to portable settings once and clearing survives reload',async()=>{
 const settings={};const legacy={'session:test':{text:'unsent draft',updated:123}};
 const initial=harness(settings,legacy);await initial.store.load();assert.equal(initial.input.value,'unsent draft');assert.equal(initial.writes.length,1);assert.equal(settings['drafts.legacy-migrated'],true);
 initial.store.clear('session:test','unsent draft');await initial.store.flush();assert.equal(Object.keys(settings.drafts).length,0);
 const reopened=harness(settings,legacy);await reopened.store.load();assert.equal(reopened.input.value,'');assert.equal(reopened.writes.length,0);
});
test('server drafts restore without browser persistence and preserve a newer typed draft',async()=>{
 const settings={drafts:{'session:test':{text:'portable draft',updated:123}},'drafts.legacy-migrated':true};
 const h=harness(settings,{});await h.store.load();assert.equal(h.input.value,'portable draft');
 h.input.value='newer draft';h.store.capture();await h.store.flush();const next=harness(settings,{});await next.store.load();assert.equal(next.input.value,'newer draft');
});

test('legacy attachment indexes migrate once and a cleared server index survives reload',async()=>{
 const assets=path.resolve(__dirname,'../../internal/webgui/assets/js');
 const chat=fs.readFileSync(path.join(assets,'05-chat.js'),'utf8').split('function renderSentAttachments')[0];
 const settingsSource=fs.readFileSync(path.join(assets,'02-ui-settings.js'),'utf8');
 const legacyAttachments=[{session:'s1',seq:2,paths:['C:/portable/image.png'],at:1}];const saved={};const writes=[];
 async function load() {
  const input={};const c={document:{hidden:false,hasFocus:()=>true,documentElement:{classList:{contains:()=>false}}},window:{matchMedia:()=>({matches:false}),addEventListener(){}},
   $:()=>input,superCliUI:{createComposerDraftStore:()=>({})},setTimeout,clearTimeout,detectedLanguage:()=> 'en',normalizeLanguage:v=>v,loadLanguage:async v=>v,
   localStorage:{getItem:key=>JSON.stringify(key==='supercli-ui'?{}:legacyAttachments),setItem:()=>assert.fail('localStorage write')},
   j:async()=>({settings:saved}),jpost:async(url,body)=>{writes.push(body);Object.assign(saved,body);},
  };vm.createContext(c);vm.runInContext(chat,c);vm.runInContext(settingsSource,c);c.applyUI=()=>{};await c.loadUI();return c;
 }
 const first=await load();assert.equal(first.sentAttachmentIndex[0].paths[0],'C:/portable/image.png');assert.equal(writes.length,1);
 saved['supercli-sent-attachments-v1']=[];const next=await load();assert.equal(next.sentAttachmentIndex.length,0);assert.equal(writes.length,1);
});

test('GUI reuses its settings snapshot while standalone draft consumers fetch once',async()=>{
 const settings={drafts:{'session:test':{text:'restored from initial settings',updated:123}},'drafts.legacy-migrated':true};
 const gui=harness(settings,{});await gui.store.load(settings);
 assert.equal(gui.reads.length,0);assert.equal(gui.input.value,'restored from initial settings');assert.equal(gui.writes.length,0);
 const standalone=harness(settings,{});await standalone.store.load();assert.deepEqual(standalone.reads,['/api/settings']);assert.equal(standalone.input.value,'restored from initial settings');
 const failedStartup=harness(settings,{});await failedStartup.store.load(undefined);assert.equal(failedStartup.reads.length,1);assert.equal(failedStartup.input.value,'restored from initial settings');
});
