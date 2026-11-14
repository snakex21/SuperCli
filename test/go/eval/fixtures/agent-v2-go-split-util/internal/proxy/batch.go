package proxy

import "gateway/internal/util"

// Batch assigns ids to a batch of paths and returns them in order.
func Batch(paths []string) []string {
	ids := make([]string, 0, len(paths))
	for range paths {
		ids = append(ids, util.NewID("b"))
	}
	return ids
}
