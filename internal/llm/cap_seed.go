package llm

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// seedJSON is the embedded model catalog. Edit this
// file to add new models to the default knowledge.
// The binary picks it up on next compile.
//
// The file format is a JSON array of ModelInfo
// records. Validation rules (enforced by
// LoadSeed):
//
//   - id is non-empty
//   - ids are unique
//   - input_cost / output_cost are >= 0
//   - context_length is >= 0
//
//go:embed capabilities_seed.json
var seedJSON []byte

// LoadSeed returns the embedded seed catalog as a
// slice of ModelInfo. The slice is sorted by ID for
// stable order. The Source field is set to SourceSeed
// for every entry.
func LoadSeed() ([]ModelInfo, error) {
	var raw []ModelInfo
	if err := json.Unmarshal(seedJSON, &raw); err != nil {
		return nil, fmt.Errorf("llm.LoadSeed: parse: %w", err)
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]ModelInfo, 0, len(raw))
	for i, m := range raw {
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" {
			return nil, fmt.Errorf("llm.LoadSeed: entry %d has empty id", i)
		}
		if _, dup := seen[m.ID]; dup {
			return nil, fmt.Errorf("llm.LoadSeed: duplicate id %q", m.ID)
		}
		seen[m.ID] = struct{}{}
		if m.InputCost < 0 {
			return nil, fmt.Errorf("llm.LoadSeed: %q has negative input_cost", m.ID)
		}
		if m.OutputCost < 0 {
			return nil, fmt.Errorf("llm.LoadSeed: %q has negative output_cost", m.ID)
		}
		if m.ContextLength < 0 {
			return nil, fmt.Errorf("llm.LoadSeed: %q has negative context_length", m.ID)
		}
		m.Source = SourceSeed
		// Seed entries have no LastVerified.
		m.LastVerified = time.Time{}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// seedOnce is a memoized LoadSeed() for the common
// path. Tests can call LoadSeed() directly to get a
// fresh slice.
var (
	seedOnce    []ModelInfo
	seedOnceErr error
	seedOnceMu  sync.Mutex
)

func loadSeedOnce() ([]ModelInfo, error) {
	seedOnceMu.Lock()
	defer seedOnceMu.Unlock()
	if seedOnce != nil || seedOnceErr != nil {
		return seedOnce, seedOnceErr
	}
	seedOnce, seedOnceErr = LoadSeed()
	return seedOnce, seedOnceErr
}
