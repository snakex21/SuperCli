// SPDX-License-Identifier: Apache-2.0

package retry

// Backoff doubles d up to max.
func Backoff(d, max int) int {
	if d*2 > max {
		return max
	}
	return d * 2
}
