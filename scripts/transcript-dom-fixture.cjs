'use strict';
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const {parseHTML} = require('linkedom');

// Optional development fixture, not a renderer dependency. There is no layout,
// paint, GPU, compositor or WebView2 here: never report these timings as FPS/RAM.
module.exports = function transcriptDOM(root, overrides = {}) {
  const {document, window:dom} = parseHTML('<html><body><div id="stage"><div id="stream"></div></div><div id="welcome"></div><button id="reload-sessions"></button></body></html>');
  Object.defineProperty(dom.HTMLElement.prototype, 'open', {configurable:true,get(){return this.hasAttribute('open')},set(value){this.toggleAttribute('open',!!value)}});
  const c = {document, window:{}, console, performance, AbortController, Promise, Event:dom.Event,
    requestAnimationFrame(){return 1},cancelAnimationFrame(){},setInterval(){return 1},clearInterval(){},
    setTimeout(){return 1},clearTimeout(){},t:key=>key, ui:{}, runToolCount:0,
  };
  vm.createContext(c);
  for (const name of ['shared-ui/runtime.js','assets/js/00-helpers.js','assets/js/03-markdown.js','assets/js/04-transcript.js','assets/js/08-sessions.js']) {
    const source = overrides[path.basename(name)] || fs.readFileSync(path.join(root,'internal/webgui',name),'utf8');
    vm.runInContext(source,c);
  }
  c.superCliUI=c.window.SuperCliUI;
  c.i18nEl=(tag,cls,text)=>c.el(tag,cls,text);
  const chat=fs.readFileSync(path.join(root,'internal/webgui/assets/js/05-chat.js'),'utf8');
  vm.runInContext(chat.slice(chat.indexOf('function handleEvent('),chat.indexOf('function closeQuestionOverlay(')),c);
  return c;
};
