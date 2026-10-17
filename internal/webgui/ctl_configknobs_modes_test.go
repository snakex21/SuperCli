package webgui

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/system/config"
)

func TestModeKnobsSharedConfigRoundtrip(t *testing.T) {
	srv := newTestServer(t, false)
	global, _ := config.FindTomlPaths(srv.eng.DataDir(), srv.eng.Home())
	// Seed the exact fields the TUI settings menu writes, including unrelated
	// startup choices which neither surface may change as a side effect.
	on := true
	seed := config.TomlConfig{DefaultModel: "keep-model", DefaultProvider: "keep-provider",
		TaskModel: "local/worker", AdvisorModel: "off", ReviewModel: "local/checker",
		Darwin: &on, DarwinModels: []string{"local/a", "cloud/b"}, ContextWindow: 32768,
		PruneProtectTokens: 9000, VerifyCommands: []string{"go test ./..."}}
	if err := config.SaveToml(global, seed); err != nil {
		t.Fatal(err)
	}
	for key, raw := range map[string]string{"task_model": "local/worker", "advisor_model": "off", "review_model": "local/checker", "darwin_models": "local/a ; cloud/b", "context_window": "32768", "prune_protect_tokens": "9000", "verify_commands": "go test ./..."} {
		if k := findKnob(t, knobsGET(t, srv), key); k.Raw != raw {
			t.Fatalf("%s: %+v, want raw %q", key, k, raw)
		}
	}
	for key, value := range map[string]string{"task_model": "cloud/worker", "advisor_model": "local/advisor", "review_model": "off", "darwin": "off", "darwin_models": " cloud/x ; ; local/y ", "context_window": "65536", "prune_protect_tokens": "12000", "verify_commands": " go build ./... ; go test ./... "} {
		body, _ := json.Marshal(map[string]string{"key": key, "value": value})
		if rec := knobsPOST(t, srv, string(body)); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", key, rec.Code, rec.Body.String())
		}
	}
	got, err := config.LoadToml(global)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskModel != "cloud/worker" || got.AdvisorModel != "local/advisor" || !config.ReviewDisabled(got) || config.DarwinEnabled(got) || got.ContextWindow != 65536 || got.PruneProtectTokens != 12000 || !reflect.DeepEqual(got.DarwinModels, []string{"cloud/x", "local/y"}) || !reflect.DeepEqual(got.VerifyCommands, []string{"go build ./...", "go test ./..."}) {
		t.Fatalf("TUI config roundtrip: %+v", got)
	}
	if got.DefaultModel != seed.DefaultModel || got.DefaultProvider != seed.DefaultProvider {
		t.Fatal("startup choices changed")
	}
	if k := findKnob(t, knobsGET(t, srv), "review_model"); k.Value != "off" || k.Raw != "off" || k.Source != "manual" {
		t.Fatalf("explicit off lost on GET: %+v", k)
	}
	// TUI SaveToml again, then GUI reload: no stale server-side settings copy.
	got.ReviewModel = "local/new-checker"
	if err := config.SaveToml(global, got); err != nil {
		t.Fatal(err)
	}
	if k := findKnob(t, knobsGET(t, srv), "review_model"); k.Raw != got.ReviewModel {
		t.Fatalf("stale config: %+v", k)
	}
}

func TestModeKnobsValidationAndReset(t *testing.T) {
	srv := newTestServer(t, false)
	for _, key := range []string{"context_window", "prune_protect_tokens", "task_max_steps", "task_max_tokens", "draft_verify_max_rounds", "fallback_cooldown_seconds"} {
		for _, value := range []string{"-1", "one", "1.5"} {
			body, _ := json.Marshal(map[string]string{"key": key, "value": value})
			if rec := knobsPOST(t, srv, string(body)); rec.Code != http.StatusBadRequest {
				t.Fatalf("%s=%s accepted: %d", key, value, rec.Code)
			}
		}
		for _, value := range []string{"128", "0", ""} {
			body, _ := json.Marshal(map[string]string{"key": key, "value": value})
			if rec := knobsPOST(t, srv, string(body)); rec.Code != http.StatusOK {
				t.Fatalf("%s=%s rejected: %s", key, value, rec.Body.String())
			}
		}
	}
	for _, key := range []string{"darwin", "orchestrator", "task_parallel", "navigator", "darwin_auto_merge", "draft_verify"} {
		body, _ := json.Marshal(map[string]string{"key": key, "value": "invalid"})
		if rec := knobsPOST(t, srv, string(body)); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s invalid accepted", key)
		}
	}
	if rec := knobsPOST(t, srv, `{"key":"context_policy","value":"off"}`); rec.Code != http.StatusBadRequest {
		t.Fatal("read-only policy accepted")
	}
	global, _ := config.FindTomlPaths(srv.eng.DataDir(), srv.eng.Home())
	got, err := config.LoadToml(global)
	if err != nil || got.ContextWindow != 0 || got.PruneProtectTokens != 0 || !config.ReviewDisabled(got) || config.DarwinEnabled(got) {
		t.Fatalf("defaults changed: %+v, %v", got, err)
	}
	for _, key := range []string{"orchestrator", "task_model", "review_model", "darwin", "context_window", "prune_protect_tokens"} {
		if !findKnob(t, knobsGET(t, srv), key).NextSession {
			t.Fatalf("%s missing next-run contract", key)
		}
	}
}

func TestPostReviewResultUsesTranscriptNotice(t *testing.T) {
	for _, text := range []string{"review (checker): accepted", "review (checker): revise — notes sent back to the main model (1/1)", "review (checker): revise — round limit reached (1), finishing", "review (checker): no readable verdict — finished without review"} {
		wire, ok := toWireEvent(agent.NoticeEvent{Text: text})
		if !ok || wire.Type != "notice" || wire.Text != text {
			t.Fatalf("review result lost: %+v", wire)
		}
	}
}
