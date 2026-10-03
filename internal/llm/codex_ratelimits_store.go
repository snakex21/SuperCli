package llm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
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
	// never shown under another. Unknown identity is legacy-only.
	AccountID string `json:"account_id,omitempty"`
	// Limits is the parsed snapshot itself.
	Limits CodexRateLimits `json:"limits"`
}

// codexRateLimitsPath returns the snapshot path for an account.
// With an accountID it is <dataDir>/codex_ratelimits-<sha256>.json so
// each account keeps its OWN usage snapshot — without this every
// account shared one file and showed another account's numbers.
// An empty accountID keeps the legacy <dataDir>/codex_ratelimits.json
// (single-account setups and back-compat). Empty dataDir disables
// persistence (returns "").
func codexRateLimitsPath(dataDir, accountID string) string {
	if dataDir == "" {
		return ""
	}
	if accountID == "" {
		return filepath.Join(dataDir, codexRateLimitsFileName)
	}
	// Include a digest: Windows filename case folding and sanitization must not
	// let two account identities overwrite one another's snapshot.
	digest := sha256.Sum256([]byte(accountID))
	return filepath.Join(dataDir, fmt.Sprintf("codex_ratelimits-%x.json", digest[:]))
}

// sanitizeAccountID keeps an account id safe as a filename fragment
// (it can contain characters not valid in a path). Only [a-zA-Z0-9_-]
// survive; everything else becomes '_'.
func sanitizeAccountID(id string) string {
	b := make([]rune, 0, len(id))
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b = append(b, r)
		} else {
			b = append(b, '_')
		}
	}
	if len(b) == 0 {
		return "acct"
	}
	return string(b)
}

// saveCodexRateLimits writes the snapshot atomically (temp file +
// rename) so a crash mid-write can never leave a half-written JSON
// file behind. All failures are returned but the caller is expected
// to ignore them: a missed snapshot only delays the HUD by one
// response, it never breaks the stream. A non-OK snapshot is not
// written (nothing useful to persist).
func saveCodexRateLimits(dataDir, accountID string, rl CodexRateLimits) error {
	path := codexRateLimitsPath(dataDir, accountID)
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

// ClearCodexRateLimits deletes persisted usage snapshots. It is
// intended for an explicit full reset. Single-account logout must use
// ClearCodexAccountRateLimits.
// It removes both the legacy shared file and every per-account
// snapshot (codex_ratelimits-*.json). A missing file (or an empty
// dataDir) is not an error.
func ClearCodexRateLimits(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	// Legacy shared file.
	shared := codexRateLimitsPath(dataDir, "")
	if err := os.Remove(shared); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Per-account files: codex_ratelimits-<id>.json.
	matches, _ := filepath.Glob(filepath.Join(dataDir, "codex_ratelimits-*.json"))
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// ClearCodexAccountRateLimits clears only the specified identity. Empty identity
// addresses the legacy anonymous file, never all authenticated accounts.
func ClearCodexAccountRateLimits(dataDir, accountID string) error {
	if dataDir == "" {
		return nil
	}
	paths := []string{codexRateLimitsPath(dataDir, accountID)}
	if accountID != "" {
		legacy := filepath.Join(dataDir, "codex_ratelimits-"+sanitizeAccountID(accountID)+".json")
		if snap, ok := readCodexRateLimitsFile(legacy, accountID); ok && snap.AccountID == accountID {
			paths = append(paths, legacy)
		}
		// An older shared envelope can be removed only if it explicitly names
		// this account; anonymous or another account's cache is left alone.
		shared := codexRateLimitsPath(dataDir, "")
		if snap, ok := readCodexRateLimitsFile(shared, accountID); ok && snap.AccountID == accountID {
			paths = append(paths, shared)
		}
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// loadCodexRateLimits reads the persisted snapshot. It returns
// ok=false (no error) when the file is missing, unreadable, contains
// corrupt JSON, holds a non-OK snapshot, or belongs to a different
// account — all of which mean "show no tile yet", matching first-run
// behavior. This never panics on bad input.
func loadCodexRateLimits(dataDir, accountID string) (CodexRateLimits, bool) {
	path := codexRateLimitsPath(dataDir, accountID)
	if path == "" {
		return CodexRateLimits{}, false
	}
	snap, ok := readCodexRateLimitsFile(path, accountID)
	if !ok && accountID != "" {
		snap, ok = readCodexRateLimitsFile(filepath.Join(dataDir, "codex_ratelimits-"+sanitizeAccountID(accountID)+".json"), accountID)
	}
	if !ok {
		return CodexRateLimits{}, false
	}
	rl := snap.Limits
	if rl.Snapshot == nil {
		legacy := legacyCodexUsageSnapshot(rl, snap.SavedAt)
		rl.Snapshot = &legacy
	}
	usage := rl.Snapshot.At(time.Now())
	rl.Snapshot = &usage
	return rl, true
}

func readCodexRateLimitsFile(path, accountID string) (codexRateLimitsSnapshot, bool) {
	f, err := os.Open(path)
	if err != nil {
		return codexRateLimitsSnapshot{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return codexRateLimitsSnapshot{}, false
	}
	var snap codexRateLimitsSnapshot
	if json.Unmarshal(data, &snap) != nil || !snap.Limits.OK || snap.AccountID != accountID {
		return codexRateLimitsSnapshot{}, false
	}
	return snap, true
}

func LoadCodexUsageSnapshot(dataDir, accountID string) (CodexUsageSnapshot, bool) {
	rl, ok := loadCodexRateLimits(dataDir, accountID)
	if !ok || rl.Snapshot == nil {
		return CodexUsageSnapshot{}, false
	}
	return rl.Snapshot.At(time.Now()), true
}

// LoadCodexRateLimitsSnapshot reads the persisted Codex rate-limit
// snapshot for accountID from dataDir. It is the public, read-only
// counterpart to the provider's internal persistence path, intended
// for status surfaces such as the TUI HUD and web GUI. Empty accountID
// uses the legacy shared snapshot; a non-empty accountID uses the
// per-account snapshot and rejects mismatched account ids.
func LoadCodexRateLimitsSnapshot(dataDir, accountID string) (CodexRateLimits, bool) {
	return loadCodexRateLimits(dataDir, accountID)
}
