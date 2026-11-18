package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/llm/prompt"
	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// installMemorySwitch points the TUI gate at a temp data dir and restores the
// package state afterwards.
func installMemorySwitch(t *testing.T, opened bool) *memorySwitch {
	t.Helper()
	t.Setenv(config.MemoryEnv, "")
	config.SetMemoryOverride(nil)
	s := &memorySwitch{dataDir: t.TempDir(), cwd: t.TempDir(), opened: opened}
	prevSwitch, prevStartup := memSwitch, supercliMemoryAtStartup
	memSwitch = s
	t.Cleanup(func() {
		memSwitch, supercliMemoryAtStartup = prevSwitch, prevStartup
		config.SetMemoryOverride(nil)
	})
	return s
}

func TestMemorySlashOnOffPersistsAndGates(t *testing.T) {
	s := installMemorySwitch(t, true)
	if !memoryActive() {
		t.Fatal("default must be on")
	}
	out, ok := memorySwitchCommand(s, "en", "off")
	if !ok || !strings.Contains(out, "OFF") {
		t.Fatalf("/memory off = %q, %v", out, ok)
	}
	globalPath, _ := config.FindTomlPaths(s.dataDir, s.cwd)
	tc, err := config.LoadToml(globalPath)
	if err != nil || tc.Memory.Enabled == nil || *tc.Memory.Enabled {
		t.Fatalf("/memory off not persisted: %+v err=%v", tc.Memory, err)
	}
	if memoryActive() {
		t.Fatal("gate still open after /memory off")
	}
	if status, _ := memorySwitchCommand(s, "pl", "status"); !strings.Contains(status, "wyłączona") {
		t.Fatalf("/memory status (pl) = %q", status)
	}
	if out, _ := memorySwitchCommand(s, "en", "on"); !strings.Contains(out, "ON") || !memoryActive() {
		t.Fatalf("/memory on = %q active=%v", out, memoryActive())
	}
	if _, ok := memorySwitchCommand(s, "en", "search foo"); ok {
		t.Fatal("search must fall through to the memory browser")
	}
}

func TestMemorySlashReportsOverrideAndRestart(t *testing.T) {
	s := installMemorySwitch(t, false) // session started with memory off
	if memoryActive() {
		t.Fatal("stores not opened: the gate must stay closed")
	}
	out, _ := memorySwitchCommand(s, "en", "on")
	if !strings.Contains(out, "restart") {
		t.Fatalf("expected a restart note, got %q", out)
	}
	if memoryActive() {
		t.Fatal("stores were never opened; memory cannot come back before a restart")
	}

	s.opened = true
	t.Setenv(config.MemoryEnv, "off")
	out, _ = memorySwitchCommand(s, "en", "on")
	if !strings.Contains(out, "SUPERCLI_MEMORY") {
		t.Fatalf("expected the env override to be named, got %q", out)
	}
}

func TestMemoryOffSkipsAllTUISaves(t *testing.T) {
	s := installMemorySwitch(t, true)
	enabled := false
	globalPath, _ := config.FindTomlPaths(s.dataDir, s.cwd)
	if err := config.SaveToml(globalPath, config.TomlConfig{Memory: config.MemoryConf{Enabled: &enabled}}); err != nil {
		t.Fatal(err)
	}
	saver, project, global := newMemFixture(t)
	loop := loopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "cześć, nazywam się Maks"},
		llm.Message{Role: llm.RoleAssistant, Content: "Cześć Maks!"},
	)
	prog := &memProgress{}
	summarizer := &countingProvider{reply: "summary"}

	saveDeterministicMemoryFacts(saver, loop, prog)
	incrementalMemorySave(context.Background(), saver, loop, prog, summarizer)
	dumpRawMemoryTail(saver, loop, prog)
	finalizeMemorySession(saver, loop, prog)

	if n := summarizer.calls.Load(); n != 0 {
		t.Fatalf("memory off: summarizer called %d times", n)
	}
	for label, store := range map[string]*memory.Store{"project": project, "global": global} {
		entries, err := store.Recent("", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("memory off: %s store got %d entries: %+v", label, len(entries), entries)
		}
	}
	// Turns made while off are marked covered, so /memory on later in the
	// session does not save them retroactively.
	if prog.covered != 2 || prog.factsCovered != 2 {
		t.Fatalf("progress = %d/%d, want 2/2", prog.covered, prog.factsCovered)
	}
}

