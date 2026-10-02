const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');

function harness() {
  const frames = new Map(), paints = [];
  let now = 0, id = 0;
  const c = {performance:{now:()=>now},requestAnimationFrame(fn){frames.set(++id,fn);return id;},cancelAnimationFrame(id){frames.delete(id);},
    window:{},superCliUI:{},$(){return {addEventListener(){}}}};
  vm.createContext(c);
  for (const file of ['03-markdown.js','04-transcript.js']) {
    vm.runInContext(fs.readFileSync(path.resolve(__dirname,'../../internal/webgui/assets/js',file),'utf8'),c);
  }
  const plain = parts => JSON.parse(JSON.stringify(parts)).map(p=>({kind:p.kind,text:p.text}));
  const renderAssistant=c.renderAssistant;
  c.renderAssistant=n=>paints.push(plain(n._displayParts || c.assistantPartsForNode(n)));
  c.smartScroll=()=>{};
  return {c,frames,paints,plain,renderAssistant,advance(){now+=40;},frame(){now+=16;const pending=Array.from(frames.values());frames.clear();for(const fn of pending)fn();}};
}
const node = (raw='')=>({_raw:raw,_renderTimer:null,_reasoningOpen:false,isConnected:true,classList:{add(){}},appendChild(){}});

function exact(h,n,context='') {
  const actual=h.plain(h.c.assistantPartsForNode(n));
  const expected=h.plain(h.c.assistantTextParts(n._raw));
  assert.deepEqual(actual,expected,context || JSON.stringify(n._raw));
  return actual;
}

test('trusted append checkpoints equal full parsing at every UTF-16 boundary',()=>{
  const samples=[
    'Prose only, 中文 😀 zażółć.\n**bold** and code.',
    '<thinking> first 中文 😀 </thinking>\nAnswer.',
    '</think>A<think>outer<think>inner</think>tail</think>B<think> later </think>C',
    '<REASONING>Mixed<reflection>nested</THINK>tail</reflection>Answer<reasoning>second</reasoning>',
    '<thinking> \n\t </thinking><think>first nonempty</think><reflection>  later untrimmed  </reflection>',
    'Before<thinking>one</thinking>middle</reasoning>after<think>incomplete',
    '<think>A</think><thi',
    '<think>A</think><thinking>B</thinking>tail</reflection>',
    '<thinking>outer<reasoning>inner</thinking>tail</reasoning><think>repeated<reflection>x</reflection>end</think>',
    '<think>thought</think>\n<reflection>second</reflection>\n| A | B |\n| --- | --- |\n| x | y |',
    '<thinking>before\r\n\r\n</thinking>Windows\r\nanswer\u2028and\u2029next',
    'Orphan</reflection>before<not-thinking>x</not-thinking><thinking>text</thinking>after',
  ];
  for(const source of samples){
    const h=harness(),n=node();
    for(let i=0;i<source.length;i++){
      h.c.appendAssistantSource(n,source.slice(i,i+1));
      exact(h,n,'prefix '+(i+1)+' of '+JSON.stringify(source));
    }
  }
});

test('only balanced boundaries are committed and closed parts keep their identity',()=>{
  const h=harness(),n=node('<think>first</think>unfinished <think>second');
  const first=exact(h,n);
  assert.deepEqual(first,[{kind:'thinking',text:'first'},{kind:'markdown',text:'unfinished '},{kind:'markdown',text:'second'}]);
  const end='<think>first</think>'.length;
  assert.equal(n._partsCache.offset,end);
  assert.equal(n._partsCache.parts.length,1);
  const saved=n._partsCache.parts[0];
  h.c.appendAssistantSource(n,' more nested<reflection>inner</reflection>');
  const middle=h.c.assistantPartsForNode(n);
  assert.equal(middle[0],saved);
  assert.equal(n._partsCache.offset,end);
  exact(h,n);
  h.c.appendAssistantSource(n,'</think>tail');
  const after=h.c.assistantPartsForNode(n);
  assert.equal(after[0],saved);
  assert.ok(n._partsCache.offset>end);
  exact(h,n);
});

test('orphan closes and empty first thoughts retain later reasoning semantics',()=>{
  const h=harness(),n=node('</think><think>  </think>');
  exact(h,n);
  assert.equal(n._partsCache.renderedThinking,false);
  h.c.appendAssistantSource(n,'<reflection> real first </reflection>');
  assert.deepEqual(exact(h,n),[{kind:'thinking',text:'real first'}]);
  assert.equal(n._partsCache.renderedThinking,true);
  h.c.appendAssistantSource(n,'<reasoning>  later  </reasoning>');
  assert.deepEqual(exact(h,n),[{kind:'thinking',text:'real first'},{kind:'markdown',text:'  later  '}]);
});

