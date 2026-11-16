package webgui

import (
	"encoding/json"
	"fmt"
	"testing"
)

// audit-130: the GUI host approval card (05e-approval-card.js) inside the
// complete app with its real CSS, at desktop and phone widths. Answers are
// captured by replacing jpost in the page, so nothing reaches a model.

const audit130ApprovalTarget = `C:\Users\demo\outside\a_very_long_folder_name_without_spaces_that_should_wrap\old_notes.txt`

func audit130ApprovalRequest(id string, similar bool) string {
	approval := map[string]any{
		"tool": "trash", "operation": "trash_outside_workspace", "risk": "write_reversible", "reversible": true,
		"target": audit130ApprovalTarget, "summary": "Remove it?",
		"summary_key": "approval.summary.trash_outside_workspace", "summary_args": map[string]string{"path": audit130ApprovalTarget},
		"approve_label": "Zatwierdź", "deny_label": "Odrzuć",
	}
	options := []map[string]string{{"label": "Odrzuć"}, {"label": "Zatwierdź"}}
	if similar {
		approval["similar_label"], approval["grant_minutes"] = "Zatwierdź podobne", 30
		options = append(options, map[string]string{"label": "Zatwierdź podobne"})
	}
	b, _ := json.Marshal(map[string]any{"id": id, "question": "q", "confirmation": true, "options": options, "approval": approval})
	return string(b)
}

// audit130ShowCard installs a capturing jpost and opens one card.
func audit130ShowCard(app *audit130App, id string, similar bool) {
	app.t.Helper()
	app.eval(`(async()=>{window.__posts=window.__posts||[];window.jpost=function(u,b){__posts.push({url:u,body:JSON.parse(JSON.stringify(b))});return Promise.resolve({ok:true});};
 showApprovalCard(`+audit130ApprovalRequest(id, similar)+`);await new Promise(r=>setTimeout(r,120));return true;})()`, nil)
}

func TestAudit130ApprovalCardKeysAndLayout(t *testing.T) {
	app := audit130StartApp(t, 1280, 900)
	app.setLanguage("pl")
	var failures []string
	for _, width := range []int{1280, 420, 360} {
		app.viewport(width, 800)
		app.settle()
		for _, c := range []struct {
			key, want string
			similar   bool
		}{
			{"y", "approve", true}, {"Y", "approve", true}, {"n", "deny", true}, {"N", "deny", true},
			{"Escape", "deny", true}, {"a", "similar", true}, {"A", "similar", true},
			{"a", "none", false}, // without a session grant "a" does nothing
		} {
			id := fmt.Sprintf("audit-%d-%s-%v", width, c.key, c.similar)
			audit130ShowCard(app, id, c.similar)
			var got struct {
				Failures []string `json:"failures"`
			}
			app.eval(fmt.Sprintf(audit130ApprovalProbe, jsString(id), jsString(c.key), jsString(c.want), jsString(audit130ApprovalTarget)), &got)
			for _, f := range got.Failures {
				failures = append(failures, fmt.Sprintf("@%dpx key %q: %s", width, c.key, f))
			}
		}
		app.shot(fmt.Sprintf("approval_card_answered_%d", width))
	}
	if len(failures) > 0 {
		t.Fatalf("approval card problems:\n  %s", audit130Join(failures))
	}
}

