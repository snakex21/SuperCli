// SPDX-License-Identifier: MIT

package slices2

// Chunk splits xs into pieces of n.
func Chunk(xs []int, n int) [][]int {
	var out [][]int
	for n > 0 && len(xs) > n {
		out, xs = append(out, xs[:n]), xs[n:]
	}
	return append(out, xs)
}
