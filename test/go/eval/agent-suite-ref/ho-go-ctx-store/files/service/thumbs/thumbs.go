// Package thumbs renders (fake) thumbnails of stored images.
package thumbs

import (
	"context"
	"fmt"

	"blobhub/cache"
	"blobhub/storage"
)

// Renderer renders thumbnails and caches the results.
type Renderer struct {
	Store storage.Store
	Cache cache.Cache
}

// Render returns the thumbnail for key, using the cache when possible.
func (r Renderer) Render(ctx context.Context, key string) ([]byte, error) {
	if b, ok := r.Cache.Get("thumb:" + key); ok {
		return b, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	img, err := r.Store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("thumbs: %s: %w", key, err)
	}
	thumb := []byte(fmt.Sprintf("thumb(%d bytes)", len(img)))
	r.Cache.Add("thumb:"+key, thumb)
	return thumb, nil
}
