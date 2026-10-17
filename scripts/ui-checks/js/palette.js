import {check,waitFor,visible,frame} from './common.js';
export async function run(){
  document.documentElement.style.colorScheme='dark';
  await waitFor(()=>document.querySelector('#model-btn'),'model button');document.querySelector('#model-btn').click();
  await waitFor(()=>Array.from(document.querySelectorAll('.prow')).some(n=>n.textContent.includes('gpt-5.6-sol')),'last model attached');
  const palette=document.querySelector('#palette'),list=document.querySelector('#model-list');
  const last=Array.from(document.querySelectorAll('.prow')).find(n=>n.textContent.includes('gpt-5.6-sol'));
  const dimensions={clientHeight:list.clientHeight,scrollHeight:list.scrollHeight};
  check(dimensions.clientHeight>0,'model list has available height',dimensions);check(dimensions.scrollHeight>dimensions.clientHeight,'long model list scrolls',dimensions);
  list.scrollTop=list.scrollHeight;await new Promise(resolve=>setTimeout(resolve,120));await frame();
  const paletteBox=palette.getBoundingClientRect(),listBox=list.getBoundingClientRect(),lastBox=last.getBoundingClientRect();
  check(paletteBox.bottom<=innerHeight+1,'palette fits small viewport',paletteBox.toJSON());check(lastBox.top>=listBox.top-1,'last model not above list');
  check(lastBox.bottom<=listBox.bottom+1,'last model not clipped under list');check(visible(last),'gpt-5.6-sol reachable after scrolling');
  window.__uiCheckMetrics.palette={...dimensions,palette:paletteBox.toJSON(),list:listBox.toJSON(),last:lastBox.toJSON(),viewport:{width:innerWidth,height:innerHeight}};
}
