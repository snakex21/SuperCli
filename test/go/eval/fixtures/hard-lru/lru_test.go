package lru

import (
	"reflect"
	"testing"
)

func TestGetPromotes(t *testing.T) {
	c := New(2, nil)
	c.Put("a", 1)
	c.Put("b", 2)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = %d, %v; want 1, true", v, ok)
	}
	c.Put("c", 3)
	if _, ok := c.Peek("b"); ok {
		t.Fatalf("b should have been evicted as least recently used")
	}
	if got, want := c.Keys(), []string{"c", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Keys() = %v, want %v", got, want)
	}
}
