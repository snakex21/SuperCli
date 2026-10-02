// The test uses echo and local fixtures; it never calls an external model.
setTimeout(async function () {
 const r={errors:trialErrors,checks:{}};
 try {
  await checkHealth();
  await sendPrompt('cześć',[],null,null);
  r.checks.echo=stream.textContent.includes('[echo:echo-test] cześć');
  r.checks.model=document.querySelector('#model-name').textContent==='echo-test';
  r.viewport={width:window.innerWidth,height:window.innerHeight};
  const start=Date.now();const events=[];
  const response=await fetch('/.__trial/stream');
  await superCliUI.readSSE(response.body,event=>events.push({event,ms:Date.now()-start}));
  r.stream=events;
  r.checks.streaming=events.length===2 && events[0].event.text==='Zażółć 🦊' && events[1].event.type==='done' && events[1].ms-events[0].ms>=250;
  const controller=new AbortController();
  const slow=await fetch('/.__trial/slow',{signal:controller.signal});
  const timer=setTimeout(()=>controller.abort(),80);
  try {await superCliUI.readSSE(slow.body,()=>{});r.checks.cancel=false}
  catch(e){r.checks.cancel=e.name==='AbortError'}
  finally {clearTimeout(timer)}
  await sections.providers();
  r.checks.providers=panelContent.querySelectorAll('button').length>0;const catalog=await j('/api/providers?lang=en');renderProviderChooser(catalog.templates);r.checks.providers=r.checks.providers && panelContent.querySelectorAll('.tpl-grid button').length>20;
  await sections.appearance();
  r.checks.appearance=panelContent.querySelectorAll('select').length>0;
  await loadLanguage('pl');ui.lang='pl';applyUI();r.checks.language=t('composer.send')==='Wyślij';
  ui.lang='en';applyUI();
  const session=activeSessionID;
  await resumeSession(session);await sessionRuntimeReady;r.checks.runtimeRestore=document.querySelector('#toast').textContent!==t('session.runtimeFailed');
  r.checks.history=stream.textContent.includes('cześć') && stream.querySelectorAll('.msg-user').length>0;
  let submissions=0;const submitted=()=>submissions++;
  document.querySelector('#composer').addEventListener('submit',submitted);
  promptEl.value='';document.querySelector('#composer').requestSubmit();if(typeof Window!=='undefined' && Window.this)document.querySelector('#send-btn').dispatchEvent(new Event('click',{bubbles:true,cancelable:true}));else document.querySelector('#send-btn').click();
  document.querySelector('#composer').removeEventListener('submit',submitted);
  r.submitEvents=submissions;r.checks.submit=submissions===2;
  const box=document.querySelector('#stage').getBoundingClientRect();
  r.checks.layout=box.width>window.innerWidth*.9 && box.height>300;
 } catch(e) {r.testError=String(e)+'\n'+(e.stack||'')}
 r.passed=!r.testError && !trialErrors.length && Object.values(r.checks).length===11 && Object.values(r.checks).every(Boolean);
 await fetch('/.__trial/report',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(r)});
 if(typeof Graphics!=='undefined') {
  await fetch('/.__trial/capture');
  const image=new Graphics.Image(1200,820,document.body);
  await fetch('/.__trial/snapshot',{method:'POST',body:image.toBytes('png')});
 }
},2500);
