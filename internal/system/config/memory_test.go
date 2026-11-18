package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestEffectiveMemoryPrecedence(t *testing.T) {
	t.Cleanup(func() { SetMemoryOverride(nil) })
	off := TomlConfig{Memory: MemoryConf{Enabled: boolPtr(false)}}

	t.Setenv(MemoryEnv, "")
	SetMemoryOverride(nil)
	if on, src := EffectiveMemory(TomlConfig{}); !on || src != MemorySourceDefault {
		t.Fatalf("default = %v/%s, want on/default", on, src)
	}
	if on, src := EffectiveMemory(off); on || src != MemorySourceConfig {
		t.Fatalf("config off = %v/%s, want off/config", on, src)
	}

	t.Setenv(MemoryEnv, "on")
	if on, src := EffectiveMemory(off); !on || src != MemorySourceEnv {
		t.Fatalf("env on over config off = %v/%s", on, src)
	}
	t.Setenv(MemoryEnv, "0")
	if on, src := EffectiveMemory(TomlConfig{}); on || src != MemorySourceEnv {
		t.Fatalf("env 0 = %v/%s", on, src)
	}
	t.Setenv(MemoryEnv, "maybe")
	if on, src := EffectiveMemory(TomlConfig{}); !on || src != MemorySourceDefault {
		t.Fatalf("unknown env value must be ignored, got %v/%s", on, src)
	}

	// The CLI flag (--no-memory) wins over env and config.
	t.Setenv(MemoryEnv, "on")
	SetMemoryOverride(boolPtr(false))
	if on, src := EffectiveMemory(TomlConfig{Memory: MemoryConf{Enabled: boolPtr(true)}}); on || src != MemorySourceFlag {
		t.Fatalf("flag off = %v/%s, want off/flag", on, src)
	}
	SetMemoryOverride(boolPtr(true))
	if on, src := EffectiveMemory(off); !on || src != MemorySourceFlag {
		t.Fatalf("flag on = %v/%s, want on/flag", on, src)
	}
}

func TestEffectiveMemoryAtProjectOverride(t *testing.T) {
	t.Cleanup(func() { SetMemoryOverride(nil) })
	SetMemoryOverride(nil)
	t.Setenv(MemoryEnv, "")
	dataDir := t.TempDir()
	workspace := t.TempDir()
	globalPath, projectPath := FindTomlPaths(dataDir, workspace)

	if on, src := EffectiveMemoryAt(dataDir, workspace); !on || src != MemorySourceDefault {
		t.Fatalf("fresh = %v/%s", on, src)
	}
	if err := SaveToml(globalPath, TomlConfig{Memory: MemoryConf{Enabled: boolPtr(false)}}); err != nil {
		t.Fatal(err)
	}
	if on, src := EffectiveMemoryAt(dataDir, workspace); on || src != MemorySourceConfig {
		t.Fatalf("global off = %v/%s", on, src)
	}
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte("[memory]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if on, src := EffectiveMemoryAt(dataDir, workspace); !on || src != MemorySourceProject {
		t.Fatalf("project on over global off = %v/%s", on, src)
	}
	// The merged view used at runtime agrees with the per-file resolution.
	merged, err := ResolveConfig(dataDir, workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	if on, _ := EffectiveMemory(merged); !on {
		t.Fatal("ResolveConfig must merge the project [memory] override")
	}
}

func TestMemorySectionOmittedWhenUnsetAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveToml(path, TomlConfig{DefaultModel: "m"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "[memory]") {
		t.Fatalf("unset memory switch must not write a [memory] section:\n%s", raw)
	}
	if err := SaveToml(path, TomlConfig{Memory: MemoryConf{Enabled: boolPtr(false)}}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Memory.Enabled == nil || *got.Memory.Enabled {
		t.Fatalf("round trip lost enabled=false: %+v", got.Memory)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "[memory]") || !strings.Contains(string(raw), "enabled = false") {
		t.Fatalf("expected [memory] enabled = false, got:\n%s", raw)
	}
}
