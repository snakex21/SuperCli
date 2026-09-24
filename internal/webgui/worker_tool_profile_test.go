package webgui

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

func TestWebWorkerUsesItsOwnBackendProfile(t *testing.T) {
	for _, tc := range []struct {
		name, url      string
		full, wantThin bool
	}{
		{"local worker", "http://127.0.0.1:1234/v1", false, true},
		{"cloud worker", "https://models.example.invalid/v1", false, false},
		{"explicit full tools", "http://127.0.0.1:1234/v1", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SUPERCLI_CATALOG_HOIST", "")
			dir := t.TempDir()
			reg := tools.NewRegistry()
			main, err := llm.NewEcho("coordinator")
			if err != nil {
				t.Fatal(err)
			}
			loop, err := agent.NewLoop(agent.LoopConfig{Provider: main, Registry: reg, ThinTools: !tc.wantThin, BaseDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			off := false
			cfg := config.TomlConfig{
				PreflightRepo: &off, SmallFullTools: tc.full,
				TaskModel:  "worker-backend/worker",
				Providers:  []config.ProviderConf{{Name: "worker-backend", Type: config.ProviderEcho, BaseURL: tc.url}},
				ModelTiers: []config.ModelTierConf{{Pattern: "worker", Tier: "big"}},
			}

			eng, err := NewEngine(echoConfig(), dir, dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = eng.Close() })
			if err := eng.wireTaskTool(loop, reg, main, nil, dir, cfg); err != nil {
				t.Fatal(err)
			}
			workers := eng.workers
			spec, ok := reg.Get("task")
			if !ok {
				t.Fatal("task not registered")
			}
			result, err := spec.Fn(context.Background(), json.RawMessage(`{"agent":"code","prompt":"done"}`))
			if err != nil || result.Err != nil {
				t.Fatalf("task: %v %+v", err, result)
			}
			worker, ok := workers.Get("worker-1")
			if !ok {
				t.Fatal("worker not created")
			}
			visible := worker.Loop.VisibleToolNames()
			for _, name := range []string{"tool_search", "invoke_tool"} {
				if slices.Contains(visible, name) != tc.wantThin {
					t.Fatalf("%s visibility=%v, want thin=%v (worker inherited opposite parent profile)", name, visible, tc.wantThin)
				}
			}
		})
	}
}
