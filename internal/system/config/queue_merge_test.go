package config

import (
	"path/filepath"
	"testing"
)

func TestQueueMergeConfig(t *testing.T) {
	var dst TomlConfig
	if dst.QueueMerge != "" {
		t.Fatal("default must be off/empty")
	}
	mergeToml(&dst, TomlConfig{QueueMerge: "auto"})
	mergeToml(&dst, TomlConfig{})
	if dst.QueueMerge != "auto" {
		t.Fatal("empty override changed mode")
	}
	mergeToml(&dst, TomlConfig{QueueMerge: "off"})
	if dst.QueueMerge != "off" {
		t.Fatal("project off must override global auto")
	}
	for _, value := range []string{"off", "auto"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := SaveToml(path, TomlConfig{QueueMerge: value}); err != nil {
			t.Fatal(err)
		}
		got, err := LoadToml(path)
		if err != nil || got.QueueMerge != value {
			t.Fatalf("roundtrip = %q, %v", got.QueueMerge, err)
		}
	}
}
