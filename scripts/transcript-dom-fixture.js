'use strict';
import {readSource, sourcePath, createSandbox, runScript, parseHTML} from '../test/ui/harness.js';

// Isolated native DOM fixture, not a renderer dependency. Its deterministic
// scheduling is not a measurement of layout, paint, FPS or WebView2 memory.
export default function transcriptDOM(root = sourcePath('scripts', '..'), overrides = {}) {
  const {document, window:dom} = parseHTML('<html><body><div id="stage"><div id="stream"></div></div><div id="welcome"></div><button id="reload-sessions"></button></body></html>');
  Object.defineProperty(dom.HTMLElement.prototype, 'open', {configurable:true,get(){return this.hasAttribute('open')},set(value){this.toggleAttribute('open',!!value)}});
  const c = {document, window:{}, console, performance, AbortController, Promise, Event:dom.Event,
    requestAnimationFrame(){return 1},cancelAnimationFrame(){},setInterval(){return 1},clearInterval(){},
    setTimeout(){return 1},clearTimeout(){},t:key=>key, ui:{}, runToolCount:0,
  };
  createSandbox(c);
  for (const name of ['shared-ui/runtime.js','assets/js/00-helpers.js','assets/js/03-markdown.js','assets/js/04-transcript.js','assets/js/08-sessions.js']) {
    const source = overrides[name.slice(name.lastIndexOf('/') + 1)] || readSource(sourcePath(root,'internal/webgui',name),'utf8');
    runScript(source,c);
  }
  c.superCliUI=c.window.SuperCliUI;
  c.i18nEl=(tag,cls,text)=>c.el(tag,cls,text);
  const chat=readSource(sourcePath(root,'internal/webgui/assets/js/05-chat.js'),'utf8');
  runScript(chat.slice(chat.indexOf('function handleEvent('),chat.indexOf('function closeQuestionOverlay(')),c);
  return c;
};
