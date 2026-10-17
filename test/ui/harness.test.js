import {
  test, assert, readSource, readDir, sourcePath, testDir, createSandbox,
  runScript, runInNewSandbox, Script, parseHTML, run
} from './harness.js';

const tick = () => new Promise(resolve => setTimeout(resolve, 0));

test('harness: source reads and directory listings use the repository snapshot', () => {
  assert.equal(testDir, 'test/ui');
  const assets = sourcePath(testDir, '../../internal/webgui/assets');
  assert.equal(assets, 'internal/webgui/assets');
  assert.equal(sourcePath('internal\\webgui', './assets/js/../locales'), assets + '/locales');
  const locales = readDir(sourcePath(assets, 'locales'));
  assert(locales.includes('en.json'));
  assert.equal(new Set(locales).size, locales.length);
  assert(locales.every(name => !name.includes('/')));
  assert.equal(JSON.parse(readSource(sourcePath(assets, 'locales/en.json'), 'utf8'))['common.cancel'], 'Cancel');
  assert.match(readSource('internal/system/uilang/language.go'), /package uilang/);
  assert.throws(() => readSource('not-in-the-snapshot.js'), /not in snapshot/);
  assert.throws(() => readDir('not-in-the-snapshot'), /not in snapshot/);
  assert.throws(() => sourcePath('../outside'), /escapes/);
});

test('harness: context mutations and closures share var, let and function bindings', () => {
  const context = createSandbox({busy: 'fixture default'});
  const value = runScript(`'use strict';
    var busy = false, count = 1;
    let selected = 'first';
    const fixed = 9;
    function read() { return [busy, count, selected, fixed]; }
    function change() { busy = !busy; count++; selected = 'inside'; }
    read();`, context);
  assert.deepEqual(value, [false, 1, 'first', 9]);
  context.busy = true;
  context.count = 4;
  context.selected = 'outside';
  assert.deepEqual(context.read(), [true, 4, 'outside', 9]);
  context.change();
  assert.deepEqual([context.busy, context.count, context.selected], [false, 5, 'inside']);
  assert.throws(() => { context.fixed = 10; }, TypeError);
  const replacement = () => ['replacement'];
  context.read = replacement;
  assert.equal(runScript('read', context), replacement);
});

test('harness: successive scripts see prior bindings and preserve expression completion', () => {
  const context = createSandbox({});
  assert.equal(runScript('var value = 2; function previous() { return value; } value;', context), 2);
  assert.equal(runScript('value += 3; let later = previous() * 2; later;', context), 10);
  assert.equal(new Script('value = later + 1; previous();', {filename: 'later.js'}).runInContext(context), 11);
  assert.equal(context.later, 10);
  assert.equal(context.previous(), 11);
  assert.equal(runScript('42; var empty;', context), 42);
  assert.equal(runInNewSandbox('var result = 6; result * 7'), 42);
});

test('harness: the lexer skips comments, nested functions, templates and regex literals', async () => {
  const context = createSandbox({});
  runScript(`'use strict';
    // var commentLeak = 1;
    /* function commentFunction() {} */
    var literal = 'var stringLeak = 1; function hidden() {}',
        regex = /[{};,/]+\\/var regexLeak/g,
        quotient = 24 / 3 / 2,
        template = \`var templateLeak; \${(() => { const nested = \`inner \${1 + 2}\`; return nested; })()}\`,
        object = {method() { let methodLeak = 1; return methodLeak; }};
    function outer() { var localLeak = 1; function localFunction() {} return localLeak; }
    async function asynchronous() { let asyncLeak = 1; return asyncLeak; }
    function* sequence() { const generatorLeak = 2; yield generatorLeak; }
    class Example { method() { var classLeak = 3; return classLeak; } }
    if (true) /var controlRegexLeak[{}]/.test('nothing');
    var afterRegex = 7;
    var __publish = 'ordinary', __source = 'identifiers', __value = 1;
  `, context);
  assert.equal(context.quotient, 4);
  assert.equal(context.outer(), 1);
  assert.equal(await context.asynchronous(), 1);
  assert.deepEqual([...context.sequence()], [2]);
  assert.equal(new context.Example().method(), 3);
  assert.equal(context.template, 'var templateLeak; inner 3');
  assert.equal(context.afterRegex, 7);
  context.__value = 12;
  assert.equal(runScript('__value', context), 12);
  for (const name of ['commentLeak', 'commentFunction', 'stringLeak', 'hidden', 'regexLeak', 'templateLeak',
    'nested', 'methodLeak', 'localLeak', 'localFunction', 'asyncLeak', 'generatorLeak', 'classLeak', 'controlRegexLeak']) {
    assert.equal(Object.hasOwn(context, name), false, `Leaked non-global binding: ${name}`);
  }
});

