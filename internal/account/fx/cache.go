package fx

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	cacheFileName = "currency-rates.db"
	dateLayout    = "2006-01-02"
	lookbackDays  = 7
	requestBudget = 10 * time.Second
	fetchBudget   = 45 * time.Second
	refreshBudget = 150 * time.Millisecond
	rateSource    = "NBP table A"
	quoteBSource  = "NBP table B / USD table A"
	bLookbackDays = 14 // weekly B, including a Wednesday holiday shifted to Tuesday
)

// Rate converts one USD into the selected currency. Date is its publication
// date; USDDate identifies the independently frozen daily A reference when a
// quote was added later, including weekly B rates.
type Rate struct {
	Multiplier float64 `json:"multiplier"`
	Date       string  `json:"date"`
	Source     string  `json:"source"`
	USDDate    string  `json:"usd_date,omitempty"`
}

type dayRates struct {
	Date        string             `json:"date"`
	Source      string             `json:"source"`
	Multipliers map[string]float64 `json:"multipliers"`
}

type cacheFlight struct {
	days     []string
	currency string // empty means the original complete A-day fetch
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	err      error
}

// Cache freezes the first successfully fetched table for each usage day.
// Callers should keep one Cache for their application data directory.
type Cache struct {
	mu sync.Mutex
	// Lookup never waits for HTTP, database locks, or historical batch writes.
	lookupMu    sync.RWMutex
	path        string
	client      *http.Client
	db          *sql.DB
	lastID      int64
	lastQuoteID int64
	closed      bool
	days        map[string]dayRates
	quotes      map[string]map[string]Rate
	flight      *cacheFlight
}

func New(dataDir string) (*Cache, error) {
	return NewWithClient(dataDir, nil)
}

// NewWithClient permits transport injection without adding a configurable
// production endpoint. Requests always address the official HTTPS NBP API.
func NewWithClient(dataDir string, client *http.Client) (*Cache, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("fx: portable data directory is required")
	}
	root, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("fx: data directory: %w", err)
	}
	copyClient := http.Client{}
	if client != nil {
		copyClient = *client
	}
	if copyClient.Timeout <= 0 || copyClient.Timeout > requestBudget {
		copyClient.Timeout = requestBudget
	}
	redirect := copyClient.CheckRedirect
	copyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.Host != "api.nbp.pl" {
			return fmt.Errorf("fx: redirect outside the official HTTPS API")
		}
		if redirect != nil {
			return redirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("fx: too many redirects")
		}
		return nil
	}
	c := &Cache{path: filepath.Join(root, cacheFileName), client: &copyClient, days: make(map[string]dayRates), quotes: make(map[string]map[string]Rate)}
	if err := c.open(); err != nil {
		return nil, err
	}
	return c, nil
}

// Lookup reads only memory. USD is always one and needs no cached table.
func (c *Cache) Lookup(day, currency string) (Rate, bool) {
	code, err := NormalizeCurrency(currency)
	if err != nil {
		return Rate{}, false
	}
	if _, err := parseDay(day); err != nil {
		return Rate{}, false
	}
	if code == "USD" {
		return Rate{Multiplier: 1, Date: day, Source: "USD"}, true
	}
	if c == nil {
		return Rate{}, false
	}
	c.lookupMu.RLock()
	snapshot, ok := c.days[day]
	multiplier := snapshot.Multipliers[code]
	quote, quoteOK := c.quotes[day][code]
	c.lookupMu.RUnlock()
	if ok && validMultiplier(multiplier) {
		return Rate{Multiplier: multiplier, Date: snapshot.Date, Source: snapshot.Source}, true
	}
	return quote, quoteOK && validMultiplier(quote.Multiplier)
}

// Refresh imports already committed rows from other application instances.
// It never starts HTTP or waits for an in-progress commit; a busy cache keeps
// its current snapshot until the next explicit read/event can synchronize it.
func (c *Cache) Refresh(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("fx: cache is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.mu.TryLock() {
		return ctx.Err()
	}
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closed {
		return fmt.Errorf("fx: cache is closed")
	}
	refreshCtx, cancel := context.WithTimeout(ctx, refreshBudget)
	defer cancel()
	return c.refreshPersisted(refreshCtx)
}

