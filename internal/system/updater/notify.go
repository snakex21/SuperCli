package updater

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"supercli/internal/buildinfo"
)

// NoticeInterval bounds how often the background notice contacts GitHub.
const NoticeInterval = 24 * time.Hour

const noticeFile = "update-check.json"

type noticeCache struct {
	Version   string `json:"version"`
	CheckedAt int64  `json:"checked_at"`
	State     State  `json:"state"`
}

// CachedCheck backs the background new-release notice (GUI and TUI). A result
// younger than NoticeInterval, recorded by this same version, is reused without
// a network request; installing a new version invalidates it. Failures are not
// cached. It only checks: downloading and installing stay explicit actions.
// An empty dataDir disables the cache. A nil check uses the real updater.
func CachedCheck(ctx context.Context, dataDir string, check func(context.Context) (State, error)) (State, error) {
	if check == nil {
		check = func(ctx context.Context) (State, error) {
			m, err := New()
			if err != nil {
				return State{}, err
			}
			return m.Check(ctx)
		}
	}
	return cachedCheck(ctx, dataDir, buildinfo.Version, time.Now(), check)
}

func cachedCheck(ctx context.Context, dataDir, version string, now time.Time, check func(context.Context) (State, error)) (State, error) {
	path := ""
	if dataDir != "" {
		path = filepath.Join(dataDir, noticeFile)
		if raw, err := os.ReadFile(path); err == nil {
			var cached noticeCache
			if json.Unmarshal(raw, &cached) == nil && cached.Version == version {
				if age := now.Sub(time.Unix(cached.CheckedAt, 0)); age >= 0 && age < NoticeInterval {
					return cached.State, nil
				}
			}
		}
	}
	state, err := check(ctx)
	if err != nil {
		return state, err
	}
	if path != "" {
		if raw, err := json.Marshal(noticeCache{Version: version, CheckedAt: now.Unix(), State: state}); err == nil {
			tmp := path + ".tmp"
			if os.WriteFile(tmp, raw, 0o600) == nil {
				_ = os.Rename(tmp, path)
			}
		}
	}
	return state, nil
}
