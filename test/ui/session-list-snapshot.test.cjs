
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

function harness() {
  const nodes=new Map();let now=Date.parse('2026-10-01T12:30:05Z'),locale='en',failure=null,rows=[],created=0,cleared=0;
  function el(tag,cls,text){
    created++;
    const n={tag,children:[],dataset:{},listeners:{},className:cls||'',style:{setProperty(){},removeProperty(){}},
      appendChild(child){this.children.push(child);return child;},addEventListener(type,fn){this.listeners[type]=fn;},
      setAttribute(){},removeAttribute(){},classList:{add(){},remove(){},toggle(){},contains(){return false;}}};
    Object.defineProperty(n,'textContent',{get(){return this.children.map(c=>c.textContent).join('')||this.text||''},set(v){this.text=String(v);this.children=[]}});
    Object.defineProperty(n,'innerHTML',{set(){cleared++;this.children=[]}});
    if(text!=null)n.textContent=text;
    return n;
  }
  function $(selector){if(!nodes.has(selector))nodes.set(selector,el('div'));return nodes.get(selector);}
  class Clock extends Date{constructor(...args){super(...(args.length?args:[now]));}static now(){return now;}}
  const c={$,$$:()=>[],el,i18nEl:(tag,cls,key)=>el(tag,cls,key),t:key=>locale+':'+key,Date:Clock,Intl,AbortController,
    projectEpoch:1,activeSessionID:'',statsLocale:()=>locale,fmtWhen:()=>String(Math.floor(now/60000)),
    j:async()=>{if(failure)throw failure;return JSON.parse(JSON.stringify(rows));}};
  vm.createContext(c);vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js/08-sessions.js'),'utf8'),c);
  return {c,$,rows(v){rows=v;},fail(v){failure=v;},locale(v){locale=v;},tick(ms){now+=ms;},counts(){return {created,cleared};}};
}
const rows=()=>[{id:'session-a',first_user_msg:'Fix scrolling',message_count:2,updated_at:'2026-10-01T12:29:00Z',model:'model-a'}];

test('unchanged session metadata preserves sidebar rows and their focus/hover state',async()=>{
  const h=harness();h.rows(rows());await h.c.loadSessions();const list=h.$('#session-list'),before=list.children.slice(),counts=h.counts();
  await h.c.loadSessions();
  assert.deepEqual(list.children,before);assert.deepEqual(h.counts(),counts);
  assert.equal(h.c.sessionByID['session-a'].first_user_msg,'Fix scrolling');
});

test('title, activity, selection, locale and minute changes still refresh the list',async()=>{
  const h=harness(),data=rows();h.rows(data);await h.c.loadSessions();
  for(const change of [
    ()=>{data[0].first_user_msg='Rename sidebar';h.rows(data);},
    ()=>{data[0].message_count++;data[0].updated_at='2026-10-01T12:30:00Z';h.rows(data);},
    ()=>{h.c.activeSessionID='session-a';},
    ()=>h.locale('pl'),
    ()=>h.tick(60000),
  ]){
    const before=h.$('#session-list').children.slice();change();await h.c.loadSessions();
    assert.notDeepEqual(h.$('#session-list').children,before);
  }
  assert.equal(h.c.sessionByID['session-a'].first_user_msg,'Rename sidebar');
});

test('a failed refresh invalidates the snapshot so an identical successful retry restores rows',async()=>{
  const h=harness();h.rows(rows());await h.c.loadSessions();h.fail(new Error('offline'));await h.c.loadSessions();
  assert.equal(h.$('#session-list').children[0].textContent,'common.error');
  h.fail(null);await h.c.loadSessions();
  assert.ok(h.$('#session-list').children.some(node=>node.dataset.sessionId==='session-a'));
});

test('an overlapping stale response cannot replace the current sidebar snapshot',async()=>{
  const h=harness(),pending=[];h.c.j=()=>new Promise(resolve=>pending.push(resolve));
  const old=h.c.loadSessions(),current=h.c.loadSessions();
  pending[1](rows());await current;const before=h.$('#session-list').children.slice();
  pending[0]([{...rows()[0],first_user_msg:'Stale name'}]);await old;
  assert.deepEqual(h.$('#session-list').children,before);
  assert.equal(h.c.sessionByID['session-a'].first_user_msg,'Fix scrolling');
});

