package webgui

import (
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// An auth action updates future runs without replacing a live loop snapshot.
// A simultaneous model switch wins over this rebuild.
func (e *Engine) reloadCodexAccounts() error {
	e.mu.RLock()
	cfg, before := e.cfg, e.prov
	e.mu.RUnlock()
	if cfg.Provider != config.ProviderCodex {
		return nil
	}
	tc, _ := config.ResolveConfig(e.dataDir, e.Home(), "")
	e.factory.SetCodexAuthOptions(e.providerManager().CodexAuthOptions())
	replacement, err := e.factory.BuildChain(cfg, tc, llm.PurposeMain)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.prov == before {
		e.prov = replacement
	}
	return nil
}
