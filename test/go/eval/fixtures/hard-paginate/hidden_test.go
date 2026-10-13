//go:build evalhidden

package pager

import (
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
)

func collect(t *testing.T, s *Store, limit int) ([]int, []int) {
	t.Helper()
	var ids, sizes []int
	cursor := ""
	for n := 0; ; n++ {
		if n > 1000 {
			t.Fatal("pagination does not terminate")
		}
		p, err := s.List(cursor, limit)
		if err != nil {
			t.Fatalf("List(%q, %d): %v", cursor, limit, err)
		}
		sizes = append(sizes, len(p.Items))
		for _, it := range p.Items {
			ids = append(ids, it.ID)
		}
		if p.Next == "" {
			return ids, sizes
		}
		cursor = p.Next
	}
}

func seq(from, to int) []int {
	var out []int
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func storeOf(ids ...int) *Store {
	s := NewStore()
	for _, id := range ids {
		s.Put(Item{ID: id})
	}
	return s
}

func TestHiddenExactMultiple(t *testing.T) {
	ids, sizes := collect(t, storeOf(seq(1, 6)...), 3)
	if !reflect.DeepEqual(ids, seq(1, 6)) || !reflect.DeepEqual(sizes, []int{3, 3}) {
		t.Fatalf("ids=%v sizes=%v; want 1..6 in pages [3 3]", ids, sizes)
	}
}

func TestHiddenSinglePageAndEmpty(t *testing.T) {
	ids, sizes := collect(t, storeOf(10, 20), 5)
	if !reflect.DeepEqual(ids, []int{10, 20}) || !reflect.DeepEqual(sizes, []int{2}) {
		t.Fatalf("ids=%v sizes=%v", ids, sizes)
	}
	p, err := NewStore().List("", 5)
	if err != nil || len(p.Items) != 0 || p.Next != "" {
		t.Fatalf("empty store List = %+v, %v; want no items, no Next", p, err)
	}
}

func TestHiddenLimitOne(t *testing.T) {
	ids, sizes := collect(t, storeOf(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12), 1)
	if !reflect.DeepEqual(ids, seq(1, 12)) || len(sizes) != 12 {
		t.Fatalf("ids=%v sizes=%v", ids, sizes)
	}
}

func TestHiddenNegativeAndLargeIDs(t *testing.T) {
	want := []int{-1 << 40, -300, -5, 0, 7, 1 << 40}
	ids, _ := collect(t, storeOf(7, 1<<40, -5, 0, -300, -1<<40), 2)
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids=%v want %v", ids, want)
	}
}

func TestHiddenClampToMaxPageSize(t *testing.T) {
	ids, sizes := collect(t, storeOf(seq(1, 250)...), 1000)
	if !reflect.DeepEqual(ids, seq(1, 250)) || !reflect.DeepEqual(sizes, []int{100, 100, 50}) {
		t.Fatalf("sizes=%v (len ids %d); want [100 100 50]", sizes, len(ids))
	}
}

func TestHiddenBadLimit(t *testing.T) {
	s := storeOf(1, 2, 3)
	for _, limit := range []int{0, -1} {
		_, err := s.List("", limit)
		if !errors.Is(err, ErrBadLimit) {
			t.Errorf("List(\"\", %d) error = %v; want ErrBadLimit", limit, err)
		}
	}
}

func TestHiddenMutationBetweenPages(t *testing.T) {
	s := storeOf(seq(1, 9)...)
	p1, err := s.List("", 3)
	if err != nil || p1.Next == "" {
		t.Fatalf("first page: %+v, %v", p1, err)
	}
	s.Delete(3)
	s.Delete(4)
	s.Put(Item{ID: 2, Name: "updated"})
	s.Put(Item{ID: 0})
	s.Put(Item{ID: 100})
	var ids []int
	cursor := p1.Next
	for cursor != "" {
		p, err := s.List(cursor, 3)
		if err != nil {
			t.Fatalf("List(%q): %v", cursor, err)
		}
		for _, it := range p.Items {
			ids = append(ids, it.ID)
		}
		cursor = p.Next
	}
	if want := []int{5, 6, 7, 8, 9, 100}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("after mutation got %v, want %v", ids, want)
	}
}

func TestHiddenItemsDoNotAlias(t *testing.T) {
	s := NewStore(Item{1, "a"}, Item{2, "b"}, Item{3, "c"})
	p, err := s.List("", 2)
	if err != nil {
		t.Fatal(err)
	}
	p.Items[0].Name = "mutated"
	p.Items = append(p.Items, Item{ID: 99, Name: "appended"})
	s.Put(Item{ID: 1, Name: "a2"})
	if p.Items[0].Name != "mutated" {
		t.Fatalf("page items changed after store Put: %+v", p.Items)
	}
	p2, err := s.List("", 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Item{{1, "a2"}, {2, "b"}, {3, "c"}}; !reflect.DeepEqual(p2.Items, want) {
		t.Fatalf("store changed through page items: %+v, want %+v", p2.Items, want)
	}
}

func TestHiddenBadCursors(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	s := storeOf(1, 2, 3)
	for _, c := range []string{"%%%", "not a cursor!", enc([]byte("foo")), enc([]byte("after:")), enc([]byte("after:x1")), enc([]byte("before:1"))} {
		p, err := s.List(c, 2)
		if !errors.Is(err, ErrBadCursor) {
			t.Errorf("List(%q) = %+v, %v; want ErrBadCursor", c, p, err)
		}
	}
}