// %[1]s id, %[2]s key, %[3]s expected answer, %[4]s target text.
const audit130ApprovalProbe = `(async()=>{
 const fail=[],sleep=ms=>new Promise(r=>setTimeout(r,ms));
 const panels=[...document.querySelectorAll('.approval-card')];const card=panels[panels.length-1];
 if(!card){return {failures:['card not rendered']};}
 const deny=card.querySelector('.approval-deny');
 if(document.activeElement!==deny)fail.push('deny is not focused first (focus: '+(document.activeElement&&document.activeElement.className)+')');
 const cr=card.getBoundingClientRect();
 if(cr.left<-0.5||cr.right>innerWidth+0.5)fail.push('card outside the window horizontally: '+cr.left+'..'+cr.right);
 const btns=[...card.querySelectorAll('.approval-actions button')];
 const boxes=btns.map(b=>b.getBoundingClientRect());
 const hit=(a,b)=>a.left<b.right-0.5&&b.left<a.right-0.5&&a.top<b.bottom-0.5&&b.top<a.bottom-0.5;
 btns.forEach((b,i)=>{
   if(boxes[i].left<cr.left-0.5||boxes[i].right>cr.right+0.5)fail.push('"'+b.textContent+'" sticks out of the card');
   if(b.scrollWidth>b.clientWidth+1)fail.push('"'+b.textContent+'" label clipped');
   if(boxes[i].height<24)fail.push('"'+b.textContent+'" too small: '+boxes[i].height+'px');
   for(let j=i+1;j<btns.length;j++)if(hit(boxes[i],boxes[j]))fail.push('"'+b.textContent+'" overlaps "'+btns[j].textContent+'"');
 });
 for(const n of card.querySelectorAll('dd,p,pre,h2')){if(n.scrollWidth>n.clientWidth+1)fail.push(n.tagName+' text overflows its box ('+n.scrollWidth+'>'+n.clientWidth+')');}
 if(!card.textContent.includes(%[4]s))fail.push('target path not shown verbatim');
 if(document.scrollingElement.scrollWidth>innerWidth+0.5)fail.push('page scrolls horizontally: '+document.scrollingElement.scrollWidth);
 const before=__posts.length;
 const ev=new KeyboardEvent('keydown',{key:%[2]s,bubbles:true,cancelable:true});
 document.activeElement.dispatchEvent(ev);
 // A second, repeated press must not answer twice.
 document.activeElement.dispatchEvent(new KeyboardEvent('keydown',{key:%[2]s,bubbles:true,cancelable:true}));
 await sleep(60);
 const posts=__posts.slice(before).filter(p=>p.body.id===%[1]s);
 const want=%[3]s;
 if(want==='none'){
   if(posts.length)fail.push('answered without a session-grant option: '+JSON.stringify(posts));
   if(!card.querySelector('.approval-actions'))fail.push('card closed although nothing was chosen');
   // Close it with Escape so the next case starts clean.
   card.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));await sleep(60);
   return {failures:fail};
 }
 if(posts.length!==1)fail.push('expected exactly one answer, got '+posts.length);
 const b=posts[0]&&posts[0].body;
 if(b){
   if(want==='deny'&&!(b.cancelled===true&&b.selected.length===0))fail.push('deny posted '+JSON.stringify(b));
   if(want==='approve'&&!(b.cancelled===false&&b.selected[0]==='Zatwierdź'))fail.push('approve posted '+JSON.stringify(b));
   if(want==='similar'&&!(b.cancelled===false&&b.selected[0]==='Zatwierdź podobne'))fail.push('similar posted '+JSON.stringify(b));
 }
 if(card.querySelector('.approval-actions'))fail.push('buttons still shown after answering');
 const receipt=card.querySelector('.approval-receipt');
 if(!receipt||!receipt.textContent.trim())fail.push('no receipt after answering');
 return {failures:fail};
})()`

// After the card is answered its buttons are removed. Keyboard focus should
// return to the composer so the user can keep typing; it must not be lost on
// <body> (keyboard users then have to Tab through the whole header again).
func TestAudit130ApprovalCardFocusReturnsToComposer(t *testing.T) {
	audit130KnownFailing(t, "GUI approval card drops keyboard focus to <body> after an answer instead of returning it to the composer")
	app := audit130StartApp(t, 1280, 900)
	app.setLanguage("pl")
	failures := []string{}
	for _, key := range []string{"y", "n", "Escape", "a"} {
		id := "audit-focus-" + key
		audit130ShowCard(app, id, true)
		var got struct {
			Focus string `json:"focus"`
		}
		app.eval(`(async()=>{document.activeElement.dispatchEvent(new KeyboardEvent('keydown',{key:`+jsString(key)+`,bubbles:true}));await new Promise(r=>setTimeout(r,120));const a=document.activeElement;return {focus:a?(a.tagName+'#'+a.id+'.'+a.className):'null'};})()`, &got)
		if got.Focus != "TEXTAREA#prompt." {
			failures = append(failures, fmt.Sprintf("after %q focus is on %s, want the composer textarea#prompt", key, got.Focus))
		}
	}
	if len(failures) > 0 {
		t.Fatalf("%s", audit130Join(failures))
	}
}

// On a phone-sized window the card opens scrolled to its bottom (smartScroll),
// so its title (what is being approved) can be hidden under the header.
func TestAudit130ApprovalCardTitleVisibleWhenOpenedOnPhone(t *testing.T) {
	audit130KnownFailing(t, "on a 360x800 window the approval card opens with its title scrolled under the top bar")
	app := audit130StartApp(t, 360, 800)
	app.setLanguage("pl")
	audit130ShowCard(app, "audit-title-360", true)
	app.shot("approval_card_open_360")
	var got struct {
		Title, Stage, Deny []float64
	}
	app.eval(`(()=>{const c=[...document.querySelectorAll('.approval-card')].pop();const r=e=>{const b=e.getBoundingClientRect();return [b.top,b.bottom];};
 return {title:r(c.querySelector('h2')),stage:r(document.querySelector('#stage')),deny:r(c.querySelector('.approval-deny'))};})()`, &got)
	t.Logf("title %v stage %v deny %v", got.Title, got.Stage, got.Deny)
	if got.Title[0] < got.Stage[0]-0.5 {
		t.Fatalf("approval title top %.1f is above the visible transcript top %.1f (hidden under the header); deny button at %v", got.Title[0], got.Stage[0], got.Deny)
	}
}
