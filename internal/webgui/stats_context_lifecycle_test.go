package webgui

import (
	"context"
	"reflect"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestStatsContextResolutionWithoutUsageWindow(t *testing.T) {
	tests := []struct {
		name, baseURL, source string
		window                int
		setup                 func(*testing.T, *Engine, string)
	}{
		{name: "unknown local", baseURL: "http://127.0.0.1:8080", window: agent.DefaultContextWindow(), source: "fallback"},
		{name: "unknown remote", baseURL: "https://fixture.invalid/v1", window: 128000, source: "fallback-remote"},
		{name: "catalog", window: 32000, source: "catalog", setup: func(t *testing.T, e *Engine, _ string) {
			e.caps.Register(llm.ModelInfo{ID: "context-fixture", ContextLength: 32000})
		}},
		{name: "unique catalog alias", window: 48000, source: "catalog-alias", setup: func(t *testing.T, e *Engine, _ string) {
			e.caps.Register(llm.ModelInfo{ID: "vendor/context-fixture", ContextLength: 48000})
		}},
		{name: "learned", window: 42000, source: "learned", setup: func(t *testing.T, e *Engine, _ string) {
			e.learned.Learn("context-fixture", 42000)
		}},
		{name: "ambiguous aliases use learned", window: 42000, source: "learned", setup: func(t *testing.T, e *Engine, _ string) {
			e.caps.Register(llm.ModelInfo{ID: "vendor-a/context-fixture", ContextLength: 100000})
			e.caps.Register(llm.ModelInfo{ID: "vendor-b/context-fixture", ContextLength: 200000})
			e.learned.Learn("context-fixture", 42000)
		}},
		{name: "configured", window: 64000, source: "config", setup: func(t *testing.T, e *Engine, dir string) {
			writeDataConfig(t, dir, "context_window = 64000\n")
			e.caps.Register(llm.ModelInfo{ID: "context-fixture", ContextLength: 32000})
		}},
		{name: "scoped override wins", window: 52000, source: "model-override", setup: func(t *testing.T, e *Engine, dir string) {
			writeDataConfig(t, dir, "context_window = 64000\n")
			if err := e.modelContexts.Set(e.providerNameForConfig(e.cfg), "context-fixture", 52000); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, store, sess, dir := statsFixture(t)
			eng.cfg.Model = "context-fixture"
			if tt.baseURL != "" {
				eng.cfg.BaseURL = tt.baseURL
			}
			eng.caps = llm.NewCapabilityRegistry()
			if tt.setup != nil {
				tt.setup(t, eng, dir)
			}
			ctx := context.Background()
			fresh, err := eng.stats(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Context.Window != tt.window || fresh.Context.WindowSource != tt.source || fresh.Context.HasSnapshot || fresh.Context.EstimatedUsed != 0 {
				t.Fatalf("new-chat context=%+v, want window=%d source=%s without snapshot", fresh.Context, tt.window, tt.source)
			}
			id := eng.usageIdentity(eng.cfg, "model")
			u := usageRecordFromIdentity(id)
			u.SessionID, u.Input, u.Output = sess.ID, 100, 10
			u.ContextWindow, u.ContextUser = 0, tt.window/10
			if err := store.AppendUsage(ctx, u); err != nil {
				t.Fatal(err)
			}
			got, err := eng.stats(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Context.Window != tt.window || got.Context.WindowSource != tt.source || !got.Context.HasSnapshot || got.Context.Percent != 10 || got.Context.CompactThreshold != agent.AutoCompactThreshold(tt.window) {
				t.Fatalf("recovered context=%+v", got.Context)
			}
			if got.Tokens.Input != 100 || got.Tokens.Output != 10 {
				t.Fatalf("fallback fabricated billable usage: %+v", got.Tokens)
			}
		})
	}
}

func TestStatsContextKeepsMainSnapshotAfterHelpers(t *testing.T) {
	for _, source := range []string{"", "model", llm.PurposeMain} {
		t.Run("main_"+source, func(t *testing.T) {
			eng, store, sess, _ := statsFixture(t)
			ctx := context.Background()
			main := session.UsageRecord{SessionID: sess.ID, Provider: "coordinator", ProviderType: config.ProviderOpenAI,
				EndpointHost: "main.invalid", Model: "main-model", Source: source,
				Input: 1000, Output: 100, ContextWindow: 10000, ContextSystem: 20, ContextUser: 400, ContextTool: 80}
			if err := store.AppendUsage(ctx, main); err != nil {
				t.Fatal(err)
			}
			for _, helper := range []string{"worker", llm.PurposeCompact, llm.PurposeTitle, llm.PurposeNavigator, llm.PurposeMemory, llm.PurposeProbe} {
				if err := store.AppendUsage(ctx, session.UsageRecord{SessionID: sess.ID, Provider: "helper-provider", ProviderType: config.ProviderEcho,
					EndpointHost: "helper.invalid", Model: "helper-model", Source: helper,
					Input: 10, Output: 5, ContextWindow: 900, ContextUser: 700}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := eng.stats(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantContext := contextFromUsage(main)
			wantContext.RequestsToday = got.Context.RequestsToday
			if got.Model != main.Model || got.Session.Model != main.Model || got.Session.Provider != main.Provider || got.Session.ProviderType != main.ProviderType || got.Context != wantContext {
				t.Fatalf("helper replaced coordinator snapshot: %+v", got)
			}
			if got.Tokens.Input != 1060 || got.Tokens.Output != 130 || got.Tokens.Total != 1190 {
				t.Fatalf("helpers excluded from bills: %+v", got.Tokens)
			}
			rows, err := store.ReadUsage(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Cost, resolveStatsCost(eng.tomlConfig(), rows, session.UsageRecord{})) {
				t.Fatal("helper costs changed")
			}
		})
	}
}

func TestStatsActiveWindowUsesLoopProviderScopeWithoutChangingBillingIdentity(t *testing.T) {
	eng, _, _, dir := statsFixture(t)
	eng.cfg.Model = "scoped-model"
	writeDataConfig(t, dir, `[[providers]]
name = "billing-profile"
type = "echo"
base_url = "http://localhost"
model = "other-model"

[[providers]]
name = "loop-profile"
type = "echo"
base_url = "http://localhost"
model = "scoped-model"
`)
	if err := eng.modelContexts.Set("billing-profile", eng.cfg.Model, 24000); err != nil {
		t.Fatal(err)
	}
	if err := eng.modelContexts.Set("loop-profile", eng.cfg.Model, 64000); err != nil {
		t.Fatal(err)
	}
	id := eng.usageIdentity(eng.cfg, "model")
	if id.Provider != "billing-profile" || id.ContextWindow != 64000 || id.WindowSource != "model-override" {
		t.Fatalf("window/billing provider contracts diverged: %+v", id)
	}
}

func TestStatsMissingWindowDoesNotMatchSameModelOnDifferentProvider(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	eng.cfg.Model = "shared-model"
	if err := eng.modelContexts.Set(eng.providerNameForConfig(eng.cfg), eng.cfg.Model, 100000); err != nil {
		t.Fatal(err)
	}
	if err := eng.modelContexts.Set("earlier-provider", eng.cfg.Model, 25000); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(context.Background(), session.UsageRecord{SessionID: sess.ID, Provider: "earlier-provider", ProviderType: config.ProviderOpenAI,
		EndpointHost: "earlier.invalid", Model: eng.cfg.Model, Source: llm.PurposeMain, Input: 500, Output: 40, ContextUser: 2500}); err != nil {
		t.Fatal(err)
	}
	got, err := eng.stats(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Context.Window != 25000 || got.Context.Percent != 10 || got.Session.Provider != "earlier-provider" {
		t.Fatalf("same model incorrectly matched active provider: %+v", got)
	}
}

func TestStatsMissingWindowKeepsHistoricalProfileOnSameEndpoint(t *testing.T) {
	eng, store, sess, dir := statsFixture(t)
	eng.cfg.Model = "shared-model"
	writeDataConfig(t, dir, `[[providers]]
name = "current-profile"
type = "echo"
base_url = "http://localhost"
model = "shared-model"

[[providers]]
name = "earlier-profile"
type = "echo"
base_url = "http://localhost"
model = "different-default"
`)
	if err := eng.modelContexts.Set("current-profile", eng.cfg.Model, 100000); err != nil {
		t.Fatal(err)
	}
	if err := eng.modelContexts.Set("earlier-profile", eng.cfg.Model, 25000); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(context.Background(), session.UsageRecord{SessionID: sess.ID, Provider: "earlier-profile", ProviderType: config.ProviderEcho,
		EndpointHost: "localhost", Model: eng.cfg.Model, Source: llm.PurposeMain, Input: 500, Output: 40, ContextUser: 2500}); err != nil {
		t.Fatal(err)
	}
	got, err := eng.stats(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Context.Window != 25000 || got.Context.Percent != 10 || got.Session.Provider != "earlier-profile" {
		t.Fatalf("same endpoint/model incorrectly matched active profile: %+v", got)
	}
}

func TestUsageCallSinkSeparatesHelperPurposesAndPreservesNoUsageContract(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	sink := eng.usageCallSink(store, sess.ID)
	purposes := []string{"", llm.PurposeMain, llm.PurposeTask, llm.PurposeCompact, llm.PurposeTitle, llm.PurposeNavigator, llm.PurposeMemory, llm.PurposeProbe}
	wantSources := []string{"model", "model", "worker", "compact", "title", "navigator", "memory", "probe"}
	for _, purpose := range purposes {
		sink(llm.CallStat{Purpose: purpose, Model: eng.cfg.Model, TokensIn: 100, TokensOut: 10, Request: llm.RequestBreakdown{User: 20}})
	}
	sink(llm.CallStat{Purpose: llm.PurposeMain, Model: eng.cfg.Model, Canceled: true, Request: llm.RequestBreakdown{User: 99}})
	rows, err := store.ReadUsage(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(wantSources) {
		t.Fatalf("usage-less cancellation created billable row: %+v", rows)
	}
	window := eng.usageIdentity(eng.cfg, "model").ContextWindow
	for i, row := range rows {
		if row.Source != wantSources[i] || row.ContextWindow != window || row.ContextUser != 20 {
			t.Fatalf("row %d=%+v, want source=%s window=%d", i, row, wantSources[i], window)
		}
	}
}

func TestStatsHelperOnlyAndUnmeteredHistoryUseActiveWindow(t *testing.T) {
	for _, helperOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmetered", true: "helper only"}[helperOnly], func(t *testing.T) {
			eng, store, sess, _ := statsFixture(t)
			ctx := context.Background()
			message := llm.Message{Role: llm.RoleUser, Content: "Keep all data next to the executable and do not publish the changes."}
			appendStatsMessage(t, store, sess.ID, message)
			if helperOnly {
				if err := store.AppendUsage(ctx, session.UsageRecord{SessionID: sess.ID, Provider: "helper", Model: "title-model", Source: llm.PurposeTitle, Input: 12, Output: 3, ContextWindow: 1000, ContextUser: 900}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := eng.stats(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			preview := eng.usageIdentity(eng.cfg, "model")
			want := contextFromMessages([]llm.Message{message}, preview.ContextWindow)
			if !got.Context.HasSnapshot || got.Context.Window != want.Window || got.Context.EstimatedUsed != want.EstimatedUsed || got.Context.Breakdown != want.Breakdown || got.Session.Model == "title-model" || got.Session.Provider == "helper" {
				t.Fatalf("history fallback=%+v want=%+v", got, want)
			}
			if helperOnly && (got.Tokens.Input != 12 || got.Tokens.Output != 3) {
				t.Fatalf("helper-only bills lost: %+v", got.Tokens)
			}
		})
	}
}

func TestStatsMissingWindowUsesRecordIdentityAfterModelSwitch(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	eng.cfg.Model = "current-model"
	eng.caps = llm.NewCapabilityRegistry()
	if err := eng.modelContexts.Set(eng.providerNameForConfig(eng.cfg), eng.cfg.Model, 100000); err != nil {
		t.Fatal(err)
	}
	if err := eng.modelContexts.Set("earlier-provider", "earlier-model", 25000); err != nil {
		t.Fatal(err)
	}
	u := session.UsageRecord{SessionID: sess.ID, Provider: "earlier-provider", ProviderType: config.ProviderOpenAI,
		EndpointHost: "earlier.invalid", Model: "earlier-model", Source: llm.PurposeMain, Input: 500, Output: 40, ContextUser: 2500}
	if err := store.AppendUsage(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	got, err := eng.stats(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != u.Model || got.Context.Window != 25000 || got.Context.Percent != 10 || got.Context.WindowSource != "model-override" {
		t.Fatalf("active selection overwrote older request denominator: %+v", got)
	}
}
