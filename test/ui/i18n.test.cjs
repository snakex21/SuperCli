const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const assets = path.resolve(__dirname, '../../internal/webgui/assets/js');
const locales = path.resolve(assets, '../locales');
const languages = ['en','bg','cs','da','de','el','es','et','fi','fr','hr','hu','it','lt','lv','nb','nl','pl','pt-BR','ro','ru','sk','sl','sr-Latn','sv','tr','uk'];
function catalog(code) { return JSON.parse(fs.readFileSync(path.join(locales,code+'.json'),'utf8')); }
function languageUI(fetchOverride) {
 const nodes=[]; const requests=[];
 const context={ ui:{lang:'pl'}, document:{documentElement:{}},
  I18N:{en:catalog('en')}, UI_LANGUAGES:languages.map(code=>({code,name:code,dir:'ltr'})),
  navigator:{languages:['en-US'],language:'en-US'},
  fetch:fetchOverride || (async (url,opts)=>{
   requests.push({url,opts});
   return {ok:true,json:async()=>catalog(decodeURIComponent(url.match(/([^/]+)\.json$/)[1]))};
  }), requests,
  el(tag,cls,text) {
   const node={tag,dataset:{},children:[],className:cls};
   const label={nodeValue:text,parentNode:node};
   node.firstChild=label;node.children.push(label);nodes.push(node);return node;
  },
  $$(selector) {return selector==='[data-i18n-text]' ? nodes.filter(n=>n.dataset.i18nText) : []}
 };
 vm.createContext(context);
 vm.runInContext(fs.readFileSync(path.join(assets,'01-i18n.js'),'utf8'),context);
 return context;
}
test('switching language preserves nested controls and user drafts',async()=>{
 const ui=languageUI(); await ui.loadLanguage('pl');
 const node=ui.i18nEl('button','action','common.save');
 const editor={value:'draft stays here',focused:true,parentNode:node};node.children.push(editor);
 assert.equal(node.firstChild.nodeValue,'Zapisz');
 ui.ui.lang='en';ui.applyI18n();
 assert.equal(node.firstChild.nodeValue,'Save');assert.equal(node.children[1],editor);
 assert.equal(editor.value,'draft stays here');assert.equal(editor.focused,true);
 assert.equal(ui.document.documentElement.lang,'en');
 ui.ui.lang='pl';ui.applyI18n();assert.equal(node.firstChild.nodeValue,'Zapisz');
});
test('translation leaves a temporary run status intact',async()=>{
 const ui=languageUI();await ui.loadLanguage('pl');
 const node=ui.i18nEl('button','','common.save');node.firstChild.parentNode=null;
 const status={nodeValue:'working',parentNode:node};node.firstChild=status;node.children=[status];
 ui.ui.lang='en';ui.applyI18n();assert.equal(node.firstChild.nodeValue,'working');
});
test('catalogs have exactly the supported languages, keys and placeholders',()=>{
 assert.deepEqual(fs.readdirSync(locales).filter(x=>x.endsWith('.json')).map(x=>x.slice(0,-5)).sort(),languages.slice().sort());
 const english=catalog('en');const keys=Object.keys(english).sort();
 for(const lang of languages) {
  const copy=catalog(lang);assert.deepEqual(Object.keys(copy).sort(),keys,lang+' keys');
  let translated=0;
  for(const key of keys) {
   assert.equal(typeof copy[key],'string',lang+'/'+key);assert.ok(copy[key].trim(),lang+'/'+key);
   assert.deepEqual((copy[key].match(/\{[\w]+\}/g)||[]).sort(),(english[key].match(/\{[\w]+\}/g)||[]).sort(),lang+'/'+key+' placeholders');
   if(copy[key]!==english[key])translated++;
  }
  if(lang!=='en')assert.ok(translated>keys.length*.7,lang+' must contain translated sentences, not an English clone');
 }
 for(const lang of languages) { const copy=catalog(lang); for(const key of ['model.contextHint','model.contextBudget','model.contextInvalid'])for(const term of ['100k','1m','auto'])if(english[key].includes(term))assert.ok(copy[key].includes(term),lang+'/'+key+' token '+term); }
 const serbian=Object.values(catalog('sr-Latn')).join(' ');assert.doesNotMatch(serbian,/[А-Яа-яЉЊЏљњџ]/);
});
test('every static and dynamic catalog reference resolves',()=>{
 const english=catalog('en');
 for(const file of fs.readdirSync(assets).filter(x=>x.endsWith('.js'))) {
  const source=fs.readFileSync(path.join(assets,file),'utf8');new vm.Script(source,{filename:file});
  const direct=source.matchAll(/\bt\(\s*"([a-z][\w.-]+)"\s*\)/g);
  const dynamic=source.matchAll(/i18nEl\([^\n]*?,\s*"([a-z][\w.-]+)"\)/g);
  for(const match of [...direct,...dynamic])assert.equal(typeof english[match[1]],'string',file+': '+match[1]);
 }
 const html=fs.readFileSync(path.resolve(assets,'../index.html'),'utf8');
 for(const match of html.matchAll(/data-i18n(?:-ph|-title|-aria)?="([^"]+)"/g))assert.equal(typeof english[match[1]],'string','HTML '+match[1]);
});
test('loading is lazy, deduplicates requests and keeps immediate English fallback',async()=>{
 const ui=languageUI();assert.equal(ui.requests.length,0);assert.equal(ui.t('common.save'),'Save');
 await ui.loadLanguage('en');assert.equal(ui.requests.length,0);
 await Promise.all([ui.loadLanguage('de-DE'),ui.loadLanguage('de')]);
 assert.equal(ui.requests.length,1);assert.equal(ui.requests[0].url,'/locales/de.json');
 assert.equal(ui.requests[0].opts.cache,'no-store');assert.deepEqual(Object.keys(ui.I18N).sort(),['de','en']);
 ui.ui.lang='de';assert.equal(ui.t('common.save'),catalog('de')['common.save']);
});
test('a failed locale keeps English usable and can be retried',async()=>{
 let fail=true;const ui=languageUI(async()=>{if(fail)throw Error('offline');return {ok:true,json:async()=>catalog('fr')};});
 ui.ui.lang='fr';await assert.rejects(ui.loadLanguage('fr'));assert.equal(ui.t('common.save'),'Save');
 fail=false;await ui.loadLanguage('fr');assert.equal(ui.t('common.save'),catalog('fr')['common.save']);
});
test('browser and saved locales normalize to the exact catalog codes',()=>{
 const ui=languageUI();
 for(const [input,expected] of [['EN_us','en'],['pl_PL.UTF-8','pl'],['pt-BR','pt-BR'],['pt_PT','pt-BR'],['sr_RS@latin','sr-Latn'],['sr-Latn-RS','sr-Latn'],['sr-Cyrl-RS','sr-Latn'],['no-NO','nb'],['de-DE','de'],['zz-ZZ','']])assert.equal(ui.normalizeLanguage(input),expected,input);
 ui.navigator.languages=['zz-ZZ','fi-FI'];assert.equal(ui.detectedLanguage(),'fi');
});

test('the empty-model fallback follows language changes while actual model identifiers stay intact',async()=>{
 const ui=languageUI();await ui.loadLanguage('pl');const model={textContent:'no model'};ui.$=selector=>selector==='#model-name'?model:null;ui.activeModelID='no model';
 ui.ui.lang='en';ui.applyI18n();assert.equal(model.textContent,'no model');
 ui.ui.lang='pl';ui.applyI18n();assert.equal(model.textContent,catalog('pl')['model.none']);assert.equal(ui.modelDisplayName('no model'),catalog('pl')['model.none']);
 ui.activeModelID='custom/my-model';model.textContent=ui.activeModelID;ui.ui.lang='en';ui.applyI18n();assert.equal(model.textContent,'custom/my-model');assert.equal(ui.modelDisplayName('custom/my-model'),'custom/my-model');
});
