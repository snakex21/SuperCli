'use strict';
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const assert=require('node:assert/strict');
const {execFileSync}=require('node:child_process');
const fixture=require('./transcript-dom-fixture.cjs');
const root=path.resolve(__dirname,'..');
const ref=process.env.TRANSCRIPT_BASE_REF || 'e83ca331b0a0892518a3083fad1fbe27aee21128';
const baseline=Object.fromEntries(['03-markdown.js','04-transcript.js','08-sessions.js'].map(name=>[name,execFileSync('git',['show',ref+':internal/webgui/assets/js/'+name],{cwd:root,encoding:'utf8'})]));
const current=fixture(root);current.baseline=baseline;
const correctness=vm.runInContext('('+require('./transcript-performance-checks.cjs').toString()+')({baseline})',current);
console.log('PASS HTML DOM regression',JSON.stringify(correctness));
const paragraphs=Array.from({length:400},(_,i)=>'Paragraph '+i+' has **bold**, `code`, and ordinary streaming words.\n\n').join('');
const code='```js\n'+Array.from({length:1200},(_,i)=>'const item'+i+' = "<&value>";\n').join('');
function stream(overrides,text) {
 const c=fixture(root,overrides);let chars=0,calls=0;
 const renderer=c.renderMarkdownish;
 c.renderMarkdownish=text=>{chars+=text.length;calls++;return renderer(text)};
 const node=c.addAssistantMsg(),start=performance.now();
 for(let i=64;i<text.length+64;i+=64){node._raw=text.slice(0,i);c.renderAssistant(node)}
 return {ms:performance.now()-start,markdownInputChars:chars,markdownCalls:calls,elements:node.querySelectorAll('*').length};
}
function medianRuns(overrides,text) {
 stream(overrides,text); // warmup
 const runs=Array.from({length:5},()=>stream(overrides,text));
 return {...runs[0],ms:runs.map(r=>r.ms).sort((a,b)=>a-b)[2],runs:5};
}
const result={baseline:ref,runtime:process.version,kind:'Node + LinkeDOM; parsing and DOM construction only',correctness,cases:{}};
for(const [name,text] of Object.entries({paragraphs,code})) {
 const before=medianRuns(baseline,text),after=medianRuns({},text);
 assert.ok(after.markdownInputChars<before.markdownInputChars/10,'Expected bounded-tail parsing for '+name);
 result.cases[name]={sourceChars:text.length,chunkChars:64,before,after};
}
function history(overrides){
 const c=fixture(root,overrides),node=c.addAssistantMsg();node._history=true;
 node._raw='<thinking>'+paragraphs.repeat(5)+'</thinking>Answer';
 c.renderAssistant(node);node.querySelectorAll('details').forEach(d=>d.open=false);
 return {sourceChars:node._raw.length,elements:node.querySelectorAll('*').length};
}
result.history={before:history(baseline),after:history({})};
console.log(JSON.stringify(result,null,2));
if(process.env.TRANSCRIPT_BENCH_OUTPUT)fs.writeFileSync(process.env.TRANSCRIPT_BENCH_OUTPUT,JSON.stringify(result,null,2)+'\n');
