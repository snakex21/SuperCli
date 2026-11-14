package board

import (
	"context"
	"sync"
)

// Source produces one player's score.
type Source interface {
	Fetch(ctx context.Context) (Entry, error)
}

// CollectAll fetches every source concurrently. Results are returned in
// the same order as srcs. If any fetch fails, the error returned is
// the one from the earliest failing source in srcs order.
func CollectAll(ctx context.Context, srcs []Source) ([]Entry, error) {
	out := make([]Entry, len(srcs))
	errs := make([]error, len(srcs))
	var wg sync.WaitGroup
	for i, s := range srcs {
		wg.Add(1)
		go func(i int, s Source) {
			defer wg.Done()
			out[i], errs[i] = s.Fetch(ctx)
		}(i, s)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
