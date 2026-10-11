package webgui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/llm/factory"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

type compactResolverParentKey struct{}

type compactResolverRequest struct {
	body     []byte
	purpose  string
	parent   any
	deadline time.Time
	effort   string
}

// The provider-boundary oracle records the complete message/tool payload. It
// sends no inference request and supplies deterministic usage including cache
// and reasoning subsets, so construction cannot masquerade as a model call.
type compactResolverProvider struct {
	t        *testing.T
	model    string
	requests []compactResolverRequest
	onCall   func(context.Context)
	fail     bool
}

func (p *compactResolverProvider) Name() string { return p.model }

func (p *compactResolverProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct {
		Messages []llm.Message
		Tools    []llm.ToolDef
	}{messages, defs})
	if err != nil {
		p.t.Fatal(err)
	}
	deadline, _ := ctx.Deadline()
	p.requests = append(p.requests, compactResolverRequest{
		body: body, purpose: llm.PurposeFromContext(ctx), parent: ctx.Value(compactResolverParentKey{}),
		deadline: deadline, effort: llm.ReasoningEffort(),
	})
	if p.onCall != nil {
		p.onCall(ctx)
	}
	if p.fail {
		return nil, errors.New("synthetic side completion failure")
	}
	text := "The requested work is complete."
	if llm.PurposeFromContext(ctx) == llm.PurposeCompact {
		text = "Goal: finish the coding task.\nDone: inspected the old source.\nState: keep portable data and every user constraint.\nPending: finish the current task."
	}
	ch := make(chan llm.Delta, 1)
	ch <- llm.Delta{Content: text, FinishReason: "stop", Usage: &llm.Usage{Input: 120, Output: 24, CachedInput: 16, Reasoning: 5}}
	close(ch)
	return ch, nil
}

func compactResolverEngine(t *testing.T, body string) (*Engine, *compactResolverProvider) {
	t.Helper()
	dir := t.TempDir()
	writeDataConfig(t, dir, "orchestrator = false\npreflight_repo = false\ncontext_window = 100000\n"+body)
	e, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	main := &compactResolverProvider{t: t, model: e.cfg.Model}
	e.prov = llm.MeteredConnection(main, e.cfg.Provider, e.cfg.BaseURL, e.cfg.APIKey, llm.PurposeMain, nil)
	return e, main
}

func compactResolverRun(t *testing.T, l *agent.Loop, ctx context.Context) {
	t.Helper()
	events, err := l.Run(ctx, "Give the completed answer.")
	if err != nil {
		t.Fatal(err)
	}
	for ev := range events {
		if failure, ok := ev.(agent.ErrorEvent); ok {
			t.Fatalf("run failed: %+v", failure)
		}
	}
}

func compactResolverParent(t *testing.T) context.Context {
	t.Helper()
	ctx := context.WithValue(context.Background(), compactResolverParentKey{}, "request-snapshot")
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func TestWebCompactResolverOrdinaryTurnDoesNotBuildSideProvider(t *testing.T) {
	e, main := compactResolverEngine(t, "compact_model = \"summary-fixture\"\n")
	var calls []llm.CallStat
	e.prov = llm.MeteredConnection(main, e.cfg.Provider, e.cfg.BaseURL, e.cfg.APIKey, llm.PurposeMain, func(c llm.CallStat) { calls = append(calls, c) })
	builds := 0
	e.factory = factory.New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		builds++
		return &compactResolverProvider{t: t, model: cfg.Model}, nil
	}, e.dataDir, e.caps)
	l, err := e.newLoop()
	if err != nil {
		t.Fatal(err)
	}
	_ = l.ContextReport()
	compactResolverRun(t, l, compactResolverParent(t))
	if builds != 0 || len(main.requests) != 1 || main.requests[0].purpose == llm.PurposeCompact {
		t.Fatalf("ordinary turn built optional backend or issued helpers: builds=%d main=%d", builds, len(main.requests))
	}
	// The context label and wrapper default are separate contracts. An unlabeled
	// ordinary request is metered as main; an explicit loop label wins. Neither
	// the raw transport nor a chat/agent route must manufacture PurposeMain.
	wantPurpose := main.requests[0].purpose
	if wantPurpose == "" {
		wantPurpose = llm.PurposeMain
	}
	if len(calls) != 1 || calls[0].Purpose != wantPurpose || calls[0].Model != main.model ||
		calls[0].TokensIn != 120 || calls[0].TokensOut != 24 || calls[0].TokensCached != 16 || calls[0].TokensReasoning != 5 ||
		calls[0].Canceled || calls[0].Failed {
		t.Fatalf("ordinary main request was not metered exactly once: %+v", calls)
	}
}

