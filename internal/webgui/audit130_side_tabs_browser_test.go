package webgui

import (
	"fmt"
	"testing"
)

// audit-130: the 1.0.29 fix "side panel tabs never overlap; the row scrolls
// with faded edges" (73575d7e) verified on the complete app in headless
// Chrome at real window widths, with the real boot sequence (compact/mobile
// classes, sidebar overlay) instead of an isolated CSS fixture.
func TestAudit130SideTabsRealLayoutAcrossWidthsAndLanguages(t *testing.T) {
	app := audit130StartApp(t, 1600, 900)
	var failures, scrolled []string
	for _, lang := range []string{"en", "pl", "de", "bg"} {
		app.setLanguage(lang)
		for _, width := range []int{1600, 1024, 800, 600, 420, 360} {
			app.viewport(width, 800)
			app.settle()
			var got struct {
				Failures []string `json:"failures"`
				Scrolls  bool     `json:"scrolls"`
			}
			app.eval(audit130SideTabsProbe, &got)
			if got.Scrolls {
				scrolled = append(scrolled, fmt.Sprintf("%s@%d", lang, width))
			}
			for _, f := range got.Failures {
				failures = append(failures, fmt.Sprintf("%s @%dpx: %s", lang, width, f))
			}
			if width == 360 && lang == "pl" {
				app.shot("side_tabs_pl_360")
			}
			if width == 360 && lang == "de" {
				app.shot("side_tabs_de_360")
			}
		}
	}
	t.Logf("tab rows that scroll: %v", scrolled)
	// The narrow German row must exercise the scrolling path, otherwise
	// the fade and scroll-into-view checks above proved nothing.
	if len(scrolled) == 0 {
		failures = append(failures, "no width/language combination made the tab row scroll")
	}
	if len(failures) > 0 {
		t.Fatalf("side tab layout problems:\n  %s", audit130Join(failures))
	}
}

const audit130SideTabsProbe = `(async()=>{
 const sleep=ms=>new Promise(r=>setTimeout(r,ms));
 const frame=()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
 const fail=[];
 ui.sidebarHidden=false;applyUI();await frame();await sleep(250);
 const sidebar=document.querySelector('#sidebar'),nav=document.querySelector('#side-tabs');
 if(getComputedStyle(sidebar).display==='none'||sidebar.getBoundingClientRect().width<1)fail.push('sidebar not shown after opening');
 const sb=sidebar.getBoundingClientRect(),nr=nav.getBoundingClientRect();
 if(nr.left<sb.left-0.5||nr.right>sb.right+0.5)fail.push('tab row overflows the panel: nav '+nr.left+'..'+nr.right+' panel '+sb.left+'..'+sb.right);
 if(sb.left<-0.5||sb.right>innerWidth+0.5)fail.push('panel outside the window: '+sb.left+'..'+sb.right+' / '+innerWidth);
 const tabs=[...nav.querySelectorAll('button')];
 const boxes=tabs.map(b=>b.getBoundingClientRect());
 const hit=(a,b)=>a.left<b.right-0.5&&b.left<a.right-0.5&&a.top<b.bottom-0.5&&b.top<a.bottom-0.5;
 for(let i=0;i<tabs.length;i++){
   if(tabs[i].scrollWidth>tabs[i].clientWidth+1)fail.push('"'+tabs[i].textContent+'" clipped inside its tab ('+tabs[i].scrollWidth+'>'+tabs[i].clientWidth+')');
   if(!tabs[i].title)fail.push('"'+tabs[i].textContent+'" has no tooltip');
   for(let j=i+1;j<tabs.length;j++)if(hit(boxes[i],boxes[j]))fail.push('"'+tabs[i].textContent+'" overlaps "'+tabs[j].textContent+'"');
 }
 const close=document.querySelector('#close-sidebar');
 if(getComputedStyle(close).display!=='none'){
   const cr=close.getBoundingClientRect();
   if(cr.right>innerWidth+0.5)fail.push('close button outside the window');
   if(nr.right>cr.left+0.5)fail.push('tab row runs under the close button: nav right '+nr.right+' close left '+cr.left);
 }
 const scrolls=nav.scrollWidth>nav.clientWidth+1;
 if(scrolls){
   const ox=getComputedStyle(nav).overflowX;
   if(ox!=='auto'&&ox!=='scroll')fail.push('overflowing row does not scroll (overflow-x '+ox+')');
   nav.scrollLeft=0;nav.dispatchEvent(new Event('scroll'));await frame();
   if(!nav.classList.contains('fade-r'))fail.push('no right fade while more tabs are hidden on the right');
   if(nav.classList.contains('fade-l'))fail.push('left fade at scrollLeft 0');
 }else if(nav.classList.contains('fade-r')||nav.classList.contains('fade-l')){
   fail.push('fade shown on a row that fits');
 }
 // The active tab is scrolled into view (last, then first again).
 for(const name of ['goal','stats']){
   const b=nav.querySelector('[data-tab="'+name+'"]');b.click();await frame();await sleep(350);
   const r=b.getBoundingClientRect(),n=nav.getBoundingClientRect();
   if(!b.classList.contains('active'))fail.push(name+' tab not active after click');
   if(r.left<n.left-1||r.right>n.right+1)fail.push('active "'+b.textContent+'" not scrolled into view: tab '+Math.round(r.left)+'..'+Math.round(r.right)+' row '+Math.round(n.left)+'..'+Math.round(n.right));
   if(b.getAttribute('aria-selected')!=='true')fail.push(name+' tab lacks aria-selected=true');
   if(scrolls&&name==='goal'&&!nav.classList.contains('fade-l'))fail.push('no left fade after scrolling to the last tab');
 }
 const se=document.scrollingElement;
 if(se.scrollWidth>innerWidth+0.5)fail.push('page scrolls horizontally: scrollWidth '+se.scrollWidth+' > '+innerWidth);
 return {failures:fail,scrolls};
})()`
