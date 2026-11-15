package admin

import (
	"testing"
	"time"

	"slab/lru"
	"slab/ttl"
)

func TestPurge(t *testing.T) {
	s := lru.New(8)
	s.Put("a", nil)
	s.Put("b", nil)
	PurgeKeys(s, []string{"a", "b"})
	if s.Len() != 0 {
		t.Fatalf("Len = %d", s.Len())
	}
	c := ttl.NewCache(time.Hour)
	c.Set("t", "1")
	PurgeTokens(c, []string{"t"})
	if _, ok := c.Get("t"); ok {
		t.Fatal("token left")
	}
}
