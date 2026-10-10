package webgui

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// Isolate staging for a 5 MiB DOCX upload. Parsing the document and encoding
// its text response are common to both paths and intentionally excluded.
func BenchmarkDocumentUploadStaging(b *testing.B) {
	root := b.TempDir()
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		b.Setenv(key, root)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "large.docx")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), 5<<20)); err != nil {
		b.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		b.Fatal(err)
	}
	contentType, raw := mw.FormDataContentType(), body.Bytes()
	for _, method := range []string{"previous-two-copies", "portable-stream"} {
		b.Run(method, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(5 << 20)
			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest(http.MethodPost, "/api/document/import", bytes.NewReader(raw))
				req.Header.Set("Content-Type", contentType)
				req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, maxLocalDocumentImportBytes+(1<<20))
				if method == "portable-stream" {
					upload, cleanup, err := stageMultipartUpload(req, root, "bench-*", maxLocalDocumentImportBytes, "file", "document", "files")
					if err != nil {
						b.Fatal(err)
					}
					if upload.Size != 5<<20 {
						b.Fatalf("size=%d", upload.Size)
					}
					cleanup()
					continue
				}
				if err := req.ParseMultipartForm(4 << 20); err != nil {
					b.Fatal(err)
				}
				source, _, err := req.FormFile("file")
				if err != nil {
					req.MultipartForm.RemoveAll()
					b.Fatal(err)
				}
				tmp, err := os.CreateTemp(root, "old-docx-*")
				if err != nil {
					source.Close()
					req.MultipartForm.RemoveAll()
					b.Fatal(err)
				}
				written, copyErr := io.Copy(tmp, io.LimitReader(source, maxLocalDocumentImportBytes+1))
				closeErr := tmp.Close()
				source.Close()
				os.Remove(tmp.Name())
				req.MultipartForm.RemoveAll()
				if copyErr != nil || closeErr != nil || written != 5<<20 {
					b.Fatalf("written=%d copy=%v close=%v", written, copyErr, closeErr)
				}
			}
		})
	}
}
