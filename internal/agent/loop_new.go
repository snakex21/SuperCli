package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"supercli/internal/agent/ultrawork"
	"supercli/internal/llm"
	"supercli/internal/llm/draft"
)

// newRunID returns a short opaque identifier for one agent loop. It
// only has to be unique among the runs sharing a tool_errors.log, so
// 8 random bytes is ample and no clock or counter is involved.
func newRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// NewLoop returns a configured Loop. Provider and Registry are
// required; an error is returned if either is nil.
// DefaultMaxSteps is the built-in runaway safety net for one Run, shared by
// every surface (TUI, WebGUI, batch). It is deliberately far above anything a
// real task needs: the old 25-step ceiling was a work budget, and it ended the
// turn mid-task and made the user type "continue". Real loops are caught by
// repeatProgress, not by this number.
const DefaultMaxSteps = 300

func NewLoop(cfg LoopConfig) (*Loop, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("agent.NewLoop: provider is nil")
	}
	if cfg.Registry == nil {
		return nil, fmt.Errorf("agent.NewLoop: registry is nil")
	}
	cfg.Registry.EnsureReadOutput()
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = DefaultMaxSteps
	}
	msgs := make([]llm.Message, 0, len(cfg.InitialMessages)+4)
	if cfg.System != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: cfg.System})
	}
	msgs = append(msgs, cfg.InitialMessages...)

	loop := &Loop{
		provider:              cfg.Provider,
		registry:              cfg.Registry,
		caps:                  cfg.Caps,
		system:                cfg.System,
		briefing:              cfg.Briefing,
		maxSteps:              cfg.MaxSteps,
		thinTools:             cfg.ThinTools,
		stableToolset:         cfg.StableToolset,
		catalogHoist:          cfg.CatalogHoist,
		orchestrator:          cfg.Orchestrator,
		taskParallel:          cfg.TaskParallel,
		taskParallelWarnLocal: cfg.TaskParallelWarnLocal,
		thinHintMax:           cfg.ThinHintMax,
		baseDir:               cfg.BaseDir,
		writer:                cfg.Writer,
		errorLog:              cfg.ErrorLog,
		runID:                 newRunID(),
		reflector:             cfg.Reflector,
		reflectEvery:          cfg.ReflectEvery,
		adaptiveReflect:       cfg.AdaptiveReflection,
		patternInjector:       cfg.PatternInjector,
		creditTracker:         cfg.CreditTracker,
		modelID:               cfg.Provider.Name(),
		windowFor:             cfg.WindowFor,
		contextWindowFor:      cfg.ContextWindowFor,
		contextProvider:       cfg.ContextProvider,
		scopedWindowFor:       cfg.ScopedContextWindowFor,
		summarizer:            cfg.Summarizer,
		learnLimit:            cfg.LearnLimit,
		pruneProtect:          cfg.PruneProtectTokens,
		Messages:              msgs,
		routeMap:              DefaultRouteMap(),
		route:                 RouteCoordinator,
		navigate:              cfg.EnableNavigator,
		navAuto:               cfg.NavigatorAuto,
		navKeywordsOnly:       cfg.NavigatorKeywordsOnly,
		navProvider:           cfg.NavigatorProvider,
		// Phase telemetry rides the same recorder (and the same
		// default-on wiring) as the historical per-turn stats — it is
		// no longer gated on the F11 draft bridge being configured.
		stats: cfg.Stats,
	}

	// F9 ultrawork wiring. We build the Sisyphus enforcer
	// once at construction time and Reset() it at the
	// start of every Run. The gates live in the Wiring
	// itself; we hold a pointer so the loop can call
	// CheckGates directly.
	if cfg.Ultrawork != nil {
		loop.ultraworkGates = cfg.Ultrawork
		loop.ultraworkSisyphus = &ultrawork.Sisyphus{
			Goal:           cfg.Ultrawork.Goal,
			MaxConsecutive: cfg.Ultrawork.SisyphusMax,
		}
	}

	// F11 draft wiring. We build the bridge once at
	// construction time; per-Run the policy's Drafted
	// set is reset. Both Draft and DraftProvider must
	// be non-nil for the bridge to be constructed —
	// half-configured F11 is treated as off.
	if cfg.Draft != nil && cfg.DraftProvider != nil {
		bridge, err := draft.NewBridge(cfg.Draft, cfg.DraftProvider)
		if err == nil {
			loop.draftBridge = bridge
			loop.draftPolicy = cfg.Draft
			loop.draftSavings = draft.NewSavings()
			loop.draftOverrideSink = cfg.DraftOverrideSink
		}
	}

	// F5.d: build the patterns section at session start
	// and append it as a system message. We do this
	// synchronously so the very first provider call sees
	// the patterns. A build error is logged but not fatal.
	if cfg.PatternInjector != nil {
		injection, err := cfg.PatternInjector.Build(context.Background(), cfg.System)
		if err == nil && injection != "" {
			patMsg := llm.Message{Role: llm.RoleSystem, Content: injection}
			loop.Messages = append(loop.Messages, patMsg)
			loop.persist(context.Background(), patMsg)
		}
	}
	return loop, nil
}
