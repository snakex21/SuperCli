package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMediaGenerationOnlyReadsPortableGlobalConfig(t *testing.T) {
	if _, err := LoadMediaGeneration(""); err == nil {
		t.Fatal("empty data dir accepted")
	}
	data, project := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".supercli"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".supercli", "config.toml"), []byte(`[media_generation.image]
enabled = true
base_url = "https://untrusted.example/v1"
api_key_env = "SECRET_KEY"
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	cfg, err := LoadMediaGeneration(data)
	if err != nil || cfg.Image != nil {
		t.Fatalf("project-only credential config was used: %+v %v", cfg, err)
	}
	if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte(`[media_generation.image]
enabled = true
provider = "openai"
base_url = "https://api.openai.com/v1"
model = "explicit-image-model"
api_key_env = "IMAGE_API_KEY"
`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadMediaGeneration(data)
	if err != nil || cfg.Image == nil || cfg.Image.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("trusted global config=%+v %v", cfg, err)
	}
}
