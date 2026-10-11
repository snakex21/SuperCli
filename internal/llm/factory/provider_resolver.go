package factory

import (
	"context"
	"sync"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// ProviderResolver snapshots an optional provider's configuration and builds
// its genuine metered transport on first use. The resolver, not a Provider
// proxy, owns the cache: native transport interfaces remain available. Independent
// loops receive independent resolvers; concurrent inherited summaries share a
// single successful construction or ordinary build failure within that scope.
func (f *Factory) ProviderResolver(cfg config.Config, purpose string) func(context.Context) (llm.Provider, error) {
	if cfg.Temperature != nil {
		value := *cfg.Temperature
		cfg.Temperature = &value
	}
	var mu sync.Mutex
	var done chan struct{}
	var ready bool
	var provider llm.Provider
	var buildErr error
	return func(ctx context.Context) (llm.Provider, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			mu.Lock()
			if ready {
				p, err := provider, buildErr
				mu.Unlock()
				return p, err
			}
			if pending := done; pending != nil {
				mu.Unlock()
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-pending:
					continue
				}
			}
			done = make(chan struct{})
			pending := done
			mu.Unlock()

			p, err := f.BuildContext(ctx, cfg, purpose)
			mu.Lock()
			// A cancelled owner must not make a healthy waiter inherit its
			// failed construction. It retries under its own current context.
			if ctx.Err() == nil {
				provider, buildErr, ready = p, err, true
			}
			done = nil
			close(pending)
			mu.Unlock()
			return p, err
		}
	}
}
