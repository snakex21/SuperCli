package stats

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentInc(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				c.Inc(fmt.Sprintf("k%d", i%4))
			}
		}(g)
	}
	wg.Wait()
	if c.Total() != 16000 {
		t.Fatalf("Total = %d, want 16000", c.Total())
	}
	if c.Get("k0") != 4000 {
		t.Fatalf("k0 = %d, want 4000", c.Get("k0"))
	}
}
