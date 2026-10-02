const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),test=require('node:test'),vm=require('node:vm');
const source=fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js/site-preview.js'),'utf8');
function harness(request) {
 const all=[],requests=[];
 function element(tag) {
  const node={tag,className:'',children:[],attributes:{},events:{},value:'',textContent:'',parentNode:null,removed:false,
   classList:{add(name){node.className+=' '+name;}},
   appendChild(child){node.children.push(child);child.parentNode=node;return child;},
   setAttribute(key,value){node.attributes[key]=value;},
   addEventListener(key,fn){node.events[key]=fn;},
   remove(){node.removed=true;if(node.parentNode)node.parentNode.children=node.parentNode.children.filter(x=>x!==node);},
   focus(){node.focused=true;},select(){node.selected=true;}
  };all.push(node);return node;
 }
 const c={window:{},document:{createElement:element},console};vm.createContext(c);vm.runInContext(source,c);
 const root=element('section');
 const options={language:'en',text:key=>options.language+' '+key,url:'',close(){controller.destroy();},
  request:async(url,body)=>{requests.push({url,body});return request?request(url,body):{url:'http://localhost:5173/'};}
 };
 const controller=c.window.SuperCliPreview.create(root,options);
 return {all,root,requests,controller,options,address:all.find(n=>n.tag==='input'),form:all.find(n=>n.tag==='form'),button:key=>all.find(n=>n._copyKey===key),frames:()=>all.filter(n=>n.tag==='iframe'&&!n.removed)};
}
const tick=()=>new Promise(resolve=>setImmediate(resolve));
test('opening an empty preview creates no frame and sends no request',()=>{
 const h=harness();assert.equal(h.frames().length,0);assert.equal(h.requests.length,0);
 h.controller.focus();assert.equal(h.address.focused,true);assert.equal(h.address.selected,true);
});
test('navigation creates one isolated frame and reload keeps its identity',async()=>{
 const h=harness();h.address.value='localhost:5173';h.form.events.submit({preventDefault(){}});
 await tick();assert.equal(h.requests.length,1);assert.equal(h.frames().length,1);
 const frame=h.frames()[0];assert.equal(frame.src,'http://localhost:5173/');
 assert.equal(frame.attributes.referrerpolicy,'no-referrer');
 assert.equal(frame.attributes.sandbox,'allow-scripts allow-same-origin allow-forms allow-modals');
 h.button('preview.reload').events.click();assert.equal(h.frames()[0],frame);assert.equal(h.requests.length,1);
 h.controller.destroy();assert.equal(h.frames().length,0);assert.equal(h.root.removed,true);
});
test('closing while validation is pending cannot recreate the frame',async()=>{
 let finish;const h=harness(()=>new Promise(resolve=>finish=resolve));
 h.address.value='localhost:5173';h.button('preview.open').events.click();h.controller.destroy();
 finish({url:'http://localhost:5173/'});await tick();assert.equal(h.frames().length,0);
});
test('only the latest URL request can navigate the preview',async()=>{
 const pending=[];const h=harness(()=>new Promise(resolve=>pending.push(resolve)));
 h.address.value='localhost:5173/old';h.button('preview.open').events.click();
 h.address.value='localhost:5173/new';h.button('preview.open').events.click();
 pending[1]({url:'http://localhost:5173/new'});await tick();
 pending[0]({url:'http://localhost:5173/old'});await tick();
 assert.equal(h.frames().length,1);assert.equal(h.frames()[0].src,'http://localhost:5173/new');
 assert.equal(h.address.value,'http://localhost:5173/new');
});
test('external open uses the current input without creating a frame',async()=>{
 const h=harness();h.address.value='example.com';
 h.button('preview.browser').events.click();assert.equal(h.button('preview.browser').disabled,true);
 await tick();assert.equal(h.requests[0].url,'/api/browser/open');assert.equal(h.requests[0].body.url,'example.com');
 assert.equal(h.frames().length,0);assert.equal(h.button('preview.browser').disabled,false);
});
test('language refresh preserves the address, frame and focus',async()=>{
 const h=harness();h.address.value='localhost:5173';h.button('preview.open').events.click();await tick();
 const frame=h.frames()[0];h.address.value='draft.example';h.controller.focus();h.options.language='pl';
 h.controller.refresh();assert.equal(h.address.value,'draft.example');assert.equal(h.frames()[0],frame);assert.equal(h.address.focused,true);assert.equal(h.button('preview.browser').title,'pl preview.browser');
});
test('invalid URLs never create a frame and show a localized notice',async()=>{
 const h=harness(()=>{throw Error('invalid');});h.address.value='javascript:alert(1)';h.button('preview.open').events.click();await tick();
 assert.equal(h.frames().length,0);assert.equal(h.all.find(n=>n.className==='preview-status').textContent,'en preview.invalid');
});

