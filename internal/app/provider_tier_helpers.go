package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/account/tier"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// tierRulesFromToml converts config.toml [[model_tiers]]
// entries into tier.Rule values.
func tierRulesFromToml(t config.TomlConfig) []tier.Rule {
	if len(t.ModelTiers) == 0 {
		return nil
	}
	out := make([]tier.Rule, 0, len(t.ModelTiers))
	for _, r := range t.ModelTiers {
		out = append(out, tier.Rule{Pattern: r.Pattern, Tier: r.Tier})
	}
	return out
}

// pickSmallestSmallTierModel scans the capability registry for
// the model with the smallest parsed (active) parameter count
// that classifies as small-tier. Used as the second priority
// for draft-model selection (after an explicit --draft-model /
// config, before the price-based fallback).
func pickSmallestSmallTierModel(caps *llm.CapabilityRegistry, exclude string, rules []tier.Rule) (string, bool) {
	best := ""
	bestParams := 0.0
	for _, m := range caps.All() {
		if m.ID == exclude {
			continue
		}
		params, ok := tier.ParseParams(m.ID)
		if !ok {
			continue
		}
		if tier.Classify(m.ID, m.InputCost, m.OutputCost, rules) != tier.Small {
			continue
		}
		if best == "" || params < bestParams {
			best, bestParams = m.ID, params
		}
	}
	return best, best != ""
}

// compactNum formats large numbers as "1.2k" etc.
func compactNum(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// shortenDir renders an absolute directory for the status header: the user
// home prefix collapses to "~", and an over-long tail keeps only the last
// two path segments so the header stays on one line.
func shortenDir(p string) string {
	if p == "" {
		return ""
	}
	if uh, err := os.UserHomeDir(); err == nil && uh != "" {
		if rel, err := filepath.Rel(uh, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			p = "~" + string(filepath.Separator) + rel
		} else if err == nil && rel == "." {
			return "~"
		}
	}
	if len(p) <= 40 {
		return p
	}
	parts := strings.Split(filepath.ToSlash(p), "/")
	if len(parts) > 2 {
		return "…/" + strings.Join(parts[len(parts)-2:], "/")
	}
	return p
}
