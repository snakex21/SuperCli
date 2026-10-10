package mail

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/storage"
)

func requireThunderbirdPathInRoot(t *testing.T, root, path string) {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("path %q escapes root %q: rel=%q err=%v", path, root, rel, err)
	}
}

func blockThunderbirdSystemTemp(t *testing.T, root string) {
	t.Helper()
	blocked := filepath.Join(root, "blocked-system-temp")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(key, blocked)
	}
	if filepath.Clean(os.TempDir()) != filepath.Clean(blocked) {
		t.Fatalf("test did not block OS temp: got %q want %q", os.TempDir(), blocked)
	}
}

func TestThunderbirdRuntimeDataDirPortableOverrides(t *testing.T) {
	base := t.TempDir()
	t.Setenv(storage.DataRootEnv, "")
	t.Setenv(storage.HomeEnv, filepath.Join(base, "workspace"))
	got, err := thunderbirdRuntimeDataDir("")
	if err != nil || got != storage.PortableDataRoot() {
		t.Fatalf("default root=%q err=%v want %q", got, err, storage.PortableDataRoot())
	}

	envRoot := filepath.Join(base, "env-data")
	t.Setenv(storage.DataRootEnv, envRoot)
	got, err = thunderbirdRuntimeDataDir("")
	if err != nil || got != envRoot {
		t.Fatalf("env root=%q err=%v want %q", got, err, envRoot)
	}

	flagRoot := filepath.Join(base, "flag-data")
	got, err = thunderbirdRuntimeDataDir(flagRoot)
	if err != nil || got != flagRoot {
		t.Fatalf("flag root=%q err=%v want %q", got, err, flagRoot)
	}
	for _, root := range []string{envRoot, flagRoot} {
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("resolver created %q: %v", root, err)
		}
	}
}

func TestThunderbirdConstructorIsLazy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	globalThunderbirdBridge.mu.Lock()
	before := globalThunderbirdBridge.dataDir
	globalThunderbirdBridge.mu.Unlock()
	tool := NewThunderbirdMail(root)
	if tool.dataDir != root {
		t.Fatalf("tool root=%q want %q", tool.dataDir, root)
	}
	globalThunderbirdBridge.mu.Lock()
	after := globalThunderbirdBridge.dataDir
	globalThunderbirdBridge.mu.Unlock()
	if before != after {
		t.Fatalf("constructor redirected global bridge from %q to %q", before, after)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("constructor created root: %v", err)
	}
}

func TestThunderbirdBridgePinsActiveDataDir(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "active-data")
	other := filepath.Join(base, "other-data")
	state := &thunderbirdBridgeState{}
	if err := state.configureDataDir(root); err != nil {
		t.Fatal(err)
	}
	if err := state.configureDataDir(filepath.Join(root, ".")); err != nil {
		t.Fatalf("same root rejected: %v", err)
	}
	if err := state.configureDataDir(other); err == nil {
		t.Fatal("different instance redirected an active bridge")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("configuration created root: %v", err)
	}
	t.Setenv(storage.DataRootEnv, other)
	dir, err := state.attachmentCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	requireThunderbirdPathInRoot(t, root, dir)
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Fatalf("active bridge followed a changed env root: %v", err)
	}
}

