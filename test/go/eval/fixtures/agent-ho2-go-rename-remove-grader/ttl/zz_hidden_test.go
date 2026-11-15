package ttl

import (
	"reflect"
	"testing"
	"time"
)

func TestHiddenCacheUnchanged(t *testing.T) {
	c := NewCache(time.Hour)
	c.Set("k", "v")
	c.Evict("k")
	if _, ok := c.Get("k"); ok {
		t.Fatal("Evict")
	}
	if _, has := reflect.TypeOf(c).MethodByName("Remove"); has {
		t.Fatal("ttl.Cache must not get a Remove method")
	}
}
