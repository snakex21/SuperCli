'use strict';
const test = require('node:test');
const assert = require('node:assert');
const p = require('../src/pricing');

test('formatMoney exported and used', () => {
  assert.strictEqual(typeof p.formatMoney, 'function');
  assert.strictEqual(p.formatMoney(2.5), '$2.50');
  assert.strictEqual(p.formatMoney(0.125), '$0.13');
  assert.strictEqual(p.formatGross(10, 0.075), '$10.75');
  assert.strictEqual(p.formatDiscounted(19.99, 10), '$17.99');
});
