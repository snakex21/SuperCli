package webgui

import (
	"fmt"
	"testing"
)

// audit-130: switching the GUI language (pl <-> en) through the real
// Appearance select must leave no visible text, tooltip, placeholder or
// aria-label in the previous language, in the shell and in every control
// panel section that is safe to open offline. Raw catalog keys must never
// be visible either.
func TestAudit130LanguageSwitchLeavesNoStaleStrings(t *testing.T) {
	app := audit130StartApp(t, 1400, 900)
	app.setLanguage("en")
	var failures []string
	for _, step := range []struct{ from, to string }{{"en", "pl"}, {"pl", "en"}} {
		var got struct {
			Failures []string `json:"failures"`
			Checked  int      `json:"checked"`
		}
		app.eval(fmt.Sprintf(audit130LanguageProbe, jsString(step.from), jsString(step.to)), &got)
		t.Logf("%s -> %s: checked %d visible strings", step.from, step.to, got.Checked)
		for _, f := range got.Failures {
			failures = append(failures, step.from+"->"+step.to+": "+f)
		}
	}
	if len(failures) > 0 {
		t.Fatalf("stale or raw strings after a language switch:\n  %s", audit130Join(failures))
	}
}

// %[1]s previous language, %[2]s new language.
const audit130LanguageProbe = `(async()=>{
 const sleep=ms=>new Promise(r=>setTimeout(r,ms));
 const from=%[1]s,to=%[2]s,fail=[];let checked=0;
 await loadLanguage(from);await loadLanguage(to);
 const old=I18N[from]||I18N.en,cur=I18N[to]||I18N.en;
 const stale=new Map();for(const k in old){const o=(old[k]||'').trim();if(o.length>3&&cur[k]&&cur[k].trim()!==o&&!/[{#]/.test(o))stale.set(o,k);}
 // Values shared by both catalogs are not stale.
 for(const k in cur)stale.delete((cur[k]||'').trim());
 const keys=new Set(Object.keys(I18N.en));
 const shown=e=>{if(!e)return false;const s=getComputedStyle(e);return s.display!=='none'&&s.visibility!=='hidden'&&e.getClientRects().length>0;};
 function scan(root,where){
   const w=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);let n;
   while(n=w.nextNode()){const s=n.nodeValue.trim(),p=n.parentElement;if(!s||!p||p.closest('script,style,.skill-card,.skills-list,pre,code')||!shown(p))continue;checked++;
     if(stale.has(s))fail.push(where+': text "'+s+'" is still '+from+' ('+stale.get(s)+')');
     if(keys.has(s))fail.push(where+': raw catalog key "'+s+'"');}
   for(const e of root.querySelectorAll('[title],[aria-label],[placeholder]')){if(!shown(e))continue;
     for(const a of ['title','aria-label','placeholder']){const v=(e.getAttribute(a)||'').trim();if(!v)continue;checked++;
       if(stale.has(v))fail.push(where+': '+a+' "'+v+'" on '+(e.id||e.className||e.tagName)+' is still '+from+' ('+stale.get(v)+')');
       if(keys.has(v))fail.push(where+': raw key in '+a+' "'+v+'"');}}
 }
 // Switch through the real Appearance select, as a user would.
 openPanel('appearance');await sleep(200);
 const sel=[...document.querySelectorAll('#panel-content select')].find(s=>[...s.options].some(o=>o.value==='pl')&&[...s.options].some(o=>o.value==='en'));
 if(!sel)return {failures:['language select not found'],checked};
 sel.value=to;sel.dispatchEvent(new Event('change',{bubbles:true}));
 for(let i=0;i<60&&document.documentElement.lang!==to;i++)await sleep(50);
 await sleep(300);
 if(document.documentElement.lang!==to)fail.push('html lang stayed '+document.documentElement.lang);
 scan(document.querySelector('#panel-overlay'),'appearance');
 // Skills/Accounts/Usage/Runtime/Thunderbird are left out: they show
 // third-party text or probe local services and accounts.
 for(const sec of ['settings','modes','models','providers','workflow','mcp','data','memory','goal','files','about']){
   openPanel(sec);await sleep(500);scan(document.querySelector('#panel-overlay'),'panel '+sec);
 }
 closePanel();await sleep(50);
 ui.sidebarHidden=false;applyUI();await sleep(100);
 for(const tab of ['stats','sessions','projects','tasks','goal']){document.querySelector('#side-tabs [data-tab="'+tab+'"]').click();await sleep(200);scan(document.querySelector('#sidebar'),'side '+tab);}
 document.querySelector('#side-tabs [data-tab="stats"]').click();
 scan(document.querySelector('.topbar'),'header');
 scan(document.querySelector('.composer-wrap')||document.querySelector('#prompt').closest('form')||document.body,'composer');
 scan(document.querySelector('#stage'),'transcript');
 return {failures:[...new Set(fail)].slice(0,60),checked};
})()`
