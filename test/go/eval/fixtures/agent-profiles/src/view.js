'use strict';
const { fetchUserData } = require('./api');

async function renderProfile(id, transport) {
  const user = await fetchUserData(id, transport);
  return `<h1>${user.name}</h1>`;
}

module.exports = { renderProfile };
