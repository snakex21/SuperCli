package board

import (
	"context"
	"fmt"
	"testing"
)

func TestRank(t *testing.T) {
	got := Rank(map[string]int{"dora": 5, "ann": 7, "cid": 5, "bob": 7, "eve": 1, "fay": 5})
	want := []Entry{{"ann", 7}, {"bob", 7}, {"cid", 5}, {"dora", 5}, {"fay", 5}, {"eve", 1}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Rank = %v, want %v", got, want)
	}
}

type staticSource Entry

func (s staticSource) Fetch(context.Context) (Entry, error) { return Entry(s), nil }

func TestCollect(t *testing.T) {
	var srcs []Source
	for i := 0; i < 8; i++ {
		srcs = append(srcs, staticSource{Name: fmt.Sprintf("p%d", i), Score: i})
	}
	got, err := CollectAll(context.Background(), srcs)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range got {
		if e.Score != i {
			t.Fatalf("CollectAll order = %v", got)
		}
	}
}
