// Package audit checks that expected blobs are present.
package audit

import (
	"context"
	"errors"
	"sort"

	"blobhub/storage"
)

// Missing returns the keys that s does not have, sorted.
func Missing(s storage.Store, keys []string) ([]string, error) {
	var out []string
	for _, k := range keys {
		if _, err := s.Get(context.Background(), k); errors.Is(err, storage.ErrNotFound) {
			out = append(out, k)
		} else if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}
