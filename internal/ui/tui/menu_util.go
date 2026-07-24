package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/storage/goal"
)

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (m Model) filteredModelRows() []llm.ModelInfo {
	rows := []llm.ModelInfo{}
	if m.modelLister != nil {
		rows = m.modelLister.ListModels()
	}
	if len(rows) == 0 && m.caps != nil {
		rows = m.caps.All()
	}
	// Merge the provider's persisted last-known inventory. This keeps local
	// and remote self-hosted models available after a restart even when their
	// server is currently offline. Runtime registry rows win on duplicates.
	if m.providerMgr != nil {
		seen := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			seen[row.Provider+"\x00"+row.ID] = struct{}{}
		}
		for _, p := range m.providerMgr.ListConfigured(m.caps) {
			for _, row := range p.Models {
				key := row.Provider + "\x00" + row.ID
				if _, ok := seen[key]; ok {
					continue
				}
				rows = append(rows, row)
				seen[key] = struct{}{}
			}
		}
	}

	// Only show models from configured providers —
	// hide seed/hardcoded models (e.g. gpt-4o-mini)
	// unless their provider is in the [[providers]] list.
	if m.providerMgr != nil {
		catalog := m.menu.kind != menuModels
		visible := func(provider, id string) bool {
			if catalog {
				return m.providerMgr.ModelCatalogVisible(provider, id)
			}
			return m.providerMgr.ModelVisible(provider, id)
		}
		configured := m.configuredProviderNames()
		if len(configured) > 0 {
			filtered := make([]llm.ModelInfo, 0, len(rows))
			for _, r := range rows {
				// Once providers are configured, do not display embedded
				// seed models as if they were available from that API key.
				// The scanner must confirm models via /v1/models first.
				if r.Source == llm.SourceSeed {
					continue
				}
				if !visible(r.Provider, r.ID) {
					continue
				}
				for _, name := range configured {
					if r.Provider == name {
						filtered = append(filtered, r)
						break
					}
				}
			}
			// A4 guard: if the provider-name filter would leave
			// the picker EMPTY (registry entries lost their
			// Provider field, or the startup scan failed), fall
			// back to all non-seed rows. An imperfect list beats
			// a blank menu that looks like data loss.
			if len(filtered) == 0 {
				for _, r := range rows {
					if r.Source != llm.SourceSeed && visible(r.Provider, r.ID) {
						filtered = append(filtered, r)
					}
				}
			}
			rows = filtered
		}
	}

	if m.menu.provider != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if m.providerMgr != nil && !m.providerMgr.ModelCatalogVisible(r.Provider, r.ID) {
				continue
			}
			if r.Provider == m.menu.provider {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	// The fast /model picker hides disabled models. The complete /models
	// catalog and per-provider view keep them visible so the user can turn
	// them back on without editing config.toml.
	if m.menu.kind == menuModels && m.providerMgr != nil {
		filtered := rows[:0]
		for _, r := range rows {
			if !m.providerMgr.ModelVisible(r.Provider, r.ID) {
				continue
			}
			if !m.providerMgr.IsHiddenFor(r.Provider, r.ID) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	q := strings.ToLower(m.menu.filter)
	if q != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if fuzzy(strings.ToLower(r.ID+" "+r.Provider), q) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Provider == rows[j].Provider {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Provider < rows[j].Provider
	})
	return rows
}

// providerRows returns the configured providers WITHOUT network
// probes — this runs in the render path on every keypress, so it
// must stay cheap. Live status comes from m.providerStatuses
// (filled by async pings).
func (m Model) providerRows() []providers.ProviderInfo {
	if m.providerMgr == nil {
		return nil
	}
	return m.providerMgr.ListConfigured(m.caps)
}

// configuredProviderNames returns the names of all providers
// in the [[providers]] list. Used to filter seed models
// so only models from user-configured providers appear.
func (m Model) configuredProviderNames() []string {
	if m.providerMgr == nil {
		return nil
	}
	return m.providerMgr.Names()
}

func (m Model) goalTaskRows() []goal.Task {
	if m.goalSvc == nil {
		return nil
	}
	rows, _ := m.goalSvc.ListTasks(context.Background(), "")
	return rows
}

func fuzzy(haystack, needle string) bool {
	i := 0
	for _, r := range haystack {
		if i < len(needle) && byte(r) == needle[i] {
			i++
		}
	}
	return i == len(needle)
}
func caps(m llm.ModelInfo) string {
	parts := []string{}
	if m.Vision {
		parts = append(parts, "vision")
	}
	if m.Reasoning {
		parts = append(parts, "reasoning")
	}
	if m.ToolUse {
		parts = append(parts, "tools")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}
func price(v float64) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("$%.2f", v)
}
func ctxLen(n int) string {
	if n <= 0 {
		return "-"
	}
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
