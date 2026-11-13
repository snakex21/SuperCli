'use strict';

/**
 * Expand a page-range specification into a sorted list of numbers.
 *
 * Spec:
 * - The input is a comma-separated list of tokens; each token is either a
 *   number "N" or an inclusive range "A-B". Whitespace around tokens and
 *   around the "-" is allowed.
 * - Numbers are non-negative decimal integers.
 * - For a range A-B, A must be <= B.
 * - The result is sorted ascending with duplicates removed.
 * - An empty or whitespace-only string returns [].
 * - Any malformed token (empty token, sign, decimals, letters, A > B) throws
 *   a RangeError.
 * - If the expanded result would contain more than 10000 numbers, throw a
 *   RangeError instead of allocating it.
 *
 * @param {string} spec
 * @returns {number[]}
 */
function parseRanges(spec) {
  throw new Error('not implemented');
}

module.exports = { parseRanges };
