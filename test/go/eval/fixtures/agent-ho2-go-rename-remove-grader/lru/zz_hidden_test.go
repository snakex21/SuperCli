package lru

import (
	"reflect"
	"testing"
)

func TestHiddenRemove(t *testing.T) {
	var gone []string
	s := New(3)
	s.OnEvict = func(k string) { gone = append(gone, k) }
	s.Put("a", nil)
	s.Put("b", nil)
	var ok bool = s.Remove("a")
	if !ok || s.Remove("a") || s.Remove("zzz") {
		t.Fatal("Remove must report presence")
	}
	if len(gone) != 1 || gone[0] != "a" {
		t.Fatalf("OnEvict calls = %v", gone)
	}
	s.EvictAll()
	if s.Len() != 0 {
		t.Fatal("EvictAll")
	}
	typ := reflect.TypeOf(s)
	if _, has := typ.MethodByName("Evict"); has {
		t.Fatal("Store.Evict should be renamed to Remove")
	}
	if _, has := typ.MethodByName("RemoveAll"); has {
		t.Fatal("EvictAll must keep its name")
	}
}
