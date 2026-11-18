package config

import (
	"os"
	"strings"
	"sync/atomic"
)

// MemoryConf is the [memory] section of config.toml.
//
//	[memory]
//	enabled = false   # default true (unset)
//
// It is a normal mergeable section, so a project's
// <workspace>/.supercli/config.toml can switch memory off (or back on) for
// that workspace only.
type MemoryConf struct {
	// Enabled switches persistent memory as a whole: the memory briefing and
	// cross-session recall in the prompt, the remember/recall tools, and every
	// automatic save (session summaries, user facts, conversation capsules,
	// project card, learned error patterns). nil = on. Off never deletes the
	// stored data; switching it back on restores it.
	Enabled *bool `toml:"enabled,omitempty"`
}

// MemoryEnv is the environment override for the memory switch. Truthy values
// (1, true, yes, on) force memory on, falsy values (0, false, no, off) force it
// off; anything else is ignored.
const MemoryEnv = "SUPERCLI_MEMORY"

// Memory sources reported by EffectiveMemory, highest priority first.
const (
	MemorySourceFlag    = "flag"    // --no-memory / --memory, or /memory on|off in this process
	MemorySourceEnv     = "env"     // SUPERCLI_MEMORY
	MemorySourceProject = "project" // <workspace>/.supercli/config.toml
	MemorySourceConfig  = "config"  // global config.toml
	MemorySourceDefault = "default" // nothing set: on
)

// MemoryConfigEnabled reports the config-file value only (nil = on).
func MemoryConfigEnabled(c TomlConfig) bool {
	return c.Memory.Enabled == nil || *c.Memory.Enabled
}

// memoryOverride is the process-level override: 0 = none, 1 = on, 2 = off.
// Set by the --no-memory/--memory flags and by the TUI /memory on|off command.
var memoryOverride atomic.Int32

// SetMemoryOverride installs (or with nil clears) the process-level memory
// override. It wins over SUPERCLI_MEMORY and both config files.
func SetMemoryOverride(enabled *bool) {
	switch {
	case enabled == nil:
		memoryOverride.Store(0)
	case *enabled:
		memoryOverride.Store(1)
	default:
		memoryOverride.Store(2)
	}
}

// MemoryOverride returns the process-level override, if any.
func MemoryOverride() (enabled, ok bool) {
	switch memoryOverride.Load() {
	case 1:
		return true, true
	case 2:
		return false, true
	}
	return false, false
}

// MemoryEnvOverride parses SUPERCLI_MEMORY.
func MemoryEnvOverride() (enabled, ok bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(MemoryEnv))) {
	case "1", "true", "yes", "on", "y":
		return true, true
	case "0", "false", "no", "off", "n":
		return false, true
	}
	return false, false
}

// EffectiveMemory resolves the memory switch for an already merged config:
// process override (flag / slash command) > SUPERCLI_MEMORY > config files >
// default on. The source is MemorySourceConfig for any config-file value; use
// EffectiveMemoryAt to tell a project override apart from the global file.
func EffectiveMemory(c TomlConfig) (enabled bool, source string) {
	if v, ok := MemoryOverride(); ok {
		return v, MemorySourceFlag
	}
	if v, ok := MemoryEnvOverride(); ok {
		return v, MemorySourceEnv
	}
	if c.Memory.Enabled != nil {
		return *c.Memory.Enabled, MemorySourceConfig
	}
	return true, MemorySourceDefault
}

// EffectiveMemoryAt resolves the switch for a workspace, reading the global
// and project config files fresh. A project value is reported as
// MemorySourceProject. A malformed project file is ignored here, exactly as
// ResolveConfig keeps the global layer.
func EffectiveMemoryAt(dataDir, cwd string) (enabled bool, source string) {
	if v, ok := MemoryOverride(); ok {
		return v, MemorySourceFlag
	}
	if v, ok := MemoryEnvOverride(); ok {
		return v, MemorySourceEnv
	}
	globalPath, projectPath := FindTomlPaths(dataDir, cwd)
	if projectPath != "" {
		if project, err := LoadToml(projectPath); err == nil && project.Memory.Enabled != nil {
			return *project.Memory.Enabled, MemorySourceProject
		}
	}
	if global, err := LoadToml(globalPath); err == nil && global.Memory.Enabled != nil {
		return *global.Memory.Enabled, MemorySourceConfig
	}
	return true, MemorySourceDefault
}

// ApplyMemoryFlags installs the per-launch override from --memory and
// --no-memory. --no-memory wins when both are given: switching memory off is
// the conservative choice.
func ApplyMemoryFlags(forceOn, forceOff bool) {
	switch {
	case forceOff:
		off := false
		SetMemoryOverride(&off)
	case forceOn:
		on := true
		SetMemoryOverride(&on)
	}
}
