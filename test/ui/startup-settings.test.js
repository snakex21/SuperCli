import {assert, test, readSource, sourcePath, testDir, createSandbox, runScript} from './harness.js';
const source=readSource(sourcePath(testDir,'../../internal/webgui/assets/js/12-keyboard-init.js'),'utf8');
test('startup reuses settings for drafts and skips reasoning for the no-model sentinel',async()=>{
 for(const model of ['no model','','qwen']){
  const snapshot={'drafts':{}};const calls=[];let complete;
  const ready=new Promise(resolve=>complete=resolve);
  const c={document:{addEventListener(){}},setInterval(){},loadUI:async()=>snapshot,applyI18n(){},checkHealth:async()=>({model}),selectedModelID:value=>String(value||'').trim()==='no model'?'':value,
   composerDraftStore:{load:async value=>{assert.equal(value,snapshot);calls.push('drafts');}},loadReasoning:()=>calls.push('reasoning'),loadModels:()=>calls.push('models'),loadSessions:async()=>{},resumeLastSessionOnStart(){},loadProjects(){},loadPromptQueue(){},loadWorkers(){},renderStats(){},promptEl:{focus:()=>complete()}};
  createSandbox(c);runScript(source,c);await ready;
  assert.equal(calls.filter(x=>x==='drafts').length,1);
  assert.equal(calls.filter(x=>x==='reasoning').length,model==='qwen'?1:0);
 }
});

test('startup overlaps health with drafts but waits for both before restoring model and session state',async()=>{
 const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {promise,resolve};};
 for(const healthFirst of [false,true]){
  for(const healthValue of [{model:'qwen'},null]){
   const settings=deferred(),health=deferred(),drafts=deferred(),sessions=deferred();
   const draftsStarted=deferred(),healthObserved=deferred(),draftsObserved=deferred(),finished=deferred(),resumed=deferred();
   const snapshot={drafts:{fixture:'saved draft'}},calls=[];
   let healthDone=false,draftsDone=false;
   const afterDependencies=name=>{
    assert.ok(healthDone,`${name} must wait for health`);
    assert.ok(draftsDone,`${name} must wait for saved drafts`);
    calls.push(name);
   };
   const c={
    document:{addEventListener(){}},setInterval(){},
    loadUI(){calls.push('settings');return settings.promise;},
    applyI18n(){calls.push('i18n');},
    async checkHealth(){
     calls.push('health');await health.promise;
     healthDone=true;healthObserved.resolve();return healthValue;
    },
    composerDraftStore:{async load(value){
     assert.equal(value,snapshot,'reuse the settings snapshot');
     assert.ok(calls.includes('health'),'health starts before draft restoration finishes');
     calls.push('drafts');draftsStarted.resolve();await drafts.promise;
     draftsDone=true;draftsObserved.resolve();
    }},
    selectedModelID:value=>value,
    loadReasoning(){afterDependencies('reasoning');},
    loadModels(){afterDependencies('models');},
    loadSessions(){afterDependencies('sessions');return sessions.promise;},
    resumeLastSessionOnStart(value){afterDependencies('resume');assert.equal(value,'saved sessions');resumed.resolve();},
    loadProjects(){},loadPromptQueue(){},loadWorkers(){},
    renderStats(){afterDependencies('stats');},
    promptEl:{focus(){afterDependencies('focus');finished.resolve();}}
   };
   createSandbox(c);runScript(source,c);
   assert.deepEqual(calls,['settings'],'no startup reads or restoration before settings');
   settings.resolve(snapshot);await draftsStarted.promise;
   assert.deepEqual(calls,['settings','i18n','health','drafts']);
   if(healthFirst){health.resolve();await healthObserved.promise;}
   else{drafts.resolve();await draftsObserved.promise;}
   assert.deepEqual(calls,['settings','i18n','health','drafts'],'one pending dependency still gates model/session/stats restoration');
   if(healthFirst)drafts.resolve();else health.resolve();
   await finished.promise;
   assert.equal(calls.includes('reasoning'),!!healthValue);
   assert.equal(calls.includes('models'),!!healthValue);
   assert.ok(calls.includes('sessions'));
   assert.ok(calls.includes('stats'));
   assert.equal(calls.includes('resume'),false,'session restore waits for the session list');
   sessions.resolve('saved sessions');await resumed.promise;
   assert.equal(calls.filter(name=>name==='resume').length,1);
  }
 }
});

