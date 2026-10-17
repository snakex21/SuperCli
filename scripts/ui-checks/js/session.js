import {check,equal,deepEqual,ready,fixtureState,visible,noErrors} from './common.js';
const ids=()=>Array.from(document.querySelectorAll('#session-list .side-item')).map(n=>n.dataset.sessionId);
export async function initial(){
  await ready();ui.lang='pl';ui.rememberSessionRuntime=false;ui.sidebarHidden=false;applyUI();applyI18n();
  activateSideTab(document.querySelector('#side-tabs button[data-tab="sessions"]'));
  const originalLoad=loadSessions;loadSessions=function(){window.latestList=originalLoad();return window.latestList;};await loadSessions();
  deepEqual(ids(),['new','old'],'initial session ordering');await resumeSession('old',sessionByID.old);await sessionRuntimeReady;await window.latestList;
  deepEqual(ids(),['new','old'],'opening without writing does not promote');window.__requestsBefore=(await fixtureState()).listCount;
}
export function send(){window.turn=sendPrompt('Wracam dzisiaj do tej rozmowy.');}
export async function promoted(){
  await window.latestList;deepEqual(ids(),['old','new'],'promote before model finishes');equal(streaming,true,'promotion while streaming');
  const groups=Array.from(document.querySelectorAll('#session-list .session-date')).map(n=>n.textContent);deepEqual(groups,['Dzisiaj'],'activity date grouping');
  equal($('#session-list .side-item.active').dataset.sessionId,'old','old session remains active');equal((await fixtureState()).listCount-window.__requestsBefore,1,'one activity refresh, not polling');
  check(visible($('#session-list')),'session list visible');window.__uiCheckMetrics.groups=groups;
}
export async function complete(){await window.turn;deepEqual(ids(),['old','new'],'ordering after completed response');}
export async function reloaded(){
  await ready();await loadSessions();deepEqual(ids(),['old','new'],'ordering survives reload');
  const originalJ=j;j=function(url,opts){const next=Object.assign({},opts);delete next.signal;return originalJ(url,next);};
}
export function startStale(){window.staleList=loadSessions();}
export async function newProject(){projectEpoch++;await loadSessions();}
export async function finishStale(fail){await window.staleList;deepEqual(ids(),['other'],'stale '+(fail?'failure':'success')+' cannot overwrite another project');}
export async function finish(){noErrors();window.__uiCheckMetrics.listCount=(await fixtureState()).listCount;}
