import {assert, test, readSource, readDir, sourcePath, testDir, createSandbox, runScript} from './harness.js';
const assets = sourcePath(testDir, '../../internal/webgui/assets');
const chat = readSource(sourcePath(assets, 'js/05-chat.js'), 'utf8');
const catalog = JSON.parse(readSource(sourcePath(assets, 'locales/en.json'), 'utf8'));
function harness() {
  const windowEvents = new Map(), created = [];
  const document = {activeElement: null};
  function node(tag, cls = '', text = '') {
    const classes = new Set(cls.split(' '));
    const n = {tag, className: cls, textContent: text, children: [], events: {}, attrs: {}, style: {}, hidden: false, disabled: false,
      isConnected: true, naturalWidth: 2000, naturalHeight: 1200,
      classList: {add(k) {classes.add(k);}, remove(k) {classes.delete(k);}, toggle(k,v) {v ? classes.add(k) : classes.delete(k);}, contains(k) {return classes.has(k);}},
      appendChild(c) {this.children.push(c);c.parentNode=this;return c;}, setAttribute(k,v) {this.attrs[k]=v;},
      removeAttribute(k) {delete this[k];}, addEventListener(k,fn) {this.events[k]=fn;},
      focus() {document.activeElement=this;}, captures: new Set(),
      setPointerCapture(id) {this.captures.add(id);}, hasPointerCapture(id) {return this.captures.has(id);}, releasePointerCapture(id) {this.captures.delete(id);},
      getBoundingClientRect() {return {left:0,top:0,width:1000,height:600};},
      showModal() {this.open=true;}, close() {this.open=false;if(this.events.close)this.events.close({});},
      remove() {this.isConnected=false;if(this.parentNode)this.parentNode.children=this.parentNode.children.filter(n=>n!==this);},
      set innerHTML(v) {this.children=[];}, get innerHTML() {return '';},
    };created.push(n);return n;
  }
  document.body=node('body');document.createElement=node;
  const w={document,window:{addEventListener(k,fn){windowEvents.set(k,fn);},removeEventListener(k){windowEvents.delete(k);}},
    el:node,t:k=>{assert.ok(catalog[k], 'translation '+k);return catalog[k];},pendingAttachments:[],toast(){}};
  createSandbox(w);
  runScript(chat.slice(chat.indexOf('function attachmentName'),chat.indexOf('function attachmentTranscriptText')),w);
  runScript(chat.slice(chat.indexOf('function renderSentAttachments'),chat.indexOf('function addAttachmentPaths')),w);
  runScript(chat.slice(chat.indexOf('function renderAttachments'),chat.indexOf('function clearAttachments')),w);
  runScript(readSource(sourcePath(assets,'js/05a-lightbox.js'),'utf8'),w);
  const find = cls => created.filter(n=>n.className===cls).at(-1);
  const fire = (n,key,props={}) => {const event={type:key,target:n,defaultPrevented:false,preventDefault(){this.defaultPrevented=true;},stopPropagation(){},...props};n.events[key](event);return event;};
  // Drag moves/releases are tracked on window (capture phase), not on the stage.
  const win = (key,props={}) => {const event={type:key,defaultPrevented:false,preventDefault(){this.defaultPrevented=true;},...props};const fn=windowEvents.get(key);if(fn)fn(event);return event;};
  const button = key => find('image-lightbox-toolbar').children.find(n=>n.attrs['aria-label']===catalog[key]);
  function open(paths=['a.png','b.jpg','c.webp'],selected=paths[0]) {
    const trigger=node('button');trigger.focus();w.openImageLightbox(paths,selected,trigger);
    const dialog=find('image-lightbox'),stage=find('image-lightbox-stage'),image=stage.children[0];fire(image,'load');
    return {trigger,dialog,stage,image};
  }
  return {w,node,find,fire,win,button,open,document,windowEvents};
}
test('lightbox opens a native accessible modal, shows filename/counter, traps focus and Esc returns to thumbnail',()=>{
  const h=harness();const v=h.open(['a.png','b.jpg'],'b.jpg');
  assert.equal(v.dialog.open,true);assert.equal(v.dialog.attrs.role,'dialog');assert.equal(v.dialog.attrs['aria-modal'],'true');
  assert.equal(h.find('image-lightbox-title').textContent,'b.jpg');assert.equal(h.find('image-lightbox-counter').textContent,'2/2');
  assert.equal(h.document.activeElement,h.button('common.close'));
  h.fire(v.dialog,'keydown',{key:'Tab'});assert.equal(h.document.activeElement,h.button('attachment.previousImage'));
  h.fire(v.dialog,'keydown',{key:'Tab',shiftKey:true});assert.equal(h.document.activeElement,h.button('common.close'));
  h.fire(v.dialog,'keydown',{key:'Escape'});
  assert.equal(h.w.imageLightbox,null);assert.equal(h.document.activeElement,v.trigger);assert.equal(v.dialog.isConnected,false);
  assert.equal(h.windowEvents.size,0);assert.equal(v.image.src,undefined);
});
test('prev/next buttons and arrows navigate only this group and reset zoom/pan',()=>{
  const h=harness();const v=h.open();
  h.fire(h.button('attachment.zoomIn'),'click');assert.ok(h.w.imageLightbox.scale>.5);
  h.fire(h.button('attachment.nextImage'),'click');assert.equal(h.w.imageLightbox.index,1);assert.equal(h.w.imageLightbox.ready,false);
  assert.equal(h.find('image-lightbox-counter').textContent,'2/3');assert.match(v.image.src,/b.jpg/);
  h.fire(v.image,'load');assert.equal(h.w.imageLightbox.scale,.5);assert.equal(h.w.imageLightbox.x,0);
  h.fire(v.dialog,'keydown',{key:'ArrowRight'});assert.equal(h.w.imageLightbox.index,2);
  h.fire(v.dialog,'keydown',{key:'ArrowLeft'});assert.equal(h.w.imageLightbox.index,1);
  h.fire(h.button('attachment.previousImage'),'click');assert.equal(h.w.imageLightbox.index,0);
  h.w.imageLightbox.close();
});
test('wheel zoom anchors the cursor; +/-/fit, double-click 100% and resize update zoom state',()=>{
  const h=harness();const v=h.open();const s=h.w.imageLightbox;
  assert.equal(s.scale,.5);assert.equal(s.fit,.5);
  h.fire(v.stage,'wheel',{deltaY:-Math.log(2)/.002,clientX:600,clientY:300});
  assert.ok(Math.abs(s.scale-1)<1e-9);assert.ok(Math.abs(s.x+100)<1e-9);
  h.fire(h.button('attachment.zoomOut'),'click');assert.equal(s.scale,.8);
  h.fire(h.button('attachment.fitImage'),'click');assert.equal(s.scale,.5);assert.equal(s.x,0);
  h.fire(v.stage,'dblclick');assert.equal(s.scale,1);h.fire(v.stage,'dblclick');assert.equal(s.scale,.5);
  h.fire(h.button('attachment.zoomIn'),'click');assert.equal(s.scale,.625);
  h.windowEvents.get('resize')();assert.equal(s.scale,.5);
  h.w.imageLightbox.close();
});
test('mouse pan is bounded, touch pinch zooms and one-finger drag pans without accidental backdrop close',()=>{
  const h=harness();const v=h.open();const s=h.w.imageLightbox;
  h.fire(v.stage,'dblclick');
  h.fire(v.stage,'pointerdown',{pointerId:1,clientX:500,clientY:300,target:v.image,button:0});
  h.win('pointermove',{pointerId:1,clientX:650,clientY:350});assert.equal(s.x,150);assert.equal(s.y,50);
  h.win('pointerup',{pointerId:1});h.fire(v.stage,'click');assert.equal(h.w.imageLightbox,s);
  h.fire(h.button('attachment.fitImage'),'click');
  h.fire(v.stage,'pointerdown',{pointerId:1,pointerType:'touch',clientX:400,clientY:300,target:v.image});
  h.fire(v.stage,'pointerdown',{pointerId:2,pointerType:'touch',clientX:600,clientY:300,target:v.image});
  h.win('pointermove',{pointerId:2,clientX:800,clientY:300});assert.equal(s.scale,1);
  h.win('pointerup',{pointerId:2});
  h.win('pointermove',{pointerId:1,clientX:450,clientY:300});assert.ok(s.x>0);
  h.win('pointermove',{pointerId:1,clientX:5000,clientY:300});assert.equal(s.x,500);
  h.win('pointercancel',{pointerId:1});assert.equal(v.stage.classList.contains('interacting'),false);
  h.w.imageLightbox.close();
});
test('backdrop, close button and native cancel all close; image click does not; a new viewer retires the old one',()=>{
  const h=harness();let v=h.open(['a.png']);
  assert.equal(h.button('attachment.previousImage').disabled,true);
  h.fire(v.stage,'pointerdown',{pointerId:1,target:v.image,clientX:500,clientY:300});h.win('pointerup',{pointerId:1});h.fire(v.stage,'click');
  assert.ok(h.w.imageLightbox,'captured image click is not a backdrop click');
  h.fire(v.stage,'pointerdown',{pointerId:1,clientX:0,clientY:0});h.win('pointerup',{pointerId:1});h.fire(v.stage,'click');assert.equal(h.w.imageLightbox,null);
  v=h.open();h.fire(h.button('common.close'),'click');assert.equal(h.document.activeElement,v.trigger);
  v=h.open();h.fire(v.dialog,'cancel');assert.equal(h.w.imageLightbox,null);
  const old=h.open();v=h.open();assert.equal(old.dialog.isConnected,false);assert.equal(v.dialog.open,true);
  h.fire(v.dialog,'click');assert.equal(h.w.imageLightbox,null);
});
test('image load failure is localized and navigation remains usable',()=>{
  const h=harness();const v=h.open();h.fire(h.button('attachment.nextImage'),'click');h.fire(v.image,'error');
  assert.equal(h.find('image-lightbox-error').hidden,false);assert.equal(h.find('image-lightbox-error').textContent,catalog['attachment.imageLoadFailed']);
  h.fire(h.button('attachment.previousImage'),'click');assert.equal(h.find('image-lightbox-error').hidden,true);
  h.w.imageLightbox.close();
});
test('composer, queued rows and transcript thumbnails pass image-only groups and their focus trigger',()=>{
  const h=harness();const opens=[];h.w.openImageLightbox=(...args)=>opens.push(args);h.w.openAttachmentPreview=(...args)=>opens.push(['file',...args]);
  const paths=['a.png','notes.pdf','b.jpg'];const composer=h.node('div');h.w.renderAttachments(composer,paths,()=>{});
  const first=composer.children[0].children[0];h.fire(first,'click');assert.deepEqual(Array.from(opens[0][0]),['a.png','b.jpg']);assert.equal(opens[0][2],first);
  h.fire(composer.children[1].children[0],'click');assert.deepEqual(opens[1],['file','notes.pdf']);
  const queue=h.node('div');h.w.appendQueuedImageThumbnails(queue,{attachments:paths});assert.equal(queue.children[0].children.length,2);
  const second=queue.children[0].children[1];h.fire(second,'click');assert.equal(opens[2][1],'b.jpg');assert.equal(opens[2][2],second);
  const message=h.node('div');h.w.renderSentAttachments(message,paths);
  const gallery=message.children.find(n=>n.className.startsWith('sent-attachment-gallery'));h.fire(gallery.children[1],'click');
  assert.deepEqual(Array.from(opens[3][0]),['a.png','b.jpg']);assert.equal(opens[3][1],'b.jpg');assert.equal(opens[3][2],gallery.children[1]);
});
test('rotation button and r/R rotate clockwise; fit, pan bounds and anchored zoom use rotated dimensions',()=>{
  const h=harness();const v=h.open();const s=h.w.imageLightbox;
  assert.equal(h.button('attachment.fitImage').textContent,'⤢');
  assert.equal(h.button('attachment.rotateImage').textContent,'↻');
  h.fire(h.button('attachment.rotateImage'),'click');
  assert.equal(s.rotation,90);assert.equal(s.fit,.3);assert.equal(s.scale,.3);
  assert.match(v.image.style.transform,/rotate\(90deg\)/);
  h.fire(v.stage,'dblclick');assert.equal(s.scale,1);
  h.fire(v.stage,'pointerdown',{pointerId:1,target:v.image,clientX:500,clientY:300});
  h.win('pointermove',{pointerId:1,clientX:5000,clientY:5000});
  assert.equal(s.x,100);assert.equal(s.y,700);
  h.win('pointerup',{pointerId:1});
  h.fire(h.button('attachment.fitImage'),'click');
  assert.equal(s.rotation,90);assert.equal(s.scale,.3);assert.equal(s.x,0);assert.equal(s.y,0);
  assert.match(h.button('attachment.fitImage').title,/30%/);
  h.fire(v.stage,'wheel',{deltaY:-Math.log(2)/.002,clientX:500,clientY:400});
  assert.ok(Math.abs(s.scale-.6)<1e-9);assert.ok(Math.abs(s.y+100)<1e-9);
  h.fire(v.dialog,'keydown',{key:'r'});assert.equal(s.rotation,180);assert.equal(s.fit,.5);assert.equal(s.scale,.6);
  h.fire(v.dialog,'keydown',{key:'R'});assert.equal(s.rotation,270);assert.equal(s.fit,.3);
  h.fire(h.button('attachment.rotateImage'),'click');assert.equal(s.rotation,0);assert.equal(s.fit,.5);
  h.fire(h.button('attachment.rotateImage'),'click');
  h.fire(h.button('attachment.nextImage'),'click');h.fire(v.image,'load');
  assert.equal(s.rotation,0);assert.equal(s.scale,.5);
  h.w.imageLightbox.close();
});
test('stage prevents native selection/drag but preserves image double-click and backdrop click',()=>{
  const h=harness();const v=h.open();
  for(const type of ['mousedown','dragstart'])assert.equal(h.fire(v.stage,type).defaultPrevented,true);
  assert.equal(h.fire(v.dialog,'selectstart').defaultPrevented,true);
  assert.equal(h.fire(v.stage,'pointerdown',{pointerId:1,target:v.image,clientX:500,clientY:300}).defaultPrevented,true);
  assert.equal(v.stage.hasPointerCapture(1),true);assert.equal(h.document.activeElement,v.dialog);
  h.fire(v.dialog,'keydown',{key:'Tab',shiftKey:true});assert.equal(h.document.activeElement,h.button('common.close'));
  v.dialog.focus();h.fire(v.dialog,'keydown',{key:'Tab'});assert.equal(h.document.activeElement,h.button('attachment.previousImage'));
  h.win('pointerup',{pointerId:1});assert.equal(v.stage.hasPointerCapture(1),false);
  h.fire(v.stage,'click');assert.ok(h.w.imageLightbox);
  h.fire(v.stage,'dblclick');assert.equal(h.w.imageLightbox.scale,1);
  h.fire(v.stage,'pointerdown',{pointerId:2,clientX:0,clientY:0});h.win('pointerup',{pointerId:2});
  h.fire(v.stage,'click');assert.equal(h.w.imageLightbox,null);
});
test('slow drags and cancelled gestures never become backdrop closes and detach window listeners',()=>{
  for(const ending of ['pointerup','pointercancel','blur']){
    const h=harness();const v=h.open();h.fire(v.stage,'dblclick');
    h.fire(v.stage,'pointerdown',{pointerId:1,clientX:0,clientY:0});
    for(let x=1;x<=20;x++)h.win('pointermove',{pointerId:1,clientX:x,clientY:0});
    assert.equal(h.w.imageLightbox.x,20);
    h.win(ending,{pointerId:1});assert.equal(v.stage.classList.contains('interacting'),false);
    assert.equal(h.windowEvents.has('pointermove'),false);assert.equal(h.windowEvents.has('pointerup'),false);
    assert.equal(v.stage.hasPointerCapture(1),false);
    h.fire(v.stage,'click');assert.ok(h.w.imageLightbox,ending);
    const x=h.w.imageLightbox.x;h.win('pointermove',{pointerId:1,clientX:200,clientY:0});assert.equal(h.w.imageLightbox.x,x);
    h.w.imageLightbox.close();
  }
});
test('mouse drag pans on window events when pointer capture is lost early or refused',()=>{
  for(const failure of ['lost','throws']){
    const h=harness();const v=h.open();const s=h.w.imageLightbox;h.fire(v.stage,'dblclick');
    if(failure==='throws')v.stage.setPointerCapture=()=>{throw new Error('InvalidStateError');};
    h.fire(v.stage,'pointerdown',{pointerId:1,pointerType:'mouse',button:0,buttons:1,target:v.image,clientX:500,clientY:300});
    assert.equal(v.stage.classList.contains('interacting'),true,failure);
    if(failure==='lost'){v.stage.captures.delete(1);assert.equal(v.stage.events.lostpointercapture,undefined);}
    // The pointer has left the stage: only the window listener can see these moves.
    h.win('pointermove',{pointerId:1,pointerType:'mouse',buttons:1,clientX:560,clientY:330,target:h.document.body});
    h.win('pointermove',{pointerId:1,pointerType:'mouse',buttons:1,clientX:620,clientY:360,target:h.document.body});
    assert.equal(s.x,120,failure);assert.equal(s.y,60,failure);
    h.win('pointerup',{pointerId:1,pointerType:'mouse',buttons:0});h.fire(v.stage,'click');
    assert.equal(h.w.imageLightbox,s,'a drag is not a backdrop click');assert.equal(h.windowEvents.has('pointermove'),false);
    s.close();assert.equal(h.windowEvents.size,0);
  }
});
test('mouse moves with no buttons pressed end a drag released outside the window',()=>{
  const h=harness();const v=h.open();const s=h.w.imageLightbox;h.fire(v.stage,'dblclick');
  h.fire(v.stage,'pointerdown',{pointerId:1,pointerType:'mouse',button:0,buttons:1,target:v.image,clientX:500,clientY:300});
  h.win('pointermove',{pointerId:1,pointerType:'mouse',buttons:1,clientX:550,clientY:300});assert.equal(s.x,50);
  h.win('pointermove',{pointerId:1,pointerType:'mouse',buttons:0,clientX:700,clientY:300});
  assert.equal(s.x,50);assert.equal(v.stage.classList.contains('interacting'),false);assert.equal(h.windowEvents.has('pointermove'),false);
  s.close();
});
test('a zoomed image that still fits the stage can be dragged within the stage, both axes',()=>{
  const h=harness();const v=h.open();const s=h.w.imageLightbox;
  v.image.naturalWidth=400;v.image.naturalHeight=300;h.fire(v.image,'load');assert.equal(s.fit,1);
  h.fire(h.button('attachment.zoomIn'),'click');h.fire(h.button('attachment.zoomIn'),'click');assert.equal(s.scale,1.5625);
  assert.equal(v.stage.classList.contains('zoomed'),true);
  h.fire(v.stage,'pointerdown',{pointerId:1,pointerType:'mouse',button:0,buttons:1,target:v.image,clientX:500,clientY:300});
  h.win('pointermove',{pointerId:1,pointerType:'mouse',buttons:1,clientX:580,clientY:350});assert.equal(s.x,80);assert.equal(s.y,50);
  h.win('pointermove',{pointerId:1,pointerType:'mouse',buttons:1,clientX:5000,clientY:5000});
  assert.equal(s.x,(1000-400*1.5625)/2);assert.equal(s.y,(600-300*1.5625)/2);
  h.win('pointerup',{pointerId:1});h.fire(h.button('attachment.fitImage'),'click');
  h.fire(v.stage,'pointerdown',{pointerId:2,pointerType:'mouse',button:0,buttons:1,target:v.image,clientX:500,clientY:300});
  h.win('pointermove',{pointerId:2,pointerType:'mouse',buttons:1,clientX:600,clientY:400});assert.equal(s.x,0,'fitted image stays centred');assert.equal(s.y,0);
  s.close();
});
test('lightbox CSS disables selection throughout the modal and only shows keyboard focus and zoomed drag cursors',()=>{
  const css=readSource(sourcePath(assets,'app.css'),'utf8');
  assert.match(css,/\.image-lightbox \{[^}]*user-select: none/);
  assert.match(css,/\.image-lightbox-button:focus:not\(:focus-visible\) \{[^}]*outline: none/);
  assert.match(css,/\.image-lightbox-button:focus-visible[^}]*outline: 2px/);
  assert.match(css,/\.image-lightbox-stage\.zoomed \{ cursor: grab/);
  assert.match(css,/\.image-lightbox-stage\.zoomed\.interacting \{ cursor: grabbing/);
  assert.doesNotMatch(css.match(/\.image-lightbox-stage img \{[^}]*\}/)[0],/transition:/);
});
test('all 27 catalogs translate every lightbox control and failure message',()=>{
  const keys=['previousImage','nextImage','zoomIn','zoomOut','fitImage','rotateImage','imageLoadFailed'].map(k=>'attachment.'+k);
  const files=readDir(sourcePath(assets,'locales')).filter(f=>f.endsWith('.json'));assert.equal(files.length,27);
  for(const file of files){const values=JSON.parse(readSource(sourcePath(assets,'locales',file),'utf8'));for(const key of keys)assert.ok(values[key],file+': '+key);}
});
