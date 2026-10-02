const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');

// A serialization-only DOM double keeps this regression dependency-free.
// The separate browser/LinkeDOM fixture exercises actual HTML parsing too.
function harness() {
  const esc = text => String(text).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');
  function node(props) {
    const n={parentNode:null,...props};
    n.remove=function(){if(this.parentNode){const siblings=this.parentNode.children;siblings.splice(siblings.indexOf(this),1);this.parentNode=null;}};
    Object.defineProperty(n,'nextSibling',{get(){if(!this.parentNode)return null;const siblings=this.parentNode.children;return siblings[siblings.indexOf(this)+1]||null;}});
    return n;
  }
  function el(tag, cls) {
    const n = node({tag, cls, nodeType:tag?1:11, children: [], dataset:{}, addEventListener(){},
      appendChild(child){return this.insertBefore(child,null);},
      insertBefore(child,before){
        if(child.nodeType===11){child.children.slice().forEach(n=>this.insertBefore(n,before));return child;}
        child.remove();child.parentNode=this;
        const index=before?this.children.indexOf(before):this.children.length;
        this.children.splice(index,0,child);return child;
      },
    });
    Object.defineProperty(n,'childNodes',{get(){return this.children;}});
    if(tag==='template')n.content=el(null);
    Object.defineProperty(n,'innerHTML',{get(){return (this.content||this).children.map(serialize).join('')},set(html){
      const target=this.content||this;target.children.slice().forEach(n=>n.remove());
      if(html)target.appendChild(node({html}));
    }});
    return n;
  }
  function serialize(n) {
    if(n.nodeType===8)return '';
    if ('html' in n) return n.html;
    if ('data' in n) return esc(n.data);
    const body=n.children.map(serialize).join('');
    if(n.nodeType===11)return body;
    return '<'+n.tag+(n.cls?' class="'+esc(n.cls)+'"':'')+('lang' in n.dataset?' data-lang="'+esc(n.dataset.lang)+'"':'')+'>'+body+'</'+n.tag+'>';
  }
  const c={window:{},superCliUI:{},$(){return {addEventListener(){}}},el,escHtml:esc,escAttr:esc,t:k=>k,
    document:{createElement:el,createDocumentFragment:()=>el(null),createComment:()=>node({nodeType:8}),
      createTextNode(data){return node({data,appendData(next){this.data+=next}})}},
  };
  vm.createContext(c);
  for(const file of ['03-markdown.js','04-transcript.js']) vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js',file),'utf8'),c);
  return {c,el,serialize};
}

test('incremental Markdown equals the full renderer at every chunk boundary', () => {
  const {c,el}=harness();
  const samples=[
    '# H\n\nA **bold** paragraph.\n\n- one\n- two\n\n> quote\n> next\n\nEnd',
    '| A | B |\n| --- | --- |\n| one | two |\n\nEnd',
    '\n| A | B |\n| --- | --- |\n| one | two |',
    '\n- first\n- second\n',
    ' \n1. first\n2. second\n',
    '| A | B |\n| :--- | ---: |\n| a **bold** | b |\n| second | third |\n\nAfter',
    '- a | b\n| --- | --- |\n| one | two |',
    '# a | b\n| --- | --- |\n| one | two |',
    '- first **bold**\n+ next `code`\n* third\nplain continuation\n- later',
    '1. first\n2) second\n3. third\n- switches type\n\nEnd',
    '| A | B |\n| --- | --- |\n| code```js\ntext\n``` | b |',
    'before\n\n```js\nconst a="<&>";\n\nnext\n```\n\nafter',
    '```\nplain\n\n```more```js```end',
    '<!--\n\n-->\n\nafter\n\n<!-- not empty\n\n-->next',
    'emoji 🧪 ąćę 日本語\n\n\'quoted\' & "double"',
    'Windows\r\n\r\nNext\r\n\r\n```js\r\ncode\r\n```',
    'a\r<!---->\n\nb',
    'a\u2028<!---->\n\nb',
    'a\u2029  <!---->\n\nb',
    'a\n<!---->\n\n\rb',
    'a\n<!---->\n\n\u2028b',
    'a\n<!---->\n\n\u2029b',
  ];
  for(const source of samples) {
    const target=el('div'),state=c.markdownStream(target);
    for(let i=1;i<=source.length;i++) {
      const text=source.slice(0,i); c.updateMarkdownStream(state,text);
      assert.equal(target.innerHTML,c.renderMarkdownish(text),'prefix '+i+' of '+JSON.stringify(source));
    }
    c.updateMarkdownStream(state,'replaced\n\nsnapshot');
    assert.equal(target.innerHTML,c.renderMarkdownish('replaced\n\nsnapshot'));
  }
});