// EnsureDays fetches missing base day snapshots. Missing days are grouped into
// bounded historical requests; concurrent callers share one active fetch.
// Canceling a caller does not interrupt other callers still awaiting that fetch.
func (c *Cache) EnsureDays(ctx context.Context, days []string) error {
	if c == nil {
		return fmt.Errorf("fx: cache is nil")
	}
	wanted, err := normalizeDays(days)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return fmt.Errorf("fx: cache is closed")
		}
		if err := c.refreshPersisted(ctx); err != nil {
			c.mu.Unlock()
			return err
		}
		missing := c.missingDays(wanted)
		if len(missing) == 0 {
			c.mu.Unlock()
			return nil
		}
		flight := c.flight
		if flight == nil {
			fetchCtx, cancel := context.WithTimeout(context.Background(), fetchBudget)
			flight = &cacheFlight{days: missing, done: make(chan struct{}), cancel: cancel}
			c.flight = flight
			go c.runFlight(fetchCtx, flight)
		}
		flight.waiters++
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			c.leaveFlight(flight)
			return ctx.Err()
		case <-flight.done:
			c.leaveFlight(flight)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		remaining := c.missingDays(wanted)
		flightErr := flight.err
		c.mu.Unlock()
		if len(remaining) == 0 {
			return nil
		}
		if flightErr != nil && flight.currency == "" && daysOverlap(remaining, flight.days) {
			return flightErr
		}
		// A caller requesting different days first waits for the active fetch,
		// then fetches its still-missing subset without duplicating the overlap.
	}
}

func (c *Cache) leaveFlight(flight *cacheFlight) {
	c.mu.Lock()
	defer c.mu.Unlock()
	flight.waiters--
	if flight.waiters == 0 && c.flight == flight {
		c.flight = nil
		flight.cancel()
	}
}

func (c *Cache) runFlight(ctx context.Context, flight *cacheFlight) {
	defer flight.cancel()
	fetched, fetchErr := c.fetchDays(ctx, flight.days)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flight != flight || ctx.Err() != nil {
		flight.err = ctx.Err()
		if flight.err == nil {
			flight.err = context.Canceled
		}
	} else {
		flight.err = c.commit(ctx, fetched)
		if flight.err == nil {
			flight.err = fetchErr
		}
	}
	if c.flight == flight {
		c.flight = nil
	}
	close(flight.done)
}

// Close cancels unfinished fetches and releases the portable database.
// Existing in-memory lookups remain available; EnsureDays cannot resume.
func (c *Cache) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	if c.flight != nil {
		c.flight.cancel()
		c.flight = nil
	}
	c.mu.Unlock()
	return c.db.Close()
}

// These helpers run under mu; frozen snapshots are never replaced.
func (c *Cache) missingDays(wanted []string) []string {
	c.lookupMu.RLock()
	defer c.lookupMu.RUnlock()
	var missing []string
	for _, day := range wanted {
		if _, ok := c.days[day]; !ok {
			missing = append(missing, day)
		}
	}
	return missing
}

func daysOverlap(a, b []string) bool {
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			return true
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return false
}

func parseDay(day string) (time.Time, error) {
	parsed, err := time.Parse(dateLayout, day)
	if err != nil || parsed.Format(dateLayout) != day {
		return time.Time{}, fmt.Errorf("fx: invalid usage date %q (want YYYY-MM-DD)", day)
	}
	return parsed, nil
}

func normalizeDays(days []string) ([]string, error) {
	seen := make(map[string]struct{}, len(days))
	for _, day := range days {
		if _, err := parseDay(day); err != nil {
			return nil, err
		}
		seen[day] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for day := range seen {
		result = append(result, day)
	}
	sort.Strings(result)
	return result, nil
}
