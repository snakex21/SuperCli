import {assert, test, readSource, readDir, sourcePath, testDir, createSandbox, runScript, Script} from './harness.js';
const assets = sourcePath(testDir, '../../internal/webgui/assets/js');
const locales = sourcePath(assets, '../locales');
const languages = ['en','bg','cs','da','de','el','es','et','fi','fr','hr','hu','it','lt','lv','nb','nl','pl','pt-BR','ro','ru','sk','sl','sr-Latn','sv','tr','uk'];
function catalog(code) { return JSON.parse(readSource(sourcePath(locales,code+'.json'),'utf8')); }
function staticCatalogKeys(source) {
 const direct=source.matchAll(/\bt\(\s*"([a-z][\w.-]+)"\s*\)/g);
 // Stay inside one call: a dynamic third argument must not consume a later
 // DOM attribute or another statement on the same line as a catalog key.
 const element=source.matchAll(/\bi18nEl\(\s*[^,\n()]+,\s*[^,\n()]+,\s*"([a-z][\w.-]+)"\s*\)/g);
 return [...direct,...element].map(match=>match[1]);
}
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
   const node={tag,dataset:{},children:[],className:cls,isConnected:true,attributes:{},
    setAttribute(name,value){this.attributes[name]=value;}};
   const label={nodeValue:text,parentNode:node};
   node.firstChild=label;node.children.push(label);nodes.push(node);return node;
  },
  $$(selector) {return selector==='[data-i18n-text]' ? nodes.filter(n=>n.dataset.i18nText) :
    selector==='[data-i18n-refresh]' ? nodes.filter(n=>n.isConnected && Object.hasOwn(n.attributes,'data-i18n-refresh')) : []}
 };
 createSandbox(context);
 runScript(readSource(sourcePath(assets,'01-i18n.js'),'utf8'),context);
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
 assert.deepEqual(readDir(locales).filter(x=>x.endsWith('.json')).map(x=>x.slice(0,-5)).sort(),languages.slice().sort());
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
 for(const file of readDir(assets).filter(x=>x.endsWith('.js'))) {
  const source=readSource(sourcePath(assets,file),'utf8');new Script(source,{filename:file});
  for(const key of staticCatalogKeys(source))assert.equal(typeof english[key],'string',file+': '+key);
 }
 const html=readSource(sourcePath(assets,'../index.html'),'utf8');
 for(const match of html.matchAll(/data-i18n(?:-ph|-title|-aria)?="([^"]+)"/g))assert.equal(typeof english[match[1]],'string','HTML '+match[1]);
});

test('catalog scanning distinguishes dynamic keys from later DOM literals',()=>{
 assert.deepEqual(staticCatalogKeys('var th=i18nEl("th", "", key); th.setAttribute("scope", "col");'),[]);
 assert.deepEqual(staticCatalogKeys('i18nEl("th", "", "stats.model"); i18nEl("td", cls, "missing.label");'),['stats.model','missing.label']);
 assert.deepEqual(staticCatalogKeys('var th=i18nEl("th", "", key); label=t("common.close");'),['common.close']);
 assert.deepEqual(staticCatalogKeys('i18nEl("th", "", "stats.model")\nnext.setAttribute("role", "region");'),['stats.model']);
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

test('dynamic translations refresh connected owners and stop updating removed panels',async()=>{
 const ui=languageUI();await ui.loadLanguage('pl');
 const active=ui.el('span','',''),removed=ui.el('span','','');
 let activeCalls=0,removedCalls=0;
 ui.registerLanguageRefresh(active,()=>{activeCalls++;active.textContent=ui.t('common.save');});
 ui.registerLanguageRefresh(removed,()=>{removedCalls++;});
 ui.applyI18n();assert.equal(active.textContent,'Zapisz');assert.equal(activeCalls,1);assert.equal(removedCalls,1);
 removed.isConnected=false;ui.ui.lang='en';ui.applyI18n();
 assert.equal(active.textContent,'Save');assert.equal(activeCalls,2);assert.equal(removedCalls,1);
 ui.registerLanguageRefresh(active,()=>{active.textContent=ui.t('common.cancel');});ui.applyI18n();
 assert.equal(active.textContent,'Cancel');assert.equal(activeCalls,2,'replaced callbacks stop retaining and updating old content');
});
test('tn picks CLDR plural forms in every language and keeps "#" as the number',async()=>{
 const ui=languageUI(); await ui.loadLanguage('pl');
 assert.equal(ui.tn('run.toolCount',1),'1 narzędzie');
 assert.equal(ui.tn('run.toolCount',3),'3 narzędzia');
 assert.equal(ui.tn('run.toolCount',5),'5 narzędzi');
 assert.equal(ui.tn('run.toolCount',22),'22 narzędzia');
 ui.ui.lang='en';
 assert.equal(ui.tn('run.toolCount',1),'1 tool');
 assert.equal(ui.tn('run.toolCount',2),'2 tools');
 for (const code of languages) {
  const value=catalog(code)['run.toolCount'];
  const forms=new Set([...value.matchAll(/(zero|one|two|few|many|other)\s*\{([^{}]*)\}/g)].map(m=>m[1]));
  const rules=new Intl.PluralRules(code);
  for (let n=0;n<120;n++) assert.ok(forms.has(rules.select(n)),code+' lacks the '+rules.select(n)+' form');
 }
});
test('known backend notices and stop messages follow the UI language; unknown text stays',async()=>{
 const ui=languageUI(); await ui.loadLanguage('pl');
 assert.equal(ui.localizeSystemText('preflight: repo context ~42 tok (project turn)'),'Kontekst repozytorium ~42 tok (tura projektu)');
 assert.match(ui.localizeSystemText('fake: provider error (HTTP 500) — retrying in 300ms (retry 2/3)'),/^fake: błąd dostawcy AI \(HTTP 500\) — ponawianie za 300ms \(ponowienie 2\/3\)$/); assert.match(ui.localizeSystemText('fake: rate limited (HTTP 429) — retrying in 30s (retry 12/∞)'),/^fake: limit zapytań \(HTTP 429\) — ponawianie za 30s \(ponowienie 12\/∞\)$/); assert.match(ui.localizeSystemText('Model connection unavailable — waiting 4s before retry 2/5.'),/^Brak połączenia z modelem — czekam 4s przed ponowieniem 2\/5\.$/); assert.match(ui.localizeSystemText('gw.example: request limit 50/min reached — waiting 1.2s'),/^gw\.example: osiągnięto limit 50 zapytań\/min — czekam 1\.2s$/);
 assert.match(ui.localizeSystemText('agent: stopped — the last 8 consecutive tool rounds returned only previously observed successful results, with no new evidence'),/^Agent zatrzymany: .*: 8\./);
 assert.equal(ui.localizeSystemText('something new from the backend'),'something new from the backend');
});