test('completed blocks and a growing code node survive subsequent chunks', () => {
  const {c,el}=harness(),target=el('div'),state=c.markdownStream(target);
  c.updateMarkdownStream(state,'First paragraph.\n\n```js\nconst a');
  const first=target.children[1],code=state.code;
  c.updateMarkdownStream(state,'First paragraph.\n\n```js\nconst a = 1;\n');
  assert.equal(target.children[1],first);
  assert.equal(state.code,code);
  assert.equal(code.data,'const a = 1;');
  c.updateMarkdownStream(state,'First paragraph.\n\n```js\nconst a = 1;\n```\n\nTail');
  assert.equal(target.children[1],first);
  assert.equal(target.innerHTML,c.renderMarkdownish(state.source));
});

test('reasoning parts preserve nested, orphan and repeated protocol tags', () => {
  const {c}=harness();
  const parts=JSON.parse(JSON.stringify(c.assistantTextParts('</think>A<think>outer<think>inner</think>tail</think>B<think>later</think>C')));
  assert.deepEqual(parts,[{kind:'markdown',text:'A'},{kind:'thinking',text:'outerinnertail'},{kind:'markdown',text:'B'},{kind:'markdown',text:'later'},{kind:'markdown',text:'C'}]);
});

// Deterministic mixed delimiters exercise boundaries that occur between SSE
// chunks, without turning the expected result into another incremental parser.
test('mixed Markdown fragments keep exact full-render parity', () => {
  const {c,el}=harness();
  const tokens=['word','\n','\n\n','```','js','**',' | ','<!--','-->','> ','- ','\r\n','\r','\u2028','\u2029','<!---->','🧪'];
  let seed=92117;
  const next=()=>{seed=(Math.imul(seed,1664525)+1013904223)>>>0;return seed};
  for(let run=0;run<50;run++) {
    const target=el('div'),state=c.markdownStream(target);let text='';
    for(let chunk=0;chunk<40;chunk++) {
      text+=tokens[next()%tokens.length];c.updateMarkdownStream(state,text);
      assert.equal(target.innerHTML,c.renderMarkdownish(text),JSON.stringify(text));
    }
  }
});

test('completed table rows survive open-row updates and newly received rows',()=>{
  const {c,el}=harness(),target=el('div'),state=c.markdownStream(target);
  const head='| A | B |\n| :--- | ---: |\n';
  c.updateMarkdownStream(state,head+'| First | **bold** |\n| Se');
  const block=state.lineBlock,first=block.parent.children[0],pending=block.parent.children[1],header=target.children[1].children[0].children[0];
  c.updateMarkdownStream(state,head+'| First | **bold** |\n| Second | `code` |\n| Third');
  assert.equal(state.lineBlock,block);assert.equal(block.parent.children[0],first);assert.equal(block.parent.children[1],pending);
  assert.equal(target.children[1].children[0].children[0],header);
  assert.equal(target.innerHTML,c.renderMarkdownish(state.source));
  c.updateMarkdownStream(state,state.source+' | done |\n');
  assert.equal(block.parent.children[0],first);assert.equal(block.parent.children[1],pending);
  assert.equal(target.innerHTML,c.renderMarkdownish(state.source));
  c.updateMarkdownStream(state,state.source+'A paragraph after the table.');
  assert.equal(target.innerHTML,c.renderMarkdownish(state.source));assert.equal(state.lineBlock,block);
  assert.equal(block.parent.children[0],first);
  c.updateMarkdownStream(state,state.source+"\nNext paragraph");
  assert.equal(state.lineBlock,null);assert.equal(target.innerHTML,c.renderMarkdownish(state.source));
  c.updateMarkdownStream(state,'| Other | Header |\n| --- | --- |\n| new | values |');
  assert.equal(target.innerHTML,c.renderMarkdownish(state.source));assert.notEqual(state.lineBlock,block);
});

test('completed list items survive growing last items without joining unlike blocks',()=>{
  for(const [start,next,switchTo] of [['- First\n+ Sec','ond\n* Third','\n1. Ordered'],['1. First\n2) Sec','ond\n3. Third','\n- Unordered']]){
    const {c,el}=harness(),target=el('div'),state=c.markdownStream(target);
    c.updateMarkdownStream(state,start);const block=state.lineBlock,first=block.parent.children[0],pending=block.parent.children[1];
    c.updateMarkdownStream(state,start+next);
    assert.equal(state.lineBlock,block);assert.equal(block.parent.children[0],first);assert.equal(block.parent.children[1],pending);
    assert.equal(target.innerHTML,c.renderMarkdownish(state.source));
    c.updateMarkdownStream(state,state.source+switchTo);assert.equal(state.lineBlock,block);
    assert.equal(block.parent.children[0],first);
    assert.equal(target.innerHTML,c.renderMarkdownish(state.source));
    c.updateMarkdownStream(state,state.source+"\nMore prose");assert.equal(state.lineBlock,null);
    assert.equal(target.innerHTML,c.renderMarkdownish(state.source));
  }
});

