'use strict';

const fetchUserDataCache = new Map();

/** Load a user's profile, memoised per id. */
async function fetchUserData(id, transport) {
  if (fetchUserDataCache.has(id)) return fetchUserDataCache.get(id);
  let result;
  try {
    result = await transport.get(`/users/${id}`);
  } catch (err) {
    if (err.code !== 'ETIMEDOUT') throw err;
    result = await transport.get(`/users/${id}`);
  }
  fetchUserDataCache.set(id, result);
  return result;
}

module.exports = { fetchUserData, fetchUserDataCache };
