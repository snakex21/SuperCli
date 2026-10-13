package intervals

import (
	"reflect"
	"testing"
)

func TestMergeBasic(t *testing.T) {
	cases := []struct {
		in, want []Interval
	}{
		{[]Interval{{6, 8}, {1, 3}, {2, 4}}, []Interval{{1, 4}, {6, 8}}},
		{[]Interval{{1, 3}, {4, 6}}, []Interval{{1, 6}}},
		{[]Interval{{1, 3}, {5, 6}}, []Interval{{1, 3}, {5, 6}}},
	}
	for _, c := range cases {
		got, err := Merge(c.in)
		if err != nil {
			t.Fatalf("Merge(%v) unexpected error: %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Merge(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
