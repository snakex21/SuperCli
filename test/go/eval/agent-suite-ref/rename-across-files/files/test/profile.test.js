'use strict';
const test = require('node:test');
const assert = require('node:assert');
const lib = require('../src');

const transport = { calls: 0, async get(path) { this.calls++; return { name: 'Ada', admin: path.endsWith('/1') }; } };

test('profile and badge', async () => {
  lib.clearBadges();
  assert.strictEqual(await lib.renderProfile(1, transport), '<h1>Ada</h1>');
  assert.strictEqual(await lib.badge(1, transport), 'Ada (admin)');
  assert.strictEqual(transport.calls, 1);
  assert.strictEqual(typeof lib.loadUserProfile, 'function');
});
