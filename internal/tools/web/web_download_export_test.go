package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/checkpoint"
	"supercli/internal/tools/sandbox"
)

func TestWebDownloadRequestedExportPreservesFilesAndRejectsNoGrantBeforeHTTP(t *testing.T) {
	prior := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(prior) })
	tool, calls := downloadFixture(t, io.NopCloser(strings.NewReader("GIF89a-export-body")), "image/gif", -1, http.StatusOK)
	downloads := filepath.Join(t.TempDir(), "Downloads")
	destination := filepath.Join(downloads, "cowboys.gif")
	result := invokeDownload(t, tool, context.Background(), destination, 0)
	if !errors.Is(result.Err, sandbox.ErrEscape) || *calls != 0 {
		t.Fatalf("ungranted export reached HTTP: %+v calls=%d", result, *calls)
	}
	ctx := sandbox.WithDownloadExportDirs(context.Background(), downloads)
	result = invokeDownload(t, tool, ctx, destination, 0)
	if result.Err != nil || *calls != 1 {
		t.Fatalf("requested Downloads export: %+v calls=%d", result, *calls)
	}
	saved, err := os.ReadFile(destination)
	if err != nil || string(saved) != "GIF89a-export-body" {
		t.Fatalf("actual exported bytes: %q %v", saved, err)
	}
	result = invokeDownload(t, tool, ctx, destination, 0)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "destination_exists") || *calls != 1 {
		t.Fatalf("existing export was not preserved before HTTP: %+v calls=%d", result, *calls)
	}
	saved, err = os.ReadFile(destination)
	if err != nil || string(saved) != "GIF89a-export-body" {
		t.Fatalf("existing export changed: %q %v", saved, err)
	}
	result = invokeDownload(t, tool, context.Background(), filepath.Join(downloads, "another.gif"), 0)
	if !errors.Is(result.Err, sandbox.ErrEscape) || *calls != 1 {
		t.Fatalf("grant escaped its invocation: %+v calls=%d", result, *calls)
	}
	for _, path := range []string{filepath.Join(downloads, "..", "escape.gif"), filepath.Join(t.TempDir(), "different.gif")} {
		result = invokeDownload(t, tool, ctx, path, 0)
		if !errors.Is(result.Err, sandbox.ErrEscape) || *calls != 1 {
			t.Fatalf("export left requested folder: %+v calls=%d", result, *calls)
		}
	}
}

func TestWebDownloadExportSymlinkEscapeRejectedBeforeHTTP(t *testing.T) {
	prior := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(prior) })
	tool, calls := downloadFixture(t, io.NopCloser(strings.NewReader("unused")), "image/gif", -1, http.StatusOK)
	downloads, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(downloads, "redirect")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	ctx := sandbox.WithDownloadExportDirs(context.Background(), downloads)
	result := invokeDownload(t, tool, ctx, filepath.Join(link, "escape.gif"), 0)
	if !errors.Is(result.Err, sandbox.ErrEscape) || *calls != 0 {
		t.Fatalf("symlink escaped before destination admission: %+v calls=%d", result, *calls)
	}
	if _, err := os.Lstat(filepath.Join(outside, "escape.gif")); !os.IsNotExist(err) {
		t.Fatalf("symlink escape wrote a file: %v", err)
	}
}

func TestWebDownloadExportGrantChangeWhileReadingNeverPublishes(t *testing.T) {
	prior := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(prior) })
	tool := NewWebDownload(t.TempDir())
	downloads, outside := t.TempDir(), t.TempDir()
	alias := filepath.Join(t.TempDir(), "DownloadsAlias")
	if err := os.Symlink(downloads, alias); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	ctx := sandbox.WithDownloadExportDirs(context.Background(), alias)
	reader := &exportRootChangeReader{t: t, alias: alias, outside: outside}
	tool.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/octet-stream"}}, ContentLength: 513, Body: reader, Request: req}, nil
	})
	result := invokeDownload(t, tool, ctx, filepath.Join(alias, "file.bin"), 0)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "destination changed") || !reader.changed {
		t.Fatalf("grant mutation during body was not rejected: %+v changed=%v", result, reader.changed)
	}
	for _, dir := range []string{downloads, outside} {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("changed export retained destination or temporary bytes: dir=%q entries=%v err=%v", dir, entries, err)
		}
	}
}

type exportRootChangeReader struct {
	t              *testing.T
	alias, outside string
	reads          int
	changed        bool
}

func (r *exportRootChangeReader) Read(p []byte) (int, error) {
	r.reads++
	switch r.reads {
	case 1:
		return copy(p, make([]byte, 512)), nil
	case 2:
		if err := os.Remove(r.alias); err != nil {
			r.t.Fatal(err)
		}
		if err := os.Symlink(r.outside, r.alias); err != nil {
			r.t.Fatal(err)
		}
		r.changed = true
		return copy(p, []byte{1}), nil
	default:
		return 0, io.EOF
	}
}

func (*exportRootChangeReader) Close() error { return nil }

