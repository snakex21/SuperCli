const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const source = fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js/11e-panel-accounts-mcp.js'),'utf8');
const copy = JSON.parse(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/locales/en.json'),'utf8'));
class Node {
 constructor(tag,cls,text){this.tag=tag;this.className=cls||'';this.textContent=text==null?'':String(text);this.children=[];this.style={};this.attrs={};this.listeners={};this.isConnected=true;}
 appendChild(n){this.children.push(n);n.parentNode=this;return n;}
 set innerHTML(value){this._html=value;for(const n of this.children)n.isConnected=false;this.children=[];}
 get innerHTML(){return this._html||'';}
 setAttribute(k,v){this.attrs[k]=v;}
 addEventListener(k,fn){this.listeners[k]=fn;}
 replaceWith(n){this.replaced=n;this.isConnected=false;}
 async click(){if(!this.disabled)await this.listeners.click?.();}
 text(){return this.textContent+this.innerHTML.replace(/<[^>]*>/g,'')+this.children.map(n=>n.text()).join(' ');}
 all(cls){return [this,...this.children.flatMap(n=>n.all(cls))].filter(n=>n.className.split(' ').includes(cls));}
}
function fixture(){
 const requests=[],c={sections:{},clearTimeout(){},codexPollTimer:null,currentSection:'accounts',overlay:{hidden:false},panelContent:new Node('div'),
 el:(tag,cls,text)=>new Node(tag,cls,text),t:k=>copy[k]||k,fmtInteger:n=>String(n),fmtDateTime:s=>s,fmtWhen:s=>s,escHtml:s=>s,statsFormatter:()=>({format:n=>String(n)}),toast(){},
 i18nEl(tag,cls,key){return new Node(tag,cls,this.t(key));},
 async jpost(url,body){requests.push({url,body});return c.next||{accounts:[],usage_summary:{accounts:0,available:0,exhausted:0,unknown:0,stale:0}};} };
 c.i18nEl=(tag,cls,key)=>new Node(tag,cls,c.t(key));vm.createContext(c);vm.runInContext(source,c);return {c,requests};
}
function account(windows,more={}){return {label:'private',logged_in:true,usage:{rate_limits:[{id:'codex',...windows}],...more}};}
test('Codex renders actual weekly-only windows and leaves missing values unknown',()=>{
 const h=fixture(),root=h.c.renderCodexUsageDashboard({accounts:[account({primary:null,secondary:{window_seconds:604800,used_percent:null,remaining_percent:null,resets_at:null}})],usage_summary:{accounts:1,known:0,unknown:1,available:0,exhausted:0,stale:0}});
 assert.equal(root.all('codex-usage-window').length,1);assert.match(root.text(),/7 days window/);assert.doesNotMatch(root.text(),/5 h|0%/);assert.equal(root.all('codex-usage-meter').length,0);assert.match(root.text(),/Used —/);assert.match(root.text(),/Resets: —/);assert.equal(h.requests.length,0);
});
test('known zero and nonzero usage are preserved per account without percentage totals',()=>{
 const h=fixture(),a=account({primary:{window_seconds:18000,used_percent:0,remaining_percent:100,resets_at:1800000000},secondary:{window_seconds:604800,used_percent:75.5,remaining_percent:24.5,resets_at:1800100000}});
 const b={...account({secondary:{window_seconds:604800,used_percent:100,remaining_percent:0}}),label:'work'};
 const root=h.c.renderCodexUsageDashboard({accounts:[a,b],usage_summary:{accounts:2,known:2,unknown:0,available:1,exhausted:1,stale:0}});
 assert.equal(root.all('codex-usage-account').length,2);assert.equal(root.all('codex-usage-window').length,3);assert.equal(root.all('codex-usage-meter')[0].attrs['aria-valuenow'],'0');assert.match(root.text(),/Used 75.5%/);assert.match(root.text(),/Remaining 24.5%/);assert.match(root.text(),/Available: 1/);assert.doesNotMatch(root.text(),/175.5%/);
});
test('stale windows, plans, zero credits and unavailable snapshots remain explicit',()=>{
 const h=fixture(),a=account({secondary:{window_seconds:604800,used_percent:96,remaining_percent:4,stale:true}}, {plan_type:'pro',captured_at:'2026-10-03T12:00:00Z',credits:{has_credits:false,unlimited:false,balance:'0'}});
 const root=h.c.renderCodexUsageDashboard({accounts:[a,{label:'unknown',logged_in:true},{label:'out',logged_in:false}],usage_summary:{accounts:2,known:1,unknown:1,available:0,exhausted:0,stale:1}});
 assert.equal(root.all('codex-usage-account').length,2);assert.match(root.text(),/Stale/);assert.match(root.text(),/96%/);assert.match(root.text(),/pro/);assert.match(root.text(),/Credits: 0/);assert.match(root.text(),/Refresh limits to retrieve/);
});
test('manual refresh uses exact all/account contracts and ignores detached panels',async()=>{
 const h=fixture(),root=h.c.renderCodexUsageDashboard({accounts:[account({})],usage_summary:{accounts:1}});
 await root.children[0].children[1].click();assert.deepEqual(JSON.parse(JSON.stringify(h.requests[0])),{url:'/api/codex/usage',body:{all:true}});assert.ok(root.replaced);
 const per=h.c.renderCodexUsageDashboard({accounts:[account({})]});per.isConnected=false;await per.all('codex-usage-account-head')[0].children[2].click();assert.deepEqual(JSON.parse(JSON.stringify(h.requests[1])),{url:'/api/codex/usage',body:{label:'private'}});assert.equal(per.replaced,undefined);
});
test('only general Codex availability applies and server allowed remains authoritative',()=>{
 const h=fixture();
 assert.equal(h.c.codexUsageAccountState({usage:{availability:'available',stale:true,rate_limits:[{id:'model-only',allowed:false}]}}),'available');
 assert.equal(h.c.codexUsageAccountState({usage:{availability:'unknown',stale:false,rate_limits:[{id:'codex',allowed:true}]}}),'unknown');
 assert.equal(h.c.codexUsageAccountState(account({allowed:true,primary:{used_percent:100,remaining_percent:0}})),'available');
 assert.equal(h.c.codexUsageAccountState({usage:{rate_limits:[{id:'model-only',allowed:false,primary:{used_percent:100}}]}}),'unknown');
 assert.equal(h.c.codexUsageAccountState({usage:{stale:true,rate_limits:[{id:'codex',allowed:true,secondary:{used_percent:20,stale:false}},{id:'model-only',primary:{stale:true}}]}}),'available');
 assert.equal(h.c.codexUsageAccountState(account({allowed:true,secondary:{used_percent:20,stale:true}})),'stale');
 const root=h.c.renderCodexUsageDashboard({accounts:[{label:'general',logged_in:true,usage:{availability:'available',stale:true,rate_limits:[{id:'codex',primary:{window_seconds:18000,used_percent:20,stale:false}},{id:'model-only',secondary:{window_seconds:604800,used_percent:90,stale:true}}]}}]});
 const windows=root.all('codex-usage-window');
 assert.equal(windows[0].className,'codex-usage-window');assert.match(windows[1].className,/stale/);
});
test('browser login can be reloaded manually from cached accounts without usage polling',async()=>{
 const h=fixture();let reads=0;
 h.c.j=async url=>{assert.equal(url,'/api/codex/accounts');reads++;return {accounts:[{label:'default',login_in_progress:reads===1,logged_in:reads>1}],usage_summary:{accounts:reads>1?1:0}};};
 await h.c.sections.accounts();
 const reload=h.c.panelContent.children[0].children[0].children[0];
 assert.equal(reads,1);assert.match(h.c.panelContent.text(),/logging in/);
 await reload.click();assert.equal(reads,2);assert.equal(h.requests.length,0);
});
test('limits renderer never schedules automatic refresh or exposes raw limits JSON',()=>{
 assert.doesNotMatch(source,/setTimeout\(sections\.accounts|setInterval\(/);assert.doesNotMatch(source,/JSON\.stringify\(a\.limits/);
});

test('GUI removal names the selected local account and uses scoped logout',async()=>{
 const h=fixture();h.c.j=async()=>({accounts:[{label:'personal',logged_in:true},{label:'work',logged_in:true}],usage_summary:{accounts:2}});
 await h.c.sections.accounts();
 const buttons=h.c.panelContent.all('danger');assert.equal(buttons.length,2);assert.equal(buttons[1].textContent,'Remove account');
 await buttons[1].click();assert.deepEqual(JSON.parse(JSON.stringify(h.requests[0])),{url:'/api/codex/logout',body:{label:'work'}});
});
