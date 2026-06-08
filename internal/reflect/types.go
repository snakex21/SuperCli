// Package reflect implements F5: self-reflection checkpoints
// (mid-conversation system messages) and pattern learning
// (extracting recurring failures / successful tool combos
// from tool_errors.log and session history into reusable
// patterns that are injected into the system prompt at the
// start of the next session).
//
// The package is split into four small modules:
//   - checkpoint.go    (F5.a)   Reflector interface + model impl
//   - extractor.go     (F5.b)   tool_errors.log + history → Patterns
//   - store.go         (F5.c)   Patterns ↔ memory/patterns/<id>.md
//   - injector.go      (F5.d)   Patterns → System Context section
//
// Dependencies: llm, memory, tools (F4.d). No cycles: agent
// imports this package; this package does not import agent.
package reflect

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Kind is the type of a learned pattern. Error patterns come
// from F4.d tool_errors.log; Combo patterns come from
// successful tool sequences; AntiPattern patterns are "don't
// do this" warnings derived from repeated failures;
// DraftOverride patterns (F11) record cases where the
// verifier overrode the draft model's plan, which is a
// signal that the draft was not useful for that kind of
// task.
type Kind string

const (
	KindError        Kind = "error"
	KindCombo        Kind = "combo"
	KindAntiPattern  Kind = "anti_pattern"
	KindDraftOverride Kind = "draft_override"
)

// Pattern is one learned insight. It is the canonical unit of
// F5 storage and the input to F5.d injection.
type Pattern struct {
	// ID is a stable hash of (Kind, Title) so re-extracting
	// the same insight does not produce duplicates. Computed
	// by ID() if empty at Save time.
	ID string `json:"id"`

	// Title is the human-readable summary, e.g.
	// "search_code: rg missing".
	Title string `json:"title"`

	// Kind is one of the Kind constants.
	Kind Kind `json:"kind"`

	// Description is the body, 1-3 sentences, the model
	// will see in the system prompt.
	Description string `json:"description"`

	// Example is an optional concrete snippet, e.g. a
	// representative tool-error string. Empty when nothing
	// to show.
	Example string `json:"example,omitempty"`

	// Count is the number of times the pattern was
	// observed. Confidence = min(1.0, Count/5.0).
	Count int `json:"count"`

	// Confidence is 0..1, derived from Count. Stored
	// explicitly so the injector can sort without
	// recomputing.
	Confidence float64 `json:"confidence"`

	// Source is the F4.d category when Kind == Error, or
	// "sessions" for combo / anti-pattern. Free-form for
	// future kinds.
	Source string `json:"source"`

	// Tags are extracted keywords (lowercased) used by
	// the F5.d relevance scorer.
	Tags []string `json:"tags,omitempty"`

	// CreatedAt is set by the store on first Save.
	CreatedAt time.Time `json:"created_at"`

	// --- F5.b extractor fields (not persisted as separate
	// columns; rolled into Description/Title/Tags by the
	// store) ---
	Tool       string      `json:"tool,omitempty"`
	Category   string      `json:"category,omitempty"`
	Reason     string      `json:"reason,omitempty"`
	Suggestion string      `json:"suggestion,omitempty"`
	ObservedAt []time.Time `json:"observed_at,omitempty"`

	// Body is the raw markdown produced by Store. The
	// injector can use it directly when a pre-rendered
	// view is needed. Empty when the pattern has not
	// been round-tripped through the store.
	Body string `json:"body,omitempty"`

	// UpdatedAt is set by the store on every Save. Used
	// as a freshness proxy by the injector.
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// HashPattern returns a stable ID for the (kind, title)
// pair. The first 8 hex chars of SHA-256 are used —
// collision probability is negligible for the practical
// count of patterns per project (<< 2^32).
func HashPattern(kind Kind, title string) string {
	h := sha256.Sum256([]byte(string(kind) + "\x00" + strings.ToLower(strings.TrimSpace(title))))
	return hex.EncodeToString(h[:4])
}
