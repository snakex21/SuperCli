package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/sandbox"
)

func TestWebDownloadExactExportFilePreservesExistingAndRejectsSiblingsBeforeHTTP(t *testing.T) {
	prior := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(prior) })
	body := append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 100)...)
	tool, calls := downloadFixture(t, io.NopCloser(bytes.NewReader(body)), "image/gif", int64(len(body)), http.StatusOK)
	destination := filepath.Join(t.TempDir(), "new-parent", "chosen.gif")
	ctx := sandbox.WithDownloadExportTargets(context.Background(), nil, []string{destination})
	result := invokeDownload(t, tool, ctx, destination, 0)
	if result.Err != nil || *calls != 1 {
		t.Fatalf("exact export failed: %+v calls=%d", result, *calls)
	}
	saved, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(saved, body) {
		t.Fatalf("exact filename did not receive the bytes: %v", err)
	}
	for _, path := range []string{destination, filepath.Join(filepath.Dir(destination), "sibling.gif"), filepath.Join(destination, "child.gif")} {
		again := invokeDownload(t, tool, ctx, path, 0)
		if again.Err == nil || *calls != 1 {
			t.Fatalf("unrequested/repeated target reached HTTP: %+v calls=%d", again, *calls)
		}
		if path == destination && !strings.Contains(again.Err.Error(), "destination_exists") {
			t.Fatalf("existing exact file must report no-overwrite: %v", again.Err)
		}
	}
	saved, err = os.ReadFile(destination)
	if err != nil || !bytes.Equal(saved, body) {
		t.Fatalf("existing exact bytes changed: %v", err)
	}
}
