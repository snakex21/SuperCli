'use strict';
const assert = require('node:assert/strict');
const test = require('node:test');
const vm = require('node:vm');
const {load, baseline} = require('./tool-change-stats-fixture.cjs');
const {stats, context} = load();
const plain = value => JSON.parse(JSON.stringify(value));

test('diff summary preserves exact line-prefix and line-ending behavior', () => {
  const cases = ['', '+', '-', '++', '+++', '++++', '--', '---', '----', '-----',
    '+new\n-old', '+new\r\n-old\r\n context', '+new\rold\r-removed',
    ' context\n+last', ' context\n-last\n', '\n\n+\n-\n',
    '+++ fixture\n--- fixture\n@@ context\n+ą日本語😀\n-old',
    '\u2028+not-a-new-line\u2029-still-same-line', '+\r\n++\r\n+++\r\n',
    null, undefined, 0, false, {toString: () => '+converted\n-removed'}];
  for (const name of ['edit_line', 'edit_lines', 'insert_after', 'delete_lines', 'apply_patch', 'patch']) {
    for (const text of cases) assert.deepEqual(plain(stats(name, text)), baseline(name, text));
  }
});

test('randomized diff summaries preserve the original result', () => {
  let seed = 0x51a79d03;
  const alphabet = ['+', '-', ' ', '\n', '\r', 'x', 'ą', '日', '\u2028', '\u2029', '😀'];
  for (let example = 0; example < 1500; example++) {
    let text = '';
    for (let char = 0; char < 120; char++) {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
      text += alphabet[seed % alphabet.length];
    }
    assert.deepEqual(plain(stats('apply_patch', text)), baseline('apply_patch', text));
  }
});

test('ordinary tool results bypass conversion and diff summaries avoid line-array allocation', () => {
  assert.deepEqual(plain(stats('read_lines', {toString() {throw Error('must not convert a non-diff result');}})),
    {added: 0, removed: 0, diff: false});
  context.largeDiff = '+a\n-b\n context\r\n'.repeat(100000);
  vm.runInContext('String.prototype.split = function () { throw Error("line-array allocation"); };', context);
  assert.deepEqual(plain(vm.runInContext('toolChangeStats("apply_patch", largeDiff)', context)),
    {added: 100000, removed: 100000, diff: true});
});
