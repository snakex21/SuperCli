'use strict';
// Deterministic, model-free browser regression and benchmark. See
// docs/transcript-performance.md for setup and the separate WebView2 profile.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const http = require('node:http');
const {execFileSync} = require('node:child_process');
const {chromium} = require('playwright');
const {serveFixtureLocale} = require('./ui-fixture-locales.cjs');

(async () => {
  const root = path.resolve(__dirname, '..');
  const reference = process.env.TRANSCRIPT_BASE_REF || 'e83ca331b0a0892518a3083fad1fbe27aee21128';
  const baseline = Object.fromEntries(['03-markdown.js', '04-transcript.js'].map(name =>
    [name, execFileSync('git', ['show', reference + ':internal/webgui/assets/js/' + name], {cwd: root, encoding: 'utf8'})]));
  const assets = path.join(root, 'internal/webgui');
  const server = http.createServer(async (req, res) => {
    try {
      const pathname = new URL(req.url, 'http://localhost').pathname;
      if (serveFixtureLocale(res, pathname)) return;
      if (pathname.startsWith('/api/')) {
        res.setHeader('Content-Type', 'application/json');
        let data = {sessions: [], projects: [], providers: [], models: [], workers: [], tasks: [], settings: {'ui.lang':'en'}, ui:{lang:'en'}};
        if (pathname === '/api/sessions') data = [];
        if (pathname === '/api/models') data = {active:'', models:[]};
        if (pathname === '/api/transcript') data = {messages: [], has_more:false};
        res.end(JSON.stringify(data)); return;
      }
      const file = path.resolve(assets, pathname === '/' ? 'assets/index.html' :
        pathname.startsWith('/.__supercli/ui/') ? 'shared-ui/' + path.basename(pathname) : 'assets' + pathname);
      if (!file.startsWith(assets + path.sep)) throw Error('Not found');
      res.setHeader('Content-Type', pathname.endsWith('.css') ? 'text/css' : pathname.endsWith('.js') ? 'text/javascript' : 'text/html');
      res.end(await fs.readFile(file));
    } catch {res.statusCode = 404; res.end();}
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  let browser;
  try {
    browser = await chromium.launch({executablePath: process.env.PLAYWRIGHT_BROWSER_PATH || '/usr/bin/chromium', headless:true});
    const page = await browser.newPage({viewport:{width:1250, height:900}});
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    await page.goto('http://127.0.0.1:' + server.address().port, {waitUntil:'load'});
    await page.evaluate(async () => {await sessionRuntimeReady;});
    const correctness = await page.evaluate(require('./transcript-performance-checks.cjs'), {baseline});
    assert.deepEqual(errors, []);
    console.log('PASS correctness', JSON.stringify(correctness));
    await browser.close(); browser = null;
  } finally {
    if (browser) await browser.close();
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => {console.error(error); process.exitCode = 1;});
