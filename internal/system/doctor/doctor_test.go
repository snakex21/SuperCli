package doctor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools"
)

func TestRunIncludesCoreChecks(t *testing.T) {
	dir := t.TempDir()
	rep := Run(context.Background(), Env{Version: "test", Home: dir, DataDir: dir, Registry: tools.NewRegistry()})
	if rep.Version != "test" {
		t.Fatalf("version = %q", rep.Version)
	}
	names := map[string]bool{}
	for _, c := range rep.Checks {
		names[c.Name] = true
	}
	for _, name := range []string{"binary", "runtime", "home", "data dir", "tools"} {
		if !names[name] {
			t.Fatalf("missing check %q in %+v", name, rep.Checks)
		}
	}
}

func TestRenderPlain(t *testing.T) {
	rep := Report{Version: "x", Checks: []Check{{Name: "home", Status: OK, Detail: "/tmp"}, {Name: "provider", Status: Warn, Detail: "echo"}}}
	out := RenderPlain(rep)
	if !strings.Contains(out, "supercli x") || !strings.Contains(out, "home") || !strings.Contains(out, "provider") {
		t.Fatalf("unexpected render: %q", out)
	}
}

func TestSQLiteIntegrityCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE test (id INTEGER PRIMARY KEY, value TEXT)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if got := sqliteIntegrityCheck("valid db", path, true); got.Status != OK {
		t.Fatalf("valid db check = %+v", got)
	}

	broken := filepath.Join(dir, "broken.db")
	if err := os.WriteFile(broken, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sqliteIntegrityCheck("broken db", broken, true); got.Status != Fail {
		t.Fatalf("broken db check = %+v, want fail", got)
	}

	missing := filepath.Join(dir, "missing.db")
	if got := sqliteIntegrityCheck("missing db", missing, false); got.Status != Warn {
		t.Fatalf("missing optional db check = %+v, want warn", got)
	}
}
