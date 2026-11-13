package stats

import (
	"sync"
	"testing"
)

func TestHiddenSnapshotIsCopyAndRaceFree(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				c.Inc("a")
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				for range c.Snapshot() {
				}
				_ = c.Get("a")
			}
		}()
	}
	wg.Wait()
	snap := c.Snapshot()
	snap["a"] = -1
	if c.Get("a") != 2000 {
		t.Fatalf("Snapshot must return a copy; Get(a) = %d", c.Get("a"))
	}
}
