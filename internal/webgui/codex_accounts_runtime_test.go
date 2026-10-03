package webgui

import (
	"errors"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/system/config"
	"testing"
)

func TestCodexAuthRebuildKeepsRunningSnapshotAndNewModelSelection(t *testing.T) {
	srv := newTestServer(t, false)
	e := srv.eng
	e.cfg.Provider = config.ProviderCodex
	e.cfg.Model = "future-codex"
	before := e.prov
	var builds int
	e.factory = factory.New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		builds++
		if cfg.Model != "future-codex" {
			t.Fatalf("changed model: %q", cfg.Model)
		}
		return llm.NewEcho(cfg.Model)
	}, e.dataDir, e.caps)
	if err := e.reloadCodexAccounts(); err != nil {
		t.Fatal(err)
	}
	if e.prov == before || llm.ProviderModelName(e.prov) != "future-codex" || builds != 1 {
		t.Fatal("next run did not receive refreshed accounts")
	}
	if before.Name() != echoConfig().Model {
		t.Fatal("old run snapshot modified")
	}
	newer, _ := llm.NewEcho("user-selected")
	e.factory = factory.New(func(config.Config, string, *llm.CapabilityRegistry) (llm.Provider, error) {
		e.mu.Lock()
		e.prov = newer
		e.mu.Unlock()
		return llm.NewEcho("future-codex")
	}, e.dataDir, e.caps)
	if err := e.reloadCodexAccounts(); err != nil {
		t.Fatal(err)
	}
	if e.prov != newer {
		t.Fatal("auth rebuild overwrote concurrent model selection")
	}
}
func TestCodexAuthRebuildErrorDoesNotReplaceProvider(t *testing.T) {
	srv := newTestServer(t, false)
	e := srv.eng
	e.cfg.Provider = config.ProviderCodex
	before := e.prov
	e.factory = factory.New(func(config.Config, string, *llm.CapabilityRegistry) (llm.Provider, error) {
		return nil, errors.New("no accounts")
	}, e.dataDir, e.caps)
	if err := e.reloadCodexAccounts(); err == nil {
		t.Fatal("missing rebuild error")
	}
	if e.prov != before {
		t.Fatal("failed rebuild replaced provider")
	}
	e.cfg.Provider = config.ProviderEcho
	if err := e.reloadCodexAccounts(); err != nil {
		t.Fatal("non-Codex provider was rebuilt")
	}
}