func TestWebCompactResolverKeepsMutableAuthAndLegacyDiscoveryEager(t *testing.T) {
	for _, fixture := range []struct{ name, provider, baseURL string }{
		{"codex-auth-pool", config.ProviderCodex, "https://auth-fixture.invalid"},
		{"zen-openai-catalog", config.ProviderOpenAI, "https://opencode.ai/zen/v1"},
		{"zen-responses-catalog", config.ProviderResponses, "https://opencode.ai/zen/v1"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			e, main := compactResolverEngine(t, "")
			// Custom builders can consume mutable auth/account or discovery state
			// beyond config.Config. This fixture captures that state exactly when
			// construction runs, without OAuth, catalog HTTP or external APIs.
			type constructionState struct {
				issuer, clientID, catalog string
				accounts                  []string
			}
			clone := func(s constructionState) constructionState {
				s.accounts = append([]string(nil), s.accounts...)
				return s
			}
			mutable := constructionState{"issuer-before", "client-before", "catalog-before", []string{"account-before"}}
			initial := clone(mutable)
			var constructed, used []constructionState
			var transports []*compactResolverProvider
			e.factory = factory.New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
				if cfg.Provider != fixture.provider || cfg.BaseURL != fixture.baseURL || cfg.Model != "distinct-summary-fixture" {
					t.Fatalf("construction received a different backend snapshot: %+v", cfg)
				}
				captured := clone(mutable)
				constructed = append(constructed, captured)
				p := &compactResolverProvider{t: t, model: cfg.Model, onCall: func(context.Context) { used = append(used, clone(captured)) }}
				transports = append(transports, p)
				return p, nil
			}, e.dataDir, e.caps)
			tc := config.TomlConfig{CompactModel: "side/distinct-summary-fixture", Providers: []config.ProviderConf{
				{Name: "side", Type: fixture.provider, BaseURL: fixture.baseURL},
			}}
			resolve := e.compactProviderResolver(tc, e.cfg, e.prov)
			if resolve == nil || len(constructed) != 1 || !reflect.DeepEqual(constructed[0], initial) || len(transports[0].requests) != 0 {
				t.Fatal("optional provider did not capture auth/discovery at loop construction without inference")
			}
			mutable.issuer, mutable.clientID, mutable.catalog = "issuer-after", "client-after", "catalog-after"
			mutable.accounts[0] = "account-after"
			mutable.accounts = append(mutable.accounts, "new-account")
			var first llm.Provider
			for i := 0; i < 2; i++ {
				ctx := llm.WithPurpose(compactResolverParent(t), llm.PurposeCompact)
				p, err := resolve(ctx)
				if err != nil || p == nil || !llm.IsMetered(p) || llm.Unwrap(p) != transports[0] {
					t.Fatalf("eager resolver did not return its real metered snapshot: %T %v", p, err)
				}
				if first != nil && p != first {
					t.Fatal("same loop rebuilt the optional backend")
				}
				first = p
				stream, err := p.Complete(ctx, []llm.Message{{Role: llm.RoleUser, Content: "summary fixture"}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				for range stream {
				}
			}
			if len(constructed) != 1 || len(used) != 2 || !reflect.DeepEqual(used[0], initial) || !reflect.DeepEqual(used[1], initial) || len(main.requests) != 0 {
				t.Fatal("active loop borrowed later auth/discovery state or repeated construction")
			}
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			if p, err := resolve(cancelled); p != nil || !errors.Is(err, context.Canceled) || len(constructed) != 1 || len(used) != 2 {
				t.Fatal("canceled eager lookup rebuilt or invoked its provider")
			}
			// A future loop gets the new state rather than a global provider cache.
			next := e.compactProviderResolver(tc, e.cfg, e.prov)
			if next == nil || len(constructed) != 2 || !reflect.DeepEqual(constructed[1], mutable) {
				t.Fatal("future loop did not capture updated auth/discovery state")
			}
			if p, err := next(compactResolverParent(t)); err != nil || p == first || llm.Unwrap(p) != transports[1] || len(constructed) != 2 {
				t.Fatal("future loop shared or rebuilt the previous provider snapshot")
			}
		})
	}
}

