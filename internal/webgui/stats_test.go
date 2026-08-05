package webgui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/account/pricing"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	systats "supercli/internal/system/stats"
)

func statsFixture(t *testing.T) (*Engine, *session.Store, session.Session, string) {
	t.Helper()
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	store, err := session.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(dir, "gpt-5.6-sol", "priced session")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return eng, store, sess, dir
}

func appendStatsMessage(t *testing.T, store *session.Store, sessionID string, msg llm.Message) {
	t.Helper()
	encoded, err := session.FromMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(context.Background(), sessionID, encoded); err != nil {
		t.Fatal(err)
	}
}

type oneUsageProvider struct {
	name  string
	usage llm.Usage
}

func (p oneUsageProvider) Name() string { return p.name }

func (p oneUsageProvider) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	out := make(chan llm.Delta, 1)
	out <- llm.Delta{Usage: &p.usage}
	close(out)
	return out, nil
}

func TestUsageCallSinkPersistsUsageWhenConsumerCancels(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	inner := oneUsageProvider{name: "gpt-5.6-sol", usage: llm.Usage{Input: 123, Output: 45, CachedInput: 100, Reasoning: 20}}
	// The provider comes metered from the factory; the per-session
	// usage recorder rides the context as an llm.CallSink.
	provider := llm.Metered(inner, config.ProviderOpenAI, llm.PurposeMain, func(llm.CallStat) {})
	ctx, cancel := context.WithCancel(context.Background())
	ctx = llm.WithCallSink(ctx, eng.usageCallSink(store, sess.ID))
	out, err := provider.Complete(ctx, []llm.Message{{Role: llm.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for range out {
	}
	rows, err := store.ReadUsage(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Input != 123 || rows[0].CachedInput != 100 || rows[0].Reasoning != 20 {
		t.Fatalf("canceled consumer lost terminal usage: %+v", rows)
	}
}

func TestStatsAggregatesCallsWithoutDoubleCountingCacheOrReasoning(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	appendStatsMessage(t, store, sess.ID, llm.Message{Role: llm.RoleUser, Content: "calculate"})
	appendStatsMessage(t, store, sess.ID, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "read", Arguments: `{}`}}})
	appendStatsMessage(t, store, sess.ID, llm.Message{Role: llm.RoleTool, ToolCallID: "call-1", Name: "read", Content: "result"})
	appendStatsMessage(t, store, sess.ID, llm.Message{Role: llm.RoleAssistant, Content: "done"})

	ctx := context.Background()
	first := session.UsageRecord{
		SessionID: sess.ID, Provider: "openai", ProviderType: config.ProviderOpenAI,
		EndpointHost: "api.openai.com", Model: "gpt-5.6-sol",
		Input: 1_000_000, Output: 100_000, CachedInput: 800_000, Reasoning: 50_000,
		HasCachedInput: true, HasReasoning: true, ContextWindow: 1_050_000,
		ContextSystem: 100, ContextUser: 200, ContextAssistant: 300, ContextTool: 400, ContextOther: 50,
	}
	if err := store.AppendUsage(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Input, second.Output, second.CachedInput, second.Reasoning = 1_000, 200, 0, 0
	second.HasCachedInput, second.HasReasoning = false, false
	second.ContextUser, second.ContextAssistant, second.ContextTool = 250, 350, 450
	if err := store.AppendUsage(ctx, second); err != nil {
		t.Fatal(err)
	}

	got, err := eng.stats(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tokens.Input != 1_001_000 || got.Tokens.Output != 100_200 || got.Tokens.Total != 1_101_200 {
		t.Fatalf("token totals = %+v", got.Tokens)
	}
	if got.Tokens.CachedInput != 800_000 || got.Tokens.EvaluatedInput != 201_000 || got.Tokens.Reasoning != 50_000 {
		t.Fatalf("token subsets were double-counted or lost: %+v", got.Tokens)
	}
	if got.Context.EstimatedUsed != 1_200 || got.Context.Breakdown.Tools != 450 {
		t.Fatalf("context should describe the last model call: %+v", got.Context)
	}
	if got.Session.Messages != 4 || got.Session.UserMessages != 1 || got.Session.AssistantMsgs != 2 || got.Session.ToolMessages != 1 || got.Session.ToolCalls != 1 {
		t.Fatalf("message counts = %+v", got.Session)
	}
	if got.Cost.State != "estimated" || got.Cost.Amount == nil || math.Abs(*got.Cost.Amount-4.411) > 1e-9 {
		t.Fatalf("OpenAI cost = %+v", got.Cost)
	}
}

func TestSummarizeTelemetryFindsBottleneckWithoutPromptData(t *testing.T) {
	turns := []session.TurnSummary{
		{DurationMS: 1200, Steps: 2, ModelCalls: 2, HelperCalls: 1, ToolFailures: 1, Phases: map[string]int64{
			"request_encode": 10_000, "backend_wait": 700_000, "stream_total": 300_000,
			"tool_execution": 100_000, "context_prepare": 20_000, "next_turn_prepare": 10_000,
			"session_persist": 5_000, "tool:read_many": 80_000,
		}},
	}
	got := summarizeTelemetry(turns, statsTokensView{})
	if got.Samples != 1 || got.Bottleneck != "model" || got.ModelMS != 1010 || got.ToolsMS != 100 || got.CLIMS != 30 {
		t.Fatalf("telemetry summary = %+v", got)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "read_many" || got.Tools[0].DurationMS != 80 {
		t.Fatalf("tool telemetry = %+v", got.Tools)
	}
	if !slices.Contains(got.Signals, "model_bound") || !slices.Contains(got.Signals, "tool_failures") {
		t.Fatalf("signals = %v", got.Signals)
	}
}

// Helper inference is the number that decides whether it should be moved out
// of the reply, so the panel must report both its count and its share of the
// wall time the user waited through.
func TestSummarizeTelemetryReportsHelperInferenceShare(t *testing.T) {
	turns := []session.TurnSummary{
		{DurationMS: 1000, Steps: 1, ModelCalls: 2, AuxCalls: 1, AuxUs: 300_000, Phases: map[string]int64{
			"backend_wait": 600_000, "model:navigator": 300_000, "context_prepare": 20_000,
		}},
		{DurationMS: 1000, Steps: 1, ModelCalls: 2, AuxCalls: 2, AuxUs: 100_000, Phases: map[string]int64{
			"backend_wait": 700_000, "model:reflection": 100_000, "context_prepare": 20_000,
		}},
	}
	got := summarizeTelemetry(turns, statsTokensView{})
	if got.AuxCalls != 3 || got.AuxMS != 400 {
		t.Fatalf("aux counters = %d calls / %d ms", got.AuxCalls, got.AuxMS)
	}
	if got.AuxShare != 20 {
		t.Fatalf("aux share = %d%%, want 20%% of 2000 ms", got.AuxShare)
	}
	if !slices.Contains(got.Signals, "aux_heavy") {
		t.Fatalf("signals = %v, want aux_heavy", got.Signals)
	}
	// A turn with no helper inference must not invent one.
	quiet := summarizeTelemetry([]session.TurnSummary{{DurationMS: 1000, Phases: map[string]int64{"backend_wait": 900_000}}}, statsTokensView{})
	if quiet.AuxCalls != 0 || quiet.AuxMS != 0 || quiet.AuxShare != 0 || slices.Contains(quiet.Signals, "aux_heavy") {
		t.Fatalf("quiet turn = %+v", quiet)
	}
}

// Titles, indexing and vision belong to no turn; they must still be counted,
// and from the duration llm.Metered already measured.
func TestOffTurnCallsAreCountedFromTheMeteredDuration(t *testing.T) {
	eng := &Engine{}
	if ctx := eng.countOffTurnCalls(context.Background()); ctx == nil {
		t.Fatal("countOffTurnCalls returned no context")
	}
	sink := eng.offTurnSink()
	sink(llm.CallStat{Purpose: "title", Duration: 250 * time.Millisecond})
	sink(llm.CallStat{Purpose: "document-index", Duration: 750 * time.Millisecond})
	calls, us := eng.offTurnSnapshot()
	if calls != 2 || us != 1_000_000 {
		t.Fatalf("off-turn snapshot = %d calls / %d µs", calls, us)
	}
	if zeroCalls, zeroUs := (*Engine)(nil).offTurnSnapshot(); zeroCalls != 0 || zeroUs != 0 {
		t.Fatalf("nil engine must not panic or count: %d/%d", zeroCalls, zeroUs)
	}
}

// Start-path divergence guard: the GUI builds its own loop, so the stats
// recorder can silently be left nil there while the CLI keeps measuring.
// A live recorder on the web path is what makes every turn counter (aux
// included) real instead of a permanent zero.
func TestWebLoopRecordsTelemetryLikeTheCLI(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	recorder := systats.NewMemory()
	loop, err := eng.newLoopWithSessionAtUsageInteractive(nil, nil, eng.Home(), nil, nil, recorder)
	if err != nil {
		t.Fatalf("newLoopWithSessionAtUsageInteractive: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	events, err := loop.Run(ctx, "powiedz cokolwiek")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for range events {
	}
	turns := recorder.Snapshot()
	if len(turns) == 0 {
		t.Fatal("web loop recorded no telemetry turns: the stats recorder is not wired on the GUI path")
	}
	if len(turns[0].Phases) == 0 {
		t.Fatalf("web loop recorded a turn without phases: %+v", turns[0])
	}
}

func TestStatsCostClassificationIsConservative(t *testing.T) {
	tests := []struct {
		name         string
		usage        session.UsageRecord
		wantState    string
		wantAmount   bool
		manual       bool
		manualAmount float64
	}{
		{
			name:      "custom router does not borrow official model price",
			usage:     session.UsageRecord{Provider: "AnyRouter", ProviderType: config.ProviderOpenAI, EndpointHost: "anyrouter.top", Model: "gpt-5.6-sol", Input: 1000, Output: 1000},
			wantState: "unknown",
		},
		{
			name:      "codex is subscription",
			usage:     session.UsageRecord{Provider: "codex", ProviderType: config.ProviderCodex, EndpointHost: "chatgpt.com", Model: "gpt-5.3-codex", Input: 1000, Output: 1000},
			wantState: "subscription",
		},
		{
			name:      "private endpoint is local",
			usage:     session.UsageRecord{Provider: "ollama", ProviderType: config.ProviderOpenAI, EndpointHost: "127.0.0.1", Model: "llama", Input: 1000, Output: 1000},
			wantState: "local",
		},
		{
			name:      "explicit free model",
			usage:     session.UsageRecord{Provider: "openrouter", ProviderType: config.ProviderOpenAI, EndpointHost: "openrouter.ai", Model: "some/model:free", Input: 1000, Output: 1000},
			wantState: "free", wantAmount: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, store, sess, _ := statsFixture(t)
			tt.usage.SessionID = sess.ID
			if err := store.AppendUsage(context.Background(), tt.usage); err != nil {
				t.Fatal(err)
			}
			got, err := eng.stats(context.Background(), sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Cost.State != tt.wantState || (got.Cost.Amount != nil) != tt.wantAmount {
				t.Fatalf("cost = %+v, want state=%s amount=%v", got.Cost, tt.wantState, tt.wantAmount)
			}
		})
	}
}

func TestStatsManualPriceIsProviderScoped(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	m := eng.providerManager()
	if err := m.SetProviderPrice("AnyRouter", "shared-model", 2, 0.2, 8); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(context.Background(), session.UsageRecord{
		SessionID: sess.ID, Provider: "AnyRouter", ProviderType: config.ProviderOpenAI,
		EndpointHost: "anyrouter.top", Model: "shared-model", Input: 1_000_000, Output: 1_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := eng.stats(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cost.State != "manual" || got.Cost.Amount == nil || *got.Cost.Amount != 10 {
		t.Fatalf("manual cost = %+v", got.Cost)
	}

	other, err := store.Create(eng.Home(), "shared-model", "other provider")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(context.Background(), session.UsageRecord{
		SessionID: other.ID, Provider: "DifferentRouter", ProviderType: config.ProviderOpenAI,
		EndpointHost: "different.example", Model: "shared-model", Input: 1_000_000, Output: 1_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	got, err = eng.stats(context.Background(), other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cost.State != "unknown" || got.Cost.Amount != nil {
		t.Fatalf("manual price leaked across providers: %+v", got.Cost)
	}
}

func TestStatsMixedKnownAndUnknownCallsIsPartial(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	ctx := context.Background()
	for _, u := range []session.UsageRecord{
		{SessionID: sess.ID, Provider: "openai", ProviderType: config.ProviderOpenAI, EndpointHost: "api.openai.com", Model: "gpt-5.6-sol", Input: 1_000_000},
		{SessionID: sess.ID, Provider: "AnyRouter", ProviderType: config.ProviderOpenAI, EndpointHost: "anyrouter.top", Model: "gpt-5.6-sol", Input: 1_000_000},
	} {
		if err := store.AppendUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	got, err := eng.stats(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cost.State != "estimated" || !got.Cost.Partial || got.Cost.UnknownCalls != 1 || got.Cost.Amount == nil || *got.Cost.Amount != 5 {
		t.Fatalf("mixed cost = %+v", got.Cost)
	}
}

func TestOpenRouterRequiresItsOwnCatalogRate(t *testing.T) {
	t.Cleanup(func() {
		credits.SetFetchedRates(nil)
		credits.SetProviderRates(nil)
	})
	// A direct OpenAI fallback with the same model name is not enough to quote
	// a routed request.
	q := quoteUsage(config.TomlConfig{}, session.UsageRecord{
		Provider: "openrouter", ProviderType: config.ProviderOpenAI,
		EndpointHost: "openrouter.ai", Model: "gpt-4o", Input: 1000,
	})
	if q.state != "unknown" {
		t.Fatalf("generic direct price leaked into OpenRouter quote: %+v", q)
	}
	credits.SetProviderRates(map[string]credits.Rate{
		"openrouter/gpt-4o": {InputPer1k: 0.003, OutputPer1k: 0.012},
	})
	q = quoteUsage(config.TomlConfig{}, session.UsageRecord{
		Provider: "openrouter", ProviderType: config.ProviderOpenAI,
		EndpointHost: "openrouter.ai", Model: "gpt-4o", Input: 1000,
	})
	if q.state != "estimated" || q.amount != 0.003 || q.source != "provider" {
		t.Fatalf("exact OpenRouter rate was not used: %+v", q)
	}
}

func TestNewEngineAppliesFreshPricingCache(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() {
		credits.SetFetchedRates(nil)
		credits.SetProviderRates(nil)
	})
	entry := pricing.PriceEntry{
		ModelID: "vendor/cached-model", InputPer1M: 2, CachedInputPer1M: 0.2,
		OutputPer1M: 8, ContextLength: 777_000, Source: "openrouter", FetchedAt: time.Now().UTC(),
	}
	if err := pricing.SaveCache(dir, []pricing.PriceEntry{entry}); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	rate, _, ok := credits.LookupRateForProvider("openrouter", entry.ModelID)
	if !ok || rate.InputPer1k != 0.002 || rate.CachedInputPer1k != 0.0002 || rate.OutputPer1k != 0.008 {
		t.Fatalf("cached price was not applied: %+v ok=%v", rate, ok)
	}
	info, ok := eng.caps.Get(entry.ModelID)
	if !ok || info.ContextLength != entry.ContextLength || info.InputCost != entry.InputPer1M {
		t.Fatalf("cached model metadata was not applied: %+v ok=%v", info, ok)
	}
}

func TestApplyWebPricingEntriesMirrorsUniqueCanonicalIDToRouterAlias(t *testing.T) {
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "gpt-5.6-sol", Provider: "any-router", Source: llm.SourceProvider})
	applyWebPricingEntries(caps, []pricing.PriceEntry{{
		ModelID:       "openai/gpt-5.6-sol",
		InputPer1M:    5,
		OutputPer1M:   30,
		ContextLength: 1_050_000,
		FetchedAt:     time.Now(),
	}})

	got, ok := caps.Get("gpt-5.6-sol")
	if !ok || got.ContextLength != 1_050_000 || got.Provider != "any-router" {
		t.Fatalf("router alias metadata = %+v ok=%v", got, ok)
	}
}

func TestApplyWebPricingEntriesLeavesAmbiguousShortIDUnknown(t *testing.T) {
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "shared-model", Provider: "custom-router", Source: llm.SourceProvider})
	applyWebPricingEntries(caps, []pricing.PriceEntry{
		{ModelID: "vendor-a/shared-model", ContextLength: 100_000},
		{ModelID: "vendor-b/shared-model", ContextLength: 200_000},
	})

	got, _ := caps.Get("shared-model")
	if got.ContextLength != 0 {
		t.Fatalf("ambiguous alias received context_length %d", got.ContextLength)
	}
}

func TestStatsRejectsSessionOutsideWorkspace(t *testing.T) {
	eng, store, _, dir := statsFixture(t)
	foreign, err := store.Create(filepath.Join(dir, "other"), "model", "foreign")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.stats(context.Background(), foreign.ID); err != errSessionOutsideWorkspace {
		t.Fatalf("stats error = %v, want workspace isolation", err)
	}
	rec := httptest.NewRecorder()
	NewServer(eng, false).handleStats(rec, httptest.NewRequest(http.MethodGet, "/api/stats?session="+foreign.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign session HTTP status = %d, want 404", rec.Code)
	}
}

func TestHandleModelPriceSaveValidateAndRemove(t *testing.T) {
	srv := newTestServer(t, false)
	h := srv.Handler()
	request := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/model/price", strings.NewReader(body))
		req.Host = "127.0.0.1:8080"
		req.RemoteAddr = "127.0.0.1:43210"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := request(`{"provider":"AnyRouter","model":"gpt-custom","input_per_million":2,"cached_input_per_million":0.2,"output_per_million":8}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", rec.Code, rec.Body.String())
	}
	tc, err := config.LoadToml(filepath.Join(srv.eng.DataDir(), "config.toml"))
	if err != nil || len(tc.ModelPrices) != 1 {
		t.Fatalf("saved config = %+v err=%v", tc.ModelPrices, err)
	}
	if tc.ModelPrices[0].Provider != "AnyRouter" || tc.ModelPrices[0].CachedInputCost != 0.2 {
		t.Fatalf("saved price = %+v", tc.ModelPrices[0])
	}

	if bad := request(`{"provider":"AnyRouter","model":"gpt-custom","input_per_million":-1}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("negative price status=%d", bad.Code)
	}
	if unknown := request(`{"provider":"AnyRouter","model":"gpt-custom","wat":1}`); unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d", unknown.Code)
	}

	rec = request(`{"provider":"AnyRouter","model":"gpt-custom","remove":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove status=%d body=%s", rec.Code, rec.Body.String())
	}
	tc, _ = config.LoadToml(filepath.Join(srv.eng.DataDir(), "config.toml"))
	if len(tc.ModelPrices) != 0 {
		payload, _ := json.Marshal(tc.ModelPrices)
		t.Fatalf("price was not removed: %s", payload)
	}
}
