// draft_overrides.go is the F11 sink the agent loop calls
// when the verifier overrode the draft model's plan. The
// sink writes a JSONL log to <home>/.supercli/reflect/
// draft_overrides.jsonl, which the F5.b extractor will
// pick up on the next session-start and roll into Pattern
// rows of kind = "draft_override".
//
// Why a JSONL log instead of writing Patterns directly?
//   - F5.b's extractor is the single source of truth for
//     Pattern quality (scoring, dedup, body render). Having
//     draft overrides flow through the same pipeline means
//     they get the same treatment: de-duped by
//     HashPattern(kind, title), scored by Count, ranked by
//     Confidence.
//   - The loop stays decoupled from the reflect package's
//     in-memory Pattern state. The agent doesn't know or
//     care how patterns are scored; it just records raw
//     observations.
//   - Survives a panic / crash mid-session: the log is
//     append-only, no fsync needed beyond what os.File
//     already does.
//
// The sink is a fresh struct per session; close it when
// the session ends so the file handle is released.
//
// Dependency direction: reflect imports agent only for
// the DraftOverride data type. This is fine because
// agent does NOT import reflect (architecturally, reflect
// is a sub-feature used by agent — but the type
// dependency goes the other way to avoid an import
// cycle that would block defining the data type where
// it is first used).

package reflect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"supercli/internal/agent"
)

// DraftOverrideRecord is the on-disk shape of one
// "verifier overrode draft" observation. The agent
// loop records this as agent.DraftOverride (the
// in-memory wire shape); we serialize it to JSON for
// append-only storage. Keeping the disk format
// separate lets us evolve the on-disk shape without
// touching the agent API.
type DraftOverrideRecord struct {
	TS            time.Time `json:"ts"`
	Step          int       `json:"step"`
	DraftModel    string    `json:"draft_model"`
	VerifierModel string    `json:"verifier_model"`
	DraftText     string    `json:"draft_text,omitempty"`
	VerifierText  string    `json:"verifier_text,omitempty"`
	UserPrompt    string    `json:"user_prompt,omitempty"`
}

// JSONLDraftOverrideSink writes one JSONL line per
// override to <dir>/draft_overrides.jsonl. The file is
// created on first write, not on New, so New never
// fails on a missing parent directory.
//
// The concrete type satisfies agent.DraftOverrideSink
// structurally (same method name, same parameter
// type, same return type). Close() is an extra method
// not on the interface — main.go calls it via type
// assertion or stores the concrete *JSONLDraftOverrideSink.
type JSONLDraftOverrideSink struct {
	dir string

	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// NewJSONLDraftOverrideSink prepares a sink rooted at
// dir. The file is opened lazily on the first record so
// New never fails (the home dir might not exist yet
// during early init; we'll create it on demand).
func NewJSONLDraftOverrideSink(dir string) *JSONLDraftOverrideSink {
	return &JSONLDraftOverrideSink{dir: dir}
}

func (s *JSONLDraftOverrideSink) ensureFile() error {
	if s.f != nil {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("reflect: draft override sink mkdir: %w", err)
	}
	path := filepath.Join(s.dir, "draft_overrides.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("reflect: draft override sink open: %w", err)
	}
	s.f = f
	s.enc = json.NewEncoder(f)
	return nil
}

// RecordDraftOverride satisfies agent.DraftOverrideSink.
// Translates the in-memory DraftOverride to the on-disk
// shape, sets TS if zero, and appends a JSONL line.
func (s *JSONLDraftOverrideSink) RecordDraftOverride(_ context.Context, ev agent.DraftOverride) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureFile(); err != nil {
		return err
	}
	rec := DraftOverrideRecord{
		Step:          ev.Step,
		DraftModel:    ev.DraftModel,
		VerifierModel: ev.VerifierModel,
		DraftText:     ev.DraftText,
		VerifierText:  ev.VerifierText,
		UserPrompt:    ev.UserPrompt,
	}
	if rec.TS.IsZero() {
		rec.TS = time.Now()
	}
	return s.enc.Encode(rec)
}

// Close releases the file handle. Safe to call multiple
// times; second and later calls are no-ops.
func (s *JSONLDraftOverrideSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	s.enc = nil
	return err
}

// NoopDraftOverrideSink is the F11 sink that drops
// overrides. Useful in tests and when the user has
// disabled F5 reflector storage (e.g. read-only home).
type NoopDraftOverrideSink struct{}

// NewNoopDraftOverrideSink returns a fresh no-op sink.
func NewNoopDraftOverrideSink() *NoopDraftOverrideSink { return &NoopDraftOverrideSink{} }

// RecordDraftOverride discards the record.
func (NoopDraftOverrideSink) RecordDraftOverride(_ context.Context, _ agent.DraftOverride) error { return nil }

// Close is a no-op.
func (NoopDraftOverrideSink) Close() error { return nil }
