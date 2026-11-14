package proxy

import "gateway/internal/ids"

// Batch assigns ids to a batch of paths and returns them in order.
func Batch(paths []string) []string {
	out := make([]string, 0, len(paths))
	for range paths {
		out = append(out, ids.New("b"))
	}
	return out
}
