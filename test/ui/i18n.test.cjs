const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const assets = path.resolve(__dirname, '../../internal/webgui/assets/js');
function languageUI() {
 const nodes=[];
 const context={ ui:{lang:'pl'}, document:{documentElement:{}},
  el(tag,cls,text) {
   const node={tag,dataset:{},children:[],className:cls};
   const label={nodeValue:text,parentNode:node};
   node.firstChild=label;node.children.push(label);nodes.push(node);
   return node;
  },
  $$(selector) {return selector==='[data-i18n-text]' ? nodes.filter(n=>n.dataset.i18nText) : []}
 };
 vm.createContext(context);
 vm.runInContext(fs.readFileSync(path.join(assets,'01-i18n.js'),'utf8'),context);
 return context;
}
test('switching language updates existing labels without replacing nested controls',()=>{
 const ui=languageUI();
 const node=ui.i18nEl('button','action','common.save');
 const editor={value:'draft stays here',focused:true,parentNode:node};
 node.children.push(editor);
 assert.equal(node.firstChild.nodeValue,'Zapisz');
 ui.ui.lang='en';ui.applyI18n();
 assert.equal(node.firstChild.nodeValue,'Save');
 assert.equal(node.children[1],editor);
 assert.equal(editor.value,'draft stays here');
 assert.equal(editor.focused,true);
 assert.equal(ui.document.documentElement.lang,'en');
 ui.ui.lang='pl';ui.applyI18n();
 assert.equal(node.firstChild.nodeValue,'Zapisz');
});
test('translation does not overwrite a label temporarily replaced by run status',()=>{
 const ui=languageUI();
 const node=ui.i18nEl('button','','common.save');
 node.firstChild.parentNode=null;
 const status={nodeValue:'working',parentNode:node};
 node.firstChild=status;node.children=[status];
 ui.ui.lang='en';ui.applyI18n();
 assert.equal(node.firstChild.nodeValue,'working');
});
test('all translated dynamic elements have Polish and English entries',()=>{
 const ui=languageUI();
 for(const file of fs.readdirSync(assets).filter(x=>x.endsWith('.js'))) {
  const source=fs.readFileSync(path.join(assets,file),'utf8');
  new vm.Script(source,{filename:file});
  const matches=source.matchAll(/i18nEl\([^\n]*?,\s*"([a-z][\w.-]+)"\)/g);
  for(const match of matches) {
   for(const lang of ['en','pl']) assert.equal(typeof ui.I18N[lang][match[1]],'string',file+': '+lang+'/'+match[1]);
  }
 }
});
