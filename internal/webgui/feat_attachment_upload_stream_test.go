package webgui

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func uploadFixture(t testing.TB, sizes ...int) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, size := range sizes {
		part, err := w.CreateFormFile("files", "image.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(bytes.Repeat([]byte("x"), size)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), w.FormDataContentType()
}

func TestAttachmentUploadStreamsLargeFile(t *testing.T) {
	srv := newTestServer(t, false)
	// A spill into OS temp must fail rather than go unnoticed in this test.
	blockedTemp := filepath.Join(srv.eng.Home(), "not-a-temp-directory")
	if err := os.WriteFile(blockedTemp, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, blockedTemp)
	}
	body, contentType := uploadFixture(t, 9<<20)
	req := httptest.NewRequest(http.MethodPost, "/api/attachment/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	srv.handleAttachmentUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
	}
	if req.MultipartForm != nil && (len(req.MultipartForm.File) != 0 || len(req.MultipartForm.Value) != 0) {
		t.Fatal("upload buffered a multipart form instead of streaming the file")
	}
	var result struct{ Paths []string }
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || len(result.Paths) != 1 {
		t.Fatalf("paths=%v error=%v", result.Paths, err)
	}
	got, err := os.ReadFile(result.Paths[0])
	if err != nil || sha256.Sum256(got) != sha256.Sum256(bytes.Repeat([]byte("x"), 9<<20)) {
		t.Fatalf("uploaded bytes differ: %v", err)
	}
}

func TestAttachmentUploadCleansPartialFiles(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sizes    []int
		truncate bool
	}{
		{name: "too many files", sizes: make([]int, maxChatAttachments+1)},
		{name: "oversized file", sizes: []int{int(maxChatAttachmentBytes) + 1}},
		{name: "broken body", sizes: []int{1024}, truncate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, false)
			body, contentType := uploadFixture(t, tc.sizes...)
			if tc.truncate {
				body = body[:len(body)-100]
			}
			req := httptest.NewRequest(http.MethodPost, "/api/attachment/upload", bytes.NewReader(body))
			req.Header.Set("Content-Type", contentType)
			rec := httptest.NewRecorder()
			srv.handleAttachmentUpload(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			entries, err := os.ReadDir(filepath.Join(srv.eng.Home(), ".supercli", "attachments"))
			if err != nil && !os.IsNotExist(err) || len(entries) != 0 {
				t.Fatalf("partial uploads remain: %v error=%v", entries, err)
			}
		})
	}
}

func BenchmarkAttachmentUpload9MiB(b *testing.B) {
	body, contentType := uploadFixture(b, 9<<20)
	srv := &Server{eng: &Engine{home: b.TempDir(), dataDir: b.TempDir()}}
	b.ReportAllocs()
	b.SetBytes(9 << 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/attachment/upload", bytes.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		srv.handleAttachmentUpload(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		b.StopTimer()
		var result struct{ Paths []string }
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || len(result.Paths) != 1 {
			b.Fatalf("paths=%v error=%v", result.Paths, err)
		}
		if err := os.RemoveAll(filepath.Dir(result.Paths[0])); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}
