package llm

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCodexRateLimits_PerAccountIsolation is the regression guard
// for the bug where two accounts shared one snapshot file, so a
// second (untouched) account showed the first account's usage.
func TestCodexRateLimits_PerAccountIsolation(t *testing.T) {
	dir := t.TempDir()
	rlA := CodexRateLimits{PrimaryUsedPct: 1, SecondaryUsedPct: 11, OK: true}
	rlB := CodexRateLimits{PrimaryUsedPct: 50, SecondaryUsedPct: 0, OK: true}

	if err := saveCodexRateLimits(dir, "acct-A", rlA); err != nil {
		t.Fatalf("save A: %v", err)
	}
	if err := saveCodexRateLimits(dir, "acct-B", rlB); err != nil {
		t.Fatalf("save B: %v", err)
	}

	gotA, okA := loadCodexRateLimits(dir, "acct-A")
	gotB, okB := loadCodexRateLimits(dir, "acct-B")
	if !okA || !okB {
		t.Fatalf("load failed: A=%v B=%v", okA, okB)
	}
	if gotA.SecondaryUsedPct != 11 || gotA.PrimaryUsedPct != 1 {
		t.Errorf("account A snapshot polluted: %+v", gotA)
	}
	if gotB.PrimaryUsedPct != 50 || gotB.SecondaryUsedPct != 0 {
		t.Errorf("account B snapshot polluted by A: %+v", gotB)
	}
	// Distinct files on disk.
	if _, err := os.Stat(filepath.Join(dir, "codex_ratelimits-acct-A.json")); err != nil {
		t.Errorf("per-account file A missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex_ratelimits-acct-B.json")); err != nil {
		t.Errorf("per-account file B missing: %v", err)
	}
}

func TestCodexRateLimits_EmptyAccountUsesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	rl := CodexRateLimits{PrimaryUsedPct: 7, OK: true}
	if err := saveCodexRateLimits(dir, "", rl); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex_ratelimits.json")); err != nil {
		t.Errorf("legacy shared file missing: %v", err)
	}
	got, ok := loadCodexRateLimits(dir, "")
	if !ok || got.PrimaryUsedPct != 7 {
		t.Errorf("legacy load = %+v ok=%v", got, ok)
	}
}

func TestClearCodexRateLimits_RemovesAllSnapshots(t *testing.T) {
	dir := t.TempDir()
	saveCodexRateLimits(dir, "", CodexRateLimits{PrimaryUsedPct: 1, OK: true})
	saveCodexRateLimits(dir, "acct-A", CodexRateLimits{PrimaryUsedPct: 2, OK: true})
	saveCodexRateLimits(dir, "acct-B", CodexRateLimits{PrimaryUsedPct: 3, OK: true})

	if err := ClearCodexRateLimits(dir); err != nil {
		t.Fatalf("clear: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "codex_ratelimits*.json"))
	if len(matches) != 0 {
		t.Errorf("clear left files behind: %v", matches)
	}
}
