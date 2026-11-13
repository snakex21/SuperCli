'use strict';
const { loadUserProfile } = require('./api');

async function renderProfile(id, transport) {
  const user = await loadUserProfile(id, transport);
  return `<h1>${user.name}</h1>`;
}

module.exports = { renderProfile };
