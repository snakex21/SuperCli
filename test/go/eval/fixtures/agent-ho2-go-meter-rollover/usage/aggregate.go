// Package usage turns raw readings into consumption per customer.
package usage

import (
	"fmt"
	"sort"

	"meterd/ingest"
	"meterd/meter"
	"meterd/readings"
)

// PerCustomer sums the consumption (kWh) of every customer's meters.
func PerCustomer(meters []ingest.Meter, rs []readings.Reading) (map[string]int64, error) {
	byMeter := readings.ByMeter(rs)
	ids := make([]string, 0, len(meters))
	info := map[string]ingest.Meter{}
	for _, m := range meters {
		ids = append(ids, m.ID)
		info[m.ID] = m
	}
	sort.Strings(ids)
	out := map[string]int64{}
	for _, id := range ids {
		m := info[id]
		series := byMeter[id]
		readings.SortByTime(series)
		ds, err := readings.Deltas(series, meter.Modulus(m.Model))
		if err != nil {
			return nil, fmt.Errorf("usage: meter %s: %w", id, err)
		}
		out[m.Customer] += readings.Sum(ds)
	}
	return out, nil
}
