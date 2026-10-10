package webgui

import (
	"context"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestStatsActiveContextWithoutMainSnapshot(t *testing.T) {
	eng, _, _, _ := statsFixture(t)
	eng.cfg.Model = "unknown-active-fixture"
	eng.caps = llm.NewCapabilityRegistry()
	got, err := eng.stats(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	provider, model, _ := eng.RuntimeSelection()
	if got.ActiveContext.Provider != provider || got.ActiveContext.Model != model || got.ActiveContext.Window != agent.DefaultContextWindow() || got.ActiveContext.WindowSource != "fallback" || got.ActiveContext.CompactThreshold != agent.AutoCompactThreshold(agent.DefaultContextWindow()) {
		t.Fatalf("new-chat active context=%+v", got.ActiveContext)
	}
	if got.Context.HasSnapshot || got.Tokens.Total != 0 {
		t.Fatalf("active limit fabricated a request snapshot: %+v", got)
	}
}

func appendActiveContextMain(t *testing.T, store *session.Store, sessionID string) {
	t.Helper()
	if err := store.AppendUsage(context.Background(), session.UsageRecord{
		SessionID: sessionID, Provider: "previous-provider", ProviderType: config.ProviderOpenAI,
		EndpointHost: "previous.invalid", Model: "previous-model", Source: llm.PurposeMain,
		Input: 1000, Output: 100, ContextWindow: 10000, ContextUser: 2500,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertActiveContextKeepsMain(t *testing.T, got statsView) {
	t.Helper()
	if got.Context.Window != 10000 || got.Context.EstimatedUsed != 2500 || got.Context.Percent != 25 || !got.Context.HasSnapshot || got.Model != "previous-model" || got.Session.Provider != "previous-provider" || got.Tokens.Input != 1000 || got.Tokens.Output != 100 {
		t.Fatalf("active selection rewrote the previous main request: %+v", got)
	}
}

func TestStatsActiveContextChangesWithSelectedModel(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	appendActiveContextMain(t, store, sess.ID)
	eng.caps = llm.NewCapabilityRegistry()
	eng.caps.Register(llm.ModelInfo{ID: "first-model", ContextLength: 32000})
	eng.caps.Register(llm.ModelInfo{ID: "second-model", ContextLength: 64000})
	for _, selected := range []struct {
		model  string
		window int
	}{{"first-model", 32000}, {"second-model", 64000}} {
		eng.cfg.Model = selected.model
		got, err := eng.stats(context.Background(), sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ActiveContext.Model != selected.model || got.ActiveContext.Window != selected.window || got.ActiveContext.WindowSource != "catalog" || got.ActiveContext.CompactThreshold != agent.AutoCompactThreshold(selected.window) {
			t.Fatalf("selected-model active context=%+v", got.ActiveContext)
		}
		assertActiveContextKeepsMain(t, got)
	}
}

func TestStatsActiveContextChangesProviderForSameModelID(t *testing.T) {
	eng, store, sess, dir := statsFixture(t)
	appendActiveContextMain(t, store, sess.ID)
	eng.cfg.Model = "shared-active-model"
	writeDataConfig(t, dir, `[[providers]]
name = "first-profile"
type = "echo"
base_url = "http://127.0.0.1:8081"
model = "shared-active-model"

[[providers]]
name = "second-profile"
type = "echo"
base_url = "http://127.0.0.1:8082"
model = "shared-active-model"
`)
	if err := eng.modelContexts.Set("first-profile", eng.cfg.Model, 32000); err != nil {
		t.Fatal(err)
	}
	if err := eng.modelContexts.Set("second-profile", eng.cfg.Model, 64000); err != nil {
		t.Fatal(err)
	}
	for _, selected := range []struct {
		provider, baseURL string
		window            int
	}{{"first-profile", "http://127.0.0.1:8081", 32000}, {"second-profile", "http://127.0.0.1:8082", 64000}} {
		eng.cfg.BaseURL = selected.baseURL
		got, err := eng.stats(context.Background(), sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ActiveContext.Provider != selected.provider || got.ActiveContext.Model != eng.cfg.Model || got.ActiveContext.Window != selected.window || got.ActiveContext.WindowSource != "model-override" {
			t.Fatalf("provider/model-scoped active context=%+v", got.ActiveContext)
		}
		assertActiveContextKeepsMain(t, got)
	}
}

func TestStatsActiveContextManualOverrideAndAutoKeepHistoricalPercent(t *testing.T) {
	eng, store, sess, dir := statsFixture(t)
	appendActiveContextMain(t, store, sess.ID)
	eng.cfg.Model = "manual-active-model"
	eng.caps = llm.NewCapabilityRegistry()
	eng.caps.Register(llm.ModelInfo{ID: eng.cfg.Model, ContextLength: 32000})
	writeDataConfig(t, dir, "context_window = 48000\n")
	provider, _, _ := eng.RuntimeSelection()
	for _, selected := range []struct {
		manual int
		source string
		window int
	}{{64000, "model-override", 64000}, {96000, "model-override", 96000}, {0, "config", 48000}} {
		if selected.manual > 0 {
			if err := eng.modelContexts.Set(provider, eng.cfg.Model, selected.manual); err != nil {
				t.Fatal(err)
			}
		} else if _, err := eng.modelContexts.Remove(provider, eng.cfg.Model); err != nil {
			t.Fatal(err)
		}
		got, err := eng.stats(context.Background(), sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.ActiveContext.Window != selected.window || got.ActiveContext.WindowSource != selected.source || got.ActiveContext.CompactThreshold != agent.AutoCompactThreshold(selected.window) {
			t.Fatalf("manual/auto active context=%+v", got.ActiveContext)
		}
		assertActiveContextKeepsMain(t, got)
	}
}

func TestStatsActiveContextProviderAndWindowUseCapturedConfig(t *testing.T) {
	eng, _, _, dir := statsFixture(t)
	writeDataConfig(t, dir, `[[providers]]
name = "captured-profile"
type = "echo"
base_url = "http://127.0.0.1:8081"
model = "shared-active-model"

[[providers]]
name = "later-profile"
type = "echo"
base_url = "http://127.0.0.1:8082"
model = "shared-active-model"
`)
	captured := config.Config{Provider: config.ProviderEcho, Model: "shared-active-model", BaseURL: "http://127.0.0.1:8081"}
	eng.cfg = config.Config{Provider: config.ProviderEcho, Model: "shared-active-model", BaseURL: "http://127.0.0.1:8082"}
	if err := eng.modelContexts.Set("captured-profile", captured.Model, 32000); err != nil {
		t.Fatal(err)
	}
	if err := eng.modelContexts.Set("later-profile", eng.cfg.Model, 64000); err != nil {
		t.Fatal(err)
	}
	got := eng.statsActiveContext(captured)
	if got.Provider != "captured-profile" || got.Model != captured.Model || got.Window != 32000 || got.WindowSource != "model-override" {
		t.Fatalf("active context reread the later model/provider: %+v", got)
	}
}
