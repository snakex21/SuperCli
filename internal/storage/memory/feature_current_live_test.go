package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Optional offline audit: bypass OpenStore, migrations and mirror recovery.
// Print aggregate counts only, never remembered content or credentials.
func TestCurrentMemoryReadOnlyAudit(t *testing.T) {
	path := os.Getenv("SUPERCLI_MEMORY_AUDIT_DB")
	if path == "" {
		t.Skip("opt-in read-only memory audit")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat, err := os.Stat(abs); err != nil || !stat.Mode().IsRegular() {
		t.Fatal("existing database required")
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var readonly int
	if err := db.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&readonly); err != nil || readonly != 1 {
		t.Fatal("query-only guard missing")
	}
	s := &Store{db: db}
	before, err := s.List("", 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(before)
	digest := sha256.Sum256(encoded)
	corrections, err := s.memoryCorrections()
	if err != nil {
		t.Fatal(err)
	}
	current := resolveCurrentEntries(before, corrections)
	changed := 0
	for _, e := range before {
		resolved := resolveCurrentEntries([]Entry{e}, corrections)
		if resolved[0].ID != e.ID {
			changed++
		}
	}
	briefing := BuildBriefing(nil, s, "audit-project", 1000)
	after, err := s.List("", 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(after)
	if sha256.Sum256(encoded) != digest {
		t.Fatal("read-only audit changed entries")
	}
	if changed == 0 {
		t.Fatal("audit expected an explicit existing correction")
	}
	t.Logf("archive_entries=%d corrections=%d replaced_entries=%d current_entries=%d briefing_bytes=%d; source unchanged", len(before), len(corrections), changed, len(current), len(briefing))
}
