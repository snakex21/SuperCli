const assert=require('node:assert/strict');
const fs=require('node:fs');const path=require('node:path');const vm=require('node:vm');const test=require('node:test');
const assets=path.resolve(__dirname,'../../internal/webgui/assets');
const english=JSON.parse(fs.readFileSync(path.join(assets,'locales/en.json'),'utf8'));
function node(tag,cls,text) {return {tag,style:{},className:cls,textContent:text||'',children:[],listeners:{},hidden:false,disabled:false,appendChild(child){this.children.push(child);return child;},setAttribute(name,value){this[name]=value;},addEventListener(name,fn){this.listeners[name]=fn;}};}
function harness() {
 const calls=[];const responses=[];const refreshers=[];const c={sections:{},URL,I18N:{en:english},
  registerLanguageRefresh:(node,refresh)=>refreshers.push(refresh),
  t:key=>english[key]||key,el:node,i18nEl:(tag,cls,key)=>node(tag,cls,english[key]||key),
  fetch:async(url,options)=>{calls.push({url,options});const item=responses.shift();if(!item)throw Error('Unexpected request');return {ok:item.ok!==false,json:async()=>item.body};},
 };
 vm.createContext(c);vm.runInContext(fs.readFileSync(path.join(assets,'js/11h-panel-files-about.js'),'utf8'),c);
 return {c,calls,responses,refreshers};
}
async function click(n) {n.listeners.click();await new Promise(resolve=>setImmediate(resolve));}
test('update panel makes no automatic network check and uses three explicit actions',async()=>{
 const h=harness();const g=h.c.createUpdatePanel('1.0.0');assert.equal(h.calls.length,0);
 const actions=g.children.at(-1).children;const [check,download,install,release]=actions;
 assert.equal(download.disabled,true);assert.equal(install.disabled,true);
 h.responses.push({body:{current_version:'1.0.0',latest_version:'1.0.1',available:true,supported:true,status:'available',url:'https://github.com/test/supercli/releases/tag/v1.0.1'}});
 await click(check);assert.equal(h.calls[0].url,'/api/update');assert.equal(h.calls[0].options.cache,'no-store');assert.equal(download.disabled,false);assert.equal(install.disabled,true);assert.equal(release.hidden,false);
 h.responses.push({body:{current_version:'1.0.0',latest_version:'1.0.1',available:true,supported:true,status:'downloaded'}});
 await click(download);assert.deepEqual(JSON.parse(h.calls[1].options.body),{action:'download'});assert.equal(install.disabled,false);
 h.responses.push({body:{current_version:'1.0.0',latest_version:'1.0.1',available:true,supported:true,status:'installed',restart_required:true}});
 await click(install);assert.deepEqual(JSON.parse(h.calls[2].options.body),{action:'install'});assert.match(g.children.at(-2).textContent,/Restart SuperCli/);assert.equal(install.disabled,true);assert.equal(h.calls.length,3);
});
test('update errors show the translated code and unsafe release URLs remain hidden',async()=>{
 const h=harness();const g=h.c.createUpdatePanel('1.0.0');const check=g.children.at(-1).children[0];
 h.responses.push({ok:false,body:{error:'debug detail',code:'update.busy'}});await click(check);assert.equal(g.children.at(-2).textContent,english['update.busy']);
 h.c.updateState={status:'available',available:true,supported:true,url:'javascript:alert(1)'};
 const other=h.c.createUpdatePanel('1.0.0');assert.equal(other.children.at(-1).children.at(-1).hidden,true);
});
test('GUI writes drafts, preferences and attachment indexes through portable server settings only',()=>{
 const files=['js/02-ui-settings.js','js/05-chat.js'];
 const runtime=path.resolve(assets,'../shared-ui/runtime.js');
 for(const file of files.map(name=>path.join(assets,name)).concat(runtime))assert.doesNotMatch(fs.readFileSync(file,'utf8'),/localStorage\.setItem|indexedDB/);
});

test('open update status and errors change language without resetting the update state',async()=>{
 const h=harness();h.c.updateState={status:'available',available:true,supported:true,latest_version:'1.0.1'};
 const group=h.c.createUpdatePanel('1.0.0');const pl=JSON.parse(fs.readFileSync(path.join(assets,'locales/pl.json'),'utf8'));
 const saved=h.c.updateState;h.c.t=key=>pl[key]||key;h.refreshers.forEach(fn=>fn());
 assert.equal(group.children.at(-2).textContent,pl['update.available'].replace('{version}','1.0.1'));assert.equal(h.c.updateState,saved);assert.equal(h.calls.length,0);
 const check=group.children.at(-1).children[0];h.responses.push({ok:false,body:{code:'update.busy'}});await click(check);
 h.c.t=key=>english[key]||key;h.refreshers.forEach(fn=>fn());assert.equal(group.children.at(-2).textContent,english['update.busy']);
});

test('doctor labels use stable record identifiers and leave raw runtime details untouched',()=>{
 const h=harness();const raw='C:/portable/sessions.db · quick_check ok';const record={ID:'sessions_db',Name:'untranslated display label',Detail:raw,Status:'ok'};
 const row=h.c.createDoctorRow(record);assert.equal(row.children[0].textContent,english['doctor.sessionsDB']);assert.equal(row.children[1].textContent,raw);
 const pl=JSON.parse(fs.readFileSync(path.join(assets,'locales/pl.json'),'utf8'));h.c.t=key=>pl[key]||key;h.refreshers.forEach(fn=>fn());
 assert.equal(row.children[0].textContent,pl['doctor.sessionsDB']);assert.equal(row.children[1].textContent,raw);assert.equal(row.children[1].title,raw);
 assert.equal(h.c.doctorCheckLabel({Name:'git command'}),pl['doctor.command'].replace('{name}','git'));
 assert.equal(h.c.doctorCheckLabel({Name:'provider MyHost'}),pl['stats.provider']+' · MyHost');
 assert.equal(h.c.doctorCheckLabel({Name:'ollama server'}),pl['doctor.server'].replace('{name}','Ollama'));
 assert.equal(h.c.doctorCheckLabel({Name:'unknown record'}),'unknown record');
});
test('the About model fallback refreshes when the language changes',async()=>{
 const h=harness();h.c.ui={keybinds:{}};h.c.panelContent=node('div','','');h.c.checkHealth=async()=>({model:'no model',home:'C:/app'});h.c.j=async()=>({summary:{},checks:[]});
 h.c.modelDisplayName=value=>value==='no model'||!value?h.c.t('model.none'):value;await h.c.sections.about();
 const info=h.c.panelContent.children.find(group=>group.children.some(row=>row.children.some(child=>child.textContent===english['about.model'])));
 const modelRow=info.children.find(row=>row.children[0].textContent===english['about.model']);assert.equal(modelRow.children[1].textContent,english['model.none']);
 const pl=JSON.parse(fs.readFileSync(path.join(assets,'locales/pl.json'),'utf8'));h.c.t=key=>pl[key]||key;h.refreshers.forEach(fn=>fn());assert.equal(modelRow.children[1].textContent,pl['model.none']);
});
