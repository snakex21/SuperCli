package providers

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func (m *Manager) SetCodexAuthOptions(opts codexauth.Options) {
	m.mu.Lock()
	m.codexAuthOptions = opts.WithDefaults()
	m.mu.Unlock()
}

func (m *Manager) CodexAuthOptions() codexauth.Options {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.codexAuthOptions.WithDefaults()
}

// EnsureCodexProvider exposes saved ChatGPT logins in the provider picker.
// Existing entries (including disabled ones) are respected, and no model is
// selected or guessed. A name collision never overwrites an API provider.
func (m *Manager) EnsureCodexProvider() (string, error) {
	labels, err := codexauth.ListAccounts(m.home)
	if err != nil {
		return "", err
	}
	hasLogin := false
	for _, label := range labels {
		if codexauth.NewManagerFor(m.home, label, codexauth.Options{}).LoggedIn() {
			hasLogin = true
			break
		}
	}
	if !hasLogin {
		return "", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	used := make(map[string]bool)
	for _, p := range m.providers {
		if p.Type == config.ProviderCodex {
			return p.Name, nil
		}
		used[strings.ToLower(p.Name)] = true
	}
	name := "codex"
	for suffix := 2; used[strings.ToLower(name)]; suffix++ {
		name = fmt.Sprintf("codex-%d", suffix)
	}
	opts := m.codexAuthOptions.WithDefaults()
	m.providers = append(m.providers, config.ProviderConf{Name: name, Type: config.ProviderCodex, BaseURL: opts.BackendURL})
	if err := m.saveLocked(); err != nil {
		m.providers = m.providers[:len(m.providers)-1]
		return "", err
	}
	return name, nil
}

// codexLoginIdentity invalidates cached account choices after login, logout or
// token replacement. Neither the token nor the account id is persisted here.
func codexLoginIdentity(mgr *codexauth.Manager) (string, error) {
	return mgr.CatalogIdentity()
}

// CodexAccountModelAvailability excludes an account only when its current
// login has a fresh server catalog proving the selected model absent. Unknown
// or stale evidence permits the existing backend validation/failover path.
func CodexAccountModelAvailability(dataDir, backendURL string, mgr *codexauth.Manager, model string) (available, known bool) {
	identity, err := codexLoginIdentity(mgr)
	if err != nil {
		return false, false
	}
	return llm.CodexModelCacheAvailability(dataDir, backendURL, identity, model)
}

func (m *Manager) codexManagers(p config.ProviderConf) ([]*codexauth.Manager, string, error) {
	m.mu.RLock()
	opts := m.codexAuthOptions.WithDefaults()
	m.mu.RUnlock()
	if p.BaseURL != "" {
		opts.BackendURL = p.BaseURL
	}
	labels, err := codexauth.ListAccounts(m.home)
	if err != nil {
		return nil, opts.BackendURL, err
	}
	var managers []*codexauth.Manager
	for _, label := range labels {
		mgr := codexauth.NewManagerFor(m.home, label, opts)
		if mgr.LoggedIn() {
			managers = append(managers, mgr)
		}
	}
	return managers, opts.BackendURL, nil
}

// LoadCodexModels loads metadata for current saved logins without network I/O.
// Old static/config-only inventories are not treated as account availability.
func (m *Manager) LoadCodexModels(caps *llm.CapabilityRegistry) int {
	total := 0
	for _, p := range m.Configured() {
		if p.Disabled || p.Type != config.ProviderCodex {
			continue
		}
		managers, backendURL, _ := m.codexManagers(p)
		var models []llm.CodexModel
		for _, mgr := range managers {
			identity, err := codexLoginIdentity(mgr)
			if err != nil {
				continue
			}
			if cached, _, ok := llm.ReadCodexModelCache(m.home, backendURL, identity); ok {
				models = append(models, cached...)
			}
		}
		ids := llm.RegisterCodexModels(caps, p.Name, backendURL, models)
		m.mu.Lock()
		for i := range m.providers {
			if m.providers[i].Name == p.Name {
				m.providers[i].CachedModels = normalizeModelIDs(ids)
			}
		}
		m.mu.Unlock()
		total += len(ids)
	}
	return total
}

// RefreshCodexModels refreshes only configured Codex providers. The cache TTL
// and scan mutex bound automatic picker refreshes without per-completion work.
func (m *Manager) RefreshCodexModels(ctx context.Context, caps *llm.CapabilityRegistry) int {
	total := 0
	for _, p := range m.Configured() {
		if p.Disabled || p.Type != config.ProviderCodex {
			continue
		}
		res := m.scanCodexProvider(ctx, p, caps, false)
		if res.Err == nil {
			m.cacheProviderModels(p.Name, res.Models)
			total += len(res.Models)
		}
	}
	return total
}

func (m *Manager) scanCodexProvider(ctx context.Context, p config.ProviderConf, caps *llm.CapabilityRegistry, force bool) ScanResult {
	m.codexScanMu.Lock()
	defer m.codexScanMu.Unlock()
	res := ScanResult{Provider: p.Name}
	if caps == nil {
		res.Err = fmt.Errorf("capability registry is not available")
		return res
	}
	managers, backendURL, err := m.codexManagers(p)
	if err != nil {
		res.Err = err
		return res
	}
	if len(managers) == 0 {
		res.Err = fmt.Errorf("codex models: not logged in — run /login first")
		return res
	}
	m.mu.RLock()
	client := m.codexHTTPClient
	m.mu.RUnlock()
	ctx, cancel := context.WithTimeout(ctx, llm.ProviderDiscoveryTimeout)
	defer cancel()
	var models []llm.CodexModel
	succeeded := 0
	for _, mgr := range managers {
		identity, err := codexLoginIdentity(mgr)
		if err != nil {
			continue
		}
		cached, _, ok := llm.ReadCodexModelCache(m.home, backendURL, identity)
		if !force && !llm.CodexCatalogRefreshDue(m.home, backendURL, identity) {
			if ok {
				models = append(models, cached...)
				succeeded++
			} else {
				res.Err = fmt.Errorf("codex models: last automatic discovery failed; use an explicit scan to retry")
			}
			continue
		}
		fresh, err := llm.ListCodexModels(ctx, llm.CodexCatalogConfig{BackendURL: backendURL, Tokens: mgr, HTTPClient: client})
		if err != nil {
			_ = llm.MarkCodexCatalogAttempt(m.home, backendURL, identity)
			res.Err = err
			// Preserve previously observed metadata for this same login offline.
			if ok {
				models = append(models, cached...)
			}
			continue
		}
		succeeded++
		models = append(models, fresh...)
		// A 401 may have refreshed tokens; bind the cache to the current login.
		if identity, err := codexLoginIdentity(mgr); err == nil {
			_ = llm.SaveCodexModelCache(m.home, backendURL, identity, fresh)
		}
	}
	res.Models = llm.RegisterCodexModels(caps, p.Name, backendURL, models)
	if succeeded > 0 {
		res.Err = nil // one unavailable account does not hide successful accounts
	}
	return res
}
