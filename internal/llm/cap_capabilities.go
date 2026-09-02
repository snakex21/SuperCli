package llm

import (
	"database/sql"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ModelInfo is the F16 unified record for a model. It
// holds the boolean capability flags, optional cost
// and context-length metadata, and a Source field
// recording which loader populated the entry.
//
// The zero Source is SourceUnknown; new entries
// created in Go should always set Source explicitly.
type ModelInfo struct {
	ID       string `json:"id"`
	Provider string `json:"provider,omitempty"`
	// Transport is the provider SDK/wire protocol advertised by a dynamic
	// catalog (openai-compatible, responses, anthropic, google).
	Transport string `json:"transport,omitempty"`
	Vision    bool   `json:"vision"`
	// VisionKnown distinguishes an authoritative "text only" result from a
	// provider that did not publish modality metadata at all.
	VisionKnown    bool      `json:"vision_known,omitempty"`
	ToolUse        bool      `json:"tool_use"`
	Stream         bool      `json:"stream"`
	Reasoning      bool      `json:"reasoning"`
	ReasoningKnown bool      `json:"reasoning_known,omitempty"`
	ContextLength  int       `json:"context_length,omitempty"`
	InputCost      float64   `json:"input_cost,omitempty"`
	OutputCost     float64   `json:"output_cost,omitempty"`
	Notes          string    `json:"notes,omitempty"`
	LastVerified   time.Time `json:"last_verified,omitempty"`
	Source         Source    `json:"-"`
}

// ModelCapabilities is the small struct used by the
// F1/F4 callers (Vision / ToolUse / Stream only).
// It is the backward-compat accessor on ModelInfo so
// older code does not need to migrate.
type ModelCapabilities struct {
	Vision  bool
	ToolUse bool
	Stream  bool
}

// Capabilities returns the F1/F4 subset of the model
// info. New callers should use the booleans on
// ModelInfo directly.
func (m ModelInfo) Capabilities() ModelCapabilities {
	return ModelCapabilities{
		Vision:  m.Vision,
		ToolUse: m.ToolUse,
		Stream:  m.Stream,
	}
}

// CapabilityRegistry maps model ids to their
// capabilities. It is safe for concurrent use. The
// F16 rewrite removes the hardcoded builtinModels
// table entirely: the registry is built at runtime
// from the seed JSON, the user's <home>/.supercli/
// models.json, /v1/models, and on-demand probes.
type CapabilityRegistry struct {
	mu     sync.RWMutex
	models map[string]ModelInfo
}

// NewCapabilityRegistry returns an empty registry.
// Callers (typically main.go) populate it via
// NewCapabilityRegistryFromSources, or by manual
// Register/RegisterAll calls in tests.
func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{models: make(map[string]ModelInfo)}
}

// Register adds or replaces a model entry. The
// caller's Source field is respected; the entry will
// not be silently downgraded to a lower-priority
// source.
func (r *CapabilityRegistry) Register(m ModelInfo) {
	r.mu.Lock()
	r.models[m.ID] = m
	r.mu.Unlock()
}

// RegisterAll upserts every entry. The merge uses
// Source.Overrides, so lower-priority entries do
// not overwrite higher-priority ones.
func (r *CapabilityRegistry) RegisterAll(entries []ModelInfo) {
	r.mu.Lock()
	for _, m := range entries {
		if existing, ok := r.models[m.ID]; ok {
			if !m.Source.Overrides(existing.Source) {
				continue
			}
		}
		r.models[m.ID] = m
	}
	r.mu.Unlock()
}

// Get returns the info for a model id. The second
// return is false when the id is unknown.
func (r *CapabilityRegistry) Get(id string) (ModelInfo, bool) {
	r.mu.RLock()
	m, ok := r.models[id]
	r.mu.RUnlock()
	return m, ok
}

// HasVision reports whether the model supports image
// inputs. Unknown models return false.
func (r *CapabilityRegistry) HasVision(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	if !ok {
		return false
	}
	return m.Vision
}

