// Package webgui serves a local, dark-themed web GUI for SuperCli.
//
// It reuses the existing core packages (agent loop, providers,
// sessions, credits, goals, memory) through their public APIs, so
// the GUI is a real front-end over the same engine the TUI drives —
// not a mock. The server is pure net/http + embedded assets; no CGO,
// no new dependencies, keeping the single-binary, portable contract.
package webgui

import (
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"supercli/internal/account/pricing"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

func (e *Engine) codeIntelFor(home string) *tools.CodeIntel {
	abs, key := workspaceCacheKey(home)
	e.codeIntelMu.Lock()
	defer e.codeIntelMu.Unlock()
	if tool := e.codeIntel[key]; tool != nil {
		return tool
	}
	tool := tools.NewCodeIntel(abs)
	e.codeIntel[key] = tool
	return tool
}

func (e *Engine) processSessionFor(home string) *tools.ProcessSession {
	abs, key := workspaceCacheKey(home)
	e.processMu.Lock()
	defer e.processMu.Unlock()
	if tool := e.processes[key]; tool != nil {
		return tool
	}
	tool := tools.NewProcessSession(abs)
	e.processes[key] = tool
	return tool
}

func (e *Engine) skillDiscovererFor(home string) *tools.Discoverer {
	abs, key := workspaceCacheKey(home)
	e.skillMu.Lock()
	defer e.skillMu.Unlock()
	if discoverer := e.skillCatalog[key]; discoverer != nil {
		return discoverer
	}
	discoverer := tools.NewDiscovererWithBuiltins(abs, e.dataDir)
	e.skillCatalog[key] = discoverer
	return discoverer
}

func workspaceCacheKey(home string) (string, string) {
	abs, err := filepath.Abs(home)
	if err != nil {
		abs = filepath.Clean(home)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = resolved
	}
	key := filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return abs, key
}

func (e *Engine) recordProviderPerformance(provider string, stat llm.CallStat) {
	if e == nil || strings.TrimSpace(provider) == "" {
		return
	}
	perf := providerCallPerformance{
		Model: stat.Model, TTFTMS: stat.TTFT.Milliseconds(), DurationMS: stat.Duration.Milliseconds(),
		TokensIn: stat.TokensIn, TokensOut: stat.TokensOut,
		Failed: stat.Failed, Canceled: stat.Canceled, CompletedAt: time.Now().UTC(),
	}
	if generated := stat.Duration - stat.TTFT; stat.TokensOut > 0 && generated > 0 {
		perf.TokensPerS = float64(stat.TokensOut) / generated.Seconds()
	}
	e.perfMu.Lock()
	e.perf[provider] = perf
	e.perfMu.Unlock()
}

func (e *Engine) providerPerformance(provider string) (providerCallPerformance, bool) {
	if e == nil {
		return providerCallPerformance{}, false
	}
	e.perfMu.RLock()
	defer e.perfMu.RUnlock()
	perf, ok := e.perf[provider]
	return perf, ok
}

// RefreshPricingAsync mirrors the CLI startup policy: a fresh cache is used
// immediately and network refresh, when needed, never delays the GUI opening.
func (e *Engine) RefreshPricingAsync() {
	if e == nil || e.caps == nil {
		return
	}
	if cached := pricing.LoadCache(e.dataDir); len(cached) > 0 && pricing.HasContextMetadata(cached) {
		return
	}
	fetcher := pricing.NewFetcher(e.dataDir)
	snapshot := e.caps.All()
	go func() {
		updated := fetcher.FetchAndUpdate(snapshot)
		applyWebModelInfo(e.caps, updated)
	}()
}

func applyWebPricingEntries(caps *llm.CapabilityRegistry, entries []pricing.PriceEntry) {
	infos := make([]llm.ModelInfo, 0, len(entries))
	for _, entry := range entries {
		infos = append(infos, llm.ModelInfo{
			ID: entry.ModelID, InputCost: entry.InputPer1M, OutputCost: entry.OutputPer1M,
			ContextLength: entry.ContextLength, Source: llm.SourceExternal, LastVerified: entry.FetchedAt,
		})
	}
	applyWebModelInfo(caps, infos)
}

func applyWebModelInfo(caps *llm.CapabilityRegistry, infos []llm.ModelInfo) {
	if caps == nil {
		return
	}
	shortCounts := make(map[string]int)
	for _, info := range infos {
		if slash := strings.IndexByte(info.ID, '/'); slash > 0 && slash < len(info.ID)-1 {
			shortCounts[strings.ToLower(info.ID[slash+1:])]++
		}
	}
	for _, info := range infos {
		if info.ID == "" {
			continue
		}
		applyOneWebModelInfo(caps, info)
		if slash := strings.IndexByte(info.ID, '/'); slash > 0 && slash < len(info.ID)-1 {
			shortID := info.ID[slash+1:]
			if existing, ok := caps.Get(shortID); ok && shortCounts[strings.ToLower(shortID)] == 1 {
				alias := info
				alias.ID = shortID
				alias.Provider = existing.Provider
				applyOneWebModelInfo(caps, alias)
			}
		}
	}
}

func applyOneWebModelInfo(caps *llm.CapabilityRegistry, info llm.ModelInfo) {
	if existing, ok := caps.Get(info.ID); ok {
		if info.InputCost > 0 {
			existing.InputCost = info.InputCost
		}
		if info.OutputCost > 0 {
			existing.OutputCost = info.OutputCost
		}
		if existing.ContextLength == 0 && info.ContextLength > 0 {
			existing.ContextLength = info.ContextLength
		}
		if info.LastVerified.After(existing.LastVerified) {
			existing.LastVerified = info.LastVerified
		}
		caps.Register(existing)
		return
	}
	caps.Register(info)
}

// ModelName returns the active model id for display.
