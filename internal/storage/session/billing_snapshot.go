package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// PriceSnapshot is an immutable quote in USD. It stores no prompts, endpoint
// URLs or credentials. Unknown and subscription/local states intentionally
// retain a nil amount rather than fabricating a billable zero.
type PriceSnapshot struct {
	State                 string   `json:"state"`
	AmountUSD             *float64 `json:"amount_usd"`
	Source                string   `json:"source"`
	InputPerMillion       float64  `json:"input_per_million"`
	CachedInputPerMillion float64  `json:"cached_input_per_million"`
	OutputPerMillion      float64  `json:"output_per_million"`
	CacheKnown            bool     `json:"cache_known"`
	Legacy                bool     `json:"legacy"`
	PriceDate             string   `json:"price_date"`
	UsageDay              string   `json:"usage_day"`
}

func encodePriceSnapshot(snapshot *PriceSnapshot, created time.Time) (string, error) {
	if snapshot == nil {
		return "", nil
	}
	out := *snapshot
	if out.PriceDate == "" {
		out.PriceDate = time.Now().UTC().Format("2006-01-02")
	}
	if out.UsageDay == "" {
		out.UsageDay = created.In(time.Local).Format("2006-01-02")
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("session price snapshot: %w", err)
	}
	return string(data), nil
}

func decodePriceSnapshot(raw sql.NullString) (*PriceSnapshot, error) {
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var snapshot *PriceSnapshot
	if err := json.Unmarshal([]byte(raw.String), &snapshot); err != nil {
		return nil, fmt.Errorf("session price snapshot: %w", err)
	}
	return snapshot, nil
}
