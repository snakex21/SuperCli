
'use strict';
const fs=require('node:fs/promises'), path=require('node:path'),cp=require('node:child_process'),vm=require('node:vm');
const root=path.resolve(__dirname,'../..');
const outputRoot=path.join(root,'.tmp','history-eval');
const outdir=path.resolve(outputRoot,process.argv[2]||'experiment');
if(outdir!==outputRoot&&!outdir.startsWith(outputRoot+path.sep))throw Error('Output must remain under .tmp/history-eval');
const system='You are repairing a small JavaScript module. Preserve the requested API. Work only with the supplied fixture.';
const models=[
 {Model:'qwen3.8-27b-uncensored',Base:'http://127.0.0.1:1234/v1',Transport:'chat'},
 {Model:'mimo-v2.6-flash-free',Base:'https://opencode.ai/zen/v1',Transport:'chat'},
 {Model:'muse-spark-1.3-contributor-free',Base:'https://opencode.ai/zen/v1',Transport:'responses'},
 {Model:'deepseek-v4-flash-free',Base:'https://opencode.ai/zen/v1',Transport:'chat'},
];
const fixtures=[
 {id:'escaped-fields',fn:'splitEscaped',file:'parser.js',
 source:'function splitEscaped(s) { return s.split(","); }',
 spec:'splitEscaped(s) returns comma-delimited fields. A backslash escapes the next character, including commas and backslashes. Preserve empty fields, including a trailing empty field. A dangling final backslash is a literal backslash. Empty input returns one empty field. Do not trim whitespace.',
 tests:[
 {args:[''],want:['']},{args:[','],want:['','']},{args:['a,,b,'],want:['a','','b','']},
 {args:['a\\,b,c'],want:['a,b','c']},{args:['a\\\\,b'],want:['a\\','b']},
 {args:['x\\'],want:['x\\']},{args:['\\q, z '],want:['q',' z ']},{args:['\\,'],want:[',']},
 {args:['a\\\\\\,b,c'],want:['a\\,b','c']}
 ]},
 {id:'cache-expiry',fn:'runCache',file:'cache.js',
 source:'function runCache(events, ttl) { const m = new Map(), out = []; for (const e of events) { if (e.op === "put") m.set(e.key, {value:e.value, expires:e.t+ttl}); else if (e.op === "delete") m.delete(e.key); else if (e.op === "get") out.push(m.has(e.key) ? m.get(e.key).value : null); } return out; }',
 spec:'runCache(events, ttl) simulates a TTL cache and returns one value for every get, in input order. Events are {op:"put"|"get"|"delete", key, t, value?}; times are nondecreasing. put replaces value and sets expiry to t+ttl. get is a miss (null) when absent or t >= expiry; get never refreshes expiry. ttl <= 0 means immediately expired. delete removes a key. Each call is independent. No wall clock or asynchronous work.',
 tests:[
 {args:[[{op:'get',key:'x',t:0}],10],want:[null]},
 {args:[[{op:'put',key:'x',t:0,value:4},{op:'get',key:'x',t:9},{op:'get',key:'x',t:10}],10],want:[4,null]},
 {args:[[{op:'put',key:'x',t:0,value:4},{op:'get',key:'x',t:9},{op:'get',key:'x',t:15}],10],want:[4,null]},
 {args:[[{op:'put',key:'x',t:0,value:4},{op:'put',key:'x',t:8,value:7},{op:'get',key:'x',t:10},{op:'get',key:'x',t:18}],10],want:[7,null]},
 {args:[[{op:'put',key:'x',t:1,value:0},{op:'get',key:'x',t:1}],0],want:[null]},
 {args:[[{op:'put',key:'x',t:0,value:false},{op:'put',key:'y',t:1,value:''},{op:'delete',key:'x',t:2},{op:'get',key:'x',t:3},{op:'get',key:'y',t:3}],5],want:[null,'']},
 {args:[[],10],want:[]}
 ]}
];
const clone=x=>JSON.parse(JSON.stringify(x));
function events(sse){return sse.split(/\r?\n\r?\n/).flatMap(b=>{const d=b.split(/\r?\n/).filter(l=>l.startsWith('data:')).map(l=>l.slice(5).trimStart()).join('\n');try{return [JSON.parse(d)];}catch{return [];}});}
function reply(model,r){
 if(model.Transport==='responses'){
  const es=events(r.SSE),done=es.filter(e=>e.type==='response.output_item.done').map(e=>e.item);
  const native=done.length?done:es.find(e=>e.type==='response.completed')?.response?.output;
  if(!native?.length)throw Error('Missing native Responses output');
  return native;
 }
 return [{role:'assistant',content:r.Text||'',...(r.Reasoning?{reasoning_content:r.Reasoning}:{}),
  ...(r.Calls?.length?{tool_calls:r.Calls.map(c=>({id:c.ID,type:'function',function:{name:c.Name,arguments:c.Arguments}}))}:{})}];
}
function stripCompleted(history){
 return clone(history).filter(m=>m.type!=='reasoning').map(m=>{
  if(m.role==='assistant'){
   delete m.reasoning_content;delete m.reasoning_details;
   const strip=s=>s.replace(/<(think|thinking|reasoning|reflection)>[\s\S]*?<\/\1>/gi,'').trim();
   if(typeof m.content==='string')m.content=strip(m.content);
   else if(Array.isArray(m.content))m.content=m.content.filter(p=>!['thinking','redacted_thinking'].includes(p.type)).map(p=>typeof p.text==='string'?{...p,text:strip(p.text)}:p);
  }
  return m;
 });
}
function verify(f,text){
 let code=text.trim();
 const fence=code.match(/^\x60{3}(?:javascript|js)?\s*\n([\s\S]*?)\n\x60{3}\s*$/i);if(fence)code=fence[1];
 const ctx=vm.createContext(Object.create(null),{codeGeneration:{strings:false,wasm:false}});
 try{
  const expr=code+'\n;JSON.stringify('+JSON.stringify(f.tests.map(t=>t.args))+'.map(a=>'+f.fn+'(...a)))';
  const got=JSON.parse(new vm.Script(expr).runInContext(ctx,{timeout:500}));
  const passed=f.tests.filter((t,i)=>JSON.stringify(t.want)===JSON.stringify(got[i])).length;
  return {passed,total:f.tests.length,success:passed===f.tests.length,got};
 }catch(e){return {passed:0,total:f.tests.length,success:false,error:e.message};}
}
let seq=0;
async function call(model,history,session,label){
 const input={...model,History:history,Session:session,Effort:'low'};
 const result=await new Promise((resolve,reject)=>{
  const p=cp.spawn(path.join(outputRoot,'probe.exe'),[],{cwd:root,windowsHide:true,stdio:['pipe','pipe','pipe']});
  let stdout='',stderr='';p.stdout.on('data',x=>stdout+=x);p.stderr.on('data',x=>stderr+=x);
  p.on('error',reject);p.on('close',code=>{if(code)reject(Error('probe exit '+code+': '+stderr));else{try{resolve(JSON.parse(stdout));}catch(e){reject(Error(stdout+stderr));}}});
  p.stdin.end(JSON.stringify(input));
 });
 const n=String(++seq).padStart(3,'0');
 await fs.writeFile(path.join(outdir,n+'-'+label+'.json'),JSON.stringify({input,result},null,2));
 console.log(JSON.stringify({event:'request-complete',model:model.Model,label,ms:result.ElapsedMS,ttft:result.TTFTMS,input:result.Usage?.Input,output:result.Usage?.Output,error:result.Error||undefined}));
 return result;
}
async function turn(model,history,session,f,label){
 const stats={calls:0,toolCalls:0,input:0,output:0,reasoning:0,cached:0,ms:0,ttft:0,reasoningChars:0,errors:[]};
 let last;
 for(let step=0;step<4;step++){
  const r=await call(model,history,session,label+'-'+step);last=r;
  stats.calls++;stats.input+=r.Usage?.Input||0;stats.output+=r.Usage?.Output||0;stats.reasoning+=r.Usage?.Reasoning||0;stats.cached+=r.Usage?.CachedInput||0;stats.ms+=r.ElapsedMS;
  if(step===0)stats.ttft=r.TTFTMS;
  stats.reasoningChars+=r.Reasoning?.length||0;
  if(r.Error){stats.errors.push(r.Error);return {stats,text:r.Text};}
  history.push(...reply(model,r));
  if(!r.Calls?.length)return {stats,text:r.Text};
  for(const c of r.Calls){
   stats.toolCalls++;
   let args={};try{args=JSON.parse(c.Arguments);}catch{}
   const result=c.Name==='read'?(args.filePath===f.file?f.source:'Unknown fixture path; available: '+f.file):'No shell is available. Return the requested source as your final answer; verification is performed by the evaluator.';
   history.push(model.Transport==='responses'?{type:'function_call_output',call_id:c.ID,output:result}:{role:'tool',tool_call_id:c.ID,content:result});
  }
 }
 stats.errors.push('tool step limit');return {stats,text:last?.Text||''};
}
(async()=>{
 await fs.mkdir(outdir,{recursive:true});
 const selected=process.argv[3]?models.filter(m=>process.argv[3].split(',').includes(m.Model)):models;
 const tasks=process.argv[4]?fixtures.filter(f=>f.id===process.argv[4]):fixtures;
 const results=[];
 for(const model of selected){
  for(const [i,f] of tasks.entries()){
   const session='history-eval-'+path.basename(outdir)+'-'+model.Model+'-'+f.id;
   const history=[{role:model.Transport==='responses'?'developer':'system',content:system},{role:'user',content:'Read '+f.file+' and explain the smallest correction needed for this specification. Do not implement yet. Keep your visible explanation under 100 words.\n'+f.spec}];
   const plan=await turn(model,history,session,f,'plan-'+f.id);
   if(plan.stats.errors.length){results.push({model:model.Model,fixture:f.id,arm:'plan',...plan});break;}
   for(const arm of i%2?['drop','keep']:['keep','drop']){
    const branch=arm==='drop'?stripCompleted(history):clone(history);
    branch.push({role:'user',content:'Implement that correction now. Return only the complete '+f.fn+' function as JavaScript source, with no explanation and no markdown. Keep all requirements from my previous message.'});
    const completed=await turn(model,branch,session,f,arm+'-'+f.id);
    const item={model:model.Model,fixture:f.id,arm,plan:plan.stats,stats:completed.stats,verification:verify(f,completed.text),text:completed.text};
    results.push(item);
    console.log(JSON.stringify({event:'arm-complete',model:model.Model,fixture:f.id,arm,stats:item.stats,verification:item.verification}));
    await fs.writeFile(path.join(outdir,'results.json'),JSON.stringify(results,null,2));
   }
  }
 }
 await fs.writeFile(path.join(outdir,'results.json'),JSON.stringify(results,null,2));
})().catch(e=>{console.error(e.stack);process.exitCode=1;});
