'use strict';
const test = require('node:test');
const assert = require('node:assert');
const { parseRanges } = require('../src/ranges');

test('basic', () => {
  assert.deepStrictEqual(parseRanges('1-3,5,7-9'), [1, 2, 3, 5, 7, 8, 9]);
  assert.deepStrictEqual(parseRanges(''), []);
});

test('errors are RangeError', () => {
  assert.throws(() => parseRanges('5-3'), RangeError);
  assert.throws(() => parseRanges('a'), RangeError);
});
