package readings

import (
	"testing"
	"time"
)

func at(day int) time.Time { return time.Date(2027, 1, day, 0, 0, 0, 0, time.UTC) }

func TestDeltasWrap(t *testing.T) {
	rs := []Reading{{Value: 99990, At: at(1)}, {Value: 5, At: at(2)}, {Value: 20, At: at(3)}}
	ds, err := Deltas(rs, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if Sum(ds) != 30 {
		t.Fatalf("Sum = %d", Sum(ds))
	}
}

func TestDeltasCorrupt(t *testing.T) {
	rs := []Reading{{Value: 900, At: at(1)}, {Value: 100, At: at(2)}}
	if _, err := Deltas(rs, 500); err == nil {
		t.Fatal("want error")
	}
}