test('existing notices change language without changing the URL',async()=>{const h=harness(()=>{throw Error('invalid');});h.address.value='bad';h.button('preview.open').events.click();await tick();h.options.language='pl';h.controller.refresh();assert.equal(h.all.find(n=>n.className==='preview-status').textContent,'pl preview.invalid');assert.equal(h.address.value,'bad');});

test('programmatic link navigation reuses the same frame and ignores a disposed controller',async()=>{
 const h=harness((_url,body)=>({url:'https://example.com/'+body.url}));
 await h.controller.navigate('one');const frame=h.frames()[0];
 await h.controller.navigate('two');assert.equal(h.frames()[0],frame);assert.equal(frame.src,'https://example.com/two');
 h.controller.destroy();await h.controller.navigate('three');assert.equal(h.requests.length,2);assert.equal(h.frames().length,0);
});
test('native launch errors stay visible as text and survive language refresh',async()=>{
 const h=harness(()=>{throw Error('ShellExecuteExW: access denied <browser>');});
 h.address.value='https://example.com';h.button('preview.browser').events.click();await tick();
 const status=h.all.find(n=>n.className==='preview-status');
 assert.equal(status.textContent,'en preview.failed\nShellExecuteExW: access denied <browser>');
 assert.equal(h.frames().length,0);assert.equal(h.button('preview.browser').disabled,false);
 h.options.language='pl';h.controller.refresh();assert.equal(status.textContent,'pl preview.failed\nShellExecuteExW: access denied <browser>');
});
test('a second click cannot duplicate a pending native launch',async()=>{
 let finish;const h=harness(()=>new Promise(resolve=>finish=resolve));
 h.button('preview.browser').events.click();h.button('preview.browser').events.click();assert.equal(h.requests.length,1);
 finish({ok:true});await tick();assert.equal(h.button('preview.browser').disabled,false);
});

test('clear removes the page and remembered URL while keeping the pane ready',async()=>{
 const remembered=[];const h=harness();h.options.remember=value=>remembered.push(value);
 await h.controller.navigate('localhost:5173');
 const frame=h.frames()[0],requests=h.requests.length;
 h.button('preview.clear').events.click();
 assert.equal(frame.removed,true);assert.equal(h.frames().length,0);assert.equal(h.root.removed,false);
 assert.equal(h.address.value,'');assert.equal(h.address.focused,true);
 assert.deepEqual(remembered,['http://localhost:5173/','']);assert.equal(h.requests.length,requests);
 h.controller.reset();assert.equal(h.requests.length,requests);
});
test('clear invalidates navigation already in flight',async()=>{
 let finish;const h=harness(()=>new Promise(resolve=>finish=resolve));
 const pending=h.controller.navigate('localhost:5173');h.controller.reset();
 finish({url:'http://localhost:5173/'});await pending;
 assert.equal(h.frames().length,0);assert.equal(h.address.value,'');
 assert.equal(h.all.find(n=>n.className==='preview-status').textContent,'');
});
test('clearing a pending browser launch keeps its stale completion out of the empty pane',async()=>{
 let finish;const h=harness(()=>new Promise(resolve=>finish=resolve));
 h.address.value='https://example.com';h.button('preview.browser').events.click();h.controller.reset();
 assert.equal(h.button('preview.browser').disabled,true);finish({ok:true});await tick();
 assert.equal(h.address.value,'');assert.equal(h.frames().length,0);assert.equal(h.button('preview.browser').disabled,false);
 assert.equal(h.all.find(n=>n.className==='preview-status').textContent,'');
});
