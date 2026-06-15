// Learned context limits: when a provider rejects a request
// with a context-length error, the limit it reported is
// persisted per model so future sessions size their
// auto-compaction correctly.
package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// learnedLimits is a tiny JSON-backed model→tokens map stored
// at <dataDir>/context_limits.json. Safe for concurrent
// use.
type learnedLimits struct {
	mu     sync.Mutex
	path   string
	limits map[string]int
}

func loadLearnedLimits(dataDir string) *learnedLimits {
	l := &learnedLimits{
		path:   filepath.Join(dataDir, "context_limits.json"),
		limits: map[string]int{},
	}
	data, err := os.ReadFile(l.path)
	if err == nil {
		_ = json.Unmarshal(data, &l.limits) // defensive: corrupt file = empty map
		if l.limits == nil {
			l.limits = map[string]int{}
		}
	}
	return l
}

// Get returns the learned limit for model, or 0.
func (l *learnedLimits) Get(model string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limits[model]
}

// Learn records limit for model and persists. Best-effort: a
// write failure loses the persistence, not the in-memory value.
func (l *learnedLimits) Learn(model string, limit int) {
	if model == "" || limit <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limits[model] = limit
	data, err := json.MarshalIndent(l.limits, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(l.path), 0o755)
	_ = os.WriteFile(l.path, data, 0o644)
}
