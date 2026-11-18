package webgui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	llmprompt "supercli/internal/llm/prompt"
	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
)

func writeGlobalMemorySwitch(t *testing.T, dataDir string, enabled bool) {
	t.Helper()
	globalPath, _ := config.FindTomlPaths(dataDir, "")
	tc, err := config.LoadToml(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	tc.Memory.Enabled = &enabled
	if err := config.SaveToml(globalPath, tc); err != nil {
		t.Fatal(err)
	}
}

// memorySwitchRun builds one run's loop and returns its tool registry
// membership, system prompt and the live memory block text.
func memorySwitchRun(t *testing.T, eng *Engine, home string) (hasRemember, hasRecall bool, system, liveMemory string) {
	t.Helper()
	var captured func(context.Context) ([]llm.ContextBlock, error)
	observeLoopConfig = func(cfg agent.LoopConfig) { captured = cfg.LiveContextBlocks }
	t.Cleanup(func() { observeLoopConfig = nil })
	loop, reg, err := eng.buildLoopWithSession(nil, nil, home, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, hasRemember = reg.Get("remember")
	_, hasRecall = reg.Get("recall")
	var b strings.Builder
	for _, m := range loop.Messages {
		b.WriteString(m.Content)
	}
	if captured == nil {
		t.Fatal("loop config was not observed")
	}
	blocks, err := captured(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range blocks {
		if block.Source == llm.SourceMemory {
			liveMemory += block.Text
		}
	}
	return hasRemember, hasRecall, b.String(), liveMemory
}

// seedPreference stores one global preference so the memory briefing has
// something to inject when memory is on.
func seedPreference(t *testing.T, dataDir string) {
	t.Helper()
	store, err := memory.OpenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Put(memory.Entry{ID: "pref-switch", Scope: memory.ScopePreference, Content: "User prefers answers in Polish.", Source: memory.SourceUser}); err != nil {
		t.Fatal(err)
	}
}

func TestWebMemorySwitchOffRemovesToolsBriefingAndGuidance(t *testing.T) {
	t.Setenv(config.MemoryEnv, "")
	config.SetMemoryOverride(nil)
	data, home := t.TempDir(), t.TempDir()
	seedPreference(t, data)
	eng, err := NewEngine(echoConfig(), home, data)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// On (default): unchanged behaviour.
	remember, recall, system, live := memorySwitchRun(t, eng, home)
	if !remember || !recall {
		t.Fatalf("memory on: remember=%v recall=%v, want both registered", remember, recall)
	}
	if !strings.Contains(system, llmprompt.MemoryGuidance(true)) || strings.Contains(system, llmprompt.MemoryOffGuidance) {
		t.Fatal("memory on: prompt must carry the normal memory guidance only")
	}
	if !strings.Contains(live, "Polish") {
		t.Fatalf("memory on: briefing should include the stored preference, got %q", live)
	}

	// Off: no tools, no briefing, no memory hint; data untouched.
	writeGlobalMemorySwitch(t, data, false)
	remember, recall, system, live = memorySwitchRun(t, eng, home)
	if remember || recall {
		t.Fatalf("memory off: remember=%v recall=%v, want neither registered", remember, recall)
	}
	if live != "" {
		t.Fatalf("memory off: live memory block must be empty, got %q", live)
	}
	if !strings.Contains(system, llmprompt.MemoryOffGuidance) {
		t.Fatal("memory off: prompt lacks the memory-off note")
	}
	if strings.Contains(system, llmprompt.MemoryGuidance(true)) || strings.Contains(system, "remember preferences; recall only") {
		t.Fatal("memory off: prompt still advertises memory tools")
	}
	store, err := memory.OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("pref-switch"); err != nil {
		t.Fatalf("memory off must not delete stored memory: %v", err)
	}
	_ = store.Close()

	// A project config can switch it back on for that workspace only.
	if err := os.MkdirAll(filepath.Join(home, ".supercli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".supercli", "config.toml"), []byte("[memory]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	remember, recall, _, live = memorySwitchRun(t, eng, home)
	if !remember || !recall || !strings.Contains(live, "Polish") {
		t.Fatalf("project override on: remember=%v recall=%v live=%q", remember, recall, live)
	}
	if st := eng.memoryStatusAt(home); !st.Enabled || st.Source != config.MemorySourceProject {
		t.Fatalf("status = %+v, want enabled from project", st)
	}
}

func TestWebMemorySwitchFlagOverridesConfig(t *testing.T) {
	t.Setenv(config.MemoryEnv, "")
	t.Cleanup(func() { config.SetMemoryOverride(nil) })
	data, home := t.TempDir(), t.TempDir()
	eng, err := NewEngine(echoConfig(), home, data)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	writeGlobalMemorySwitch(t, data, true)

	config.ApplyMemoryFlags(false, true) // --no-memory
	if remember, recall, _, _ := memorySwitchRun(t, eng, home); remember || recall {
		t.Fatal("--no-memory must win over [memory] enabled = true")
	}
	if st := eng.memoryStatusAt(home); st.Enabled || st.Source != config.MemorySourceFlag {
		t.Fatalf("status = %+v, want off from flag", st)
	}

	config.SetMemoryOverride(nil)
	t.Setenv(config.MemoryEnv, "off")
	if remember, _, _, _ := memorySwitchRun(t, eng, home); remember {
		t.Fatal("SUPERCLI_MEMORY=off must win over the config file")
	}
}

// A real chat run with memory off must not open (and therefore neither read
// nor write) any memory store: no user facts, no capsule, no recall.
func TestWebChatWithMemoryOffNeverTouchesMemory(t *testing.T) {
	t.Setenv(config.MemoryEnv, "")
	config.SetMemoryOverride(nil)
	for _, enabled := range []bool{false, true} {
		name := map[bool]string{false: "off", true: "on"}[enabled]
		t.Run(name, func(t *testing.T) {
			srv := newTestServer(t, false)
			writeGlobalMemorySwitch(t, srv.eng.DataDir(), enabled)
			rec := httptest.NewRecorder()
			srv.handleChat(rec, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"My name is Anna.","turn_id":"memory-switch-`+name+`"}`)))
			if rec.Code != http.StatusOK {
				t.Fatalf("chat status %d: %s", rec.Code, rec.Body.String())
			}
			srv.eng.memoryMu.Lock()
			opened := srv.eng.globalMemory != nil || len(srv.eng.projectMemory) > 0
			global := srv.eng.globalMemory
			srv.eng.memoryMu.Unlock()
			if !enabled {
				if opened {
					t.Fatal("memory off: a memory store was opened during the run")
				}
				if _, err := os.Stat(filepath.Join(srv.eng.DataDir(), "memory.db")); err == nil {
					t.Fatal("memory off: global memory.db was created")
				}
				return
			}
			if global == nil || len(userFactContents(t, global)) == 0 {
				t.Fatal("memory on: the user fact was not saved (control case)")
			}
		})
	}
}

func TestMemoryKnobAndStatusEndpoint(t *testing.T) {
	t.Setenv(config.MemoryEnv, "")
	config.SetMemoryOverride(nil)
	srv := newTestServer(t, false)
	status := func() memoryStatus {
		rec := httptest.NewRecorder()
		srv.handleMemoryStatus(rec, httptest.NewRequest(http.MethodGet, "/api/memory/status", nil))
		var st memoryStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatalf("status body %q: %v", rec.Body.String(), err)
		}
		return st
	}
	if st := status(); !st.Enabled || st.Source != config.MemorySourceDefault {
		t.Fatalf("fresh status = %+v", st)
	}
	if k := findKnob(t, knobsGET(t, srv), "memory"); k.State != "default" || k.Value != "on" || k.Default != "on" || k.NextSession {
		t.Fatalf("fresh knob = %+v", k)
	}

	if rec := knobsPOST(t, srv, `{"key":"memory","value":"off"}`); rec.Code != http.StatusOK {
		t.Fatalf("POST off: %d %s", rec.Code, rec.Body.String())
	}
	globalPath, _ := config.FindTomlPaths(srv.eng.DataDir(), "")
	raw, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "[memory]") || !strings.Contains(string(raw), "enabled = false") {
		t.Fatalf("GUI toggle not persisted to config.toml:\n%s", raw)
	}
	if k := findKnob(t, knobsGET(t, srv), "memory"); k.State != "off" || k.Source != "manual" {
		t.Fatalf("knob after off = %+v", k)
	}
	if st := status(); st.Enabled || st.Source != config.MemorySourceConfig {
		t.Fatalf("status after off = %+v", st)
	}

	if rec := knobsPOST(t, srv, `{"key":"memory","value":"default"}`); rec.Code != http.StatusOK {
		t.Fatalf("POST default: %d %s", rec.Code, rec.Body.String())
	}
	raw, _ = os.ReadFile(globalPath)
	if strings.Contains(string(raw), "[memory]") {
		t.Fatalf("default must drop the [memory] section:\n%s", raw)
	}
	if st := status(); !st.Enabled {
		t.Fatalf("status after default = %+v", st)
	}
}