test('replacement, recovery and untracked direct append use a fresh full parse',()=>{
  const h=harness(),n=node('<think>old thought</think>old answer');
  exact(h,n);
  let prior=n._partsCache;
  n._raw='<think>new thought</think>new answer';
  exact(h,n);
  assert.notEqual(n._partsCache,prior);
  prior=n._partsCache;
  n._raw+='<think>later</think>tail'; // Old integrations may append directly.
  exact(h,n);
  assert.notEqual(n._partsCache,prior);
  for(const replacement of ['short','<reasoning>unclosed','','</think>recovered<think>fresh</think>answer']){
    n._raw=replacement;
    exact(h,n);
    h.c.appendAssistantSource(n,' next');
    exact(h,n);
  }
});

test('multiple trusted appends before parsing preserve exact native reasoning',()=>{
  const h=harness(),n=node();
  h.c.appendAssistantReasoning(n,'First 中文');
  h.advance();
  h.c.appendAssistantReasoning(n,' and more thinking.');
  h.c.closeAssistantReasoning(n);
  h.c.appendAssistantSource(n,'Answer starts now.');
  h.c.scheduleAssistantRender(n);
  assert.deepEqual(h.paints.at(-1),[
    {kind:'thinking',text:'First 中文 and more thinking.'},
    {kind:'markdown',text:'\nAnswer starts now.'},
  ]);
  assert.equal(h.frames.size,0,'first answer must not wait for a later frame');
  exact(h,n);
  const saved=n._partsCache.parts[0];
  h.c.appendAssistantSource(n,' More answer text.');
  assert.equal(h.c.assistantPartsForNode(n)[0],saved);
  exact(h,n);
});

test('history, terminal flush, sealed and disconnected nodes retain no checkpoint',()=>{
  const h=harness(),n=node('<thinking>thought</thinking>Answer.');
  h.c.scheduleAssistantRender(n);
  assert.ok(n._partsCache);
  h.c.flushAssistantRender(n);
  assert.equal(n._partsCache,null);
  assert.deepEqual(h.paints.at(-1),h.plain(h.c.assistantTextParts(n._raw)));
  n._history=true;
  exact(h,n);
  assert.equal(n._partsCache,null);
  n._history=false;
  exact(h,n);
  h.c.sealAssistantSegment(n);
  assert.equal(n._partsCache,null);
  assert.equal(n._sealed,true);
  exact(h,n);
  assert.equal(n._partsCache,null);
  const pending=node('<think>thought</think>First.');
  h.c.scheduleAssistantRender(pending);
  h.advance();
  h.c.appendAssistantSource(pending,' several queued words');
  h.c.scheduleAssistantRender(pending);
  assert.ok(h.frames.size);
  pending.isConnected=false;
  h.frame();
  assert.equal(pending._partsCache,null);
});

test('render failure clears checkpoint and recovery remains exact',()=>{
  const h=harness(),n=node('<thinking>thought</thinking>Answer.');
  const render=h.renderAssistant;
  h.c.assistantPart=()=>{throw Error('fixture renderer failure');};
  render(n);
  assert.equal(n.textContent,n._raw);
  assert.equal(n._partsCache,null);
  h.c.assistantPart=part=>({kind:part.kind,text:part.text,markdown:true,paint(){},remove(){}});
  render(n);
  assert.deepEqual(h.plain(n._assistantParts),h.plain(h.c.assistantTextParts(n._raw)));
  h.c.appendAssistantSource(n,' recovered tail.');
  render(n);
  assert.deepEqual(h.plain(n._assistantParts),h.plain(h.c.assistantTextParts(n._raw)));
});

test('deterministic mixed protocol chunks retain exact full-parser parity',()=>{
  const h=harness(),tokens=['word',' ','\n','\r\n','🧪','中文','<think>','</think>','<thinking>','</thinking>',
    '<reasoning>','</reasoning>','<reflection>','</reflection>','<THINK>','</REFLECTION>','<thi','nking>','</thi','nk>','<','>','**bold**'];
  let seed=930102;
  const next=()=>{seed=(Math.imul(seed,1664525)+1013904223)>>>0;return seed;};
  for(let trial=0;trial<120;trial++){
    const n=node();
    for(let chunk=0;chunk<80;chunk++){
      const text=tokens[next()%tokens.length];
      if(next()%19===0)n._raw=text; // Recovery replacement amid partial protocol.
      else h.c.appendAssistantSource(n,text);
      exact(h,n);
    }
  }
});