test('harness: destructured declarations, directives and strict errors retain native behavior', () => {
  const context = createSandbox({input: {first: 3, nested: {value: 4}}, array: [5, undefined, 7]});
  runScript(`'use strict'; 'another directive';
    const {first: renamed, nested: {value}, missing = 8} = input;
    let [head, fallback = 6, ...tail] = array;
    function read() { return [renamed, value, missing, head, fallback, tail]; }
  `, context);
  context.head = 10;
  assert.deepEqual(context.read(), [3, 4, 8, 10, 6, [7]]);
  assert.throws(() => runScript(`'use strict'; accidentalGlobal = 1;`, context), ReferenceError);
  assert.throws(() => runScript(`'use strict'; var hoisted = readEarly(); let early = 1; function readEarly() { return early; }`, createSandbox({})), ReferenceError);
});

test('harness: parseHTML has native focus, layout, events and realm globals', () => {
  const {window: realm, document: doc} = parseHTML('<!doctype html><html><body><input id="input"><button id="button">Go</button></body></html>');
  assert.equal(doc.defaultView, realm);
  assert.equal(createSandbox(realm), realm);
  assert.equal(runScript('document', realm), doc);
  assert.equal(runScript('window === self && self === globalThis', realm), true);
  runScript(`var clicks = 0;
    document.querySelector('#button').addEventListener('click', () => clicks++);
    function focusInput() { document.querySelector('#input').focus(); }
    function nativeEvent() { return new MouseEvent('click', {bubbles: true}); }
  `, realm);
  realm.focusInput();
  assert.equal(doc.activeElement, doc.querySelector('#input'));
  assert(doc.querySelector('#input').getBoundingClientRect().width > 0);
  assert(realm.nativeEvent() instanceof realm.MouseEvent);
  doc.querySelector('#button').dispatchEvent(realm.nativeEvent());
  assert.equal(realm.clicks, 1);
  realm.clicks = 9;
  doc.querySelector('#button').click();
  assert.equal(realm.clicks, 10);
});

test('harness: plain document and window stubs are not replaced by native globals', () => {
  const fakeDocument = {querySelector(selector) { return {selector, fake: true}; }};
  const fakeWindow = {sentinel: true};
  const fakeSelf = {selfStub: true}, fakeGlobal = {globalStub: true};
  const context = createSandbox({document: fakeDocument, window: fakeWindow, self: fakeSelf, globalThis: fakeGlobal});
  assert.equal(runScript('document', context), fakeDocument);
  assert.equal(runScript('window', context), fakeWindow);
  assert.equal(runScript('self', context), fakeSelf);
  assert.equal(runScript('globalThis', context), fakeGlobal);
  assert.deepEqual(runScript(`function query() { return document.querySelector('#fake'); } query();`, context), {selector: '#fake', fake: true});
  assert.equal(context.document, fakeDocument);
  assert.equal(Object.hasOwn(fakeWindow, 'query'), false);
  assert.equal(runScript('Array.isArray([]) && typeof Promise === "function"', context), true);
});

