package app

import (
	"context"
	"errors"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/agent/reflect"
	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
	"supercli/internal/tools"
)

// memorySwitch is the TUI view of the persistent-memory switch
// ([memory] enabled, SUPERCLI_MEMORY, --no-memory/--memory). The config files
// are read fresh on every check, so /memory on|off and the /settings row
// apply to the next turn without a restart. opened records whether the
// memory stores were opened at startup: a session started with memory off
// never opens them (nothing is read or written, not even the project card),
// so switching memory back on takes effect at the next start.
type memorySwitch struct {
	dataDir    string
	cwd        string
	configPath string
	opened     bool
}

// memSwitch is installed by Main. nil (tests, batch) means memory on.
var memSwitch *memorySwitch

// supercliMemoryAtStartup selects the memory layer of the frozen TUI system
// prompt: MemoryGuidance when on, MemoryOffGuidance (and no memory hint in
// the core) when the session starts with memory off.
var supercliMemoryAtStartup = true

// status resolves the switch: flag > env > project > global config > on.
func (s *memorySwitch) status() (enabled bool, source string) {
	if s == nil {
		return true, config.MemorySourceDefault
	}
	if strings.TrimSpace(s.configPath) == "" {
		return config.EffectiveMemoryAt(s.dataDir, s.cwd)
	}
	tc, err := config.ResolveConfig(s.dataDir, s.cwd, s.configPath)
	if err != nil {
		return config.EffectiveMemoryAt(s.dataDir, s.cwd)
	}
	return config.EffectiveMemory(tc)
}

// active reports whether memory may be read or written right now.
func (s *memorySwitch) active() bool {
	if s == nil {
		return true
	}
	if !s.opened {
		return false
	}
	enabled, _ := s.status()
	return enabled
}

// memoryActive is the gate every TUI memory read/write path checks.
func memoryActive() bool { return memSwitch.active() }

// errMemoryOff is what remember/recall report while memory is switched off
// mid-session.
var errMemoryOff = errors.New("persistent memory is switched off (/memory on to enable it); nothing was read or saved")

// gatedKeeper keeps remember/recall registered for the session (the tool list
// and its schemas stay stable for the provider cache) but refuses every read
// and write while memory is switched off mid-session. It forwards the optional
// capabilities of *memory.Store, so recall ranks exactly as before when on.
// A session started with memory off never registers the tools at all.
type gatedKeeper struct{ store *memory.Store }

// gateMemoryKeeper returns a clean nil interface for a nil store, so the
// tools' `keeper == nil` checks keep working (a typed nil would not).
func gateMemoryKeeper(s *memory.Store) tools.MemoryKeeper {
	if s == nil {
		return nil
	}
	return gatedKeeper{store: s}
}

func (g gatedKeeper) Put(e memory.Entry) error {
	if !memoryActive() {
		return errMemoryOff
	}
	return g.store.Put(e)
}

func (g gatedKeeper) Search(query string, k int) ([]memory.Entry, error) {
	if !memoryActive() {
		return nil, errMemoryOff
	}
	return g.store.Search(query, k)
}

func (g gatedKeeper) Recent(scope string, n int) ([]memory.Entry, error) {
	if !memoryActive() {
		return nil, errMemoryOff
	}
	return g.store.Recent(scope, n)
}

func (g gatedKeeper) HybridSearch(ctx context.Context, query string, k int) ([]memory.Entry, error) {
	if !memoryActive() {
		return nil, errMemoryOff
	}
	return g.store.HybridSearch(ctx, query, k)
}

func (g gatedKeeper) RecallSearch(ctx context.Context, query string, k int) ([]memory.Entry, error) {
	if !memoryActive() {
		return nil, errMemoryOff
	}
	return g.store.RecallSearch(ctx, query, k)
}

func (g gatedKeeper) RecallRecent(n int) ([]memory.Entry, error) {
	if !memoryActive() {
		return nil, errMemoryOff
	}
	return g.store.RecallRecent(n)
}

// gatedInjector suppresses learned error patterns (stored in project memory)
// while memory is off.
type gatedInjector struct{ inner agent.PatternInjector }

func (g gatedInjector) Build(ctx context.Context, systemContext string) (string, error) {
	if !memoryActive() {
		return "", nil
	}
	return g.inner.Build(ctx, systemContext)
}

// memorySourceLabel translates a config.MemorySource* value.
func memorySourceLabel(language, source string) string {
	switch source {
	case config.MemorySourceFlag, config.MemorySourceEnv, config.MemorySourceProject, config.MemorySourceConfig:
		return uilang.Text(language, "app.memory.source."+source)
	default:
		return uilang.Text(language, "app.memory.source.default")
	}
}

// memorySwitchCommand handles /memory on|off|status. ok=false means the
// arguments are not a switch command (overview/search/forget/tidy follow).
func memorySwitchCommand(s *memorySwitch, language, args string) (string, bool) {
	arg := strings.ToLower(strings.TrimSpace(args))
	switch arg {
	case "status":
		return memoryStatusText(s, language), true
	case "on", "off":
	default:
		return "", false
	}
	if s == nil {
		return uilang.Text(language, "app.memory.unavailable"), true
	}
	want := arg == "on"
	globalPath, _ := config.FindTomlPaths(s.dataDir, s.cwd)
	tc, err := config.LoadToml(globalPath)
	if err != nil {
		return uilang.Format(language, "app.memory.saveFailed", err), true
	}
	tc.Memory.Enabled = &want
	if err := config.SaveToml(globalPath, tc); err != nil {
		return uilang.Format(language, "app.memory.saveFailed", err), true
	}
	// /memory on|off is an explicit choice for this session too, so it also
	// replaces a --no-memory/--memory flag given at launch.
	if _, flagged := config.MemoryOverride(); flagged {
		config.SetMemoryOverride(&want)
	}
	var b strings.Builder
	if want {
		b.WriteString(uilang.Text(language, "app.memory.turnedOn"))
	} else {
		b.WriteString(uilang.Text(language, "app.memory.turnedOff"))
	}
	enabled, source := s.status()
	if enabled != want {
		b.WriteString("\n")
		b.WriteString(uilang.Format(language, "app.memory.overridden", memorySourceLabel(language, source)))
	} else if want && !s.opened {
		b.WriteString("\n")
		b.WriteString(uilang.Text(language, "app.memory.restartNeeded"))
	}
	return b.String(), true
}

func memoryStatusText(s *memorySwitch, language string) string {
	enabled, source := s.status()
	state := uilang.Text(language, "app.memory.stateOff")
	if enabled {
		state = uilang.Text(language, "app.memory.stateOn")
	}
	out := uilang.Format(language, "app.memory.status", state, memorySourceLabel(language, source))
	if enabled && s != nil && !s.opened {
		out += "\n" + uilang.Text(language, "app.memory.restartNeeded")
	}
	if !enabled {
		out += "\n" + uilang.Text(language, "app.memory.offEffect")
	}
	return out
}

// gatedPatternInjector wraps a live injector with the memory gate. A nil
// injector is passed through unchanged (no stores were opened).
func gatedPatternInjector(in *reflect.Injector) agent.PatternInjector {
	if in == nil {
		return in
	}
	return gatedInjector{inner: in}
}
