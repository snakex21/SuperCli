package webgui

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/storage/memory"
)

func TestProjectCleanupPolicyBackupRoundTrip(t *testing.T) {
	for _, full := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("full=%t_enabled=%t", full, enabled), func(t *testing.T) {
				source, target := t.TempDir(), t.TempDir()
				if err := memory.SaveProjectCheckpointCleanup(source, enabled); err != nil {
					t.Fatal(err)
				}
				if err := memory.SaveProjectCheckpointCleanup(target, !enabled); err != nil {
					t.Fatal(err)
				}
				sourcePolicy, err := os.ReadFile(filepath.Join(source, "project-cleanup.json"))
				if err != nil {
					t.Fatal(err)
				}
				oldPolicy, err := os.ReadFile(filepath.Join(target, "project-cleanup.json"))
				if err != nil {
					t.Fatal(err)
				}
				archive, err := ExportDataBackup(source, full)
				if err != nil {
					t.Fatal(err)
				}
				gotFull, err := StageDataImport(target, archive)
				if err != nil || gotFull != full {
					t.Fatalf("stage: full=%t err=%v", gotFull, err)
				}
				if err := ApplyPendingDataImport(target); err != nil {
					t.Fatal(err)
				}
				if actual, err := memory.LoadProjectCheckpointCleanup(target); err != nil || actual != enabled {
					t.Fatalf("restored policy=%t err=%v, want %t", actual, err, enabled)
				}
				if raw, err := os.ReadFile(filepath.Join(target, "project-cleanup.json")); err != nil || !bytes.Equal(raw, sourcePolicy) {
					t.Fatalf("restored policy bytes=%q err=%v", raw, err)
				}
				rescues, err := filepath.Glob(filepath.Join(target, "backups", "pre-import-*", "project-cleanup.json"))
				if err != nil || len(rescues) != 1 {
					t.Fatalf("rescue policy paths=%v err=%v", rescues, err)
				}
				if raw, err := os.ReadFile(rescues[0]); err != nil || !bytes.Equal(raw, oldPolicy) {
					t.Fatalf("prior policy was not rescued: %q err=%v", raw, err)
				}
			})
		}
	}
}

func TestProjectCleanupPolicyBackupRejectsMalformedPayloadBeforePendingImport(t *testing.T) {
	for _, payload := range []string{
		`{`, `{"delete_checkpoints_on_remove":"true"}`, `{"delete_checkpoints_on_remove":42}`,
		`{"delete_checkpoints_on_remove":true} {}`, `{"unrecognized":true}`,
		strings.Repeat(" ", 4097) + `{"delete_checkpoints_on_remove":true}`,
	} {
		t.Run(fmt.Sprintf("payload_%d", len(payload)), func(t *testing.T) {
			target := t.TempDir()
			if err := memory.SaveProjectCheckpointCleanup(target, false); err != nil {
				t.Fatal(err)
			}
			archive := projectCleanupPolicyArchive(t, payload)
			if _, err := StageDataImport(target, archive); err == nil || !strings.Contains(err.Error(), "checkpoint preference") {
				t.Fatalf("malformed policy accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(target, pendingImportFile)); !os.IsNotExist(err) {
				t.Fatalf("rejected policy became pending: %v", err)
			}
			if enabled, err := memory.LoadProjectCheckpointCleanup(target); err != nil || enabled {
				t.Fatalf("rejected import changed current policy: %t %v", enabled, err)
			}
		})
	}
}

func TestProjectCleanupPolicyPendingImportRevalidatesBeforeMovingLiveData(t *testing.T) {
	target := t.TempDir()
	if err := memory.SaveProjectCheckpointCleanup(target, false); err != nil {
		t.Fatal(err)
	}
	live := []byte(`{"preserved":"live project metadata"}`)
	if err := os.WriteFile(filepath.Join(target, "projects.json"), live, 0600); err != nil {
		t.Fatal(err)
	}
	archive := projectCleanupPolicyArchive(t, `{"delete_checkpoints_on_remove":true}`)
	if _, err := StageDataImport(target, archive); err != nil {
		t.Fatal(err)
	}
	pending, err := readPendingDataImport(filepath.Join(target, pendingImportFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending.Stage, "data", "project-cleanup.json"), []byte(`{"delete_checkpoints_on_remove":"not a boolean"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPendingDataImport(target); err == nil || !strings.Contains(err.Error(), "checkpoint preference") {
		t.Fatalf("tampered pending policy accepted: %v", err)
	}
	if enabled, err := memory.LoadProjectCheckpointCleanup(target); err != nil || enabled {
		t.Fatalf("invalid pending policy changed current preference: %t %v", enabled, err)
	}
	if actual, err := os.ReadFile(filepath.Join(target, "projects.json")); err != nil || !bytes.Equal(actual, live) {
		t.Fatalf("invalid pending policy moved live metadata: %q %v", actual, err)
	}
	if rescues, err := filepath.Glob(filepath.Join(target, "backups", "pre-import-*")); err != nil || len(rescues) != 0 {
		t.Fatalf("validation started replacing live data: %v %v", rescues, err)
	}
}

func TestProjectCleanupPolicyImportRollbackRestoresPreviousPreference(t *testing.T) {
	target := t.TempDir()
	if err := memory.SaveProjectCheckpointCleanup(target, false); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(target, "project-cleanup.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StageDataImport(target, projectCleanupPolicyArchive(t, `{"delete_checkpoints_on_remove":true}`)); err != nil {
		t.Fatal(err)
	}
	pending, err := readPendingDataImport(filepath.Join(target, pendingImportFile))
	if err != nil {
		t.Fatal(err)
	}
	// Sorting installs the policy before this unsupported entry triggers the
	// existing rollback path, which must restore the original preference.
	if err := os.WriteFile(filepath.Join(pending.Stage, "data", "zz-unsupported-root"), []byte("reject this entry"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPendingDataImport(target); err == nil || !strings.Contains(err.Error(), "unsafe imported name") {
		t.Fatalf("invalid import did not fail: %v", err)
	}
	if actual, err := os.ReadFile(filepath.Join(target, "project-cleanup.json")); err != nil || !bytes.Equal(actual, original) {
		t.Fatalf("rollback lost original preference: %q %v", actual, err)
	}
	if enabled, err := memory.LoadProjectCheckpointCleanup(target); err != nil || enabled {
		t.Fatalf("failed import enabled cleanup: %t %v", enabled, err)
	}
}

func projectCleanupPolicyArchive(t *testing.T, payload string) string {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "cleanup-policy.zip")
	out, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(out)
	manifest, _ := json.Marshal(dataBackupMeta{Format: dataBackupFormat, App: "SuperCli", CreatedAt: time.Now().UTC()})
	for _, entry := range []struct{ name, content string }{{dataBackupManifest, string(manifest)}, {"data/project-cleanup.json", payload}} {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return archive
}
