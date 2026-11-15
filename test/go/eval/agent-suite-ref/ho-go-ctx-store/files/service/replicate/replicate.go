// Package replicate copies blobs between stores.
package replicate

import (
	"context"
	"fmt"

	"blobhub/storage"
)

// Copy copies keys from src to dst, stopping at the first error.
func Copy(ctx context.Context, src, dst storage.Store, keys []string) (int, error) {
	n := 0
	for _, k := range keys {
		b, err := src.Get(ctx, k)
		if err != nil {
			return n, fmt.Errorf("replicate: read %s: %w", k, err)
		}
		if err := dst.Put(k, b); err != nil {
			return n, fmt.Errorf("replicate: write %s: %w", k, err)
		}
		n++
	}
	return n, nil
}
