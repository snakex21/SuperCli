package factory

import (
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestBuildChainEmptyIsBytePathEquivalent(t *testing.T) {
	f := New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		return llm.NewEcho(cfg.Model)
	}, "", nil)
	p, err := f.BuildChain(config.Config{Provider: config.ProviderEcho, BaseURL: "echo://local", Model: "main"}, config.TomlConfig{}, llm.PurposeMain)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*llm.FailoverProvider); ok {
		t.Fatal("empty fallback list added wrapper")
	}
}

func TestBuildChainResolvesNamedProvider(t *testing.T) {
	f := New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		return llm.NewEcho(cfg.Model)
	}, "", nil)
	cfg := config.Config{Provider: config.ProviderEcho, BaseURL: "echo://local", Model: "main"}
	tc := config.TomlConfig{FallbackModels: []string{"cloud/backup"}, Providers: []config.ProviderConf{{Name: "cloud", Type: "echo", BaseURL: "echo://cloud", Model: "backup"}}}
	p, err := f.BuildChain(cfg, tc, llm.PurposeMain)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*llm.FailoverProvider); !ok {
		t.Fatalf("got %T", p)
	}
}
