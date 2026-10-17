import {check,equal,deepEqual,ready,frame,fixtureState,waitFor,noErrors} from './common.js';
const options=()=>Array.from(document.querySelectorAll('#reasoning-options button')).map(b=>({text:b.textContent,value:b.dataset.level,checked:b.getAttribute('aria-checked')}));
export async function initial(){
  await ready();ui.lang='pl';ui.rememberSessionRuntime=true;await loadSessions();activeSessionID='a';await loadReasoning();toggleReasoningMenu(true);
  const got=options();deepEqual(got.map(x=>x.value),['','none','high'],'toggle-only adaptive choices');
  check(got[1].text.includes('Wyłączone') && got[2].text.includes('Włączone'),'Polish off/on labels');equal(got[2].checked,'true','legacy low selects On');await frame();
}
export async function narrow(){await frame();const geometry=document.querySelector('#reasoning-menu').getBoundingClientRect().toJSON();check(geometry.x>=0 && geometry.x+geometry.width<=520,'narrow reasoning menu geometry',geometry);window.__uiCheckMetrics.geometry=geometry;}
export async function keyboard(){
  const high=document.querySelector('#reasoning-options button[data-level="high"]');high.focus();
  high.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowUp',bubbles:true,cancelable:true}));equal(document.activeElement.dataset.level,'none','ArrowUp focuses Off');
  // Synthetic DOM keys do not trigger trusted default activation. Exercise the
  // native button click listener explicitly after Enter, without a driver fork.
  const target=document.activeElement;const activate=target.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,cancelable:true}));if(activate)target.click();
  await sessionRuntimeReady;await waitFor(()=>!reasoningSaving,'reasoning save');
  equal((await fixtureState()).sessions[0].reasoning_effort,'none','keyboard selection persisted for A');equal($('#reasoning-level').textContent,'Wyłączone','saved Off label');
  await resumeSession('b',sessionByID.b);await sessionRuntimeReady;await resumeSession('a',sessionByID.a);await sessionRuntimeReady;await loadReasoning();
  equal((await fixtureState()).configured,'none','reopening without message keeps choice');
}
export function startStale(){window.oldModelRead=loadModels();}
export async function chooseHigh(){toggleReasoningMenu(true);$('#reasoning-options button[data-level="high"]').click();await sessionRuntimeReady;await waitFor(()=>!reasoningSaving,'save high');}
export async function finishStale(){await window.oldModelRead;equal($('#reasoning-level').textContent,'Włączone','stale catalog cannot repaint saved On');}
export function startPending(){toggleReasoningMenu(true);$('#reasoning-options button[data-level="none"]').click();}
export async function resumeB(){await resumeSession('b',sessionByID.b);}
export async function finish(){
  await sessionRuntimeReady;await loadReasoning();const state=await fixtureState();equal(state.sessions[0].reasoning_effort,'none','pending A choice saved');equal(state.configured,'medium','pending A save cannot overwrite B restoration');
  renderReasoning({configured:'xhigh',effective:'high',selected:'high',adjusted:true,supported:true,levels:['low','medium','high']});toggleReasoningMenu(true);
  const got=options();deepEqual(got.map(x=>x.value),['','low','medium','high'],'graded adaptive choices');equal(got[3].checked,'true','adjusted xhigh selects high');
  window.__uiCheckMetrics.calls=state.calls;noErrors();await frame();
}
