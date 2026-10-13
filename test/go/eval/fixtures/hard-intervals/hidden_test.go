//go:build evalhidden

package intervals

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestHiddenMerge(t *testing.T) {
	cases := []struct {
		in, want []Interval
	}{
		{[]Interval{{1, 1}, {2, 2}, {3, 3}}, []Interval{{1, 3}}},
		{[]Interval{{1, 1}, {3, 3}}, []Interval{{1, 1}, {3, 3}}},
		{[]Interval{{4, 6}, {1, 3}}, []Interval{{1, 6}}},
		{[]Interval{{1, 10}, {2, 3}, {4, 5}}, []Interval{{1, 10}}},
		{[]Interval{{1, 5}, {1, 2}}, []Interval{{1, 5}}},
		{[]Interval{{-5, -3}, {-2, 0}, {2, 2}}, []Interval{{-5, 0}, {2, 2}}},
		{[]Interval{{7, 7}}, []Interval{{7, 7}}},
		{[]Interval{{3, 4}, {3, 4}}, []Interval{{3, 4}}},
		{[]Interval{{10, 12}, {0, 2}, {6, 8}, {3, 5}}, []Interval{{0, 8}, {10, 12}}},
	}
	for _, c := range cases {
		got, err := Merge(c.in)
		if err != nil {
			t.Errorf("Merge(%v) unexpected error: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Merge(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestHiddenExtremes(t *testing.T) {
	cases := []struct {
		in, want []Interval
	}{
		{[]Interval{{math.MaxInt - 1, math.MaxInt}, {0, 1}}, []Interval{{0, 1}, {math.MaxInt - 1, math.MaxInt}}},
		{[]Interval{{0, math.MaxInt}, {5, 6}}, []Interval{{0, math.MaxInt}}},
		{[]Interval{{math.MaxInt, math.MaxInt}, {math.MaxInt - 5, math.MaxInt - 1}}, []Interval{{math.MaxInt - 5, math.MaxInt}}},
		{[]Interval{{math.MinInt, math.MinInt}, {math.MinInt + 1, 0}}, []Interval{{math.MinInt, 0}}},
		{[]Interval{{math.MinInt, math.MaxInt}, {math.MinInt, math.MaxInt}}, []Interval{{math.MinInt, math.MaxInt}}},
		{[]Interval{{math.MinInt, -1}, {1, math.MaxInt}}, []Interval{{math.MinInt, -1}, {1, math.MaxInt}}},
	}
	for _, c := range cases {
		got, err := Merge(c.in)
		if err != nil {
			t.Errorf("Merge(%v) unexpected error: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Merge(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestHiddenEmpty(t *testing.T) {
	for _, in := range [][]Interval{nil, {}} {
		got, err := Merge(in)
		if err != nil || len(got) != 0 {
			t.Errorf("Merge(%v) = %v, %v; want empty, nil", in, got, err)
		}
	}
}

func TestHiddenInputNotModified(t *testing.T) {
	in := []Interval{{5, 8}, {1, 3}, {2, 4}, {9, 9}}
	orig := append([]Interval(nil), in...)
	got, err := Merge(in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, orig) {
		t.Fatalf("Merge modified its input: %v, was %v", in, orig)
	}
	got[0].End = 1000
	if !reflect.DeepEqual(in, orig) {
		t.Fatalf("result shares memory with input: %v, was %v", in, orig)
	}
}

func TestHiddenInvalid(t *testing.T) {
	cases := [][]Interval{
		{{2, 1}},
		{{1, 3}, {5, 4}},
		{{math.MaxInt, math.MinInt}},
	}
	for _, in := range cases {
		got, err := Merge(in)
		if !errors.Is(err, ErrInvalid) || got != nil {
			t.Errorf("Merge(%v) = %v, %v; want nil, ErrInvalid", in, got, err)
		}
	}
}
