const test=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const path=require('node:path');const vm=require('node:vm');
const source=fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js/12-keyboard-init.js'),'utf8');
test('startup reuses settings for drafts and skips reasoning for the no-model sentinel',async()=>{
 for(const model of ['no model','','qwen']){
  const snapshot={'drafts':{}};const calls=[];let complete;
  const ready=new Promise(resolve=>complete=resolve);
  const c={document:{addEventListener(){}},setInterval(){},loadUI:async()=>snapshot,applyI18n(){},checkHealth:async()=>({model}),selectedModelID:value=>String(value||'').trim()==='no model'?'':value,
   composerDraftStore:{load:async value=>{assert.equal(value,snapshot);calls.push('drafts');}},loadReasoning:()=>calls.push('reasoning'),loadModels:()=>calls.push('models'),loadSessions(){},loadProjects(){},loadPromptQueue(){},loadWorkers(){},renderStats(){},promptEl:{focus:()=>complete()}};
  vm.createContext(c);vm.runInContext(source,c);await ready;
  assert.equal(calls.filter(x=>x==='drafts').length,1);
  assert.equal(calls.filter(x=>x==='reasoning').length,model==='qwen'?1:0);
 }
});

test('initial health and model discovery keep the empty-model label localized',async()=>{
 const assets=path.resolve(__dirname,'../../internal/webgui/assets');
 const i18n=fs.readFileSync(path.join(assets,'js/01-i18n.js'),'utf8');
 const shell=fs.readFileSync(path.join(assets,'js/06-shell.js'),'utf8');
 const models=fs.readFileSync(path.join(assets,'js/10-models.js'),'utf8');
 const health=shell.slice(shell.indexOf('async function checkHealth()'),shell.indexOf('/* ═══ side panel: tabs'));
 const discovery=models.slice(models.indexOf('async function loadModels()'),models.indexOf('function contextBudgetText('));
 const english=JSON.parse(fs.readFileSync(path.join(assets,'locales/en.json'),'utf8'));
 const polish=JSON.parse(fs.readFileSync(path.join(assets,'locales/pl.json'),'utf8'));
 for(const model of ['no model','','custom/my-model']){
  const expected=model==='custom/my-model'?model:polish['model.none'];
  const nodes={};const node=selector=>nodes[selector]||(nodes[selector]={textContent:'',value:'',disabled:false});
  node('#model-name').textContent='no model';
  let complete;const ready=new Promise(resolve=>complete=resolve);
  const calls=[];const c={ui:{lang:'pl'},I18N:{en:english,pl:polish},UI_LANGUAGES:[{code:'en'},{code:'pl'}],
   document:{documentElement:{},addEventListener(){}},navigator:{languages:['pl-PL']},$:node,$$:()=>[],
   activeModelID:'',activeProviderID:'',activeWorkspacePath:'',streaming:false,reasoningRevision:0,
   workspaceDisplayName:value=>value,slimModels:values=>values,saveBlobKey(){},renderModelList(){},renderReasoning(){},
   j:async url=>{calls.push(url);if(url==='/api/health')return {model,home:''};if(url==='/api/models'){assert.equal(node('#model-name').textContent,expected,'health label');return {active:model,provider:'',models:[]};}throw Error('Unexpected request '+url);},
   loadUI:async()=>({}),composerDraftStore:{load:async()=>{}},loadReasoning(){},loadSessions(){},loadProjects(){},loadPromptQueue(){},loadWorkers(){},renderStats(){},setInterval(){},promptEl:{focus(){}}};
  vm.createContext(c);vm.runInContext(i18n,c);vm.runInContext(health,c);vm.runInContext(discovery,c);
  const discover=c.loadModels;c.loadModels=()=>discover().then(complete);
  vm.runInContext(source,c);await ready;
  assert.deepEqual(calls,['/api/health','/api/models']);
  assert.equal(c.activeModelID,model==='custom/my-model'?model:'');
  assert.equal(node('#model-name').textContent,expected,'model discovery label');
 }
});