func TestThunderbirdAttachmentUploadPortableWithBlockedSystemTemp(t *testing.T) {
	base := t.TempDir()
	envRoot := filepath.Join(base, "env-data")
	flagRoot := filepath.Join(base, "flag-data")
	t.Setenv(storage.DataRootEnv, envRoot)
	blockThunderbirdSystemTemp(t, base)
	for _, tc := range []struct {
		name string
		root string
		want string
	}{
		{"env", "", envRoot},
		{"explicit", flagRoot, flagRoot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &thunderbirdBridgeState{dataDir: tc.root, downloads: make(map[string]thunderbirdDownloadedAttachment)}
			body := []byte("portable attachment payload")
			req := httptest.NewRequest(http.MethodPost, "/attachment-file?token="+thunderbirdBridgeToken+"&id=portable&filename=notes.txt&content_type=text%2Fplain", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			state.handleAttachmentFile(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			item, ok := state.downloadedAttachment("portable")
			if !ok {
				t.Fatal("attachment was not registered")
			}
			requireThunderbirdPathInRoot(t, tc.want, item.Path)
			got, err := os.ReadFile(item.Path)
			if err != nil || !bytes.Equal(got, body) || item.Name != "notes.txt" || item.ContentType != "text/plain" || item.Size != int64(len(body)) {
				t.Fatalf("transport changed: item=%+v bytes=%q err=%v", item, got, err)
			}
		})
	}
}

type thunderbirdFailedUploadReader struct{}

func (thunderbirdFailedUploadReader) Read(p []byte) (int, error) {
	return copy(p, "partial attachment"), errors.New("simulated upload read failure")
}

func TestThunderbirdAttachmentUploadFailureRemovesPartialFile(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	blockThunderbirdSystemTemp(t, base)
	state := &thunderbirdBridgeState{dataDir: root, downloads: make(map[string]thunderbirdDownloadedAttachment)}
	req := httptest.NewRequest(http.MethodPost, "/attachment-file?token="+thunderbirdBridgeToken+"&id=failed&filename=failed.pdf", thunderbirdFailedUploadReader{})
	rec := httptest.NewRecorder()
	state.handleAttachmentFile(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(state.downloads) != 0 {
		t.Fatal("failed upload registered a download")
	}
	entries, err := os.ReadDir(filepath.Join(root, "cache", "thunderbird", "attachments"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial upload remains: entries=%v err=%v", entries, err)
	}
}

func TestThunderbirdPortableWriteFailureHasNoFallback(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data-is-a-file")
	if err := os.WriteFile(root, []byte("blocked data directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "message.msg")
	if err := os.WriteFile(source, []byte("synthetic MSG input"), 0o600); err != nil {
		t.Fatal(err)
	}
	blockThunderbirdSystemTemp(t, base)

	state := &thunderbirdBridgeState{dataDir: root, downloads: make(map[string]thunderbirdDownloadedAttachment)}
	req := httptest.NewRequest(http.MethodPost, "/attachment-file?token="+thunderbirdBridgeToken+"&id=blocked&filename=blocked.txt", bytes.NewReader([]byte("payload")))
	rec := httptest.NewRecorder()
	state.handleAttachmentFile(rec, req)
	if rec.Code != http.StatusInternalServerError || len(state.downloads) != 0 {
		t.Fatalf("write failure fell back: status=%d downloads=%d", rec.Code, len(state.downloads))
	}

	called := false
	res, err := NewThunderbirdMail(root).importMSGWithConverter(context.Background(), thunderbirdToolArgs{Path: source}, func(context.Context, string, string) (msgConversionMeta, error) {
		called = true
		return msgConversionMeta{}, nil
	})
	if err != nil || res.Err == nil || called || !strings.Contains(res.Err.Error(), "cannot create temporary .eml") {
		t.Fatalf("MSG write failure: err=%v result=%+v converterCalled=%v", err, res, called)
	}
	got, err := os.ReadFile(root)
	if err != nil || string(got) != "blocked data directory" {
		t.Fatalf("blocked root changed: bytes=%q err=%v", got, err)
	}
}

func TestThunderbirdMSGConversionFailureCleansPortableEML(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "message.msg")
	if err := os.WriteFile(source, []byte("synthetic MSG input"), 0o600); err != nil {
		t.Fatal(err)
	}
	blockThunderbirdSystemTemp(t, base)
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"conversion", errors.New("simulated conversion failure")},
		{"canceled", context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(base, tc.name)
			var destination string
			res, err := NewThunderbirdMail(root).importMSGWithConverter(context.Background(), thunderbirdToolArgs{Path: source}, func(_ context.Context, gotSource, gotDestination string) (msgConversionMeta, error) {
				if gotSource != source {
					t.Fatalf("source=%q want %q", gotSource, source)
				}
				destination = gotDestination
				requireThunderbirdPathInRoot(t, root, destination)
				if err := os.WriteFile(destination, []byte("partial EML"), 0o600); err != nil {
					t.Fatal(err)
				}
				return msgConversionMeta{}, tc.err
			})
			if err != nil || res.Err == nil || !strings.Contains(res.Err.Error(), tc.err.Error()) {
				t.Fatalf("conversion failure: err=%v result=%+v", err, res)
			}
			if destination == "" {
				t.Fatal("converter was not called")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("temporary EML remains: %v", err)
			}
			entries, err := os.ReadDir(filepath.Dir(destination))
			if err != nil || len(entries) != 0 {
				t.Fatalf("conversion staging remains: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestThunderbirdMSGAttachmentDirBesidePortableEML(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	blockThunderbirdSystemTemp(t, base)
	file, err := createThunderbirdMSGFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	destination := file.Name()
	dir, err := createThunderbirdMSGAttachmentDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	requireThunderbirdPathInRoot(t, root, destination)
	requireThunderbirdPathInRoot(t, filepath.Dir(destination), dir)
	if err := os.WriteFile(filepath.Join(dir, "attachment.bin"), []byte("attachment"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if _, err := createThunderbirdMSGAttachmentDir(""); err == nil {
		t.Fatal("empty destination accepted")
	}
}

func TestThunderbirdPortableAttachmentRetentionUnchanged(t *testing.T) {
	root := t.TempDir()
	state := &thunderbirdBridgeState{dataDir: root, downloads: make(map[string]thunderbirdDownloadedAttachment)}
	upload := func(id, payload string) thunderbirdDownloadedAttachment {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/attachment-file?token="+thunderbirdBridgeToken+"&id="+id+"&filename=notes.txt", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		state.handleAttachmentFile(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		item, ok := state.downloadedAttachment(id)
		if !ok {
			t.Fatalf("download %q unavailable", id)
		}
		requireThunderbirdPathInRoot(t, root, item.Path)
		return item
	}
	expired := upload("expired", "expired content")
	old := upload("same-id", "old content")
	state.mu.Lock()
	item := state.downloads["expired"]
	item.CreatedAt = time.Now().Add(-31 * time.Minute)
	state.downloads["expired"] = item
	state.mu.Unlock()
	current := upload("same-id", "new content")
	for _, path := range []string{expired.Path, old.Path} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stale download remains: %q err=%v", path, err)
		}
	}
	if _, ok := state.downloadedAttachment("expired"); ok {
		t.Fatal("expired download still registered")
	}
	file, err := os.Open(current.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "new content" {
		t.Fatalf("retained download changed: bytes=%q err=%v", data, err)
	}
}
