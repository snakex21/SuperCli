'use strict';

function formatNet(amount) {
  return '$' + (Math.round(amount * 100) / 100).toFixed(2);
}

function formatGross(amount, taxRate) {
  const value = amount * (1 + taxRate);
  return '$' + (Math.round(value * 100) / 100).toFixed(2);
}

function formatDiscounted(amount, percent) {
  const value = amount * (1 - percent / 100);
  return '$' + (Math.round(value * 100) / 100).toFixed(2);
}

module.exports = { formatNet, formatGross, formatDiscounted };
