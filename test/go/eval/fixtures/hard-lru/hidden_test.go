//go:build evalhidden

package lru

import (
	"fmt"
	"reflect"
	"testing"
)

type evictLog struct{ events []string }

func (l *evictLog) fn(k string, v int) { l.events = append(l.events, fmt.Sprintf("%s=%d", k, v)) }

func keysEqual(t *testing.T, c *Cache, want []string) {
	t.Helper()
	got := c.Keys()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Keys() = %v, want %v", got, want)
	}
	if c.Len() != len(want) {
		t.Fatalf("Len() = %d, want %d", c.Len(), len(want))
	}
}

func TestHiddenUpdateExistingPromotesWithoutEviction(t *testing.T) {
	log := &evictLog{}
	c := New(2, log.fn)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("a", 10)
	keysEqual(t, c, []string{"a", "b"})
	if len(log.events) != 0 {
		t.Fatalf("updating an existing key must not evict, got %v", log.events)
	}
	if v, _ := c.Peek("a"); v != 10 {
		t.Fatalf("Peek(a) = %d, want 10", v)
	}
	c.Put("c", 3)
	keysEqual(t, c, []string{"c", "a"})
	if !reflect.DeepEqual(log.events, []string{"b=2"}) {
		t.Fatalf("evictions = %v, want [b=2]", log.events)
	}
}

func TestHiddenPeekDoesNotPromote(t *testing.T) {
	c := New(2, nil)
	c.Put("a", 1)
	c.Put("b", 2)
	if v, ok := c.Peek("a"); !ok || v != 1 {
		t.Fatalf("Peek(a) = %d, %v", v, ok)
	}
	keysEqual(t, c, []string{"b", "a"})
	c.Put("c", 3)
	if _, ok := c.Peek("a"); ok {
		t.Fatal("a should be evicted: Peek must not promote")
	}
	keysEqual(t, c, []string{"c", "b"})
}

func TestHiddenGetMissing(t *testing.T) {
	c := New(2, nil)
	c.Put("a", 1)
	if v, ok := c.Get("zz"); ok || v != 0 {
		t.Fatalf("Get(missing) = %d, %v; want 0, false", v, ok)
	}
	if v, ok := c.Peek("zz"); ok || v != 0 {
		t.Fatalf("Peek(missing) = %d, %v; want 0, false", v, ok)
	}
	keysEqual(t, c, []string{"a"})
}

func TestHiddenRemove(t *testing.T) {
	log := &evictLog{}
	c := New(3, log.fn)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3)
	if !c.Remove("b") {
		t.Fatal("Remove(b) = false, want true")
	}
	if c.Remove("b") {
		t.Fatal("second Remove(b) = true, want false")
	}
	keysEqual(t, c, []string{"c", "a"})
	c.Put("d", 4)
	keysEqual(t, c, []string{"d", "c", "a"})
	if len(log.events) != 0 {
		t.Fatalf("Remove and a non-overflowing Put must not call onEvict, got %v", log.events)
	}
	c.Put("e", 5)
	keysEqual(t, c, []string{"e", "d", "c"})
	if !reflect.DeepEqual(log.events, []string{"a=1"}) {
		t.Fatalf("evictions = %v, want [a=1]", log.events)
	}
}

func TestHiddenZeroAndNegativeCapacity(t *testing.T) {
	for _, capacity := range []int{0, -3} {
		log := &evictLog{}
		c := New(capacity, log.fn)
		c.Put("a", 1)
		if _, ok := c.Get("a"); ok {
			t.Fatalf("capacity %d: cache must store nothing", capacity)
		}
		keysEqual(t, c, nil)
		if len(log.events) != 0 {
			t.Fatalf("capacity %d: onEvict must not be called, got %v", capacity, log.events)
		}
	}
}

func TestHiddenCapacityOne(t *testing.T) {
	log := &evictLog{}
	c := New(1, log.fn)
	c.Put("a", 1)
	c.Put("a", 2)
	c.Put("b", 3)
	keysEqual(t, c, []string{"b"})
	if !reflect.DeepEqual(log.events, []string{"a=2"}) {
		t.Fatalf("evictions = %v, want [a=2]", log.events)
	}
}

func TestHiddenResize(t *testing.T) {
	log := &evictLog{}
	c := New(4, log.fn)
	for i, k := range []string{"a", "b", "c", "d"} {
		c.Put(k, i)
	}
	c.Get("a")
	keysEqual(t, c, []string{"a", "d", "c", "b"})
	c.Resize(2)
	keysEqual(t, c, []string{"a", "d"})
	if !reflect.DeepEqual(log.events, []string{"b=1", "c=2"}) {
		t.Fatalf("evictions = %v, want [b=1 c=2] (least recent first)", log.events)
	}
	c.Resize(3)
	c.Put("e", 9)
	keysEqual(t, c, []string{"e", "a", "d"})
	if len(log.events) != 2 {
		t.Fatalf("growing must not evict, got %v", log.events)
	}
	c.Resize(0)
	keysEqual(t, c, nil)
	if !reflect.DeepEqual(log.events, []string{"b=1", "c=2", "d=3", "a=0", "e=9"}) {
		t.Fatalf("evictions = %v", log.events)
	}
	c.Put("x", 1)
	keysEqual(t, c, nil)
}

func TestHiddenKeysIsACopy(t *testing.T) {
	c := New(3, nil)
	c.Put("a", 1)
	c.Put("b", 2)
	k := c.Keys()
	k[0] = "zzz"
	keysEqual(t, c, []string{"b", "a"})
}

func TestHiddenManyOperations(t *testing.T) {
	log := &evictLog{}
	c := New(3, log.fn)
	for i := 0; i < 1000; i++ {
		c.Put(fmt.Sprintf("k%d", i%5), i)
		if i%7 == 0 {
			c.Get(fmt.Sprintf("k%d", (i+1)%5))
		}
	}
	if c.Len() != 3 || len(c.Keys()) != 3 {
		t.Fatalf("Len() = %d, len(Keys()) = %d; want 3", c.Len(), len(c.Keys()))
	}
	if c.Keys()[0] != "k4" {
		t.Fatalf("most recent key = %s, want k4", c.Keys()[0])
	}
}
