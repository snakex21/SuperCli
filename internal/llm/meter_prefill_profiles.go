package llm

// Adaptive prompt-prefill profiles.
//
// A model's advertised context window is a correctness limit, not a latency
// budget. Two servers exposing the same model can prefill at radically
// different speeds, and either server may be reached through localhost, a LAN
// address, a public URL, or a reverse proxy. These profiles therefore learn
// only from measured calls and are scoped by configured provider identity plus
// model ID. No local/remote classification participates in the decision.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	prefillProfilesFile = "prefill-profiles.json"

	// Fifteen seconds is the latency objective for the first model delta. It is
	// deliberately not a context limit: fast/cache-effective backends never
	// activate a smaller budget, while slow backends learn one from throughput.
	prefillTargetTTFT = 15 * time.Second
	// One call taking twice the target is enough evidence to protect the next
	// step immediately. Less severe slowdowns require repeated samples.
	prefillExtremeTTFT = 2 * prefillTargetTTFT
	// Below this size there is too little useful history to trade away. The hard
	// model window can still clamp this lower for genuinely small contexts.
	prefillMinBudgetTokens = 8_000
	// Ignore tiny probes/greetings: fixed network and scheduler latency dominates
	// their tokens/second ratio and says little about prompt processing.
	prefillMinSampleTokens = 4_000
)

// PrefillSample is one successful foreground model call.
type PrefillSample struct {
	InputTokens  int
	CachedTokens int
	TTFT         time.Duration
}

// PrefillProfile is persisted in the portable SuperCli data directory. The
// EWMA candidate is a total-prompt budget predicted to meet prefillTargetTTFT.
type PrefillProfile struct {
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	BudgetTokens    int     `json:"budget_tokens,omitempty"`
	CandidateTokens float64 `json:"candidate_tokens,omitempty"`
	Samples         int     `json:"samples"`
	SlowSamples     int     `json:"slow_samples,omitempty"`
	FastSamples     int     `json:"fast_samples,omitempty"`
	LastInputTokens int     `json:"last_input_tokens,omitempty"`
	LastCached      int     `json:"last_cached_tokens,omitempty"`
	LastEvaluated   int     `json:"last_evaluated_tokens,omitempty"`
	LastTokensPerS  float64 `json:"last_tokens_per_second,omitempty"`
	LastTTFTMS      int64   `json:"last_ttft_ms,omitempty"`
	UpdatedAt       int64   `json:"updated_at,omitempty"`
}

type prefillProfileFile struct {
	Version int              `json:"version"`
	Entries []PrefillProfile `json:"entries"`
}

// PrefillProfiles is a small concurrency-safe JSON store shared by the TUI,
// WebGUI, batch runs, coordinators, and workers in one SuperCli installation.
type PrefillProfiles struct {
	mu      sync.RWMutex
	path    string
	entries map[string]PrefillProfile
}

