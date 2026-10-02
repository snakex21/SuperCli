
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
