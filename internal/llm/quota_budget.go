// Daily request budget: a tiny per-endpoint counter persisted across
// runs, so every front-end can see how much of a metered provider's
// daily allowance the session has already spent.
//
// Motivation: OpenCode Zen's free tier allows ~100 completion requests
// per UTC day. An agentic loop spends those fast — each tool-call
// round-trip is its own request, and auxiliary calls (compaction,
// council sampling, draft verification) bill against the same pool.
// Without a counter the only signal is the provider's own 429, which
// arrives after the budget is gone. With one, the UI can show "37/100
// today" and features can degrade gracefully BEFORE the wall.
//
// The store is deliberately dumb: one JSON file, one mutex, UTC-day
// rollover on touch. Counts are keyed by base URL because that is the
// identity a transport has (see model_unavailable.go for the same
// reasoning). Local endpoints (LM Studio/Ollama) are counted too —
// harmless, and the number is occasionally interesting.
package llm

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RequestBudgetFileName lives in the data dir next to
// context_limits.json and pricing_cache.json.
const RequestBudgetFileName = "request_budget.json"

type requestBudget struct {
	mu     sync.Mutex
	path   string
	day    string // UTC "2006-01-02" the counts belong to
	counts map[string]int
}

var globalBudget struct {
	sync.Mutex
	b *requestBudget
}

// InitRequestBudget points the process-wide counter at
// <dataDir>/request_budget.json and loads today's counts. Safe to
// call again (re-points and reloads); safe to never call, in which
// case counting is a no-op and ProviderRequestsToday reports 0.
func InitRequestBudget(dataDir string) {
	if dataDir == "" {
		return
	}
	b := &requestBudget{
		path:   filepath.Join(dataDir, RequestBudgetFileName),
		counts: map[string]int{},
	}
	if data, err := os.ReadFile(b.path); err == nil {
		var saved struct {
			Day    string         `json:"day"`
			Counts map[string]int `json:"counts"`
		}
		if json.Unmarshal(data, &saved) == nil {
			b.day = saved.Day
			b.counts = saved.Counts
		}
	}
	globalBudget.Lock()
	globalBudget.b = b
	globalBudget.Unlock()
}

func budgetDay() string { return time.Now().UTC().Format("2006-01-02") }

// noteProviderRequest records one completion request dispatched to
// baseURL. Called from transports right after the server accepted an
// HTTP request (transport-level failures never reach the provider and
// must not consume quota).
func noteProviderRequest(baseURL string) {
	globalBudget.Lock()
	b := globalBudget.b
	globalBudget.Unlock()
	if b == nil || baseURL == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	today := budgetDay()
	if b.day != today {
		b.day = today
		b.counts = map[string]int{}
	}
	b.counts[baseURL]++
	data, err := json.MarshalIndent(struct {
		Day    string         `json:"day"`
		Counts map[string]int `json:"counts"`
	}{Day: b.day, Counts: b.counts}, "", "  ")
	if err != nil {
		return
	}
	tmp := b.path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, b.path)
	}
}

// ProviderRequestsToday reports how many requests went to baseURL
// today (UTC). 0 also covers "counter not initialized".
func ProviderRequestsToday(baseURL string) int {
	globalBudget.Lock()
	b := globalBudget.b
	globalBudget.Unlock()
	if b == nil || baseURL == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.day != budgetDay() {
		return 0
	}
	return b.counts[baseURL]
}

// ProviderRequestsTodayFlexible resolves today's count for endpoint,
// which may be a full base URL or a bare host. Exact URL match wins;
// otherwise every counted endpoint whose URL host equals the given
// one (case-insensitive) is summed. Front-ends hold different slices
// of the same identity — transports know full URLs, session records
// often only a host — and both must read the same number.
func ProviderRequestsTodayFlexible(endpoint string) int {
	if n := ProviderRequestsToday(endpoint); n > 0 {
		return n
	}
	// A bare host ("opencode.ai") parses as a path, not a URL — give
	// it a scheme so Hostname() sees what the caller meant.
	normalized := strings.TrimSpace(endpoint)
	if normalized != "" && !strings.Contains(normalized, "://") {
		normalized = "https://" + normalized
	}
	u, err := url.Parse(normalized)
	if err != nil || u.Hostname() == "" {
		return 0
	}
	host := strings.ToLower(u.Hostname())
	globalBudget.Lock()
	b := globalBudget.b
	globalBudget.Unlock()
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.day != budgetDay() {
		return 0
	}
	total := 0
	for counted, n := range b.counts {
		cu, err := url.Parse(counted)
		if err != nil {
			continue
		}
		if strings.ToLower(cu.Hostname()) == host {
			total += n
		}
	}
	return total
}

// ResetRequestBudgetForTest clears the process-wide counter. Tests only.
func ResetRequestBudgetForTest() {
	globalBudget.Lock()
	globalBudget.b = nil
	globalBudget.Unlock()
}
