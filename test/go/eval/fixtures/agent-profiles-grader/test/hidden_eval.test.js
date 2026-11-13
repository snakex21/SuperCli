'use strict';
const test = require('node:test');
const assert = require('node:assert');
const lib = require('../src');
const api = require('../src/api');

test('renamed export', async () => {
  assert.strictEqual(typeof lib.loadUserProfile, 'function');
  assert.strictEqual(typeof api.loadUserProfile, 'function');
  assert.strictEqual(lib.fetchUserData, undefined);
  assert.ok(api.fetchUserDataCache instanceof Map);
});
