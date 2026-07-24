package webgui

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
)

func TestDataBackupRoundTripPreservesSecretsOutsideArchive(t *testing.T) {
	source := t.TempDir()
	sourceSessions, err := session.OpenStore(source)
	if err != nil {
		t.Fatal(err)
	}
	created, err := sourceSessions.Create(t.TempDir(), "echo", "imported conversation")
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceSessions.Close(); err != nil {
		t.Fatal(err)
	}
	sourceMemory, err := memory.OpenStore(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceMemory.Put(memory.Entry{ID: "remember-me", Scope: memory.ScopePreference, Content: "compact answers", Source: memory.SourceUser, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := sourceMemory.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "config.toml"), []byte("api_key = \"source-secret\""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "auth.json"), []byte(`{"token":"source-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceDocument := filepath.Join(source, "module-sources", "ocr-test", "history.png")
	if err := os.MkdirAll(filepath.Dir(sourceDocument), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceDocument, []byte("saved OCR source"), 0o600); err != nil {
		t.Fatal(err)
	}
	userInstructions := `{"version":1,"enabled":true,"active_id":"office","presets":[{"id":"office","name":"Office","content":"Keep formatting"}]}`
	if err := os.WriteFile(filepath.Join(source, "user-instructions.json"), []byte(userInstructions), 0o600); err != nil {
		t.Fatal(err)
	}

	exportStage, err := buildDataExport(source)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(exportStage)
	archivePath := filepath.Join(t.TempDir(), "backup.zip")
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeZip(archive, exportStage); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range zr.File {
		if strings.Contains(file.Name, "config.toml") || strings.Contains(file.Name, "auth.json") {
			t.Fatalf("secret-bearing file leaked into backup: %s", file.Name)
		}
	}
	zr.Close()

	target := t.TempDir()
	targetSessions, err := session.OpenStore(target)
	if err != nil {
		t.Fatal(err)
	}
	old, err := targetSessions.Create(t.TempDir(), "echo", "old conversation")
	if err != nil {
		t.Fatal(err)
	}
	if err := targetSessions.Close(); err != nil {
		t.Fatal(err)
	}
	targetConfig := []byte("api_key = \"target-secret\"")
	if err := os.WriteFile(filepath.Join(target, "config.toml"), targetConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	imports := filepath.Join(target, "imports")
	stage := filepath.Join(imports, "roundtrip")
	if err := os.MkdirAll(imports, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractDataBackup(archivePath, stage); err != nil {
		t.Fatal(err)
	}
	pending, _ := json.Marshal(pendingDataImport{Stage: stage, CreatedAt: time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(target, pendingImportFile), pending, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPendingDataImport(target); err != nil {
		t.Fatal(err)
	}
	importedSessions, err := session.OpenStore(target)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := importedSessions.List(0)
	if err != nil {
		t.Fatal(err)
	}
	importedSessions.Close()
	if len(rows) != 1 || rows[0].ID != created.ID || rows[0].ID == old.ID {
		t.Fatalf("imported sessions=%+v", rows)
	}
	importedMemory, err := memory.OpenStore(target)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := importedMemory.List("", 0)
	if err != nil {
		t.Fatal(err)
	}
	importedMemory.Close()
	if len(entries) != 1 || entries[0].ID != "remember-me" {
		t.Fatalf("imported memory=%+v", entries)
	}
	if got, err := os.ReadFile(filepath.Join(target, "config.toml")); err != nil || !bytes.Equal(got, targetConfig) {
		t.Fatalf("target secret config changed: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(target, "module-sources", "ocr-test", "history.png")); err != nil || string(got) != "saved OCR source" {
		t.Fatalf("OCR source was not restored: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(target, "user-instructions.json")); err != nil || string(got) != userInstructions {
		t.Fatalf("user instructions were not restored: %q err=%v", got, err)
	}
	backups, err := filepath.Glob(filepath.Join(target, "backups", "pre-import-*", "sessions.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("rescue backup=%v err=%v", backups, err)
	}
}

func TestFullBackupRoundTripIncludesPortableCredentials(t *testing.T) {
	source := t.TempDir()
	store, err := session.OpenStore(source)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(t.TempDir(), "openai", "portable conversation")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"config.toml":                   "[providers.cloud]\napi_key = \"portable-secret\"\n",
		"auth.json":                     `{"tokens":{"access_token":"portable-token"}}`,
		"auth-work.json":                `{"tokens":{"access_token":"work-token"}}`,
		"models.json":                   `[{"id":"custom-model"}]`,
		"context_limits.json":           `{"custom-model":131072}`,
		"mcp/browser/manifest.toml":     "name = \"browser\"\n",
		"skills/writer/SKILL.md":        "# Writer\n",
		"tools/check-project/tool.toml": "command = \"check\"\n",
		"profiles/local.txt":            "Keep the KV prefix stable.\n",
	}
	for name, content := range files {
		path := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stage, err := buildFullDataExport(source)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	archiveFile, err := os.Create(filepath.Join(t.TempDir(), "full.zip"))
	if err != nil {
		t.Fatal(err)
	}
	archive := archiveFile.Name()
	if err := writeZip(archiveFile, stage); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "config.toml"), []byte("old-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	importStage := filepath.Join(target, "imports", "full")
	meta, err := readDataBackupMeta(archive)
	if err != nil || !meta.Secrets {
		t.Fatalf("full backup manifest=%+v err=%v", meta, err)
	}
	meta, err = extractDataBackupMode(archive, importStage, true)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.Secrets {
		t.Fatal("full backup manifest was not marked secret-bearing")
	}
	pending, _ := json.Marshal(pendingDataImport{Stage: importStage, CreatedAt: time.Now().UTC(), Full: true})
	if err := os.WriteFile(filepath.Join(target, pendingImportFile), pending, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPendingDataImport(target); err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Errorf("restored %s = %q, err=%v; want %q", name, got, err, want)
		}
	}
	imported, err := session.OpenStore(target)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := imported.List(0)
	_ = imported.Close()
	if err != nil || len(rows) != 1 || rows[0].ID != created.ID {
		t.Fatalf("full backup conversations=%+v err=%v", rows, err)
	}
}

func TestFullBackupHTTPExportAndImport(t *testing.T) {
	srv := newTestServer(t, false)
	exported := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/data/export/full", nil)
	req.Host = "127.0.0.1:8765"
	req.RemoteAddr = "127.0.0.1:12345"
	srv.Handler().ServeHTTP(exported, req)
	if exported.Code != http.StatusOK {
		t.Fatalf("full export=%d %s", exported.Code, exported.Body.String())
	}
	if !bytes.HasPrefix(exported.Body.Bytes(), []byte("PK")) {
		t.Fatal("full export is not a ZIP backup")
	}

	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("backup", "full.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(exported.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	imported := httptest.NewRecorder()
	importReq := httptest.NewRequest(http.MethodPost, "/api/data/import", &upload)
	importReq.Header.Set("Content-Type", writer.FormDataContentType())
	importReq.Host = "127.0.0.1:8765"
	importReq.RemoteAddr = "127.0.0.1:12345"
	srv.Handler().ServeHTTP(imported, importReq)
	if imported.Code != http.StatusOK {
		t.Fatalf("full import=%d %s", imported.Code, imported.Body.String())
	}
	pending, err := readPendingDataImport(filepath.Join(srv.eng.DataDir(), pendingImportFile))
	if err != nil || !pending.Full {
		t.Fatalf("pending full import=%+v err=%v", pending, err)
	}
}

func TestFullBackupEndpointsStayLoopbackOnlyWithAllowRemote(t *testing.T) {
	srv := newTestServer(t, true)
	for _, item := range []struct{ method, path string }{{http.MethodGet, "/api/data/export/full"}, {http.MethodPost, "/api/data/import"}} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(item.method, item.path, nil)
		req.Host = "example.test"
		req.RemoteAddr = "203.0.113.10:12345"
		req.Header.Set("Authorization", "Bearer "+srv.sessionToken)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("remote %s=%d, want 403", item.path, rec.Code)
		}
	}
}

func TestDataClearEndpointsKeepActionsSeparate(t *testing.T) {
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(srv.eng.Home(), "echo", "delete me"); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.OpenStore(srv.eng.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Put(memory.Entry{ID: "keep-until-memory-clear", Scope: memory.ScopePreference, Content: "fact", Source: memory.SourceUser}); err != nil {
		t.Fatal(err)
	}
	mem.Close()
	reflectDir := filepath.Join(srv.eng.DataDir(), "reflect")
	if err := os.MkdirAll(reflectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reflectDir, "draft.jsonl"), []byte("learned\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	clearSessions := httptest.NewRecorder()
	srv.handleDataClear(clearSessions, httptest.NewRequest(http.MethodPost, "/api/data/clear", strings.NewReader(`{"action":"sessions"}`)))
	if clearSessions.Code != http.StatusOK {
		t.Fatalf("clear sessions=%d %s", clearSessions.Code, clearSessions.Body.String())
	}
	if rows, _ := store.List(0); len(rows) != 0 {
		t.Fatalf("sessions remain=%+v", rows)
	}
	if count, err := countAllMemory(srv.eng.DataDir()); err != nil || count != 1 {
		t.Fatalf("memory changed with sessions: count=%d err=%v", count, err)
	}

	clearMemory := httptest.NewRecorder()
	srv.handleDataClear(clearMemory, httptest.NewRequest(http.MethodPost, "/api/data/clear", strings.NewReader(`{"action":"memory"}`)))
	if clearMemory.Code != http.StatusOK {
		t.Fatalf("clear memory=%d %s", clearMemory.Code, clearMemory.Body.String())
	}
	if count, err := countAllMemory(srv.eng.DataDir()); err != nil || count != 0 {
		t.Fatalf("memory remains: count=%d err=%v", count, err)
	}
	if _, err := os.Stat(reflectDir); !os.IsNotExist(err) {
		t.Fatalf("reflection history remains after memory clear: %v", err)
	}
}

func TestAllowedBackupPathRejectsNestedDatabaseAndUnsafeProjectNames(t *testing.T) {
	for _, name := range []string{
		"data/projects/project/memory.db/extra",
		"data/projects/bad:name/memory.db",
		"data/projects/project/memory",
		"data/memory.db/extra",
	} {
		if allowedBackupPath(name) {
			t.Errorf("allowed unsafe backup path %q", name)
		}
	}
	for _, name := range []string{
		"data/projects/project/memory.db",
		"data/projects/project/memory/patterns/item.md",
		"data/memory/patterns/item.md",
		"data/module-sources/ocr-test/history.png",
		"data/folder-indexing.json",
		"data/schedules.json",
		"data/user-instructions.json",
	} {
		if !allowedBackupPath(name) {
			t.Errorf("rejected supported backup path %q", name)
		}
	}
	for _, name := range []string{
		"data/config.toml",
		"data/auth.json",
		"data/auth-work.json",
		"data/models.json",
		"data/context_limits.json",
		"data/mcp/browser/manifest.toml",
		"data/skills/writer/SKILL.md",
		"data/tools/check-project/tool.toml",
		"data/profiles/local.txt",
	} {
		if allowedBackupPath(name) {
			t.Errorf("safe backup allowed secret-bearing path %q", name)
		}
		if !allowedBackupPathMode(name, true) {
			t.Errorf("full backup rejected portable path %q", name)
		}
	}
}

func TestExportAndStageDataBackupSharedWorkflow(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "projects.json"), []byte(`{"portable":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	archive, err := ExportDataBackup(source, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(archive) != ".zip" {
		t.Fatalf("archive=%q", archive)
	}
	target := t.TempDir()
	full, err := StageDataImport(target, archive)
	if err != nil {
		t.Fatal(err)
	}
	if full {
		t.Fatal("safe backup reported credentials")
	}
	if err := ApplyPendingDataImport(target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"portable":true}` {
		t.Fatalf("projects.json=%s", got)
	}
}