func LoadPrefillProfiles(dataDir string) *PrefillProfiles {
	s := &PrefillProfiles{
		path:    filepath.Join(dataDir, prefillProfilesFile),
		entries: make(map[string]PrefillProfile),
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var file prefillProfileFile
	if json.Unmarshal(data, &file) != nil {
		return s
	}
	for _, entry := range file.Entries {
		entry.Provider = strings.TrimSpace(entry.Provider)
		entry.Model = strings.TrimSpace(entry.Model)
		if entry.Provider == "" || entry.Model == "" {
			continue
		}
		s.entries[prefillProfileKey(entry.Provider, entry.Model)] = entry
	}
	return s
}

func prefillProfileKey(provider, model string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.TrimSpace(model)
}

// Budget returns an active learned prompt budget. hardLimit is the ordinary
// window-minus-generation-reserve threshold; the profile can only reduce it.
func (s *PrefillProfiles) Budget(provider, model string, hardLimit int) (int, bool) {
	if s == nil || hardLimit <= 0 {
		return 0, false
	}
	s.mu.RLock()
	entry, ok := s.entries[prefillProfileKey(provider, model)]
	s.mu.RUnlock()
	if !ok || entry.BudgetTokens <= 0 || entry.BudgetTokens >= hardLimit {
		return 0, false
	}
	return entry.BudgetTokens, true
}

// Profile returns a copy for diagnostics and tests.
func (s *PrefillProfiles) Profile(provider, model string) (PrefillProfile, bool) {
	if s == nil {
		return PrefillProfile{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[prefillProfileKey(provider, model)]
	return entry, ok
}

// Observe updates the profile from one successful call. It returns the new
// profile and whether the active budget changed.
func (s *PrefillProfiles) Observe(provider, model string, sample PrefillSample) (PrefillProfile, bool) {
	if s == nil || sample.InputTokens < prefillMinSampleTokens || sample.TTFT <= 0 {
		return PrefillProfile{}, false
	}
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return PrefillProfile{}, false
	}
	if sample.CachedTokens < 0 {
		sample.CachedTokens = 0
	}
	if sample.CachedTokens > sample.InputTokens {
		sample.CachedTokens = sample.InputTokens
	}

	// Predict the total prompt that would meet the target at this call's
	// measured throughput. A reported cached prefix is added back because it
	// did not consume prefill work; when cache telemetry is absent the whole
	// prompt is conservatively treated as evaluated.
	evaluated := sample.InputTokens - sample.CachedTokens
	if evaluated < 1 {
		evaluated = 1
	}
	ratio := float64(prefillTargetTTFT) / float64(sample.TTFT)
	candidate := float64(sample.CachedTokens) + float64(evaluated)*ratio
	if candidate < prefillMinBudgetTokens {
		candidate = prefillMinBudgetTokens
	}
	// Corrupt clocks or a near-zero TTFT must not create an absurd persisted
	// value. The hard context limit applies later; this is only a file guard.
	if candidate > 10_000_000 {
		candidate = 10_000_000
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := prefillProfileKey(provider, model)
	entry := s.entries[key]
	entry.Provider = provider
	entry.Model = model
	oldBudget := entry.BudgetTokens
	entry.Samples++
	if sample.TTFT > prefillTargetTTFT {
		entry.SlowSamples++
		entry.FastSamples = 0
	} else if sample.TTFT < prefillTargetTTFT/2 {
		entry.FastSamples++
	} else {
		entry.FastSamples = 0
	}
	// Slow calls carry more information about the dangerous side of the
	// distribution, so move toward them faster than toward fast samples.
	alpha := 0.2
	if sample.TTFT > prefillTargetTTFT {
		alpha = 0.45
	}
	if entry.CandidateTokens <= 0 {
		entry.CandidateTokens = candidate
	} else {
		entry.CandidateTokens = (1-alpha)*entry.CandidateTokens + alpha*candidate
	}

	proposed := int(math.Round(entry.CandidateTokens))
	extreme := sample.TTFT >= prefillExtremeTTFT
	switch {
	case entry.BudgetTokens == 0 && (extreme || (entry.Samples >= 3 && entry.SlowSamples >= 2)):
		entry.BudgetTokens = proposed
	case entry.BudgetTokens > 0 && sample.TTFT > prefillTargetTTFT:
		// Lower promptly, but avoid a single moderate sample cutting more than
		// 25%. Extreme calls may apply the measured candidate immediately.
		lowered := int(math.Round(float64(entry.BudgetTokens)*0.75 + float64(proposed)*0.25))
		if extreme {
			lowered = proposed
		}
		if lowered < entry.BudgetTokens {
			entry.BudgetTokens = lowered
		}
	case entry.BudgetTokens > 0 && entry.FastSamples >= 3 && sample.InputTokens >= entry.BudgetTokens*3/4:
		// A sustained fast edge permits gradual growth. Large one-step jumps
		// would oscillate between long prefills and destructive rewrites.
		raised := entry.BudgetTokens + maxInt(1_000, entry.BudgetTokens/8)
		if raised > proposed {
			raised = proposed
		}
		if raised > entry.BudgetTokens {
			entry.BudgetTokens = raised
		}
		entry.FastSamples = 0
	}
	if entry.BudgetTokens > 0 && entry.BudgetTokens < prefillMinBudgetTokens {
		entry.BudgetTokens = prefillMinBudgetTokens
	}
	entry.LastInputTokens = sample.InputTokens
	entry.LastCached = sample.CachedTokens
	entry.LastEvaluated = evaluated
	entry.LastTokensPerS = float64(evaluated) / sample.TTFT.Seconds()
	entry.LastTTFTMS = sample.TTFT.Milliseconds()
	entry.UpdatedAt = time.Now().UTC().Unix()
	s.entries[key] = entry
	s.saveLocked()
	return entry, oldBudget != entry.BudgetTokens
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *PrefillProfiles) saveLocked() {
	file := prefillProfileFile{Version: 1, Entries: make([]PrefillProfile, 0, len(s.entries))}
	for _, entry := range s.entries {
		file.Entries = append(file.Entries, entry)
	}
	sort.Slice(file.Entries, func(i, j int) bool {
		if file.Entries[i].Provider != file.Entries[j].Provider {
			return file.Entries[i].Provider < file.Entries[j].Provider
		}
		return file.Entries[i].Model < file.Entries[j].Model
	})
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(s.path), 0o755) != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0o644)
}