// AllowsVisionAttempt reports whether an image should be sent to the
// provider. Only authoritative text-only metadata blocks the attempt.
// Missing models and dynamically discovered models without modality
// metadata are unknown, not text-only, so they are tried optimistically.
// Curated seed/catalog entries remain strict when they explicitly encode
// Vision=false (older curated entries predate VisionKnown).
func (r *CapabilityRegistry) AllowsVisionAttempt(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	if !ok {
		return true
	}
	if m.Vision {
		return true
	}
	if m.VisionKnown {
		return false
	}
	return m.Source == SourceProvider
}

// HasToolUse reports whether the model supports
// function/tool calls. Unknown models return false.
func (r *CapabilityRegistry) HasToolUse(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	if !ok {
		return false
	}
	return m.ToolUse
}

// HasReasoning reports whether the model exposes a
// separate reasoning-token stream (o1, o3, R1, ...).
// Unknown models return false. Added in F16.
func (r *CapabilityRegistry) HasReasoning(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	if !ok {
		return false
	}
	return m.Reasoning
}

// Provider returns the provider hint for a model
// id (e.g. "openai", "anthropic"). Empty for
// unknown models.
func (r *CapabilityRegistry) Provider(id string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	if !ok {
		return ""
	}
	return m.Provider
}

// Models returns the known model ids in stable
// sorted order. Useful for --list-models and tests.
func (r *CapabilityRegistry) Models() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.models))
	for id := range r.models {
		out = append(out, id)
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}

// Len returns the number of registered models.
func (r *CapabilityRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.models)
}

