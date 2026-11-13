'use strict';

function parseRanges(spec) {
  if (spec.trim() === '') return [];
  const seen = new Set();
  for (const raw of spec.split(',')) {
    const m = /^\s*(\d+)\s*(?:-\s*(\d+)\s*)?$/.exec(raw);
    if (!m) throw new RangeError(`bad token: ${raw}`);
    const a = Number(m[1]);
    const b = m[2] === undefined ? a : Number(m[2]);
    if (a > b) throw new RangeError(`bad range: ${raw}`);
    if (b - a + 1 > 10000) throw new RangeError('too many numbers');
    for (let i = a; i <= b; i++) seen.add(i);
    if (seen.size > 10000) throw new RangeError('too many numbers');
  }
  if (seen.size > 10000) throw new RangeError('too many numbers');
  return [...seen].sort((x, y) => x - y);
}

module.exports = { parseRanges };