test('harness: assertions compare cross-realm structures, cycles, maps and typed data', () => {
  const other = createSandbox({});
  const value = runScript(`({array: [1, NaN, -0], date: new Date(123), regex: /value/gi,
    map: new Map([[{key: 1}, new Set([2, 3])]]), bytes: new Uint8Array([1, 2, 3])})`, other);
  const expected = {array: [1, NaN, -0], date: new Date(123), regex: /value/gi,
    map: new Map([[{key: 1}, new Set([3, 2])]]), bytes: new Uint8Array([1, 2, 3])};
  assert.deepEqual(value, expected);
  assert.notDeepEqual(value, {...expected, array: [1, NaN, 0]});
  assert.notDeepEqual([,], [undefined]);
  const cyclic = runScript('var cycle = {}; cycle.self = cycle; cycle;', other);
  const local = {}; local.self = local;
  assert.deepEqual(cyclic, local);
  const left = {child: {}}; left.same = left.child;
  assert.notDeepEqual(left, {child: {}, same: {}});
  assert.throws(() => assert.equal({}, {}), {name: 'AssertionError'});
  assert.throws(() => assert.equal(-0, 0), /equal/);
  assert.equal(NaN, NaN);
  assert.notEqual(1, '1');
  assert.match('text', /text/g);
  assert.doesNotMatch('text', /other/);
  assert.throws(() => assert.fail('regression'), /regression/);
});

test('harness: throws and rejects handle native realm errors and async failures', async () => {
  const context = createSandbox({});
  assert.throws(() => runScript('throw new TypeError("realm failure")', context), TypeError);
  assert.throws(() => { throw {code: 42}; }, error => error.code === 42);
  await assert.rejects(runScript('Promise.reject(new Error("realm rejection"))', context), /realm rejection/);
  await assert.rejects(async () => { await tick(); throw new Error('late failure'); }, {message: /late failure/});
  await assert.rejects(() => assert.rejects(Promise.resolve()), {name: 'AssertionError'});
  await assert.rejects(() => assert.rejects(Promise.reject(new Error('wrong')), /expected/), {name: 'AssertionError'});
  await assert.doesNotReject(async () => { await tick(); return 1; });
});

test('harness: independent sandboxes isolate globals and intrinsic prototypes', () => {
  const first = createSandbox({}), second = createSandbox({});
  runScript('var privateValue = 1; Array.prototype.harnessMarker = 7; window.marker = 9;', first);
  assert.equal(runScript('typeof privateValue', second), 'undefined');
  assert.equal(runScript('Array.prototype.harnessMarker', second), undefined);
  assert.equal(second.marker, undefined);
  assert.equal([].harnessMarker, undefined);
  assert.equal(first.window, first);
  assert.equal(first.self, first);
  assert.equal(first.globalThis, first);
});

test('harness: async subtests and per-test cleanup hooks retain their names', async t => {
  const order = [];
  t.beforeEach(() => { order.push('before'); });
  t.afterEach(() => { order.push('after'); });
  await t.test('awaited child', async child => {
    child.after(() => { order.push('cleanup'); });
    await tick();
    order.push('child');
  });
  assert.deepEqual(order, ['before', 'child', 'cleanup', 'after']);
});

test('harness: production transcript and stats declarations are all published before initialization', () => {
  for (const file of ['04-transcript.js', '07-stats.js']) {
    const source = readSource(sourcePath('internal/webgui/assets/js', file));
    const context = createSandbox({$: () => { throw new Error('fixture initialization boundary'); }});
    try { runScript(source, context); } catch { /* Publication precedes any fixture-dependent initialization. */ }
    const expected = [...source.matchAll(/^(?:async )?function\s+([\w$]+)/gm)].map(match => match[1]);
    assert(expected.length > 10);
    const missing = expected.filter(name => !Object.hasOwn(context, name));
    assert.deepEqual(missing, [], `${file}: missing declarations ${missing.join(', ')}`);
  }
});

