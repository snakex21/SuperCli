import {check,equal,waitFor,visible,frame} from './common.js';
export async function save({spec,scale}){
  const response=await fetch('/api/settings',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({[spec.key]:scale})});
  if(!response.ok)throw Error(await response.text());
}
export async function run({spec,scale}){
  await waitFor(()=>document.documentElement.getAttribute(spec.attribute)===scale,'persisted scale '+scale);
  document.getAnimations().forEach(animation=>animation.finish());await frame();
  const rect=selector=>document.querySelector(selector).getBoundingClientRect().toJSON();
  const geometry={innerWidth,innerHeight,composer:rect(spec.composer),send:rect(spec.send),workspace:document.querySelector('.workspace')?.getBoundingClientRect().toJSON(),welcome:document.querySelector('.welcome-intro')?.getBoundingClientRect().toJSON(),zoom:Number.parseFloat(document.documentElement.style.zoom||'1')};
  const prefix=spec.name+' '+innerWidth+'x'+innerHeight+' '+scale,tolerance=2;
  check(geometry.composer.left>=-tolerance && geometry.composer.right<=innerWidth+tolerance && geometry.composer.bottom<=innerHeight+tolerance && geometry.send.left>=-tolerance && geometry.send.right<=innerWidth+tolerance && geometry.send.bottom<=innerHeight+tolerance,prefix+' composer and send fit viewport',geometry);
  if(spec.centeredEmptyState){
    const groupCenterY=(geometry.welcome.top+geometry.composer.bottom)/2,workspaceCenterY=(geometry.workspace.top+geometry.workspace.bottom)/2,composerCenterX=(geometry.composer.left+geometry.composer.right)/2,workspaceCenterX=(geometry.workspace.left+geometry.workspace.right)/2,centerTolerance=Math.max(4,geometry.zoom*3);
    check(Math.abs(groupCenterY-workspaceCenterY)<=centerTolerance && Math.abs(composerCenterX-workspaceCenterX)<=centerTolerance,prefix+' centered empty state',{groupCenterY,workspaceCenterY,composerCenterX,workspaceCenterX,geometry});
  }
  const metrics={geometry};
  const dialogTolerance=8;
  const checkDialog=(value,name)=>check(value.dialog.right<=innerWidth+dialogTolerance && value.dialog.bottom<=innerHeight+dialogTolerance && value.control.right<=innerWidth+tolerance && value.control.bottom<=innerHeight+tolerance,prefix+' '+name,value);
  if(spec.settingsDialog){
    window.openNestCafeSettingsPage('general');
    equal(document.querySelectorAll('[aria-label="Ostrzegaj przy aktywnym zadaniu"]').length,0,prefix+' redundant close-confirmation setting absent');
    await waitFor(()=>document.querySelector('input[name="backup-scope"][value="full"]'),'full backup control');
    const full=document.querySelector('input[name="backup-scope"][value="full"]'),safe=document.querySelector('input[name="backup-scope"][value="safe"]');safe.click();
    await waitFor(()=>document.querySelector('[data-backup-scope="safe"][data-export-path="/api/data/export"]'),'safe backup scope');equal(document.querySelectorAll('.settings-backup-scope .excluded').length,1,prefix+' safe backup excludes credentials');
    full.click();await waitFor(()=>document.querySelector('[data-backup-scope="full"][data-export-path="/api/data/export/full"]'),'full backup scope');equal(document.querySelectorAll('.settings-backup-scope .excluded').length,0,prefix+' full backup includes all categories');
    const selector='select[aria-label="Skala interfejsu"]';await waitFor(()=>visible(document.querySelector(selector)),'scale control visible');document.querySelector(selector).scrollIntoView({block:'nearest'});await frame();
    metrics.settingsGeometry={innerWidth,innerHeight,dialog:rect(spec.settingsDialog),control:rect(selector)};checkDialog(metrics.settingsGeometry,'settings geometry');document.querySelector(spec.settingsDialog).close();
    window.openNestCafeSettingsPage('folders');
    const exact=text=>Array.from(document.querySelectorAll('body *')).some(node=>node.textContent.trim()===text && visible(node));
    await waitFor(()=>exact('Analiza dokumentów przez AI'),'document AI heading');await waitFor(()=>exact('Poczta Outlook'),'Outlook heading');
    document.querySelector('.folder-outlook-name').scrollIntoView({block:'nearest'});await frame();metrics.sourceGeometry={innerWidth,innerHeight,dialog:rect(spec.settingsDialog),control:rect('.folder-outlook-name')};checkDialog(metrics.sourceGeometry,'document sources geometry');document.querySelector(spec.settingsDialog).close();
  }
  if(spec.modelDialog){
    document.body.classList.remove('empty-state');document.querySelector('#model-button').click();
    await waitFor(()=>visible(document.querySelector('#model-list .model-row')),'model menu rows');document.querySelector('#model-list .model-row:last-child').scrollIntoView({block:'nearest'});await frame();
    const modelGeometry={innerWidth,innerHeight,dialog:rect(spec.modelDialog),list:rect('#model-list'),last:rect('#model-list .model-row:last-child')};
    check(modelGeometry.dialog.left>=-tolerance && modelGeometry.dialog.right<=innerWidth+tolerance && modelGeometry.dialog.top>=-tolerance && modelGeometry.dialog.bottom<=innerHeight+tolerance && modelGeometry.last.top>=modelGeometry.list.top-tolerance && modelGeometry.last.bottom<=modelGeometry.list.bottom+tolerance,prefix+' model menu fits and last model reachable',modelGeometry);
    metrics.modelGeometry=modelGeometry;document.querySelector(spec.modelDialog).close();
  }
  window.__uiCheckMetrics[prefix]=metrics;
}
