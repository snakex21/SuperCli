// Share the production English bootstrap and language registry with UI fixtures.
const fs=require('node:fs');const path=require('node:path');
const root=path.resolve(__dirname,'..');
const source=fs.readFileSync(path.join(root,'internal/system/uilang/language.go'),'utf8');
const languages=Array.from(source.matchAll(/\{"([\w-]+)", "([^"]+)", "(ltr|rtl)"\}/g),m=>({code:m[1],name:m[2],dir:m[3]}));
if(languages.length!==27)throw Error('Fixture language registry does not match production');
const catalogRoot=path.join(root,'internal/webgui/assets/locales');
const english=JSON.parse(fs.readFileSync(path.join(catalogRoot,'en.json'),'utf8'));
function serveFixtureLocale(res,pathname){
 if(pathname==='/locales/en.js'){
  res.setHeader('Content-Type','application/javascript; charset=utf-8');res.setHeader('Cache-Control','no-store');
  res.end('var I18N = {en:'+JSON.stringify(english)+'}; var UI_LANGUAGES = '+JSON.stringify(languages)+';');return true;
 }
 const match=pathname.match(/^\/locales\/([\w-]+)\.json$/);
 if(match&&languages.some(lang=>lang.code===match[1])){
  res.setHeader('Content-Type','application/json; charset=utf-8');res.end(fs.readFileSync(path.join(catalogRoot,match[1]+'.json')));return true;
 }
 return false;
}
module.exports={serveFixtureLocale};
