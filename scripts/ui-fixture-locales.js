// Share the production English bootstrap and language registry with UI fixtures.
import {readSource, sourcePath} from '../test/ui/harness.js';
const root=sourcePath('scripts','..');
const source=readSource(sourcePath(root,'internal/system/uilang/language.go'),'utf8');
const languages=Array.from(source.matchAll(/\{"([\w-]+)", "([^"]+)", "(ltr|rtl)"\}/g),m=>({code:m[1],name:m[2],dir:m[3]}));
if(languages.length!==27)throw Error('Fixture language registry does not match production');
const catalogRoot=sourcePath(root,'internal/webgui/assets/locales');
const english=JSON.parse(readSource(sourcePath(catalogRoot,'en.json'),'utf8'));
function serveFixtureLocale(res,pathname){
 if(pathname==='/locales/en.js'){
  res.setHeader('Content-Type','application/javascript; charset=utf-8');res.setHeader('Cache-Control','no-store');
  res.end('var I18N = {en:'+JSON.stringify(english)+'}; var UI_LANGUAGES = '+JSON.stringify(languages)+';');return true;
 }
 const match=pathname.match(/^\/locales\/([\w-]+)\.json$/);
 if(match&&languages.some(lang=>lang.code===match[1])){
  res.setHeader('Content-Type','application/json; charset=utf-8');res.end(readSource(sourcePath(catalogRoot,match[1]+'.json')));return true;
 }
 return false;
}
export {serveFixtureLocale};
