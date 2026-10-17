import {test, assert, createSandbox, runScript} from './harness.js';
import {serveFixtureLocale} from '../../scripts/ui-fixture-locales.js';
test('legacy browser fixtures bootstrap actual catalogs and all native language names',()=>{
 const response={headers:{},setHeader(k,v){this.headers[k]=v;},end(body){this.body=String(body);}};
 assert.equal(serveFixtureLocale(response,'/locales/en.js'),true);
 const context={};createSandbox(context);runScript(response.body,context);
 assert.equal(context.UI_LANGUAGES.length,27);assert.equal(context.UI_LANGUAGES.find(l=>l.code==='bg').name,'Български');
 assert.equal(context.I18N.en['common.save'],'Save');
 assert.equal(serveFixtureLocale(response,'/locales/pl.json'),true);assert.equal(JSON.parse(response.body)['common.save'],'Zapisz');
 assert.equal(serveFixtureLocale(response,'/locales/../../auth.json'),false);
});
