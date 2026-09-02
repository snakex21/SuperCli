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

func TestBuildChainDoesNotAutomaticallyRotateAnonymousOpenCodeFreeModels(t *testing.T) {
	var built []string
	f := New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		built = append(built, cfg.Model)
		return llm.NewEcho(cfg.Model)
	}, "", nil)
	cfg := config.Config{
		Provider: config.ProviderOpenAI,
		BaseURL:  "https://opencode.ai/zen/v1",
		Model:    "mimo-v2.5-free",
	}
	tc := config.TomlConfig{Providers: []config.ProviderConf{{
		Name:         "opencode",
		Type:         config.ProviderOpenAI,
		BaseURL:      cfg.BaseURL,
		CachedModels: []string{"mimo-v2.5-free", "deepseek-v4-flash-free", "hy3-free", "paid-model"},
	}}}
	p, err := f.BuildChain(cfg, tc, llm.PurposeMain)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*llm.FailoverProvider); ok {
		t.Fatalf("got %T: cached OpenCode models must not create automatic failover", p)
	}
	if len(built) != 1 || built[0] != "mimo-v2.5-free" {
		t.Fatalf("built models = %v, want only the selected model", built)
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
