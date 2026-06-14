package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// codexRateLimitsFileName is the on-disk snapshot of the last known
// Codex usage limits, stored inside the resolved SuperCli data dir.
// It exists so the HUD `limit:` tile can render the most recently
// known numbers IMMEDIATELY at startup — before the first /responses
// call returns fresh X-Codex-* headers — without ever issuing an
// extra network request just to refresh the limit. The snapshot is
// refreshed for free on the next real response.
const codexRateLimitsFileName = "codex_ratelimits.json"

// codexRateLimitsSnapshot is the JSON envelope persisted to disk. It
// wraps a CodexRateLimits with the metadata needed to decide whether
// the snapshot is still relevant (which account it belongs to) and
// how stale it is (when it was captured).
type codexRateLimitsSnapshot struct {
	// SavedAt is the wall-clock time the snapshot was written, used
	// only to gauge freshness. Zero when unknown.
	SavedAt time.Time `json:"saved_at"`
	// AccountID is the ChatGPT account the limits belong to. A load
	// for a different account is discarded so one account's usage is
	// never shown under another. Empty matches any account.
	AccountID string `json:"account_id,omitempty"`
	// Limits is the parsed snapshot itself.
	Limits CodexRateLimits `json:"limits"`
}

// codexRateLimitsPath returns <dataDir>/codex_ratelimits.json, or ""
// when dataDir is empty (persistence disabled, e.g. in unit tests
// that don't wire a data dir).
func codexRateLimitsPath(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, codexRateLimitsFileName)
}

// saveCodexRateLimits writes the snapshot atomically (temp file +
// rename) so a crash mid-write can never leave a half-written JSON
// file behind. All failures are returned but the caller is expected
// to ignore them: a missed snapshot only delays the HUD by one
// response, it never breaks the stream. A non-OK snapshot is not
// written (nothing useful to persist).
func saveCodexRateLimits(dataDir, accountID string, rl CodexRateLimits) error {
	path := codexRateLimitsPath(dataDir)
	if path == "" || !rl.OK {
		return nil
	}
	snap := codexRateLimitsSnapshot{
		SavedAt:   time.Now(),
		AccountID: accountID,
		Limits:    rl,
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), codexRateLimitsFileName+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename succeeds.
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ClearCodexRateLimits deletes the persisted usage snapshot. It is
// called on /logout so a fresh login does not show the previous
// account's limits in the HUD before the first response arrives.
// A missing file (or an empty dataDir) is not an error.
func ClearCodexRateLimits(dataDir string) error {
	path := codexRateLimitsPath(dataDir)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// loadCodexRateLimits reads the persisted snapshot. It returns
// ok=false (no error) when the file is missing, unreadable, contains
// corrupt JSON, holds a non-OK snapshot, or belongs to a different
// account — all of which mean "show no tile yet", matching first-run
// behavior. This never panics on bad input.
func loadCodexRateLimits(dataDir, accountID string) (CodexRateLimits, bool) {
	path := codexRateLimitsPath(dataDir)
	if path == "" {
		return CodexRateLimits{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return CodexRateLimits{}, false
	}
	var snap codexRateLimitsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return CodexRateLimits{}, false
	}
	if !snap.Limits.OK {
		return CodexRateLimits{}, false
	}
	// Discard a snapshot saved under a different account. An empty
	// stored or requested account id matches anything (e.g. older
	// snapshots written before account scoping, or callers that
	// don't know the account yet).
	if accountID != "" && snap.AccountID != "" && snap.AccountID != accountID {
		return CodexRateLimits{}, false
	}
	return snap.Limits, true
}
