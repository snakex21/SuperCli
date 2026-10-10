package webgui

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools/office"
)

func blockSystemTemp(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "blocked-system-temp")
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(key, path)
	}
}

func assertPortableStagingEmpty(t *testing.T, dataDir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, ".supercli", "staging"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("request left staged files: %v", entries)
	}
}

func TestPortableExportNeverUsesSystemTempOrIncludesStaging(t *testing.T) {
	root := t.TempDir()
	blockSystemTemp(t, root)
	if err := os.WriteFile(filepath.Join(root, "workspace.json"), []byte(`{"project":"example"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, full := range []bool{false, true} {
		stage, err := buildDataExportMode(root, full)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, stage)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("stage outside app data: %q %v", stage, err)
		}
		var archive bytes.Buffer
		if err := writeZip(&archive, stage); err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range zr.File {
			if strings.Contains(f.Name, "staging") || strings.Contains(f.Name, "export-") {
				t.Fatalf("archive includes staging: %s", f.Name)
			}
			if f.Name == "data/workspace.json" {
				found = true
			}
		}
		if !found {
			t.Fatal("backup lost workspace")
		}
		if err := os.RemoveAll(stage); err != nil {
			t.Fatal(err)
		}
	}
	assertPortableStagingEmpty(t, root)
}

func TestPortableDocumentImportLargeFileAndCleanup(t *testing.T) {
	srv := newTestServer(t, false)
	root := srv.eng.DataDir()
	blockSystemTemp(t, root)
	content := []byte("# Header\n" + strings.Repeat("text ", 1<<20)) // exceeds old 4 MiB spill threshold
	rec := importDocumentForTest(t, srv, "large.md", content)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Text != strings.TrimSpace(string(content)) {
		t.Fatal("document text changed")
	}
	assertPortableStagingEmpty(t, root)
	var doc bytes.Buffer
	if err := office.WriteSimpleDocx(&doc, "# Portable DOCX\n\nPreserve text."); err != nil {
		t.Fatal(err)
	}
	rec = importDocumentForTest(t, srv, "good.docx", doc.Bytes())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Preserve text.") {
		t.Fatalf("DOCX status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertPortableStagingEmpty(t, root)
	rec = importDocumentForTest(t, srv, "bad.docx", []byte("not a docx"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid DOCX status=%d", rec.Code)
	}
	assertPortableStagingEmpty(t, root)
	rec = importDocumentForTest(t, srv, "bad.exe", []byte("unsupported"))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported status=%d", rec.Code)
	}
	assertPortableStagingEmpty(t, root)
}

func TestPortableBackupImportStreamsBeyondOldSpillThreshold(t *testing.T) {
	srv := newTestServer(t, false)
	root := srv.eng.DataDir()
	blockSystemTemp(t, root)
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	manifest, err := zw.Create(dataBackupManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(manifest).Encode(dataBackupMeta{Format: dataBackupFormat, App: "SuperCli", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	payload, err := zw.CreateHeader(&zip.FileHeader{Name: "data/memory/large.md", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(payload, strings.NewReader(strings.Repeat("x", 9<<20)), 9<<20); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	p, err := mw.CreateFormFile("backup", "backup.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/data/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.handleDataImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertPortableStagingEmpty(t, root)
	pending, err := readPendingDataImport(filepath.Join(root, pendingImportFile))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(pending.Stage, "data", "memory", "large.md"))
	if err != nil || len(data) != 9<<20 {
		t.Fatalf("staged backup length=%d err=%v", len(data), err)
	}
}

func TestPortableMultipartPriorityLimitsAndUnwritableRoot(t *testing.T) {
	root := t.TempDir()
	makeRequest := func() *http.Request {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		for _, pair := range [][2]string{{"document", "secondary"}, {"file", "preferred"}, {"file", "duplicate"}} {
			p, err := mw.CreateFormFile(pair[0], pair[1]+".txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(p, pair[1]); err != nil {
				t.Fatal(err)
			}
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/upload", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		return req
	}
	upload, cleanup, err := stageMultipartUpload(makeRequest(), root, "test-*", 100, "file", "document", "files")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(upload.Path)
	if err != nil || string(data) != "preferred" {
		t.Fatalf("priority changed: %q %v", data, err)
	}
	cleanup()
	assertPortableStagingEmpty(t, root)
	if _, _, err := stageMultipartUpload(makeRequest(), root, "test-*", 2, "file", "document"); err == nil {
		t.Fatal("oversized upload succeeded")
	}
	assertPortableStagingEmpty(t, root)
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := stageMultipartUpload(makeRequest(), blocked, "test-*", 100, "file"); uploadErrorStatus(err) != http.StatusInternalServerError {
		t.Fatalf("unwritable portable root silently fell back: %v", err)
	}
	if _, err := portableWorkDir("", "test-*"); err == nil {
		t.Fatal("empty root accepted")
	}
}

func TestStatsRecentTelemetryProjectionKeepsDashboard(t *testing.T) {
	eng, store, sess, _ := statsFixture(t)
	appendStatsMessage(t, store, sess.ID, llm.Message{Role: llm.RoleAssistant, Content: "done"})
	files := make([]session.FileChange, 128)
	for i := range files {
		files[i] = session.FileChange{Path: strings.Repeat("project/", 100), Kind: "modified"}
	}
	turn := session.TurnSummary{SessionID: sess.ID, DurationMS: 2000, Steps: 4, ModelCalls: 3, HelperCalls: 1, AuxCalls: 1, AuxUs: 500_000, FailedCalls: 1, CanceledCalls: 1, ToolFailures: 2, Phases: map[string]int64{"backend_wait": 900_000, "tool_execution": 400_000, "tool:read_many": 400_000, "context_prepare": 100_000}, ToolDiag: session.TurnToolDiag{Failures: map[string]int{"read_many": 2}, NoOpSearches: 4}, FileChanges: files}
	if err := store.AppendTurnSummary(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	full, err := store.ReadRecentTurnSummaries(context.Background(), time.Now().Add(-7*24*time.Hour), 2000)
	if err != nil || len(full) != 1 || len(full[0].FileChanges) != 128 {
		t.Fatalf("full payload lost: %v", err)
	}
	got, err := eng.stats(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := summarizeTelemetry(full, got.Tokens)
	want.Scope = "7d"
	if !reflect.DeepEqual(got.Telemetry, want) {
		t.Fatalf("dashboard changed: got=%+v want=%+v", got.Telemetry, want)
	}
}

func TestPortableDocumentPriorityIgnoresOversizedUnselectedFile(t *testing.T) {
	srv := newTestServer(t, false)
	oversized := bytes.Repeat([]byte("x"), maxLocalDocumentImportBytes+1)
	for _, fileFirst := range []bool{false, true} {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		names := []string{"document", "file"}
		if fileFirst {
			names = []string{"file", "document"}
		}
		for _, name := range names {
			part, err := mw.CreateFormFile(name, name+".txt")
			if err != nil {
				t.Fatal(err)
			}
			content := oversized
			if name == "file" {
				content = []byte("preferred text")
			}
			if _, err := part.Write(content); err != nil {
				t.Fatal(err)
			}
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/document/import", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		srv.handleDocumentImport(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "preferred text") {
			t.Fatalf("fileFirst=%v status=%d body=%s", fileFirst, rec.Code, rec.Body.String())
		}
		assertPortableStagingEmpty(t, srv.eng.DataDir())
	}
}

func TestPortableMultipartLimitsIgnoredPartsAndCleansFailure(t *testing.T) {
	root := t.TempDir()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	p, err := mw.CreateFormFile("file", "good.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(p, "ok"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		if err := mw.WriteField("ignored", "x"); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if _, _, err := stageMultipartUpload(req, root, "test-*", 32<<20, "file"); err == nil {
		t.Fatal("accepted more than 1000 parts")
	}
	assertPortableStagingEmpty(t, root)
}

func TestPortableMultipartLimitsHeadersAcrossParts(t *testing.T) {
	root := t.TempDir()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for i := 0; i < 260; i++ {
		headers := make(textproto.MIMEHeader)
		headers.Set("Content-Disposition", `form-data; name="ignored"; filename="ignored.txt"`)
		for j := 0; j < 40; j++ {
			headers.Add("X-Test", "x")
		}
		part, err := mw.CreatePart(headers)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if _, _, err := stageMultipartUpload(req, root, "test-*", 32<<20, "file"); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("accepted excessive aggregate headers: %v", err)
	}
	assertPortableStagingEmpty(t, root)
}

func TestPortableMultipartIgnoredBodyStillHasRequestLimit(t *testing.T) {
	root := t.TempDir()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("ignored", strings.Repeat("x", 1024)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, 512)
	if _, _, err := stageMultipartUpload(req, root, "test-*", 32<<20, "file"); err == nil {
		t.Fatal("ignored field bypassed request limit")
	}
	assertPortableStagingEmpty(t, root)
}
