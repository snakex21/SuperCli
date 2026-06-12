package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCouncilConf_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	cfg := TomlConfig{Council: CouncilConf{Models: []string{"ollama/llama3:8b", "openai/gpt-4o-mini"}}}
	if err := SaveToml(path, cfg); err != nil {
		t.Fatalf("SaveToml: %v", err)
	}
	got, err := LoadToml(path)
	if err != nil {
		t.Fatalf("LoadToml: %v", err)
	}
	if len(got.Council.Models) != 2 || got.Council.Models[0] != "ollama/llama3:8b" {
		t.Errorf("Council.Models = %v", got.Council.Models)
	}
}

func TestCouncilConf_TomlSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "[council]\nmodels = [\"a/b\", \"c\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToml(path)
	if err != nil {
		t.Fatalf("LoadToml: %v", err)
	}
	if len(got.Council.Models) != 2 || got.Council.Models[1] != "c" {
		t.Errorf("Council.Models = %v", got.Council.Models)
	}
}

func TestMergeToml_CouncilProjectOverridesGlobal(t *testing.T) {
	dst := TomlConfig{Council: CouncilConf{Models: []string{"global/one"}}}
	mergeToml(&dst, TomlConfig{Council: CouncilConf{Models: []string{"proj/a", "proj/b"}}})
	if len(dst.Council.Models) != 2 || dst.Council.Models[0] != "proj/a" {
		t.Errorf("merged = %v", dst.Council.Models)
	}
	// Empty src leaves dst untouched.
	mergeToml(&dst, TomlConfig{})
	if len(dst.Council.Models) != 2 {
		t.Errorf("merge with empty src clobbered roster: %v", dst.Council.Models)
	}
}
