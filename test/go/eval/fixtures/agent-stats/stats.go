package stats

// Counter counts events by name. It is shared by many goroutines.
type Counter struct {
	counts map[string]int
	total  int
}

// New returns an empty Counter.
func New() *Counter { return &Counter{counts: map[string]int{}} }

// Inc records one event.
func (c *Counter) Inc(name string) {
	c.counts[name]++
	c.total++
}

// Get returns the count for name.
func (c *Counter) Get(name string) int { return c.counts[name] }

// Total returns the number of events recorded.
func (c *Counter) Total() int { return c.total }

// Snapshot returns the counts by name.
func (c *Counter) Snapshot() map[string]int { return c.counts }
