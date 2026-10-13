package pager

import (
	"reflect"
	"testing"
)

func TestListAllPages(t *testing.T) {
	s := NewStore(
		Item{5, "e"}, Item{1, "a"}, Item{7, "g"}, Item{3, "c"},
		Item{2, "b"}, Item{6, "f"}, Item{4, "d"},
	)
	var ids []int
	cursor, pages := "", 0
	for {
		p, err := s.List(cursor, 3)
		if err != nil {
			t.Fatalf("List(%q, 3) on page %d: %v", cursor, pages+1, err)
		}
		pages++
		for _, it := range p.Items {
			ids = append(ids, it.ID)
		}
		if p.Next == "" {
			break
		}
		if pages > 10 {
			t.Fatalf("too many pages, collected %v", ids)
		}
		cursor = p.Next
	}
	if want := []int{1, 2, 3, 4, 5, 6, 7}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("collected IDs %v, want %v", ids, want)
	}
	if pages != 3 {
		t.Fatalf("got %d pages, want 3", pages)
	}
}