test("row caching applies to prose after the native reasoning closing newline",()=>{
  for(const text of ["\n- first\n- sec","\n| A | B |\n| --- | --- |\n| first | row |\n| sec"]){
    const {c,el}=harness(),target=el("div"),state=c.markdownStream(target);
    c.updateMarkdownStream(state,text);const block=state.lineBlock,first=block.parent.children[0];
    c.updateMarkdownStream(state,text+"ond");
    assert.equal(state.lineBlock,block);assert.equal(block.parent.children[0],first);
    assert.equal(target.innerHTML,c.renderMarkdownish(state.source));
  }
});


test('growing plain paragraphs retain both their element and their single text node',()=>{
  const {c,el}=harness(),target=el('div'),state=c.markdownStream(target);
  let source='Wiadomość: ';c.updateMarkdownStream(state,source);
  const paragraph=state.paragraph,node=paragraph.node,text=paragraph.text;
  for(const chunk of ['ąćę ','<script> & ','emoji 😀 ','tekst ']){
    source+=chunk;c.updateMarkdownStream(state,source);
    assert.equal(state.paragraph,paragraph);assert.equal(state.paragraph.node,node);assert.equal(state.paragraph.text,text);
    assert.equal(target.innerHTML,c.renderMarkdownish(source));
  }
  source+='**pogrubienie**';c.updateMarkdownStream(state,source);
  assert.equal(state.paragraph.node,node);assert.equal(target.innerHTML,c.renderMarkdownish(source));
  source+=' i [link](https://example.com)';c.updateMarkdownStream(state,source);
  assert.equal(state.paragraph.node,node);assert.equal(target.innerHTML,c.renderMarkdownish(source));
  c.updateMarkdownStream(state,'# Replacement heading');
  assert.equal(state.paragraph,null);assert.equal(target.innerHTML,c.renderMarkdownish('# Replacement heading'));
});

test('unfinished paragraph markup and block markers retain exact prefix rendering',()=>{
  const {c,el}=harness();
  for(const source of ['A **bold** and _italic_ plus '+String.fromCharCode(96)+'code'+String.fromCharCode(96)+' [link](https://example.com) & <tag>',
    'a\nb\nc', 'a\n\n# Header', '+ item', '> quote', '---', '### header', '1) ordered', '| A |\n| --- |\n| row |', '<!---->after']){
    const target=el('div'),state=c.markdownStream(target);
    for(let i=1;i<=source.length;i++){
      c.updateMarkdownStream(state,source.slice(0,i));
      assert.equal(target.innerHTML,c.renderMarkdownish(source.slice(0,i)),JSON.stringify(source.slice(0,i)));
    }
  }
});


test('ordinary suffixes reuse formatted paragraph markup while preserving the full renderer',()=>{
  const {c,el}=harness(),target=el('div'),state=c.markdownStream(target);
  let source='A **bold** and _italic_ paragraph with [a link](https://example.com) and ordinary words';
  c.updateMarkdownStream(state,source);
  const paragraph=state.paragraph.node,original=c.renderMarkdownish;let reparses=0;
  c.renderMarkdownish=text=>{reparses++;return original(text)};
  for(const chunk of [' more words',' & <literal>',' with emoji 😀',' and trailing spaces  ','followed by content']){
    source+=chunk;c.updateMarkdownStream(state,source);
    assert.equal(state.paragraph.node,paragraph);
    assert.equal(target.innerHTML,original(source));
  }
  assert.equal(reparses,0,'ordinary suffixes must not reparse the preceding formatted paragraph');
});

test('formatted paragraph suffixes respect inline closures, block markers and source corrections',()=>{
  const {c,el}=harness();
  for(const source of ['[link](https://example.com)','*italic*letters','_italic_letters',
    '*italic* more words','_italic_ more words','prefix **bold** later _italic_',
    '# heading','1. ordered','+ item','---','a\nb','a\rb','a\x00b',
    'prefix **bold** [new](https://example.com)','prefix **bold** \x60code\x60']){
    const target=el('div'),state=c.markdownStream(target);
    for(let i=1;i<=source.length;i++){
      const prefix=source.slice(0,i);c.updateMarkdownStream(state,prefix);
      assert.equal(target.innerHTML,c.renderMarkdownish(prefix),JSON.stringify(prefix));
    }
    for(const corrected of ['Changed **prefix** with words','Changed **prefix** with different words','# Replaced block']){
      c.updateMarkdownStream(state,corrected);
      assert.equal(target.innerHTML,c.renderMarkdownish(corrected),JSON.stringify(corrected));
    }
  }
});
