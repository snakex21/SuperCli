package ttl

import (
	"testing"
	"time"
)

func TestExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	c := NewCache(time.Minute)
	c.Now = func() time.Time { return now }
	c.Set("k", "v")
	if v, ok := c.Get("k"); !ok || v != "v" {
		t.Fatal("want v")
	}
	c.Evict("k")
	if _, ok := c.Get("k"); ok {
		t.Fatal("evicted")
	}
	c.Set("x", "y")
	now = now.Add(2 * time.Minute)
	if n := c.Sweep(); n != 1 {
		t.Fatalf("Sweep = %d", n)
	}
}
