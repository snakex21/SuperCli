package checkpoint

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"supercli/internal/system/config"
)

// Only the managed production collector inherits the session policy. Strict
// budget APIs keep their original contract. This runs under pressure only, not
// on the per-tool fast path, and never creates/repairs a session database.
func applyManagedSessionPolicy(ctx context.Context, dataDir string, audit *StoreRetentionAudit) error {
	path := filepath.Join(dataDir, "sessions.db")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	} // Standalone checkpoint stores have no sessions.
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrStoreInventory
	}
	tc, err := config.LoadToml(filepath.Join(dataDir, "config.toml"))
	if err != nil {
		return err
	}
	c := tc.Housekeeping.Defaults()
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&_pragma=busy_timeout(0)"}
	if !strings.HasPrefix(uri.Path, "/") {
		uri.Path = "/" + uri.Path
	}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id FROM sessions`)
	if err != nil {
		return err
	}
	sessions := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		sessions[id] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	keep, age := c.CheckpointKeepPerSession, c.CheckpointMaxAgeDays
	if keep < 0 {
		keep = 0
	}
	if age < 0 {
		age = 0
	}
	if age > 36500 {
		age = 36500
	}
	plan := sessionRetentionPlan(audit, sessions, nil, SessionRetention{KeepPerSession: keep, MaxAge: time.Duration(age) * 24 * time.Hour}, time.Now())
	guards := map[RetentionKey]bool{}
	for _, key := range plan.Kept {
		guards[key] = true
	}
	for i := range audit.Inventory.Records {
		if guards[audit.Inventory.Records[i].Key] {
			audit.Inventory.Records[i].Protected = true
		}
	}
	return nil
}
