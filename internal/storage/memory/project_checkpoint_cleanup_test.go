package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectCheckpointCleanupIsPortableExplicitAndFailClosed(t *testing.T) {
	data := t.TempDir()
	if enabled, err := LoadProjectCheckpointCleanup(data); err != nil || enabled {
		t.Fatalf("new installation deletes history: %v/%v", enabled, err)
	}
	for _, enabled := range []bool{true, false, true} {
		if err := SaveProjectCheckpointCleanup(data, enabled); err != nil {
			t.Fatal(err)
		}
		if actual, err := LoadProjectCheckpointCleanup(data); err != nil || actual != enabled {
			t.Fatalf("preference not persisted: %v/%v", actual, err)
		}
	}
	for _, raw := range []string{`{"delete_checkpoints_on_remove":"true"}`, `{"delete_checkpoints_on_remove":true} {}`, `{`, strings.Repeat(" ", 4097) + `{"delete_checkpoints_on_remove":true}`} {
		if err := os.WriteFile(filepath.Join(data, projectCheckpointCleanupFile), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if enabled, err := LoadProjectCheckpointCleanup(data); err == nil || enabled {
			t.Fatalf("corruption enabled cleanup: %v/%v", enabled, err)
		}
	}
	entries, err := os.ReadDir(data)
	if err != nil || len(entries) != 1 || entries[0].Name() != projectCheckpointCleanupFile {
		t.Fatalf("unexpected persistence: %v %v", entries, err)
	}
}
