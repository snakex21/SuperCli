import {check, equal, frame, ready, noErrors} from './common.js';
export async function initial() {
  await ready(); ui.lang='pl'; stream.innerHTML=''; toolRows={}; workerRows={}; openToolOrder=[];
  resetWorkerOverview(); hideWelcome(); addUserMsg('Sprawdź delegację i kontynuuj poprawki po błędzie.');
  const note=(id,agent,report)=>'<task-notification><task-id>'+id+'</task-id><agent>'+agent+'</agent><status>done</status><summary>'+agent+' done · 2 steps · 200 in/40 out tok</summary><result>'+report+'</result></task-notification>';
  window.__note=note;
  addToolCall('task',JSON.stringify({agent:'code',prompt:'Sprawdź pierwszą część projektu.'}),'spawn-a');
  addToolCall('task',JSON.stringify({agent:'code',prompt:'Sprawdź drugą część projektu.'}),'spawn-b');
  addWorkerProgress({id:'worker-2',name:'code',kind:'started',parent_call_id:'spawn-b',run:1});
  addWorkerProgress({id:'worker-1',name:'code',kind:'started',parent_call_id:'spawn-a',run:1});
  addWorkerProgress({id:'worker-2',name:'code',kind:'tool_call',parent_call_id:'spawn-b',call_id:'b-read',tool:'read_lines',args:'{"file":"second.go"}'});
  const parallelMapped=workerRows['worker-2']===toolRows['spawn-b'] && toolRows['spawn-a']._activity.querySelectorAll('.task-activity-item').length===0 && toolRows['spawn-a']._activity.querySelector('.task-activity-empty').textContent===t('task.activityWaiting') && toolRows['spawn-b']._activity.querySelectorAll('.task-activity-item').length===1 && !toolRows['spawn-b']._activity.querySelector('.task-activity-empty');
  addToolResult('spawn-a',note('worker-1','code','Pierwsze sprawdzenie zakończone.'));
  addToolResult('spawn-b',note('worker-2','code','Druga część sprawdzona.'));
  const old=toolRows['spawn-a'],before=old._body.textContent,second=toolRows['spawn-b'];window.__old=old;window.__second=second;
  toolRows={};openToolOrder=[];
  addToolCall('send_message',JSON.stringify({to:'worker-1',message:'Kontynuuj po błędzie bramy. Zachowaj wykonane zmiany i uruchom testy.'}),'resume-a');
  addWorkerProgress({id:'worker-1',name:'code',kind:'started',parent_call_id:'resume-a',run:2});
  const resumed=toolRows['resume-a'];resumed.open=false;
  addWorkerProgress({id:'worker-1',name:'code',kind:'tool_call',parent_call_id:'resume-a',call_id:'check',tool:'ctx_execute',args:'{"command":["go","test","./..."]}'});
  const collapsePreserved=!resumed.open;
  addWorkerProgress({id:'worker-1',name:'code',kind:'tool_result',parent_call_id:'resume-a',call_id:'check',output:'ok'});
  addToolResult('resume-a',note('worker-1','code','Zmiany poprawione. Testy przeszły.'));
  const isolated=old._body.textContent===before && resumed._activity.childNodes.length===1;
  old.open=false;resumed.querySelector('.task-backlink').click();const backlink=old.open && resumed._body.textContent.includes('Kontynuuj po błędzie');resumed.open=true;
  const question=id=>({id,question:'Który wariant wybrać?',header:id,options:[{label:'Zachowaj'},{label:'Zmień'}],allow_custom:true});
  showQuestion(question('worker-question-1'));showQuestion(question('worker-question-2'));
  const bothQuestions=document.querySelectorAll('.question-inline').length===2;
  closeQuestionOverlay('worker-question-1');const questionRetained=document.querySelectorAll('.question-inline').length===1 && !!questionOverlays['worker-question-2'];closeQuestionOverlay();
  old.open=false;second.open=false;
  const overviewStable=document.querySelectorAll('.worker-overview-item').length===2 && workerOverview['worker-1'].row===resumed && workerOverview['worker-1'].label.textContent==='Worker 1 · code' && workerOverview['worker-1'].status==='done';
  resumed.open=false;workerOverview['worker-1'].button.click();const overviewOpensLatest=resumed.open;stage.scrollTop=0;
  const result={parallelMapped,isolated,backlink,collapsePreserved,bothQuestions,questionRetained,overviewStable,overviewOpensLatest};
  for(const [name,value] of Object.entries(result))equal(value,true,name);
  window.__uiCheckMetrics.delegation=result;noErrors();await frame();
}
export async function narrow(){await frame();const row=toolRows['resume-a'].getBoundingClientRect();const geometry={left:row.left,right:row.right,width:innerWidth,clientWidth:document.documentElement.clientWidth,visualWidth:visualViewport.width,zoom:document.documentElement.style.zoom,shell:document.querySelector('#shell').getBoundingClientRect().toJSON(),stage:stage.getBoundingClientRect().toJSON(),sidebarHidden:ui.sidebarHidden};check(geometry.left>=-1 && geometry.right<=geometry.width+1,'narrow delegation row fits viewport',geometry);window.__uiCheckMetrics.narrow=geometry;}
export async function active(){
  const token = 'bez_spacji_żółć_'.repeat(45);
  window.__workerOverflowBrief = 'Sprawdź zapis plików i przenośność aplikacji.\n' + token + '\nZachowaj cały opis zadania.';
  addToolCall('task',JSON.stringify({agent:'review',prompt:window.__workerOverflowBrief}),'spawn-c');
  addWorkerProgress({id:'worker-3',name:'review',kind:'started',parent_call_id:'spawn-c',run:1,model:'custom/'+'gpt-6-luna-'.repeat(12),reasoning_effort:'xhigh'});
  addWorkerProgress({id:'worker-3',name:'review',kind:'tool_call',parent_call_id:'spawn-c',call_id:'c-read',tool:'read_lines',args:JSON.stringify({file:'C:/project/'+token+'.go'})});
  addWorkerProgress({id:'worker-3',name:'review',kind:'tool_call',parent_call_id:'spawn-c',call_id:'c-mcp',tool:'mcp_'+token,args:'{}'});
  addToolResult('resume-a',window.__note('worker-1','code','Raport z delegacji: '+token+'\n\nŚcieżka: `'+token+'`\n\n```text\n'+token+'\n```'));
  equal(workerOverview['worker-3'].status,'running','active worker state');const summary=$('#worker-overview-summary').textContent;
  check(summary.includes('pracuje: 1') && summary.includes('gotowe: 2'),'worker summary counts',summary);
  equal(document.querySelector('.worker-overview-item'),workerOverview['worker-3'].button,'running worker first');
  check($('#worker-overview').open,'overview open while running');toolRows['resume-a'].open=false;stage.scrollTop=0;await frame();
}
export async function layout(){
  const row = toolRows['spawn-c'], completed = toolRows['resume-a'];
  row.open = true; completed.open = true; await frame();
  equal(row.classList.contains('running'),true,'overflow fixture keeps the worker running');
  equal(row.querySelector('.task-brief').textContent,window.__workerOverflowBrief,'full delegated brief preserved');
  const measurements = [];
  for (const card of [row, completed]) {
    for (const node of [card, ...card.querySelectorAll('summary, .tbody, .task-brief, .task-activity, .task-activity-item, .task-report')]) {
      if (!node.clientWidth) continue;
      const m = {part:node.className || node.tagName,width:node.clientWidth,scrollWidth:node.scrollWidth};
      measurements.push(m);
      check(m.scrollWidth <= m.width + 1,'delegation content fits its field at '+innerWidth+'px',m);
    }
    const bounds = card.getBoundingClientRect();
    check(bounds.left >= -1 && bounds.right <= innerWidth + 1,'delegation card fits viewport at '+innerWidth+'px',bounds.toJSON());
  }
  const label = row.querySelector('.worker-model-info');
  check(label && row.title.includes('gpt-6-luna-'.repeat(12)),'full worker model stays available in tooltip');
  check(row.querySelector('.task-activity-item.running'),'live activity retained during layout checks');
  window.__uiCheckMetrics['delegationLayout'+innerWidth] = measurements;
  row.scrollIntoView({block:'start'}); await frame(); noErrors();
}
export async function cleanup(){
  settleOpenTools();
  equal(workerOverview['worker-3'].status,'running','parent completion does not infer detached worker cancellation');
  const worker = workerRows['worker-3'];
  addWorkerProgress({id:'worker-3',name:worker._taskAgent,kind:'finished',status:'stopped',run:worker._taskRun || 0});
  equal(workerOverview['worker-3'].status,'stopped','verified worker cancellation settled');
  check(worker._clock === null,'verified stopped worker clock released');
  check(!$('#worker-overview').open,'idle overview collapsed');
  pruneWorkerOverview();check($('#worker-overview').hidden && !Object.keys(workerOverview).length,'new run prunes settled workers');
  newSession();check($('#worker-overview').hidden && !Object.keys(workerOverview).length,'new session clears workers');noErrors();
}
