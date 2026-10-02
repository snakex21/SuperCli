const assert = require('node:assert/strict');
const cp = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const sourcePath = path.resolve(__dirname, '../../internal/webgui/assets/js/04-transcript.js');
function fixture(source) {
  const roots = new Map();
  function row() {
    const listeners = new Map();
    return {open: false, listeners,
      addEventListener(name, callback) { if (!listeners.has(name)) listeners.set(name, new Set()); listeners.get(name).add(callback); },
      removeEventListener(name, callback) { listeners.get(name)?.delete(callback); },
      dispatch(name) { Array.from(listeners.get(name) || []).forEach(callback => callback()); }
    };
  }
  const context = {$: key => { if (!roots.has(key)) roots.set(key, row()); return roots.get(key); }, window: {}, superCliUI: {}};
  vm.createContext(context); vm.runInContext(source, context);
  return {c: context, row};
}

test('lazy payload keeps folded bytes available and renders only once while other toggle listeners survive', () => {
  const h = fixture(fs.readFileSync(sourcePath, 'utf8')), row = h.row();
  let renders = 0, toggles = 0;
  row.addEventListener('toggle', () => toggles++);
  const raw = 'pełny wynik <data>\nsecond line 😀';
  row.cancel = h.c.renderToolPayloadWhenOpen(row, () => { renders++; row.text = raw; row.dispatch('toggle'); });
  row.dispatch('toggle'); assert.equal(renders, 0);
  row.open = true; row.dispatch('toggle');
  assert.equal(renders, 1); assert.equal(row.text, raw);
  row.open = false; row.dispatch('toggle'); row.open = true; row.dispatch('toggle');
  row.cancel(); row.cancel(); row.dispatch('toggle');
  assert.equal(renders, 1); assert.equal(row.listeners.get('toggle').size, 1); assert.equal(toggles, 6);
  const alreadyOpen = h.row(); alreadyOpen.open = true;
  h.c.renderToolPayloadWhenOpen(alreadyOpen, () => { alreadyOpen.text = raw; });
  assert.equal(alreadyOpen.text, raw); assert.equal(alreadyOpen.listeners.get('toggle').size, 0);
});

test('lazy payload cancellation and rendering exceptions preserve consumed-once behavior', () => {
  const h = fixture(fs.readFileSync(sourcePath, 'utf8')), canceled = h.row();
  let renders = 0;
  canceled.cancel = h.c.renderToolPayloadWhenOpen(canceled, () => renders++);
  // A row can be detached during reset while its cancellation handle is still held.
  canceled.isConnected = false; canceled.cancel(); canceled.open = true; canceled.dispatch('toggle');
  assert.equal(renders, 0); assert.equal(canceled.listeners.get('toggle').size, 0);
  const failed = h.row(), error = new Error('original rendering failure');
  failed.cancel = h.c.renderToolPayloadWhenOpen(failed, () => { renders++; throw error; });
  failed.open = true; assert.throws(() => failed.dispatch('toggle'), value => value === error);
  failed.open = false; failed.dispatch('toggle'); failed.open = true; failed.dispatch('toggle'); failed.cancel();
  assert.equal(renders, 1); assert.equal(failed.listeners.get('toggle').size, 0);
  const immediatelyFailed = h.row(); immediatelyFailed.open = true;
  assert.throws(() => h.c.renderToolPayloadWhenOpen(immediatelyFailed, () => { throw error; }), value => value === error);
  assert.equal(immediatelyFailed.listeners.get('toggle').size, 0);
});

async function retentionCheck(makeFixture, currentSourcePath) {
  const h = makeFixture(fs.readFileSync(currentSourcePath, 'utf8'));
  function setup(kind) {
    const row = h.row(), payload = {raw: 'generated full payload\n'.repeat(400)};
    const render = () => { row.text = payload.raw; };
    const ref = new WeakRef(render), payloadRef = new WeakRef(payload);
    row.cancel = h.c.renderToolPayloadWhenOpen(row, render);
    if (kind === 'expanded') { row.open = true; row.dispatch('toggle'); }
    if (kind === 'canceled') row.cancel();
    return {row, ref, payloadRef};
  }
  async function settledGC() { await new Promise(resolve => setImmediate(() => { global.gc(); setImmediate(() => { global.gc(); resolve(); }); })); }
  const folded = setup('folded'), expanded = setup('expanded'), canceled = setup('canceled');
  await settledGC();
  assert.ok(folded.ref.deref()); assert.ok(folded.payloadRef.deref());
  assert.equal(expanded.ref.deref(), undefined, 'materialized row retained its render closure');
  assert.equal(expanded.payloadRef.deref(), undefined, 'materialized row retained its source carrier');
  assert.equal(canceled.ref.deref(), undefined, 'canceled row retained its render closure');
  assert.equal(canceled.payloadRef.deref(), undefined, 'canceled row retained its source carrier');
  assert.equal(typeof expanded.row.cancel, 'function'); assert.equal(typeof canceled.row.cancel, 'function');
  folded.row.open = true; folded.row.dispatch('toggle');
  assert.equal(folded.row.text, 'generated full payload\n'.repeat(400));
  await settledGC(); assert.equal(folded.ref.deref(), undefined); assert.equal(folded.payloadRef.deref(), undefined);
}

test('retained cancellation handles release consumed callbacks while folded callbacks stay usable', async () => {
  const script = "const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'); const fixture=" + fixture.toString() + "; (" + retentionCheck.toString() + ")(fixture,process.argv[1]).catch(error=>{console.error(error);process.exitCode=1;});";
  await new Promise((resolve, reject) => cp.execFile(process.execPath, ['--expose-gc', '-e', script, sourcePath], {windowsHide: true}, (error, stdout, stderr) => {
    if (error) reject(new Error(stderr || stdout || error.message)); else resolve();
  }));
});
