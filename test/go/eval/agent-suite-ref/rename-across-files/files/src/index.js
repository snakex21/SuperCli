'use strict';
const { loadUserProfile, fetchUserDataCache } = require('./api');
const { renderProfile } = require('./view');
const { badge, clearBadges } = require('./badge');

module.exports = { loadUserProfile, fetchUserDataCache, renderProfile, badge, clearBadges };