func TestWebDownloadCheckpointPinsWorkspaceDestinationBeforeCapture(t *testing.T) {
	prior := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(prior) })
	tool, calls := downloadFixture(t, io.NopCloser(strings.NewReader("unused")), "image/gif", -1, http.StatusOK)
	originalWorkspace := tool.BaseDir
	replacementWorkspace := t.TempDir()
	alias := filepath.Join(t.TempDir(), "project-alias")
	if err := os.Symlink(originalWorkspace, alias); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	tool.BaseDir = alias
	manager, err := checkpoint.Open(alias, t.TempDir())
	if errors.Is(err, checkpoint.ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	turn := manager.NewTurn("workspace-pin", "download a workspace file")
	spec := tool.Spec()
	original := spec.Fn
	invoked := false
	spec.Fn = func(ctx context.Context, args json.RawMessage) (Result, error) {
		// This callback runs after the checkpoint BEFORE capture. A concurrent
		// filesystem change must not redirect the already-admitted download.
		invoked = true
		if err := os.Remove(alias); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(replacementWorkspace, alias); err != nil {
			t.Fatal(err)
		}
		return original(ctx, args)
	}
	args, _ := json.Marshal(webDownloadArgs{URL: "https://assets.example.test/file.gif", Path: "funny.gif"})
	result, err := turn.Wrap(spec).Fn(context.Background(), args)
	if err != nil || !errors.Is(result.Err, sandbox.ErrEscape) || *calls != 0 || !invoked {
		t.Fatalf("workspace changed after checkpoint admission: err=%v result=%+v calls=%d invoked=%v", err, result, *calls, invoked)
	}
	if record, err := turn.Complete(context.Background()); err != nil || record != nil {
		t.Fatalf("rejected redirected download fabricated history: record=%+v err=%v", record, err)
	}
	for _, workspace := range []string{originalWorkspace, replacementWorkspace} {
		if _, err := os.Lstat(filepath.Join(workspace, "funny.gif")); !os.IsNotExist(err) {
			t.Fatalf("redirected download created a file: workspace=%q err=%v", workspace, err)
		}
	}
}

func TestWebDownloadCheckpointActualExportsAndWorkspaceUndo(t *testing.T) {
	prior := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(prior) })
	for _, useController := range []bool{false, true} {
		name := "turn"
		if useController {
			name = "controller"
		}
		t.Run(name, func(t *testing.T) {
			tool, calls := downloadFixture(t, io.NopCloser(strings.NewReader("GIF89a-integration")), "image/gif", -1, http.StatusOK)
			manager, err := checkpoint.Open(tool.BaseDir, t.TempDir())
			if errors.Is(err, checkpoint.ErrUnavailable) {
				t.Skip(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			turn := manager.NewTurn("download-test", "download to requested folder")
			spec := turn.Wrap(tool.Spec())
			complete := turn.Complete
			if useController {
				controller := checkpoint.NewController(manager, "download-test")
				controller.Start("download to requested folder")
				spec = controller.Wrap(tool.Spec())
				complete = controller.Complete
			}
			downloads := filepath.Join(t.TempDir(), "Downloads")
			destination := filepath.Join(downloads, "cowboys.gif")
			args, _ := json.Marshal(webDownloadArgs{URL: "https://assets.example.test/file.gif", Path: destination})
			result, err := spec.Fn(context.Background(), args)
			if err != nil || !errors.Is(result.Err, sandbox.ErrEscape) || *calls != 0 {
				t.Fatalf("checkpoint accepted ungranted external download: err=%v result=%+v calls=%d", err, result, *calls)
			}
			ctx := sandbox.WithDownloadExportDirs(context.Background(), downloads)
			result, err = spec.Fn(ctx, args)
			if err != nil || result.Err != nil || *calls != 1 {
				t.Fatalf("actual checkpoint + export download failed: err=%v result=%+v calls=%d", err, result, *calls)
			}
			record, err := complete(ctx)
			if err != nil || record != nil || manager.Latest("download-test") != nil {
				t.Fatalf("external export fabricated workspace history: record=%+v err=%v", record, err)
			}
			saved, err := os.ReadFile(destination)
			if err != nil || string(saved) != "GIF89a-integration" {
				t.Fatalf("wrapped actual download did not save expected bytes: %q %v", saved, err)
			}
		})
	}
	// A workspace file still gets the ordinary checkpoint and exact undo/redo.
	tool, _ := downloadFixture(t, io.NopCloser(strings.NewReader("GIF89a-workspace")), "image/gif", -1, http.StatusOK)
	manager, err := checkpoint.Open(tool.BaseDir, t.TempDir())
	if errors.Is(err, checkpoint.ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	turn := manager.NewTurn("workspace-download", "download workspace file")
	args, _ := json.Marshal(webDownloadArgs{URL: "https://assets.example.test/file.gif", Path: "gifs/funny.gif"})
	result, err := turn.Wrap(tool.Spec()).Fn(context.Background(), args)
	if err != nil || result.Err != nil {
		t.Fatalf("wrapped workspace download failed: err=%v result=%+v", err, result)
	}
	record, err := turn.Complete(context.Background())
	if err != nil || record == nil || len(record.Files) != 1 || record.Files[0] != "gifs/funny.gif" {
		t.Fatalf("workspace download checkpoint: record=%+v err=%v", record, err)
	}
	if _, err := manager.Undo(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(tool.BaseDir, "gifs", "funny.gif")
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("undo retained new download: %v", err)
	}
	if _, err := manager.Redo(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(destination)
	if err != nil || string(saved) != "GIF89a-workspace" {
		t.Fatalf("redo did not restore exact downloaded bytes: %q %v", saved, err)
	}
}
