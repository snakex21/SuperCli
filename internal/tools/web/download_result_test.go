package web

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebDownloadResultOffersInspectionWithoutRedownloadingOrHashReads(t *testing.T) {
	var zipBody bytes.Buffer
	zw := zip.NewWriter(&zipBody)
	entry, err := zw.Create("asset.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("asset")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 32)...)
	for _, test := range []struct {
		name, path, contentType, reader string
		body                            []byte
	}{
		{"png", "asset.png", "image/png", "read_image", png},
		{"extensionless-png", "asset", "application/octet-stream", "read_image", png},
		{"pdf", "asset.pdf", "application/pdf", "read_pdf", []byte("%PDF-1.7\nasset")},
		{"zip", "asset.zip", "application/zip", "read_zip", zipBody.Bytes()},
		{"docx", "asset.docx", "application/octet-stream", "read_docx", zipBody.Bytes()},
		{"text-model", "asset.gltf", "application/json", "", []byte(`{"asset":{"version":"2.0"}}`)},
		{"svg", "asset.svg", "image/svg+xml", "", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
		{"mislabeled-text", "asset.png", "image/png", "", []byte("plain text despite the header and filename")},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := &observedDownloadReader{Reader: bytes.NewReader(test.body), t: t}
			tool, calls := downloadFixture(t, stream, test.contentType, int64(len(test.body)), http.StatusOK)
			stream.destination = filepath.Join(tool.BaseDir, test.path)
			result := invokeDownload(t, tool, context.Background(), test.path, 0)
			if result.Err != nil || *calls != 1 || !stream.closed || !strings.Contains(result.Text, fmt.Sprintf("SHA256: %x", sha256.Sum256(test.body))) {
				t.Fatalf("result lost completed stream metadata: %+v calls=%d", result, *calls)
			}
			if test.reader != "" {
				if !strings.Contains(result.Text, "Inspect only if needed with "+test.reader) || !strings.Contains(result.Text, "binary data is not text lines") {
					t.Fatalf("binary inspection has wrong route: %s", result.Text)
				}
			} else if strings.Contains(result.Text, "Inspect") || strings.Contains(result.Text, "binary") {
				t.Fatalf("textual asset was incorrectly routed as binary: %s", result.Text)
			}
		})
	}
}

func TestWebDownloadExistingFileIsConflictForSameOrDifferentSource(t *testing.T) {
	tool, calls := downloadFixture(t, io.NopCloser(strings.NewReader("unexpected replacement")), "text/plain", -1, http.StatusOK)
	path := filepath.Join(tool.BaseDir, "asset.png")
	original := []byte("existing file with unrelated content")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rawURL := range []string{"https://assets.example.test/image?token=secret", "https://other.example.test/different?token=secret"} {
		args, _ := json.Marshal(webDownloadArgs{URL: rawURL, Path: "asset.png"})
		result, err := tool.Spec().Fn(context.Background(), args)
		if err != nil || result.Err == nil || result.Text != "" || result.Inert || *calls != 0 || strings.Contains(result.Err.Error(), "secret") {
			t.Fatalf("conflicting existing file treated as download success: %+v %v calls=%d", result, err, *calls)
		}
		if !strings.Contains(result.Err.Error(), "destination_exists") || !strings.Contains(result.Err.Error(), "no HTTP request made") || !strings.Contains(result.Err.Error(), "Retrying unchanged arguments cannot replace it") {
			t.Fatalf("conflict invites identical retry: %v", result.Err)
		}
	}
	saved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(saved, original) {
		t.Fatalf("conflicting existing file changed: %q %v", saved, err)
	}
}

func TestWebDownloadInspectionAvoidsUnsupportedImageSize(t *testing.T) {
	if hint := downloadInspectionHint("large.png", "image/png", true, (10<<20)+1); strings.Contains(hint, "read_image") {
		t.Fatalf("reader default size cap would reject suggested inspection: %s", hint)
	}
	for _, path := range []string{"large.docx", "large.xlsx"} {
		if hint := downloadInspectionHint(path, "application/zip", true, (64<<20)+1); strings.Contains(hint, "read_docx") || strings.Contains(hint, "read_xlsx") {
			t.Fatalf("office reader default size cap would reject suggested inspection: %s", hint)
		}
	}
}