func TestWebCompactResolverReusesRealMeteredProviderOnlyWithinLoop(t *testing.T) {
	e, main := compactResolverEngine(t, "compact_model = \"summary-fixture\"\n")
	builds := 0
	var providers []*compactResolverProvider
	var calls []llm.CallStat
	e.factory = factory.New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		builds++
		p := &compactResolverProvider{t: t, model: cfg.Model}
		providers = append(providers, p)
		return p, nil
	}, e.dataDir, e.caps, func(c llm.CallStat) { calls = append(calls, c) })
	initial := fatTurns(4, 2000)
	l, err := e.newLoopWithSession(initial, nil)
	if err != nil {
		t.Fatal(err)
	}
	if builds != 0 {
		t.Fatal("side provider was constructed while creating the loop")
	}
	ctx := compactResolverParent(t)
	for i := 0; i < 2; i++ {
		if _, err := l.CompactNow(ctx); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			l.LoadConversation(initial)
		}
	}
	if builds != 1 || len(providers) != 1 || len(providers[0].requests) != 2 || len(main.requests) != 0 || len(calls) != 2 {
		t.Fatalf("loop memoization changed: builds=%d providers=%d main=%d metered=%d", builds, len(providers), len(main.requests), len(calls))
	}
	deadline, _ := ctx.Deadline()
	for i, c := range calls {
		request := providers[0].requests[i]
		if c.Purpose != llm.PurposeCompact || c.Model != "summary-fixture" || c.TokensIn != 120 || c.TokensOut != 24 || c.TokensCached != 16 || c.TokensReasoning != 5 {
			t.Fatalf("helper accounting changed: %+v", c)
		}
		if request.purpose != llm.PurposeCompact || request.parent != "request-snapshot" || !request.deadline.Equal(deadline) || request.effort != llm.ReasoningEffort() {
			t.Fatal("resolver lost the caller context or changed reasoning controls")
		}
	}
	other, err := e.newLoopWithSession(initial, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.CompactNow(ctx); err != nil {
		t.Fatal(err)
	}
	if builds != 2 {
		t.Fatalf("provider escaped into a global cache: builds=%d", builds)
	}
}

