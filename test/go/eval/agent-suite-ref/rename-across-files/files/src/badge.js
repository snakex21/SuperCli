'use strict';
const api = require('./api');

/** Badge text; uses loadUserProfile under the hood. */
async function badge(id, transport) {
  const user = await api.loadUserProfile(id, transport);
  return user.admin ? `${user.name} (admin)` : user.name;
}

function clearBadges() {
  api.fetchUserDataCache.clear();
}

module.exports = { badge, clearBadges };
