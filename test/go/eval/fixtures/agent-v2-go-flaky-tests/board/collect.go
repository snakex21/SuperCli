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
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		out      []Entry
		firstErr error
	)
	for _, s := range srcs {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			e, err := s.Fetch(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			out = append(out, e)
		}(s)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}