func TestWebCompactResolverFailureFallsBackAndCancellationPreservesHistory(t *testing.T) {
	for _, mode := range []string{"build-failure", "side-failure", "caller-canceled"} {
		t.Run(mode, func(t *testing.T) {
			e, main := compactResolverEngine(t, "compact_model = \"summary-fixture\"\n")
			builds := 0
			var sideCalls []llm.CallStat
			e.factory = factory.New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
				builds++
				if mode == "build-failure" {
					return nil, errors.New("synthetic unavailable backend")
				}
				return &compactResolverProvider{t: t, model: cfg.Model, fail: mode == "side-failure"}, nil
			}, e.dataDir, e.caps, func(c llm.CallStat) { sideCalls = append(sideCalls, c) })
			initial := fatTurns(4, 2000)
			l, err := e.newLoopWithSession(initial, nil)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(l.AllMessages())
			ctx, cancel := context.WithCancel(compactResolverParent(t))
			defer cancel()
			if mode == "caller-canceled" {
				cancel()
			}
			_, err = l.CompactNow(ctx)
			if mode == "caller-canceled" {
				after, _ := json.Marshal(l.AllMessages())
				if !errors.Is(err, context.Canceled) || builds != 0 || len(main.requests) != 0 || !bytes.Equal(before, after) {
					t.Fatal("canceled resolver built a backend, fell back, or changed history")
				}
				if _, err := l.CompactNow(compactResolverParent(t)); err != nil || builds != 1 {
					t.Fatalf("cancellation poisoned the healthy later invocation: %v builds=%d", err, builds)
				}
				return
			}
			if err != nil || builds != 1 || len(main.requests) != 1 || main.requests[0].purpose != llm.PurposeCompact {
				t.Fatalf("lost main fallback: %v builds=%d main=%d", err, builds, len(main.requests))
			}
			wantSideCalls := 0
			if mode == "side-failure" {
				wantSideCalls = 1
			}
			if len(sideCalls) != wantSideCalls {
				t.Fatalf("construction was counted as inference: metered=%d want=%d", len(sideCalls), wantSideCalls)
			}
			l.LoadConversation(initial)
			if _, err := l.CompactNow(compactResolverParent(t)); err != nil || builds != 1 {
				t.Fatalf("failed optional construction was repeated within the loop: %v builds=%d", err, builds)
			}
		})
	}
}

func TestWebCompactResolverCapturesModelProjectFactoryAndConfigValues(t *testing.T) {
	e, _ := compactResolverEngine(t, "")
	temperature := 0.7
	e.cfg.Temperature = &temperature
	var built []config.Config
	e.factory = factory.New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		built = append(built, cfg)
		return &compactResolverProvider{t: t, model: cfg.Model}, nil
	}, e.dataDir, e.caps)
	tc := config.TomlConfig{CompactModel: "side/summary-a", Providers: []config.ProviderConf{{Name: "side", Type: config.ProviderResponses, BaseURL: "https://fixture-a.invalid/v1/", APIKey: "fixture-a"}}}
	want := *modelOverrideConfigFor(tc, tc.CompactModel, e.cfg)
	wantTemperature := temperature
	want.Temperature = &wantTemperature
	resolver := e.compactProviderResolver(tc, e.cfg, e.prov)
	if len(built) != 0 || resolver == nil {
		t.Fatal("capturing a config built a provider or lost the override")
	}
	tc.Providers[0].BaseURL = "https://fixture-b.invalid"
	tc.Providers[0].APIKey = "fixture-b"
	tc.CompactModel = "side/summary-b"
	temperature = 1.1
	e.cfg.Model, e.cfg.BaseURL = "new-main", "https://new-main.invalid"
	replacementBuilds := 0
	e.factory = factory.New(func(config.Config, string, *llm.CapabilityRegistry) (llm.Provider, error) {
		replacementBuilds++
		return nil, errors.New("later factory must not rebuild an existing loop")
	}, e.dataDir, e.caps)
	p, err := resolver(compactResolverParent(t))
	if err != nil || p == nil || p.Name() != "summary-a" || len(built) != 1 || !reflect.DeepEqual(built[0], want) || replacementBuilds != 0 {
		t.Fatal("existing loop inherited later project, model, credential, factory or temperature changes")
	}
	// Preserve the old compactProvider contract; normalization belongs to the
	// selected runtime config, not to this construction-order optimization.
	if built[0].BaseURL != "https://fixture-a.invalid/v1/" {
		t.Fatal("resolver added normalization that was absent from the eager path")
	}
	newResolver := e.compactProviderResolver(tc, e.cfg, e.prov)
	_, _ = newResolver(compactResolverParent(t))
	if replacementBuilds != 1 {
		t.Fatal("a fresh loop failed to resolve the changed project configuration")
	}
}

