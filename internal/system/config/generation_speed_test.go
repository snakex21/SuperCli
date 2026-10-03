package config

import (
	"path/filepath"
	"testing"
)

func TestGenerationSpeedMergeAndPortableRestart(t *testing.T) {
	on, off := true, false
	c := TomlConfig{ShowGenerationSpeed: &on}
	mergeToml(&c, TomlConfig{ShowGenerationSpeed: &off})
	if c.ShowGenerationSpeed == nil || *c.ShowGenerationSpeed {
		t.Fatal("explicit false lost")
	}
	mergeToml(&c, TomlConfig{})
	if c.ShowGenerationSpeed == nil || *c.ShowGenerationSpeed {
		t.Fatal("absent setting overwrote false")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveToml(path, c); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ShowGenerationSpeed == nil || *reloaded.ShowGenerationSpeed {
		t.Fatal("off did not survive restart")
	}
	reloaded.ShowGenerationSpeed = nil
	if err := SaveToml(path, reloaded); err != nil {
		t.Fatal(err)
	}
	reloaded, err = LoadToml(path)
	if err != nil || reloaded.ShowGenerationSpeed != nil {
		t.Fatal("default reset failed")
	}
}
