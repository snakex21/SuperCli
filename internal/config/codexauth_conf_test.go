package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCodexAuthConfDefaults: an absent [codex_auth] section
// yields zero values (the codexauth package fills in compiled-in
// defaults via Options.WithDefaults).
func TestCodexAuthConfDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("default_model = \"gpt-4o\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CodexAuth.ClientID != "" || cfg.CodexAuth.Issuer != "" || cfg.CodexAuth.BackendURL != "" {
		t.Fatalf("expected empty codex_auth, got %+v", cfg.CodexAuth)
	}
}

func TestCodexAuthConfOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := `
[codex_auth]
client_id = "app_custom"
issuer = "https://auth.example.com"
backend_url = "https://example.com/backend-api/codex"
`
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CodexAuth.ClientID != "app_custom" {
		t.Errorf("client_id = %q", cfg.CodexAuth.ClientID)
	}
	if cfg.CodexAuth.Issuer != "https://auth.example.com" {
		t.Errorf("issuer = %q", cfg.CodexAuth.Issuer)
	}
	if cfg.CodexAuth.BackendURL != "https://example.com/backend-api/codex" {
		t.Errorf("backend_url = %q", cfg.CodexAuth.BackendURL)
	}
}

// Project config overrides global field-wise.
func TestCodexAuthMerge(t *testing.T) {
	var dst, src TomlConfig
	dst.CodexAuth = CodexAuthConf{ClientID: "global", Issuer: "https://g"}
	src.CodexAuth = CodexAuthConf{Issuer: "https://p"}
	mergeToml(&dst, src)
	if dst.CodexAuth.ClientID != "global" {
		t.Errorf("ClientID should survive merge, got %q", dst.CodexAuth.ClientID)
	}
	if dst.CodexAuth.Issuer != "https://p" {
		t.Errorf("Issuer should be overridden, got %q", dst.CodexAuth.Issuer)
	}
}

// Provider "codex" normalizes with a default model and no key.
func TestNormalizeCodexProvider(t *testing.T) {
	c := Config{Provider: ProviderCodex, BaseURL: "https://chatgpt.com/backend-api/codex"}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if c.Model != "gpt-5.5" {
		t.Errorf("default codex model = %q, want gpt-5.5", c.Model)
	}
}
