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
    return '<'+n.tag+('lang' in n.dataset?' data-lang="'+esc(n.dataset.lang)+'"':'')+'>'+body+'</'+n.tag+'>';
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
