const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const assets = path.resolve(__dirname, '../../internal/webgui/assets');
function harness() {
 const classes = new Set(), appended = [], saved = []; let timers = 0;
 function node(tag, cls, text) {return {tag,className:cls||'',textContent:text||'',dataset:{},children:[],style:{},events:{},classList:{add(){},toggle(){}},appendChild(n){this.children.push(n);return n},addEventListener(type,fn){this.events[type]=fn}}}
 const root={style:{setProperty(){},removeProperty(){}},dataset:{},classList:{toggle(k,on){if(on)classes.add(k);else classes.delete(k)},contains:k=>classes.has(k)}};
 const c={superCliUI:{fileMutationTools:new Set()},ui:{lang:'en'},document:{documentElement:root,createElement:tag=>node(tag)},window:{innerWidth:1400,innerHeight:900,matchMedia:()=>({matches:false}),addEventListener(){}},detectedLanguage:()=> 'en',normalizeLanguage:v=>v,loadLanguage:async()=>{},localStorage:{getItem:()=>null},el:node,$:()=>node('div'),t:k=>k,applyI18n(){},setTimeout(fn){timers++;c.pending=fn;return timers},clearTimeout(){},jpost:async(url,body)=>{saved.push([url,body]);return {}},j:async()=>({settings:{'ui.showGenerationSpeed':false}}),sentAttachmentStorageKey:'attachments',sentAttachmentIndex:[],modelCache:[]};
 vm.createContext(c);for(const file of ['js/02-ui-settings.js','js/04-transcript.js']) vm.runInContext(fs.readFileSync(path.join(assets,file),'utf8'),c);
 c.appendStream=n=>appended.push(n);c.smartScroll=()=>{};c.fmtDuration=n=>n+'ms';c.fmtTok=String;c.escHtml=String;c.runToolCount=0;
 return {c,classes,appended,saved,timers:()=>timers};
}
test('reported rate is shown separately, unknown/invalid counts never invent speed',()=>{
 const h=harness();h.c.addTurnMeta({tok_total:150,tok_in:30,tok_out:120,generation_tps:36.24},20000,0);
 assert.equal(h.appended[0].children[0].textContent,' · 36.2 tok/s');assert.equal(h.appended[0].children[0].className,'generation-speed');assert.equal(h.appended[0].children[0].title,'generation.speed_hint');
 for(const rate of [undefined,null,0,-1,NaN,Infinity,'36',{}]) {h.c.addTurnMeta({tok_total:150,tok_out:120,generation_tps:rate},20000,0);assert.equal(h.appended.at(-1).children.length,0)}
 assert.equal(h.timers(),0,'finished metric must not start timers');
});
test('portable shared preference hides/reveals existing rates without rebuilding transcript',async()=>{
 const h=harness();await h.c.loadUI();assert.equal(h.c.ui.showGenerationSpeed,false);assert.ok(h.classes.has('generation-speed-hidden'));
 h.c.addTurnMeta({generation_tps:40},7000,0);const line=h.appended[0],speed=line.children[0];
 h.c.ui.showGenerationSpeed=true;h.c.applyGenerationSpeedVisibility();assert.ok(!h.classes.has('generation-speed-hidden'));assert.equal(h.appended[0],line);assert.equal(line.children[0],speed);
 h.c.ui.showGenerationSpeed=false;h.c.applyGenerationSpeedVisibility();assert.ok(h.classes.has('generation-speed-hidden'));h.c.saveUI();await h.c.pending();assert.ok(!Object.hasOwn(h.saved.at(-1)[1],'ui.showGenerationSpeed'),'browser appearance save must not override shared config');
 const css=fs.readFileSync(path.join(assets,'app.css'),'utf8');assert.match(css,/\.generation-speed-hidden \.generation-speed\s*\{\s*display:\s*none/);
});