test('initial health and model discovery keep the empty-model label localized',async()=>{
 const assets=sourcePath(testDir,'../../internal/webgui/assets');
 const i18n=readSource(sourcePath(assets,'js/01-i18n.js'),'utf8');
 const shell=readSource(sourcePath(assets,'js/06-shell.js'),'utf8');
 const models=readSource(sourcePath(assets,'js/10-models.js'),'utf8');
 const health=shell.slice(shell.indexOf('async function checkHealth()'),shell.indexOf('/* ═══ side panel: tabs'));
 const discovery=models.slice(models.indexOf('async function loadModels()'),models.indexOf('function contextBudgetText('))+
  models.slice(models.indexOf('function activeModelEntry()'),models.indexOf('function renderActiveContextControl()'));
 const english=JSON.parse(readSource(sourcePath(assets,'locales/en.json'),'utf8'));
 const polish=JSON.parse(readSource(sourcePath(assets,'locales/pl.json'),'utf8'));
 for(const model of ['no model','','custom/my-model']){
  const expected=model==='custom/my-model'?model:polish['model.none'];
  const nodes={};const node=selector=>nodes[selector]||(nodes[selector]={textContent:'',value:'',disabled:false});
  node('#model-name').textContent='no model';
  let complete;const ready=new Promise(resolve=>complete=resolve);
  const calls=[];const c={ui:{lang:'pl'},I18N:{en:english,pl:polish},UI_LANGUAGES:[{code:'en'},{code:'pl'}],
   document:{documentElement:{},addEventListener(){}},navigator:{languages:['pl-PL']},$:node,$$:()=>[],
   activeModelID:'',activeProviderID:'',activeWorkspacePath:'',streaming:false,runFinishing:false,queueDispatching:false,pendingImmediate:null,reasoningRevision:0,modelsLoadSeq:0,modelCache:[],
   workspaceDisplayName:value=>value,slimModels:values=>values,saveBlobKey(){},renderModelList(){},renderReasoning(){},
   j:async url=>{calls.push(url);if(url==='/api/health')return {model,home:''};if(url==='/api/models'){assert.equal(node('#model-name').textContent,expected,'health label');return {active:model,provider:'',models:[]};}throw Error('Unexpected request '+url);},
   loadUI:async()=>({}),composerDraftStore:{load:async()=>{}},loadReasoning(){},loadSessions:async()=>{},resumeLastSessionOnStart(){},loadProjects(){},loadPromptQueue(){},loadWorkers(){},renderStats(){},setInterval(){},promptEl:{focus(){}}};
  createSandbox(c);runScript(i18n,c);runScript(health,c);runScript(discovery,c);
  const discover=c.loadModels;c.loadModels=()=>discover().then(complete);
  runScript(source,c);await ready;
  assert.deepEqual(calls,['/api/health','/api/models']);
  assert.equal(c.activeModelID,model==='custom/my-model'?model:'');
  assert.equal(node('#model-name').textContent,expected,'model discovery label');
  c.streaming=true;c.runFinishing=true;await c.checkHealth();
  assert.equal(node('#status-dot').className,'status-dot ok','health must not resurrect busy after terminal/Stop');
  c.pendingImmediate={text:'next'};await c.checkHealth();
  assert.equal(node('#status-dot').className,'status-dot ok busy','health preserves the pending handoff');
  c.pendingImmediate=null;c.runFinishing=false;await c.checkHealth();
  assert.equal(node('#status-dot').className,'status-dot ok busy','health keeps actual generation busy');
 }
});
