package usagecost

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"supercli/internal/account/fx"
)

// HistoryRates owns the portable exchange cache for one application lifetime.
// Reads open local storage lazily; network work only follows a usage, settings
// change or explicit retry event. Closing cancels and joins background work.
type HistoryRates struct {
	mu         sync.Mutex
	dataDir    string
	cache      *fx.Cache
	client     *http.Client
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	closed     bool
	warming    map[string]bool
	pending    int
	done       chan struct{}
	generation uint64
}

func NewHistoryRates(dataDir string) *HistoryRates {
	return NewHistoryRatesWithClient(dataDir, nil)
}

// NewHistoryRatesWithClient supports testing/offline transport injection;
// the FX cache still restricts requests to the official HTTPS API.
func NewHistoryRatesWithClient(dataDir string, client *http.Client) *HistoryRates {
	ctx, cancel := context.WithCancel(context.Background())
	return &HistoryRates{dataDir: dataDir, client: client, ctx: ctx, cancel: cancel}
}

func (h *HistoryRates) Cache() (*fx.Cache, error) {
	if h == nil {
		return nil, errors.New("exchange cache is closed")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errors.New("exchange cache is closed")
	}
	if h.cache == nil {
		cache, err := fx.NewWithClient(h.dataDir, h.client)
		if err != nil {
			return nil, err
		}
		h.cache = cache
	}
	return h.cache, nil
}

// Warm does not delay closing the model stream. Cached and already warming
// days are skipped; no timer retries failed or unavailable rates.
func (h *HistoryRates) Warm(days ...string) {
	h.WarmCurrency("PLN", days...)
}

// WarmCurrency is explicit-event work for the selected display currency.
// Existing day snapshots can lack a newly supported currency; day+currency
// coverage avoids silently skipping its immutable add-on quote.
func (h *HistoryRates) WarmCurrency(currency string, days ...string) {
	if h == nil || len(days) == 0 {
		return
	}
	code, err := fx.NormalizeCurrency(currency)
	if err != nil || code == "USD" {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	if h.warming == nil {
		h.warming = make(map[string]bool)
	}
	accepted := make([]string, 0, len(days))
	keys := make([]string, 0, len(days))
	for _, day := range days {
		key := day + "/" + code
		if h.warming[key] {
			continue
		}
		if h.cache != nil {
			if _, ok := h.cache.Lookup(day, code); ok {
				continue
			}
		}
		h.warming[key] = true
		accepted = append(accepted, day)
		keys = append(keys, key)
	}
	if len(accepted) == 0 {
		h.mu.Unlock()
		return
	}
	if h.pending == 0 {
		h.done = make(chan struct{})
		h.generation++
	}
	h.pending++
	h.wg.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.wg.Done()
		defer h.finishWarm(keys)
		ctx, cancel := context.WithTimeout(h.ctx, 12*time.Second)
		defer cancel()
		_ = h.EnsureCurrency(ctx, accepted, code)
	}()
}

func (h *HistoryRates) finishWarm(days []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, day := range days {
		delete(h.warming, day)
	}
	h.pending--
	if h.pending == 0 {
		close(h.done)
	}
}

// Pending reports accepted background work; it never starts a fetch or reads
// the database. Failed fetches cease to be pending just like successful ones.
func (h *HistoryRates) Pending() bool {
	pending, _ := h.State()
	return pending
}

// State atomically reports the pending flag and the current Warm cycle.
// Generation changes only when idle background work becomes active again.
func (h *HistoryRates) State() (bool, uint64) {
	if h == nil {
		return false, 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pending > 0, h.generation
}

// Generation identifies the current or most recently completed Warm cycle.
// Use State when the pending flag and generation must be read together.
func (h *HistoryRates) Generation() uint64 {
	_, generation := h.State()
	return generation
}

// Wait awaits completion of the current Warm cycle without starting work.
// Cancellation affects only this waiter. Completion does not imply that every
// rate was available; callers should read the cache once afterwards.
func (h *HistoryRates) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if h == nil {
		return nil
	}
	h.mu.Lock()
	done := h.done
	h.mu.Unlock()
	if done == nil {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return ctx.Err()
	}
}

func (h *HistoryRates) Ensure(ctx context.Context, days []string) error {
	cache, err := h.Cache()
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.ctx, cancel)
	defer stop()
	defer cancel()
	return cache.EnsureDays(requestCtx, days)
}

func (h *HistoryRates) EnsureCurrency(ctx context.Context, days []string, currency string) error {
	cache, err := h.Cache()
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.ctx, cancel)
	defer stop()
	defer cancel()
	return cache.EnsureCurrency(requestCtx, days, currency)
}

func (h *HistoryRates) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.cancel()
	h.mu.Unlock()
	h.wg.Wait()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cache != nil {
		return h.cache.Close()
	}
	return nil
}
