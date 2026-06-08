package reflect

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeErrors(t *testing.T, path string, recs []ErrorRecord) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExtractor_EmptyLog(t *testing.T) {
	dir := t.TempDir()
	path := DefaultErrorsPath(dir)
	ex := &Extractor{ErrorsPath: path}
	got, err := ex.Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("patterns = %d, want 0", len(got))
	}
}

func TestExtractor_MissingLog(t *testing.T) {
	// No file written; should be a no-op, not an error.
	ex := &Extractor{ErrorsPath: "/nonexistent/should/not/exist.log"}
	got, err := ex.Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract on missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("patterns = %d, want 0", len(got))
	}
}

func TestExtractor_GroupsByTriple(t *testing.T) {
	dir := t.TempDir()
	path := DefaultErrorsPath(dir)
	now := time.Now()
	writeErrors(t, path, []ErrorRecord{
		{Timestamp: now, Tool: "search_code", Category: "environment", Reason: "exec: \"rg\": executable file not found in $PATH"},
		{Timestamp: now.Add(time.Second), Tool: "search_code", Category: "environment", Reason: "exec: \"rg\": executable file not found in $PATH"},
		{Timestamp: now.Add(2 * time.Second), Tool: "ask_user", Category: "model", Reason: "schema mismatch"},
	})
	ex := &Extractor{ErrorsPath: path}
	got, err := ex.Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("patterns = %d, want 2; got %+v", len(got), got)
	}
	// First pattern (sorted by count desc) is search_code/env with count=2.
	if got[0].Tool != "search_code" {
		t.Errorf("first tool = %q, want search_code", got[0].Tool)
	}
	if got[0].Count != 2 {
		t.Errorf("first count = %d, want 2", got[0].Count)
	}
	if got[0].Category != "environment" {
		t.Errorf("first category = %q", got[0].Category)
	}
}

func TestExtractor_ConfidenceScales(t *testing.T) {
	dir := t.TempDir()
	path := DefaultErrorsPath(dir)
	now := time.Now()
	recs := make([]ErrorRecord, 5)
	for i := range recs {
		recs[i] = ErrorRecord{Timestamp: now, Tool: "echo", Category: "model", Reason: "schema mismatch"}
	}
	writeErrors(t, path, recs)
	ex := &Extractor{ErrorsPath: path}
	got, _ := ex.Extract(context.Background())
	if len(got) != 1 {
		t.Fatalf("patterns = %d", len(got))
	}
	if got[0].Confidence != 1.0 {
		t.Errorf("confidence = %f, want 1.0 (5/5)", got[0].Confidence)
	}

	// Now test 1 occurrence: confidence = 0.2.
	writeErrors(t, path, []ErrorRecord{
		{Timestamp: now, Tool: "echo", Category: "model", Reason: "single"},
	})
	ex2 := &Extractor{ErrorsPath: path}
	got2, _ := ex2.Extract(context.Background())
	if got2[0].Confidence != 0.2 {
		t.Errorf("confidence = %f, want 0.2 (1/5)", got2[0].Confidence)
	}
}

func TestExtractor_NormalizesPaths(t *testing.T) {
	dir := t.TempDir()
	path := DefaultErrorsPath(dir)
	now := time.Now()
	writeErrors(t, path, []ErrorRecord{
		{Timestamp: now, Tool: "read_file", Category: "environment", Reason: "open /var/log/a.log: no such file"},
		{Timestamp: now.Add(time.Second), Tool: "read_file", Category: "environment", Reason: "open /var/log/b.log: no such file"},
		{Timestamp: now.Add(2 * time.Second), Tool: "read_file", Category: "environment", Reason: "open /var/log/c.log: no such file"},
	})
	ex := &Extractor{ErrorsPath: path}
	got, _ := ex.Extract(context.Background())
	if len(got) != 1 {
		t.Fatalf("patterns = %d, want 1 (all paths normalize to one bucket)", len(got))
	}
	if !strings.Contains(got[0].Reason, "<path>") {
		t.Errorf("reason = %q, want it to contain <path>", got[0].Reason)
	}
	if got[0].Count != 3 {
		t.Errorf("count = %d, want 3", got[0].Count)
	}
}

func TestExtractor_MaxPatternsCap(t *testing.T) {
	dir := t.TempDir()
	path := DefaultErrorsPath(dir)
	now := time.Now()
	// 10 distinct groups
	recs := make([]ErrorRecord, 10)
	for i := range recs {
		recs[i] = ErrorRecord{Timestamp: now, Tool: "tool_" + string(rune('A'+i)), Category: "model", Reason: "reason"}
	}
	writeErrors(t, path, recs)
	ex := &Extractor{ErrorsPath: path, MaxPatterns: 3}
	got, _ := ex.Extract(context.Background())
	if len(got) != 3 {
		t.Errorf("patterns = %d, want 3", len(got))
	}
}

func TestExtractor_SinceFilter(t *testing.T) {
	dir := t.TempDir()
	path := DefaultErrorsPath(dir)
	cutoff := time.Now()
	writeErrors(t, path, []ErrorRecord{
		{Timestamp: cutoff.Add(-1 * time.Hour), Tool: "old", Category: "model", Reason: "old"},
		{Timestamp: cutoff.Add(1 * time.Hour), Tool: "new", Category: "model", Reason: "new"},
	})
	ex := &Extractor{ErrorsPath: path, Since: cutoff}
	got, _ := ex.Extract(context.Background())
	if len(got) != 1 {
		t.Fatalf("patterns = %d, want 1", len(got))
	}
	if got[0].Tool != "new" {
		t.Errorf("tool = %q, want new (older one filtered out)", got[0].Tool)
	}
}

func TestExtractor_SessionBoost(t *testing.T) {
	dir := t.TempDir()
	path := DefaultErrorsPath(dir)
	now := time.Now()
	writeErrors(t, path, []ErrorRecord{
		{Timestamp: now, Tool: "search_code", Category: "environment", Reason: "exec: rg not found"},
	})
	ex := &Extractor{
		ErrorsPath: path,
		Session: &stubSession{
			sessions: []SessionSummary{
				{ID: "s1", Title: "test", Text: "search_code keeps failing because rg is missing"},
			},
		},
	}
	got, _ := ex.Extract(context.Background())
	if len(got) != 1 {
		t.Fatalf("patterns = %d", len(got))
	}
	// Base 0.2 (1 occurrence) + session boost. The boost
	// is proportional to the token match ratio: 4 of ~9
	// tokens match, so bump ≈ 0.04 → final ≈ 0.24.
	// We only assert the boost happened.
	if got[0].Confidence <= 0.2 {
		t.Errorf("confidence = %f, want > 0.2 (no boost applied)", got[0].Confidence)
	}
}

func TestExtractor_MalformedLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	path := DefaultErrorsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "not json\n" +
		`{"ts":"2024-01-01T00:00:00Z","tool":"valid","category":"model","reason":"x"}` + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := &Extractor{ErrorsPath: path}
	got, err := ex.Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("patterns = %d, want 1 (malformed line should be skipped)", len(got))
	}
}

type stubSession struct {
	sessions []SessionSummary
}

func (s *stubSession) RecentSessions(_ context.Context, _ int) ([]SessionSummary, error) {
	return s.sessions, nil
}
