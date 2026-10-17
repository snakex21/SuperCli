package config

import (
	"path/filepath"
	"testing"
)

func TestDarwinPoolSizeMergeAndRoundtrip(t *testing.T) {
	dst := TomlConfig{DarwinPoolSize: 4}
	mergeToml(&dst, TomlConfig{})
	if dst.DarwinPoolSize != 4 {
		t.Fatalf("empty override cleared darwin_pool_size: %d", dst.DarwinPoolSize)
	}
	mergeToml(&dst, TomlConfig{DarwinPoolSize: 2})
	if dst.DarwinPoolSize != 2 {
		t.Fatalf("override not applied: %d", dst.DarwinPoolSize)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveToml(path, dst); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DarwinPoolSize != 2 {
		t.Fatalf("roundtrip darwin_pool_size = %d, want 2", got.DarwinPoolSize)
	}
}
