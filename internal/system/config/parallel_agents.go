package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DefaultMaxParallelAgents is the built-in cap on concurrently running
// delegated workers (the value used before it became configurable).
const DefaultMaxParallelAgents = 6

// HighParallelAgents is the level above which the settings warn about
// rate limits (HTTP 429), cost and local servers that serialize requests.
const HighParallelAgents = 8

// ParseMaxParallelAgents reads a max_parallel_agents setting value:
// "" or "default" = built-in default (nil); "0", "unlimited", "none",
// "bez limitu", "∞" or "inf" = no limit (0); otherwise a positive number.
func ParseMaxParallelAgents(s string) (*int, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "", "default", "domyślnie", "domyslnie":
		return nil, nil
	case "0", "unlimited", "none", "no limit", "bez limitu", "∞", "inf", "off":
		n := 0
		return &n, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("bad value %q (want a number, 0 or unlimited for no limit, or empty for the default %d)", s, DefaultMaxParallelAgents)
	}
	return &n, nil
}

// EffectiveMaxParallelAgents is the cap in force (0 = unlimited).
func EffectiveMaxParallelAgents(p *int) int {
	if p == nil {
		return DefaultMaxParallelAgents
	}
	if *p < 0 {
		return DefaultMaxParallelAgents
	}
	return *p
}

// ParseProviderAgentLimits reads "provider=N; provider2=N" (";" or ","
// separated). An empty string clears all per-provider caps.
func ParseProviderAgentLimits(s string) (map[string]int, error) {
	out := map[string]int{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || r == '\n' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, num, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("bad entry %q (want provider=number)", part)
		}
		limit, err := ParseMaxParallelAgents(num)
		if err != nil || limit == nil {
			return nil, fmt.Errorf("bad limit in %q (want provider=number, 0 = no limit)", part)
		}
		out[name] = *limit
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// FormatProviderAgentLimits renders per-provider caps as "a=1; b=4".
func FormatProviderAgentLimits(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", name, m[name]))
	}
	return strings.Join(parts, "; ")
}

// ParallelAgentsHigh reports whether the configured cap deserves the
// rate-limit / cost / local-server warning (unlimited or above
// HighParallelAgents).
func ParallelAgentsHigh(p *int) bool {
	n := EffectiveMaxParallelAgents(p)
	return n == 0 || n > HighParallelAgents
}
