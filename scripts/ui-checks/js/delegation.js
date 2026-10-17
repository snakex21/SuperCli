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
  const parallelMapped=workerRows['worker-2']===toolRows['spawn-b'] && toolRows['spawn-a']._activity.childNodes.length===0 && toolRows['spawn-b']._activity.childNodes.length===1;
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
  addToolCall('task',JSON.stringify({agent:'review',prompt:'Sprawdź zapis plików i przenośność aplikacji.'}),'spawn-c');
  addWorkerProgress({id:'worker-3',name:'review',kind:'started',parent_call_id:'spawn-c',run:1});
  addWorkerProgress({id:'worker-3',name:'review',kind:'tool_call',parent_call_id:'spawn-c',call_id:'c-read',tool:'read_lines',args:'{"file":"internal/config.go"}'});
  equal(workerOverview['worker-3'].status,'running','active worker state');const summary=$('#worker-overview-summary').textContent;
  check(summary.includes('pracuje: 1') && summary.includes('gotowe: 2'),'worker summary counts',summary);
  equal(document.querySelector('.worker-overview-item'),workerOverview['worker-3'].button,'running worker first');
  check($('#worker-overview').open,'overview open while running');toolRows['resume-a'].open=false;stage.scrollTop=0;await frame();
}
export async function cleanup(){
  settleOpenTools();equal(workerOverview['worker-3'].status,'stopped','cancelled worker settled');check(!$('#worker-overview').open,'idle overview collapsed');
  pruneWorkerOverview();check($('#worker-overview').hidden && !Object.keys(workerOverview).length,'new run prunes settled workers');
  newSession();check($('#worker-overview').hidden && !Object.keys(workerOverview).length,'new session clears workers');noErrors();
}
