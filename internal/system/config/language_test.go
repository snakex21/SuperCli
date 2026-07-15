package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetLanguagePreservesProviderConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`default_model = "local-model"

[[providers]]
name = "local"
type = "openai"
base_url = "http://192.168.0.102:8080/v1"
api_key = "secret"
model = "local-model"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetLanguage(dir, dir, "pl-PL"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Language != "pl" {
		t.Fatalf("language=%q, want pl", cfg.Language)
	}
	if cfg.DefaultModel != "local-model" || len(cfg.Providers) != 1 || cfg.Providers[0].APIKey != "secret" {
		t.Fatalf("language update damaged config: %+v", cfg)
	}
}

func TestEnsureLanguageKeepsPersistedPreference(t *testing.T) {
	dir := t.TempDir()
	if err := SetLanguage(dir, dir, "en"); err != nil {
		t.Fatal(err)
	}
	language, err := EnsureLanguage(dir, dir, "en")
	if err != nil {
		t.Fatal(err)
	}
	if language != "en" {
		t.Fatalf("language=%q, want en", language)
	}
}