func TestGatedMemoryToolsRefuseWhileOff(t *testing.T) {
	s := installMemorySwitch(t, true)
	store, err := memory.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	keeper := gateMemoryKeeper(store)
	remember := tools.NewRememberDual(keeper, nil).Spec()
	recall := tools.NewRecallDual(keeper, nil).Spec()
	args := json.RawMessage(`{"text":"The build uses Go 1.26.","scope":"project"}`)
	if _, err := remember.Fn(context.Background(), args); err != nil {
		t.Fatalf("memory on: remember failed: %v", err)
	}
	memorySwitchCommand(s, "en", "off")
	before, _ := store.Recent("", 50)
	res, err := remember.Fn(context.Background(), json.RawMessage(`{"text":"Secret plan B.","scope":"project"}`))
	if err == nil && (res.Err == nil || !strings.Contains(res.Err.Error(), "switched off")) {
		t.Fatalf("memory off: remember should refuse, got %q / %v", res.Text, res.Err)
	}
	after, _ := store.Recent("", 50)
	if len(after) != len(before) {
		t.Fatalf("memory off: remember wrote %d new entries", len(after)-len(before))
	}
	res, err = recall.Fn(context.Background(), json.RawMessage(`{"query":"build Go"}`))
	if err == nil && strings.Contains(res.Text, "1.26") {
		t.Fatalf("memory off: recall returned stored memory: %q", res.Text)
	}
	memorySwitchCommand(s, "en", "on")
	if res, err := recall.Fn(context.Background(), json.RawMessage(`{"query":"build Go"}`)); err != nil || !strings.Contains(res.Text, "1.26") {
		t.Fatalf("memory back on: recall = %q, %v", res.Text, err)
	}
}

func TestTUISystemPromptMemoryOff(t *testing.T) {
	installMemorySwitch(t, true)
	on := buildSystemPromptLayers(nil, "", false)
	if !strings.Contains(on, prompt.MemoryGuidance(true)) || strings.Contains(on, prompt.MemoryOffGuidance) {
		t.Fatal("memory on: prompt must be unchanged")
	}
	supercliMemoryAtStartup = false
	off := buildSystemPromptLayers(nil, "", true)
	if strings.Contains(off, prompt.MemoryGuidance(true)) || strings.Contains(off, "remember preferences; recall only") {
		t.Fatal("memory off: prompt still advertises memory tools")
	}
	if !strings.Contains(off, prompt.MemoryOffGuidance) {
		t.Fatal("memory off: missing memory-off note")
	}
}

func TestMemorySwitchCLIFlagWinsOverConfig(t *testing.T) {
	s := installMemorySwitch(t, true)
	enabled := true
	globalPath, _ := config.FindTomlPaths(s.dataDir, s.cwd)
	if err := config.SaveToml(globalPath, config.TomlConfig{Memory: config.MemoryConf{Enabled: &enabled}}); err != nil {
		t.Fatal(err)
	}
	config.ApplyMemoryFlags(false, true) // --no-memory
	if memoryActive() {
		t.Fatal("--no-memory must override [memory] enabled = true")
	}
	if on, src := s.status(); on || src != config.MemorySourceFlag {
		t.Fatalf("status = %v/%s, want off/flag", on, src)
	}
	// /memory on in the session replaces the launch flag.
	memorySwitchCommand(s, "en", "on")
	if !memoryActive() {
		t.Fatal("/memory on must replace the --no-memory override for this session")
	}
	_ = os.Remove(globalPath)
}
