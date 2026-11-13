'use strict';
const test = require('node:test');
const assert = require('node:assert');
const p = require('../src/pricing');

test('net', () => assert.strictEqual(p.formatNet(12.5), '$12.50'));
test('gross', () => assert.strictEqual(p.formatGross(100, 0.23), '$123.00'));
test('discounted', () => assert.strictEqual(p.formatDiscounted(80, 25), '$60.00'));