// All returns a copy of every registered model,
// sorted by id. Snapshot semantics; mutating the
// result does not affect the registry.
func (r *CapabilityRegistry) All() []ModelInfo {
	r.mu.RLock()
	out := make([]ModelInfo, 0, len(r.models))
	for _, m := range r.models {
		out = append(out, m)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// IsConfigured reports whether the model id is
// known to the registry. The registry's sources
// (seed JSON, user catalog, /v1/models probe) all
// implicitly carry "this model is reachable" —
// either the user has an API key for the
// provider, or the model is a seed default. We do
// NOT validate the API key here; that happens at
// call time (Complete() returns an error → caller
// falls back). The point of IsConfigured is to
// answer "is there any reason to even try this
// model", not "will the call definitely succeed".
func (r *CapabilityRegistry) IsConfigured(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.models[id]
	return ok
}

// SuggestCheapestForTask picks the model with the
// lowest combined (input + output) cost-per-million
// that satisfies the task's hard requirements. The
// task name is currently advisory — F11 only uses
// "plan", but the signature is open so future
// tasks ("summarize", "translate", "embed") can
// filter on different capability sets.
//
// Cost = m.InputCost + m.OutputCost. We treat a
// zero InputCost as "no data" and prefer models
// with known cost over models with cost=0 (the
// latter are typically heuristics / probes that
// never had cost filled in). When ALL models have
// cost=0 we fall back to alphabetical order so the
// result is deterministic.
//
// Filter rules (F11, task="plan"):
//   - must be configured (in the registry)
//   - must NOT be the main model id (a model
//     doesn't "draft" itself)
//   - must have ToolUse=true (the draft needs to
//     propose structured plans; pure chat models
//     are out)
//   - must have non-empty provider hint (a model
//     without a provider can't actually be reached)
//
// Returns ("", false) when no candidate matches.
// Callers must treat that as "draft disabled",
// not as an error. F11's policy layer wraps this
// in a silent fallback.
func (r *CapabilityRegistry) SuggestCheapestForTask(task, mainModel string) (string, bool) {
	ids := r.SuggestCheapestN(task, mainModel, 1)
	if len(ids) == 0 {
		return "", false
	}
	return ids[0], true
}

// SuggestCheapestN is the multi-model variant of
// SuggestCheapestForTask. F12's consult package
// uses it to pick N diverse cheap models for the
// parallel sample. The same filter and ordering
// rules apply:
//
//   - excludes the main model (a model doesn't
//     sample itself; the main provider is the
//     judge)
//   - requires ToolUse=true
//   - requires a non-empty provider hint
//   - sorts by combined cost, ascending
//   - on full tie, falls back to alphabetical
//
// n <= 0 returns an empty slice (caller must
// handle that as "consult disabled"). n greater
// than the configured population is clamped
// silently — the caller gets whatever is
// available, possibly zero. Callers that need at
// least 1 should check len(result) > 0.
func (r *CapabilityRegistry) SuggestCheapestN(task, mainModel string, n int) []string {
	if n <= 0 {
		return nil
	}
	r.mu.RLock()
	type cand struct {
		id   string
		cost float64
	}
	var withCost []cand
	var noCost []string
	for id, m := range r.models {
		if id == mainModel {
			continue
		}
		if !m.ToolUse {
			continue
		}
		if m.Provider == "" {
			continue
		}
		_ = task
		cost := m.InputCost + m.OutputCost
		if cost > 0 {
			withCost = append(withCost, cand{id, cost})
		} else {
			noCost = append(noCost, id)
		}
	}
	r.mu.RUnlock()
	// Sort cheapest first, then alphabetically for
	// determinism on ties.
	sort.Slice(withCost, func(i, j int) bool {
		if withCost[i].cost != withCost[j].cost {
			return withCost[i].cost < withCost[j].cost
		}
		return withCost[i].id < withCost[j].id
	})
	sort.Strings(noCost)
	// Prefer the known-cost list; append the
	// no-cost tail only after we've exhausted the
	// priced candidates. This gives the user
	// deterministic, cost-aware picks first.
	out := make([]string, 0, n)
	for _, c := range withCost {
		if len(out) >= n {
			break
		}
		out = append(out, c.id)
	}
	if len(out) < n {
		for _, id := range noCost {
			if len(out) >= n {
				break
			}
			out = append(out, id)
		}
	}
	return out
}

// NewCapabilityRegistryFromSources builds the
// runtime registry by loading every F16 source
// in priority order (lowest first, so higher
// overrides):
//
//  1. seed JSON (internal/capabilities_seed.json,
//     embedded via go:embed).
//  2. user catalog (<home>/.supercli/models.json).
//  3. probe cache (model_probe_cache table, when
//     db is non-nil).
//
// A load failure on the seed is fatal (the embed
// is always there). A missing catalog or cache is
// not an error. A malformed catalog or cache IS
// an error — silently dropping corrupted data
// would surprise the user.
func NewCapabilityRegistryFromSources(home string, db *sql.DB) (*CapabilityRegistry, error) {
	r := NewCapabilityRegistry()
	seed, err := LoadSeed()
	if err != nil {
		return nil, fmt.Errorf("llm: load seed: %w", err)
	}
	r.RegisterAll(seed)
	catalog, err := LoadCatalog(home)
	if err != nil {
		return nil, fmt.Errorf("llm: load catalog: %w", err)
	}
	r.RegisterAll(catalog)
	if db != nil {
		cache, err := LoadProbeCache(db)
		if err != nil {
			return nil, fmt.Errorf("llm: load probe cache: %w", err)
		}
		for id, pr := range cache {
			// A4 fix: MERGE the probe result into any existing
			// entry instead of replacing it. The old code
			// registered a fresh ModelInfo with an empty
			// Provider field, which wiped the provider set by
			// the seed/catalog entry — and the /model picker
			// filters rows by configured provider name, so the
			// model silently vanished from the list. A probe
			// must never make a model disappear.
			if existing, ok := r.Get(id); ok {
				existing.Reasoning = pr.Reasoning
				existing.Vision = existing.Vision || pr.Vision
				existing.Source = SourceProbe
				existing.LastVerified = pr.ProbedAt
				r.Register(existing)
				continue
			}
			r.Register(ModelInfo{
				ID:           id,
				Reasoning:    pr.Reasoning,
				Vision:       pr.Vision,
				Stream:       true,
				ToolUse:      true,
				Source:       SourceProbe,
				LastVerified: pr.ProbedAt,
			})
		}
	}
	return r, nil
}
