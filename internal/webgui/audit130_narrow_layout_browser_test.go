package webgui

import (
	"fmt"
	"testing"
)

// audit-130: narrow-window layout of the complete app (header, composer,
// control panel) in headless Chrome, English and Polish.

func TestAudit130NarrowWindowNoHorizontalOverflow(t *testing.T) {
	app := audit130StartApp(t, 1280, 900)
	var failures []string
	for _, lang := range []string{"en", "pl"} {
		app.setLanguage(lang)
		for _, width := range []int{1024, 600, 420, 360, 320} {
			app.viewport(width, 760)
			app.settle()
			var got struct {
				Failures []string `json:"failures"`
			}
			app.eval(audit130NarrowProbe, &got)
			for _, f := range got.Failures {
				failures = append(failures, fmt.Sprintf("%s @%dpx: %s", lang, width, f))
			}
			if lang == "pl" && width == 360 {
				app.shot("narrow_pl_360")
			}
		}
	}
	if len(failures) > 0 {
		t.Fatalf("narrow layout problems:\n  %s", audit130Join(failures))
	}
}

const audit130NarrowProbe = `(async()=>{
 const fail=[],sleep=ms=>new Promise(r=>setTimeout(r,ms));
 const shown=e=>{if(!e)return false;const s=getComputedStyle(e);const r=e.getBoundingClientRect();return s.display!=='none'&&s.visibility!=='hidden'&&r.width>0&&r.height>0;};
 const hit=(a,b)=>a.left<b.right-0.5&&b.left<a.right-0.5&&a.top<b.bottom-0.5&&b.top<a.bottom-0.5;
 const W=innerWidth;
 if(document.scrollingElement.scrollWidth>W+0.5)fail.push('page scrolls horizontally: '+document.scrollingElement.scrollWidth+' > '+W);
 const top=[...document.querySelectorAll('.topbar > *')].filter(shown);
 const tr=top.map(e=>e.getBoundingClientRect());
 top.forEach((e,i)=>{
   const name=e.id||e.className;
   if(tr[i].left<-0.5||tr[i].right>W+0.5)fail.push('header control '+name+' outside the window ('+Math.round(tr[i].left)+'..'+Math.round(tr[i].right)+')');
   for(let j=i+1;j<top.length;j++)if(hit(tr[i],tr[j]))fail.push('header '+name+' overlaps '+(top[j].id||top[j].className));
 });
 for(const id of ['#prompt','#send-btn','#attach-btn']){
   const e=document.querySelector(id);
   if(!shown(e)){fail.push(id+' not visible');continue;}
   const r=e.getBoundingClientRect();
   if(r.left<-0.5||r.right>W+0.5||r.bottom>innerHeight+0.5)fail.push(id+' outside the window ('+Math.round(r.left)+'..'+Math.round(r.right)+', bottom '+Math.round(r.bottom)+')');
 }
 const send=document.querySelector('#send-btn');
 if(send.scrollWidth>send.clientWidth+1)fail.push('send label clipped');
 // Control panel (settings) on the same window.
 openPanel('settings');await sleep(400);
 const panel=document.querySelector('#panel-overlay .panel');
 if(shown(panel)){
   const pr=panel.getBoundingClientRect();
   if(pr.left<-0.5||pr.right>W+0.5)fail.push('control panel outside the window ('+Math.round(pr.left)+'..'+Math.round(pr.right)+')');
   for(const n of panel.querySelectorAll('button,select,input,label')){
     if(!shown(n))continue;
     // Controls inside a horizontally scrolling strip (the mobile section
     // nav) may extend past the edge: they are reached by scrolling it.
     let scroller=false;for(let a=n.parentElement;a&&a!==panel.parentElement;a=a.parentElement){const ox=getComputedStyle(a).overflowX;if(ox==='auto'||ox==='scroll'||ox==='hidden'){const ar=a.getBoundingClientRect();if(ar.right<=W+0.5){scroller=true;break;}}}
     if(scroller)continue;
     const r=n.getBoundingClientRect();
     if(r.right>W+1){fail.push('panel control sticks out: '+(n.id||n.className||n.tagName)+' "'+n.textContent.trim().slice(0,30)+'" right '+Math.round(r.right));break;}
   }
   if(document.scrollingElement.scrollWidth>W+0.5)fail.push('page scrolls horizontally with the panel open');
 }else fail.push('control panel did not open');
 closePanel();await sleep(50);
 return {failures:fail};
})()`

// At phone widths app.css hides the reasoning level and chevron; the
// reasoning-effort button keeps its visible "think" label and the 44 px touch
// target phone.css asks for (it used to be an empty ~16 px box).
func TestAudit130NarrowReasoningButtonKeepsLabelAndTouchTarget(t *testing.T) {
	app := audit130StartApp(t, 420, 800)
	app.setLanguage("pl")
	var failures []string
	for _, width := range []int{560, 420, 360} {
		app.viewport(width, 800)
		app.settle()
		var got struct {
			Width   float64 `json:"width"`
			Visible string  `json:"visible"`
			Label   string  `json:"label"`
		}
		app.eval(`(()=>{const b=document.querySelector('#reasoning-btn');const r=b.getBoundingClientRect();
 const vis=[...b.querySelectorAll('*')].filter(e=>{const s=getComputedStyle(e);return s.display!=='none'&&s.visibility!=='hidden'&&e.getBoundingClientRect().width>0;});
 return {width:r.width,visible:vis.map(e=>e.textContent.trim()||e.tagName).join('|'),label:b.getAttribute('aria-label')||b.title||''};})()`, &got)
		t.Logf("@%dpx reasoning button width=%.1f visible content=%q accessible name=%q", width, got.Width, got.Visible, got.Label)
		if got.Width < 44 {
			failures = append(failures, fmt.Sprintf("@%dpx reasoning button is %.1fpx wide (< 44px touch target)", width, got.Width))
		}
		if got.Visible == "" {
			failures = append(failures, fmt.Sprintf("@%dpx reasoning button shows no label or icon", width))
		}
	}
	app.viewport(420, 800)
	app.settle()
	app.shot("reasoning_button_420")
	if len(failures) > 0 {
		t.Fatalf("%s", audit130Join(failures))
	}
}

// At <=560px the composer row is a 44px | 1fr | auto grid: attach, run
// status and Send share the first row (Send must not wrap onto a row of its
// own, which cost a phone ~48px of transcript height).
func TestAudit130NarrowComposerSendSharesRowWithAttach(t *testing.T) {
	app := audit130StartApp(t, 420, 800)
	app.setLanguage("pl")
	var failures []string
	for _, width := range []int{560, 420, 360} {
		app.viewport(width, 800)
		app.settle()
		var got struct{ Attach, Send, Status []float64 }
		app.eval(`(()=>{const r=s=>{const b=document.querySelector(s).getBoundingClientRect();return [b.top,b.bottom,b.left,b.right];};
 return {attach:r('#attach-btn'),send:r('#send-btn'),status:r('#run-status')};})()`, &got)
		t.Logf("@%dpx attach %v send %v status %v", width, got.Attach, got.Send, got.Status)
		if got.Send[0] >= got.Attach[1]-0.5 {
			failures = append(failures, fmt.Sprintf("@%dpx Send (top %.0f) sits below the attach row (bottom %.0f)", width, got.Send[0], got.Attach[1]))
		}
	}
	app.viewport(360, 800)
	app.settle()
	app.shot("composer_360")
	if len(failures) > 0 {
		t.Fatalf("%s", audit130Join(failures))
	}
}