function dateHarness(dateFactory) {
  let now=new Date(2026,9,2,12).getTime(),created=0;
  class Clock extends Date {
    constructor(...args){super(...(args.length?args:[now]));}
    static now(){return now;}
  }
  const c={window:{},document:{querySelector:()=>({addEventListener(){}})},Date:Clock,
    ui:{lang:'en'},normalizeLanguage:value=>value,Intl:{NumberFormat:Intl.NumberFormat,
      DateTimeFormat:function(...args){created++;return dateFactory?dateFactory(...args):new Intl.DateTimeFormat(...args);}}};
  c.t=key=>c.ui.lang+':'+key;
  vm.createContext(c);
  vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js/00-helpers.js'),'utf8'),c);
  vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js/08-sessions.js'),'utf8'),c);
  return {c,counts:()=>created,setNow:value=>{now=value;}};
}

test('session date labels preserve every supported locale and calendar group',()=>{
  const h=dateHarness();
  const languages=fs.readdirSync(path.resolve(__dirname,'../../internal/webgui/assets/locales'))
    .filter(name=>name.endsWith('.json')).map(name=>name.slice(0,-5));
  for(const language of languages){
    h.c.ui.lang=language;
    for(const date of [new Date(2026,5,15,12),new Date(2025,11,31,12),new Date(2027,1,3,12)]){
      const group=h.c.sessionDateGroup(date.toISOString());
      assert.equal(group.key,date.getFullYear()+'-'+date.getMonth()+'-'+date.getDate());
      assert.equal(group.label,new Intl.DateTimeFormat(language,{day:'numeric',month:'short',
        year:date.getFullYear()===2026?undefined:'numeric'}).format(date),language);
    }
    assert.equal(h.c.sessionDateGroup(new Date(2026,9,2,0).toISOString()).label,language+':session.today');
    assert.equal(h.c.sessionDateGroup(new Date(2026,9,1,23).toISOString()).label,language+':session.yesterday');
  }
  const invalid=h.c.sessionDateGroup('not-a-date');
  assert.equal(invalid.key,'unknown');assert.equal(invalid.label,'');
});

test('old session dates reuse bounded formatters and switch locale immediately',()=>{
  const h=dateHarness();
  const dates=[new Date(2026,5,15,12).toISOString(),new Date(2025,5,15,12).toISOString()];
  const initial=dates.map(date=>h.c.sessionDateGroup(date).label);
  assert.equal(h.counts(),2);
  for(let i=0;i<100;i++)assert.deepEqual(dates.map(date=>h.c.sessionDateGroup(date).label),initial);
  assert.equal(h.counts(),2,'each old row reused one of the two cached date formats');
  h.c.ui.lang='pl';dates.forEach(date=>h.c.sessionDateGroup(date));assert.equal(h.counts(),4);
  assert.equal(Object.keys(h.c.statsFormatterCache).length,2,'old locale formatters were released');
  h.c.ui.lang='en';assert.deepEqual(dates.map(date=>h.c.sessionDateGroup(date).label),initial);
  assert.equal(h.counts(),6);
  assert.ok(h.c.statsFormatterKeys.length<=32);
});

test('year rollover and unavailable ICU preserve session date behavior',()=>{
  const h=dateHarness(),date=new Date(2026,5,15,12),iso=date.toISOString();
  h.setNow(new Date(2026,11,31,12).getTime());
  assert.equal(h.c.sessionDateGroup(iso).label,new Intl.DateTimeFormat('en',{day:'numeric',month:'short'}).format(date));
  h.setNow(new Date(2027,0,1,12).getTime());
  assert.equal(h.c.sessionDateGroup(iso).label,new Intl.DateTimeFormat('en',{day:'numeric',month:'short',year:'numeric'}).format(date));
  assert.equal(h.counts(),2,'rollover selected the full-year cache entry');
  let unavailable=true;
  const retry=dateHarness((...args)=>{if(unavailable)throw Error('ICU unavailable');return new Intl.DateTimeFormat(...args);});
  assert.equal(retry.c.sessionDateGroup(iso).label,date.toLocaleDateString());
  assert.equal(Object.keys(retry.c.statsFormatterCache).length,0,'failed constructors do not poison the cache');
  unavailable=false;
  assert.equal(retry.c.sessionDateGroup(iso).label,new Intl.DateTimeFormat('en',{day:'numeric',month:'short'}).format(date));
  assert.equal(retry.counts(),2);
});
