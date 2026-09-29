// Render exported production Model.View ANSI as HTML for documentation capture.
// This is a rendering of the real view, not automation of a terminal window.
const fs = require('node:fs');
const path = require('node:path');
const input = path.resolve(process.argv[2] || '.tmp/tui-screenshots');
const output = path.resolve(process.argv[3] || '.tmp/tui-screenshots/html');
const scratch = path.resolve(__dirname, '../.tmp');
for (const target of [input,output]) {
 const rel = path.relative(scratch,target);
 if (rel.startsWith('..') || path.isAbsolute(rel)) throw new Error('Screenshot artifacts must remain in repository .tmp');
}
const escape = text => text.replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;');
const colors=['#101014','#d95a5a','#72ba72','#e0c870','#7aa2d7','#ba8dd7','#72bfc8','#e8e8ea','#68686f','#ef7676','#8bd48b','#f0d782','#91bafa','#cca1e8','#8dd2da','#ffffff'];
function indexed(n) {
 if (n < 16) return colors[n];
 if (n >= 232) { const x=8+(n-232)*10;return 'rgb('+x+','+x+','+x+')'; }
 n-=16;const parts=[Math.floor(n/36),Math.floor(n/6)%6,n%6].map(x=>x?55+x*40:0);return 'rgb('+parts.join(',')+')';
}
function htmlANSI(text) {
 let html='',last=0,fg='',bg='',bold=false,italic=false;
 const span=()=>'<span style="'+(fg?'color:'+fg+';':'')+(bg?'background:'+bg+';':'')+(bold?'font-weight:700;':'')+(italic?'font-style:italic;':'')+'">';
 html+=span();
 for(const match of text.matchAll(/\x1b\[([0-9;]*)m/g)) {
  html+=escape(text.slice(last,match.index))+'</span>';last=match.index+match[0].length;
  const values=(match[1]||'0').split(';').map(Number);
  for(let i=0;i<values.length;i++) {
   const n=values[i];
   if(n===0){fg='';bg='';bold=false;italic=false;}
   else if(n===1)bold=true;else if(n===22)bold=false;else if(n===3)italic=true;else if(n===23)italic=false;
   else if(n===39)fg='';else if(n===49)bg='';
   else if(n>=30&&n<=37)fg=colors[n-30];else if(n>=90&&n<=97)fg=colors[n-90+8];
   else if(n>=40&&n<=47)bg=colors[n-40];else if(n>=100&&n<=107)bg=colors[n-100+8];
   else if((n===38||n===48)&&values[i+1]===2){const color='rgb('+values.slice(i+2,i+5).join(',')+')';if(n===38)fg=color;else bg=color;i+=4;}
   else if((n===38||n===48)&&values[i+1]===5){const color=indexed(values[i+2]);if(n===38)fg=color;else bg=color;i+=2;}
  }
  html+=span();
 }
 html+=escape(text.slice(last))+'</span>';
 return html;
}
fs.mkdirSync(output,{recursive:true});
for(const name of fs.readdirSync(input).filter(name=>name.endsWith('.ansi'))) {
 const html='<!doctype html><html><head><meta charset="utf-8"><title>SuperCli TUI render</title><style>html,body{margin:0;background:#101014;color:#e8e8ea}pre{box-sizing:border-box;display:inline-block;margin:0;padding:22px;font:15px/1.35 Consolas,"Cascadia Mono",monospace;white-space:pre;font-variant-ligatures:none}</style></head><body><pre>'+htmlANSI(fs.readFileSync(path.join(input,name),'utf8'))+'</pre></body></html>';
 fs.writeFileSync(path.join(output,name.replace(/\.ansi$/,'.html')),html);
}
console.log('Rendered '+output);
