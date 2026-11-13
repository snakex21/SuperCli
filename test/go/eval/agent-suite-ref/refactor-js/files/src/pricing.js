'use strict';

function formatMoney(value) {
  return '$' + (Math.round(value * 100) / 100).toFixed(2);
}

function formatNet(amount) {
  return formatMoney(amount);
}

function formatGross(amount, taxRate) {
  return formatMoney(amount * (1 + taxRate));
}

function formatDiscounted(amount, percent) {
  return formatMoney(amount * (1 - percent / 100));
}

module.exports = { formatMoney, formatNet, formatGross, formatDiscounted };
