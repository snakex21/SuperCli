package usage

import (
	"testing"
	"time"

	"meterd/ingest"
	"meterd/readings"
)

func TestPerCustomer(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2027, 2, day, 6, 0, 0, 0, time.UTC) }
	ms := []ingest.Meter{{ID: "a", Customer: "c1", Model: "EM-5"}, {ID: "b", Customer: "c1", Model: "EMX"}}
	rs := []readings.Reading{{Meter: "a", At: d(2), Value: 15}, {Meter: "a", At: d(1), Value: 10},
		{Meter: "b", At: d(1), Value: 1}, {Meter: "b", At: d(2), Value: 4}}
	got, err := PerCustomer(ms, rs)
	if err != nil || got["c1"] != 8 {
		t.Fatalf("%v %v", got, err)
	}
}