test('harness: supplied native timers keep their receiver and custom timer stubs stay intact', async () => {
  const context = createSandbox({setTimeout, clearTimeout});
  assert.equal(await runScript('new Promise(resolve => setTimeout(() => resolve(42), 0))', context), 42);
  const callbacks = [];
  const stub = (callback, delay) => { callbacks.push([callback, delay]); return 17; };
  const fake = createSandbox({setTimeout: stub});
  assert.equal(runScript('setTimeout(() => 1, 123)', fake), 17);
  assert.equal(fake.setTimeout, stub);
  assert.equal(callbacks[0][1], 123);
});

async function isolatedSuite(t, source) {
  // Do not use parseHTML here: its error listener belongs to the outer harness.
  const iframe = document.createElement('iframe');
  iframe.style.cssText = 'position:fixed;left:-10000px;top:0;width:1280px;height:900px;';
  document.body.appendChild(iframe);
  const urls = [];
  t.after(() => { for (const url of urls) URL.revokeObjectURL(url); iframe.remove(); });
  const harnessURL = new URL('./harness.js', import.meta.url);
  harnessURL.searchParams.set('suite-error-regression', crypto.randomUUID());
  const harness = await iframe.contentWindow.Function('url', 'return import(url)')(harnessURL.href);
  const url = URL.createObjectURL(new Blob([`
    import {test, createSandbox, runScript, parseHTML} from ${JSON.stringify(harnessURL.href)};
    ${source}
  `], {type: 'text/javascript'}));
  urls.push(url);
  const result = await harness.run([url]);
  assert.equal(iframe.contentDocument.querySelectorAll('iframe[data-supercli-test-frame]').length, 0);
  assert.equal(result.total, result.tests.length);
  assert.equal(result.passed, result.tests.filter(item => item.status === 'passed').length);
  assert.equal(result.skipped, result.tests.filter(item => item.status === 'skipped').length);
  assert.equal(result.failures.length, result.tests.filter(item => item.status === 'failed').length);
  assert.equal(result.total, result.passed + result.skipped + result.failures.length);
  return result;
}

for (const phase of ['before', 'after']) {
  test(`harness: suite ${phase} unreturned async failures fail the run`, async t => {
    for (const variant of ['waited rejection', 'immediate rejection', 'timer callback']) {
      const marker = 'LOST_' + phase.toUpperCase() + ': ' + variant;
      const action = variant === 'timer callback' ?
        `setTimeout(() => { throw new Error(${JSON.stringify(marker)}); }, 0);` :
        `Promise.reject(new Error(${JSON.stringify(marker)}));`;
      const result = await isolatedSuite(t, `
        test('ok', () => {});
        test.${phase}(async () => {
          ${action}
          ${variant === 'waited rejection' ? 'await new Promise(resolve => setTimeout(resolve, 20));' : ''}
        });
      `);
      assert.equal(result.failures.length, 1, JSON.stringify(result));
      assert.equal(result.total, 2);
      assert.equal(result.passed, 1);
      assert.equal(result.skipped, 0);
      assert.equal(result.failures[0].name, phase);
      assert.match(result.failures[0].error, new RegExp(marker));
      assert.match(result.failures[0].stack, new RegExp(marker));
      assert.deepEqual(result.tests.map(item => [item.name, item.status]), phase === 'before' ?
        [['before', 'failed'], ['ok', 'passed']] : [['ok', 'passed'], ['after', 'failed']]);
    }
  });
}

test('harness: suite import aggregates unreturned rejection and callback errors as one failure', async t => {
  const result = await isolatedSuite(t, `
    test('ok', () => {});
    Promise.reject(new Error('LOST_IMPORT_REJECTION'));
    setTimeout(() => { throw new Error('LOST_IMPORT_CALLBACK'); }, 0);
    await new Promise(resolve => setTimeout(resolve, 20));
  `);
  assert.equal(result.total, 2);
  assert.equal(result.passed, 1);
  assert.equal(result.skipped, 0);
  assert.equal(result.failures.length, 1);
  assert.match(result.failures[0].name, /^import blob:/);
  assert.match(result.failures[0].error, /LOST_IMPORT_REJECTION/);
  assert.match(result.failures[0].error, /LOST_IMPORT_CALLBACK/);
});

