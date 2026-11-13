package stats

import "sync"

// Counter counts events by name. It is shared by many goroutines.
type Counter struct {
	mu     sync.Mutex
	counts map[string]int
	total  int
}

// New returns an empty Counter.
func New() *Counter { return &Counter{counts: map[string]int{}} }

// Inc records one event.
func (c *Counter) Inc(name string) {
	c.mu.Lock()
	c.counts[name]++
	c.total++
	c.mu.Unlock()
}

// Get returns the count for name.
func (c *Counter) Get(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[name]
}

// Total returns the number of events recorded.
func (c *Counter) Total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// Snapshot returns a copy of the counts by name.
func (c *Counter) Snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.counts))
	for k, v := range c.counts {
		out[k] = v
	}
	return out
}
