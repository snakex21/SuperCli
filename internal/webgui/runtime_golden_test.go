package webgui

import (
	"path/filepath"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/assembly/goldentest"
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// TestRuntimeGolden_Web snapshots the web composition root (and the NestCafe
// app profile) for every execution profile: registered tools, always-on set,
// system prompt, LoopConfig projection, approval gate and the provider
// requests of a scripted conversation driven through the real loop.
func TestRuntimeGolden_Web(t *testing.T) {
	for _, app := range []string{"", "nestcafe"} {
		for _, p := range goldentest.Profiles {
			surface := "web"
			if app != "" {
				surface = "web-" + app
			}
			t.Run(surface+"/"+p.Name, func(t *testing.T) {
				goldentest.CleanEnv(t)
				got := webGoldenSnapshot(t, app, p)
				goldentest.Check(t, filepath.Join(goldentest.Dir("testdata"), surface+"_"+p.Name+".golden"), got)
			})
		}
	}
}

func webGoldenSnapshot(t *testing.T, app string, p goldentest.Profile) string {
	t.Helper()
	home, dataDir := goldentest.Fixture(t, p)
	n := goldentest.NewNormalizer(home, dataDir)
	cfg := config.Config{Provider: config.ProviderEcho, Model: goldentest.Model, BaseURL: goldentest.BaseURL}
	_ = cfg.Normalize()
	eng, err := NewEngine(cfg, home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if app != "" {
		eng.SetAppProfile(app)
	}
	gate := InstallApprovalGate(dataDir)
	t.Cleanup(func() { tools.SetDefaultApprover(nil) })

	var captured agent.LoopConfig
	observeLoopConfig = func(c agent.LoopConfig) { captured = c }
	t.Cleanup(func() { observeLoopConfig = nil })

	// The human-facing chat stream: an ask channel and a checkpoint turn.
	askCh := make(chan tools.AskRequest, 4)
	var turn *checkpoint.Turn
	if manager, cpErr := checkpoint.Open(home, dataDir); cpErr == nil {
		turn = manager.NewTurn("golden-session", "golden")
	} else {
		t.Skipf("checkpoint unavailable: %v", cpErr)
	}
	loop, base, err := eng.buildLoopWithSession(nil, nil, home, askCh, turn)
	if err != nil {
		t.Fatal(err)
	}
	out := goldentest.Section("surface", "web app_profile="+app+" profile="+p.Name)
	out += goldentest.Section("base registry", goldentest.Registry(base, n))
	out += goldentest.Section("loop visible tools", joinLines(loop.VisibleToolNames()))
	out += goldentest.Section("system prompt", n.Apply(captured.System))
	out += goldentest.Section("project instructions", n.Apply(captured.ProjectInstructions))
	out += goldentest.Section("user instructions", n.Apply(captured.UserInstructions))
	out += goldentest.Section("loop config", goldentest.LoopConfig(captured, n))
	out += goldentest.Section("approval gate", goldentest.ApprovalGate(gate, n))

	// Transcript: a fresh loop, provider swapped for the scripted recorder.
	loop2, _, err := eng.buildLoopWithSession(nil, nil, home, askCh, turn)
	if err != nil {
		t.Fatal(err)
	}
	scripted := &goldentest.ScriptedProvider{ModelName: llm.ProviderModelName(loop2.Provider())}
	if err := loop2.SetAccountProvider(scripted); err != nil {
		t.Fatal(err)
	}
	events := goldentest.Drive(t, loop2, goldentest.CallMarker+`tool_search {"query":"web_fetch"}`, "thanks")
	out += goldentest.Section("events", events)
	out += goldentest.Section("transcript", scripted.Transcript(n))
	return out
}

func joinLines(names []string) string {
	out := ""
	for _, name := range names {
		out += name + "\n"
	}
	return out
}