test('harness: suite cleanup records sync and async errors and still removes its frames', async t => {
  const result = await isolatedSuite(t, `
    const context = createSandbox({setTimeout, clearTimeout});
    runScript('var pendingTimer = setTimeout(() => {}, 60000);', context);
    const nativeCancel = clearTimeout;
    globalThis.clearTimeout = function(id) {
      nativeCancel.call(globalThis, id);
      if (id === context.pendingTimer) {
        Promise.reject(new Error('LOST_CLEANUP_REJECTION'));
        throw new Error('CLEANUP_THROW');
      }
    };
    test('ok', () => {});
  `);
  assert.equal(result.total, 2);
  assert.equal(result.passed, 1);
  assert.equal(result.skipped, 0);
  assert.equal(result.failures.length, 1);
  assert.equal(result.failures[0].name, 'cleanup');
  assert.match(result.failures[0].error, /LOST_CLEANUP_REJECTION/);
  assert.match(result.failures[0].error, /CLEANUP_THROW/);
  assert.deepEqual(result.tests.map(item => [item.name, item.status]), [['ok', 'passed'], ['cleanup', 'failed']]);
});

test('harness: runner reports failures, cleans frames and continues without forced skips', async t => {
  const baselineFrames = document.querySelectorAll('iframe[data-supercli-test-frame]').length;
  const url = URL.createObjectURL(new Blob([`
    import {test, assert, createSandbox, runScript, parseHTML} from ${JSON.stringify(new URL('./harness.js', import.meta.url).href)};
    test.beforeEach(() => { globalThis.__harnessHookCount = (globalThis.__harnessHookCount || 0) + 1; });
    test('async rejection', async () => { await Promise.resolve(); throw new Error('intentional async error'); });
    test('unhandled realm rejection', () => {
      const sandbox = createSandbox({});
      runScript('Promise.reject(new Error("intentional unhandled rejection")); void 0;', sandbox);
    });
    test('native event error', () => {
      const {window, document} = parseHTML('<button>Test</button>');
      const button = document.querySelector('button');
      button.addEventListener('click', () => { throw new Error('intentional event error'); });
      button.dispatchEvent(new window.MouseEvent('click'));
    });
    test('allocate frames', () => { createSandbox({}); parseHTML('<input>'); });
    test('verify isolation', () => {
      assert.equal(document.querySelectorAll('iframe[data-supercli-test-frame]').length, ${baselineFrames});
      assert.equal(globalThis.__harnessHookCount, 5);
    });
  `], {type: 'text/javascript'}));
  t.after(() => { URL.revokeObjectURL(url); delete globalThis.__harnessHookCount; });
  const result = await run([url]);
  assert.equal(result.total, 5);
  assert.equal(result.passed, 2, JSON.stringify(result));
  assert.equal(result.skipped, 0);
  assert.deepEqual(result.tests.map(item => [item.name, item.status]), [
    ['async rejection', 'failed'], ['unhandled realm rejection', 'failed'], ['native event error', 'failed'],
    ['allocate frames', 'passed'], ['verify isolation', 'passed']
  ]);
  assert.equal(result.failures.length, 3);
  assert(result.tests.every(item => Number.isFinite(item.duration_ms) && item.duration_ms >= 0));
  assert(result.failures.every(item => typeof item.stack === 'string'));
  assert.match(JSON.stringify(result), /intentional async error/);
  assert.match(JSON.stringify(result), /intentional unhandled rejection/);
  assert.match(JSON.stringify(result), /intentional event error/);
});
