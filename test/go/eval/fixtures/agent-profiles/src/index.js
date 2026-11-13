'use strict';
const { fetchUserData, fetchUserDataCache } = require('./api');
const { renderProfile } = require('./view');
const { badge, clearBadges } = require('./badge');

module.exports = { fetchUserData, fetchUserDataCache, renderProfile, badge, clearBadges };
