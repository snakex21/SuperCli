'use strict';
const test = require('node:test');
const assert = require('node:assert');
const { parseRanges } = require('../src/ranges');

test('hidden ranges', () => {
  assert.deepStrictEqual(parseRanges(' 3 , 1 - 2 ,2, 0'), [0, 1, 2, 3]);
  assert.deepStrictEqual(parseRanges('  '), []);
  assert.deepStrictEqual(parseRanges('4-4'), [4]);
  assert.strictEqual(parseRanges('0-9999').length, 10000);
  for (const bad of ['1,,2', '-1', '+1', '1.5', '1-', '-', '1-2-3', '0-10000', 'x-2']) {
    assert.throws(() => parseRanges(bad), RangeError, bad);
  }
});