func compactResolverDescriptors(t *testing.T, reg *tools.Registry) []byte {
	t.Helper()
	names := reg.Names()
	sort.Strings(names)
	type descriptor struct {
		Name, Description, Schema, RefreshArg string
		NextTools                             []string
		ReadOnly, PreservesEvidence           bool
		ReuseTTL                              time.Duration
		RepairArgs, Fn, Verify, ReplaySuccess bool
	}
	var defs []descriptor
	for _, name := range names {
		sp, _ := reg.Get(name)
		defs = append(defs, descriptor{
			Name: sp.Name, Description: sp.Description, Schema: sp.Schema, RefreshArg: sp.RefreshArg,
			NextTools: sp.NextTools, ReadOnly: sp.ReadOnly, PreservesEvidence: sp.PreservesEvidence, ReuseTTL: sp.ReuseTTL,
			RepairArgs: sp.RepairArgs != nil, Fn: sp.Fn != nil, Verify: sp.Verify != nil, ReplaySuccess: sp.ReplaySuccess != nil,
		})
	}
	body, err := json.Marshal(defs)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestWebCompactResolverMatchesEagerRequestFactsHistoryAndMainRequest(t *testing.T) {
	e, main := compactResolverEngine(t, "compact_model = \"summary-fixture\"\n")
	primary := llm.ModelInfo{ID: main.model, ToolUse: true, Stream: true, Reasoning: true, ReasoningKnown: true, ContextLength: 50000, Source: llm.SourceCatalog}
	e.caps.Register(primary)
	var sides []*compactResolverProvider
	e.factory = factory.New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		p := &compactResolverProvider{t: t, model: cfg.Model}
		sides = append(sides, p)
		return p, nil
	}, e.dataDir, e.caps)
	initial := fatTurns(4, 2000)
	lazy, reg, err := e.buildLoopWithSession(initial, nil, e.Home(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := lazy.AllMessages()
	if len(sides) != 0 {
		t.Fatal("loop construction eagerly created the optional backend")
	}
	reg.ActivateDiscovered("web_fetch")
	if !reg.IsActive("web_fetch") {
		t.Fatal("fixture failed to activate an actual registered tool")
	}
	descriptors := compactResolverDescriptors(t, reg)
	visible, active := reg.VisibleNames(), reg.ActiveNames()
	ctx := compactResolverParent(t)
	// Independent eager oracle: exactly the old frontend's config resolution,
	// factory Build and standard provider-pinned summarizer, with no resolver.
	tc := e.tomlConfigAt(e.Home())
	cfg := e.modelOverrideConfig(tc, tc.CompactModel)
	eagerProvider, err := e.factory.Build(*cfg, llm.PurposeCompact)
	if err != nil {
		t.Fatal(err)
	}
	if !llm.IsMetered(eagerProvider) {
		t.Fatal("oracle provider is not metered")
	}
	split := len(before) - 4 // the fixture has four turns; two remain protected
	wantSummary, err := agent.NewAutoSummarizerWithProvider(eagerProvider, reg.ActiveNames)(llm.WithPurpose(ctx, llm.PurposeCompact), e.prov, before[:split])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lazy.CompactNow(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sides) != 2 || len(sides[0].requests) != 1 || len(sides[1].requests) != 1 || !bytes.Equal(sides[0].requests[0].body, sides[1].requests[0].body) {
		t.Fatal("deferred construction changed the helper message/tool request bytes")
	}
	keep := 0
	for keep < len(before) && before[keep].Role == llm.RoleSystem {
		keep++
	}
	wantBody := append([]llm.Message{{Role: llm.RoleUser, Content: wantSummary}}, before[split:]...)
	wantHistory := append(append([]llm.Message(nil), before[:keep]...), wantBody...)
	if !reflect.DeepEqual(lazy.AllMessages(), wantHistory) || !bytes.Equal(descriptors, compactResolverDescriptors(t, reg)) || !reflect.DeepEqual(visible, reg.VisibleNames()) || !reflect.DeepEqual(active, reg.ActiveNames()) {
		t.Fatal("deferred construction changed summary facts, retained history or registered/visible tool contracts")
	}
	if got, _ := e.caps.Get(main.model); !reflect.DeepEqual(got, primary) || lazy.ContextWindow() != 100000 {
		t.Fatal("unaliased side construction changed the known primary catalog or window")
	}
	// Build an independent main-loop baseline from the eager oracle's saved
	// projection. Both real engine loops receive the same subsequent prompt.
	baseline, baselineReg, err := e.buildLoopWithSession(wantBody, nil, e.Home(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	baselineReg.ActivateDiscovered("web_fetch")
	if !bytes.Equal(descriptors, compactResolverDescriptors(t, baselineReg)) || !reflect.DeepEqual(visible, baselineReg.VisibleNames()) || !reflect.DeepEqual(active, baselineReg.ActiveNames()) {
		t.Fatal("fresh eager-projection baseline has different tool contracts")
	}
	compactResolverRun(t, lazy, ctx)
	compactResolverRun(t, baseline, ctx)
	if len(main.requests) != 2 || !bytes.Equal(main.requests[0].body, main.requests[1].body) {
		t.Fatal("deferred construction changed the next main model request")
	}
}

func TestWebCompactResolverKeepsAliasedModelCapabilitiesEager(t *testing.T) {
	for _, identity := range []string{"configured-model", "provider-name"} {
		t.Run(identity, func(t *testing.T) {
			e, main := compactResolverEngine(t, "")
			model := e.cfg.Model
			if identity == "provider-name" {
				main.model, model = "transport-model-id", "transport-model-id"
			}
			builds := 0
			changed := llm.ModelInfo{ID: model, ToolUse: true, Stream: true, ContextLength: 54321, Source: llm.SourceProvider}
			e.factory = factory.New(func(config.Config, string, *llm.CapabilityRegistry) (llm.Provider, error) {
				builds++
				e.caps.Register(changed)
				return &compactResolverProvider{t: t, model: model}, nil
			}, e.dataDir, e.caps)
			tc := config.TomlConfig{CompactModel: "side/" + model, Providers: []config.ProviderConf{{Name: "side", Type: config.ProviderOpenAI, BaseURL: "https://other-endpoint.invalid"}}}
			resolver := e.compactProviderResolver(tc, e.cfg, e.prov)
			if got, ok := e.caps.Get(model); builds != 1 || !ok || !reflect.DeepEqual(got, changed) {
				t.Fatal("alias construction changed the pre-request capability contract")
			}
			for i := 0; i < 2; i++ {
				p, err := resolver(compactResolverParent(t))
				if err != nil || !llm.IsMetered(p) || builds != 1 {
					t.Fatalf("eager exception built again or returned a proxy: %v builds=%d", err, builds)
				}
			}
		})
	}
}

func TestWebCompactResolverGatewayBroadDiscoveryKeepsPrimaryMetadataEager(t *testing.T) {
	gets := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected metadata/inference request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		gets++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"A"},{"id":"B"}]}`))
	}))
	defer gateway.Close()
	e, main := compactResolverEngine(t, "compact_model = \"gateway/opencode/B\"\n[[providers]]\nname = \"gateway\"\ntype = \"opencode\"\nbase_url = \""+gateway.URL+"/v1\"\n")
	e.cfg.Model, e.cfg.Provider = "opencode/A", config.ProviderOpencode
	main.model = "opencode/A"
	e.caps.Register(llm.ModelInfo{ID: main.model, ContextLength: 100000, Vision: true, Source: llm.SourceCatalog})
	e.factory = factory.New(nil, e.dataDir, e.caps)
	l, err := e.newLoop()
	if err != nil {
		t.Fatal(err)
	}
	want := llm.ModelInfo{ID: "opencode/A", Provider: "opencode", Source: llm.SourceOpencode, Stream: true, ToolUse: true}
	if got, ok := e.caps.Get(main.model); gets != 1 || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("gateway B's discovery of A was deferred: metadata GETs=%d", gets)
	}
	main.onCall = func(context.Context) {
		if got, _ := e.caps.Get(main.model); !reflect.DeepEqual(got, want) {
			t.Error("main request did not see the old eager-discovery capabilities")
		}
	}
	compactResolverRun(t, l, compactResolverParent(t))
	if gets != 1 || len(main.requests) != 1 || strings.Contains(string(main.requests[0].body), "Earlier context was compacted") {
		t.Fatal("metadata exception added a compaction/generation request on an ordinary turn")
	}
}
