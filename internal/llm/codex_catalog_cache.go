package llm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CodexCatalogTTL bounds automatic refreshes. Explicit scans bypass it.
const CodexCatalogTTL = 5 * time.Minute

type codexCatalogCache struct {
	SavedAt     time.Time    `json:"saved_at"`
	AttemptedAt time.Time    `json:"attempted_at,omitempty"`
	Models      []CodexModel `json:"models"`
}

func codexCatalogCachePath(dataDir, backendURL, identity string) string {
	if dataDir == "" || identity == "" {
		return ""
	}
	key := sha256.Sum256([]byte(strings.TrimRight(backendURL, "/") + "\x00" + identity))
	return filepath.Join(dataDir, "codex-models", hex.EncodeToString(key[:])+".json")
}

// ReadCodexModelCache returns only a cache bound to this endpoint and login
// identity. It can be used offline; consumers must check age before treating
// model absence as evidence for routing. It never reads a global/profile path.
func ReadCodexModelCache(dataDir, backendURL, identity string) ([]CodexModel, time.Time, bool) {
	path := codexCatalogCachePath(dataDir, backendURL, identity)
	if path == "" {
		return nil, time.Time{}, false
	}
	stat, err := os.Stat(path)
	if err != nil || stat.Size() > 4<<20 {
		return nil, time.Time{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	var cached codexCatalogCache
	if json.Unmarshal(data, &cached) != nil || cached.SavedAt.IsZero() || cached.Models == nil || len(cached.Models) > 4096 {
		return nil, time.Time{}, false
	}
	return cached.Models, cached.SavedAt, true
}

// CodexModelCacheAvailability treats only a fresh account catalog as evidence
// of model absence. It never guesses entitlement from a model name.
func CodexModelCacheAvailability(dataDir, backendURL, identity, model string) (available, known bool) {
	models, savedAt, ok := ReadCodexModelCache(dataDir, backendURL, identity)
	age := time.Since(savedAt)
	if !ok || age < 0 || age >= CodexCatalogTTL {
		return false, false
	}
	for _, entry := range models {
		if entry.Slug == model && entry.Visibility == "list" {
			return true, true
		}
	}
	return false, true
}

// CodexCatalogRefreshDue also bounds unsuccessful automatic discovery. A
// metadata-only failure stamp prevents repeated picker opens from probing the
// same broken login; an explicit scan still bypasses this short TTL.
func CodexCatalogRefreshDue(dataDir, backendURL, identity string) bool {
	path := codexCatalogCachePath(dataDir, backendURL, identity)
	stat, err := os.Stat(path)
	if err != nil || stat.Size() > 4<<20 {
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	var cached codexCatalogCache
	if json.Unmarshal(data, &cached) != nil {
		return true
	}
	stamp := cached.AttemptedAt
	if stamp.IsZero() {
		stamp = cached.SavedAt
	}
	age := time.Since(stamp)
	return stamp.IsZero() || age < 0 || age >= CodexCatalogTTL
}

func MarkCodexCatalogAttempt(dataDir, backendURL, identity string) error {
	models, savedAt, _ := ReadCodexModelCache(dataDir, backendURL, identity)
	return writeCodexCatalogCache(dataDir, backendURL, identity, codexCatalogCache{SavedAt: savedAt, AttemptedAt: time.Now().UTC(), Models: models})
}

// SaveCodexModelCache persists metadata only, never credentials, in dataDir.
func SaveCodexModelCache(dataDir, backendURL, identity string, models []CodexModel) error {
	if models == nil {
		models = []CodexModel{}
	}
	now := time.Now().UTC()
	return writeCodexCatalogCache(dataDir, backendURL, identity, codexCatalogCache{SavedAt: now, AttemptedAt: now, Models: models})
}

func writeCodexCatalogCache(dataDir, backendURL, identity string, cached codexCatalogCache) error {
	path := codexCatalogCachePath(dataDir, backendURL, identity)
	if path == "" {
		return nil
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return fmt.Errorf("codex models: cache exceeds 4 MiB")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Unique temporary files avoid competing GUI/CLI scans truncating a cache.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".catalog-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
