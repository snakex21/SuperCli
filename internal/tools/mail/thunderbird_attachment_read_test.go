package mail

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func registerReadAttachment(tb testing.TB, item thunderbirdDownloadedAttachment) json.RawMessage {
	tb.Helper()
	id := fmt.Sprintf("att-read-%p", tb)
	globalThunderbirdBridge.mu.Lock()
	globalThunderbirdBridge.downloads[id] = item
	globalThunderbirdBridge.mu.Unlock()
	tb.Cleanup(func() {
		globalThunderbirdBridge.mu.Lock()
		delete(globalThunderbirdBridge.downloads, id)
		globalThunderbirdBridge.mu.Unlock()
	})
	raw, err := json.Marshal(map[string]any{"transferId": id, "partName": "1.2", "message": map[string]any{"id": 123}})
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}

func TestThunderbirdAttachmentReadPreservesFullResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix []byte
		size   int
		mime   string
	}{
		{"pdf.png", []byte("%PDF-1.7...."), 1 << 20, ""},
		{"txt.jpg", []byte("ordinary text"), 128 << 10, ""},
		{"zip.webp", []byte("PK\x03\x04........"), 1 << 20, ""},
		{"png.bin", []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10, 0, 0, 0, 0}, 128 << 10, "image/png"},
		{"jpeg.txt", []byte{0xff, 0xd8, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0}, 1024, "image/jpeg"},
		{"gif.pdf", []byte("GIF89a......"), 1024, "image/gif"},
		{"webp.zip", []byte("RIFF....WEBP"), 1024, "image/webp"},
		{"short.png", []byte("GIF89a"), 6, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := bytes.Repeat([]byte{0x5a}, tc.size)
			copy(body, tc.prefix)
			p := filepath.Join(t.TempDir(), tc.name)
			if err := os.WriteFile(p, body, 0600); err != nil {
				t.Fatal(err)
			}
			item := thunderbirdDownloadedAttachment{Path: p, Name: tc.name, ContentType: "application/octet-stream", Size: int64(len(body)), CreatedAt: time.Now()}
			args := registerReadAttachment(t, item)
			got, err := NewThunderbirdMail().attachmentResult(args)
			if err != nil || got.Err != nil {
				t.Fatalf("unexpected error: %v %v", err, got.Err)
			}
			contentType := item.ContentType
			if tc.mime != "" {
				contentType = tc.mime
				if got.Image == nil || got.Image.MediaType != tc.mime || !bytes.Equal(got.Image.Data, body) {
					t.Fatal("full image bytes or MIME changed")
				}
			} else if got.Image != nil {
				t.Fatal("non-image attached as vision")
			}
			expected, err := json.MarshalIndent(map[string]any{"filename": tc.name, "extension": strings.TrimPrefix(filepath.Ext(tc.name), "."), "contentType": contentType, "size": item.Size, "partName": "1.2", "localPath": p, "message": map[string]any{"id": 123}, "visionAttached": tc.mime != ""}, "", "  ")
			if err != nil || string(expected) != got.Text {
				t.Fatalf("result contract changed: %v\n%s", err, got.Text)
			}
		})
	}
}

func TestThunderbirdAttachmentReadUnavailableAndVisionGate(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		size                       int64
		remove, expired, directory bool
	}{
		{name: "missing", size: 12, remove: true},
		{name: "expired", size: 12, expired: true},
		{name: "directory", size: 12, directory: true},
		{name: "zero", size: 0},
		{name: "above-limit", size: maxThunderbirdVisionBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "image.png")
			if tc.directory {
				p = dir
			} else if !tc.remove {
				if err := os.WriteFile(p, []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10, 0, 0, 0, 0}, 0600); err != nil {
					t.Fatal(err)
				}
			}
			when := time.Now()
			if tc.expired {
				when = when.Add(-31 * time.Minute)
			}
			args := registerReadAttachment(t, thunderbirdDownloadedAttachment{Path: p, Name: "image.png", ContentType: "image/png", Size: tc.size, CreatedAt: when})
			got, err := NewThunderbirdMail().attachmentResult(args)
			if err != nil || got.Image != nil {
				t.Fatalf("unexpected image/error %v %v", got.Image, err)
			}
			if tc.expired {
				if got.Err == nil {
					t.Fatal("expired transfer accepted")
				}
			} else if got.Err != nil || !strings.Contains(got.Text, "\"visionAttached\": false") {
				t.Fatalf("metadata unavailable: %v %s", got.Err, got.Text)
			}
		})
	}
}

func BenchmarkThunderbirdAttachmentResult(b *testing.B) {
	for _, tc := range []struct {
		name  string
		size  int
		image bool
	}{
		{"PDF-128KiB", 128 << 10, false}, {"PDF-1MiB", 1 << 20, false}, {"PDF-8MiB", 8 << 20, false}, {"PDF-10MiB", 10 << 20, false},
		{"PNG-12B", 12, true}, {"PNG-128KiB", 128 << 10, true}, {"PNG-8MiB", 8 << 20, true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			body := bytes.Repeat([]byte{0x5a}, tc.size)
			copy(body, []byte("%PDF-1.7...."))
			if tc.image {
				copy(body, []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10, 0, 0, 0, 0})
			}
			p := filepath.Join(b.TempDir(), "attachment.bin")
			if err := os.WriteFile(p, body, 0600); err != nil {
				b.Fatal(err)
			}
			args := registerReadAttachment(b, thunderbirdDownloadedAttachment{Path: p, Name: "attachment.bin", ContentType: "application/octet-stream", Size: int64(len(body)), CreatedAt: time.Now()})
			tool := NewThunderbirdMail()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := tool.attachmentResult(args)
				if err != nil || got.Err != nil {
					b.Fatalf("result error %v %v", err, got.Err)
				}
				if tc.image {
					if got.Image == nil || len(got.Image.Data) != len(body) || got.Image.Data[len(body)-1] != body[len(body)-1] {
						b.Fatal("lost image")
					}
				} else if got.Image != nil {
					b.Fatal("PDF vision changed")
				}
			}
		})
	}
}

// Count bytes delivered by the real reader used by the production helper.
// Fixture allocation is outside the helper; this distinguishes prefix-only
// recognition from reading and discarding an entire document.
type thunderbirdCountingReader struct {
	reader       io.Reader
	bytes, calls int
}

func (r *thunderbirdCountingReader) Read(p []byte) (int, error) {
	n, e := r.reader.Read(p)
	r.bytes += n
	r.calls++
	return n, e
}

type thunderbirdReadFailure struct{ prefix []byte }

func (r *thunderbirdReadFailure) Read(p []byte) (int, error) {
	if len(r.prefix) == 0 {
		return 0, errors.New("fixture read failure")
	}
	n := copy(p, r.prefix)
	r.prefix = r.prefix[n:]
	return n, nil
}

type thunderbirdNoProgressReader struct{ calls int }

func (r *thunderbirdNoProgressReader) Read(p []byte) (int, error) { r.calls++; return 0, nil }

func TestThunderbirdVisionReadsOnlyDocumentPrefix(t *testing.T) {
	for _, prefix := range []string{"%PDF-1.7....", "PK\x03\x04........", "ordinary text"} {
		r := &thunderbirdCountingReader{reader: io.MultiReader(strings.NewReader(prefix), strings.NewReader(strings.Repeat("x", 8<<20)))}
		if got := readThunderbirdVisionData(r, 8<<20); got != nil {
			t.Fatal("document interpreted as image")
		}
		if r.bytes != 12 {
			t.Fatalf("read %d document bytes, want 12", r.bytes)
		}
	}
}

func TestThunderbirdVisionReaderMutationAndErrorBounds(t *testing.T) {
	signature := []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10, 0, 0, 0, 0}
	t.Run("growth-within-limit", func(t *testing.T) {
		actual := append(append([]byte{}, signature...), bytes.Repeat([]byte{0x5a}, 2048)...)
		r := &thunderbirdCountingReader{reader: bytes.NewReader(actual)}
		got := readThunderbirdVisionData(r, 12)
		if got == nil || got.MediaType != "image/png" || !bytes.Equal(got.Data, actual) || r.bytes != len(actual) {
			t.Fatal("grown image bytes lost")
		}
	})
	t.Run("growth-over-limit", func(t *testing.T) {
		r := &thunderbirdCountingReader{reader: io.MultiReader(bytes.NewReader(signature), strings.NewReader(strings.Repeat("x", int(maxThunderbirdVisionBytes))))}
		if got := readThunderbirdVisionData(r, 12); got != nil {
			t.Fatal("oversized grown image attached")
		}
		if r.bytes != int(maxThunderbirdVisionBytes)+1 {
			t.Fatalf("read %d bytes, want cap+1", r.bytes)
		}
	})
	for _, n := range []int{0, 6, 12} {
		t.Run(fmt.Sprintf("read-error-after-%d", n), func(t *testing.T) {
			if got := readThunderbirdVisionData(&thunderbirdReadFailure{prefix: signature[:n]}, 12); got != nil {
				t.Fatal("read error attached partial image")
			}
		})
	}
	t.Run("no-progress-after-signature", func(t *testing.T) {
		stuck := &thunderbirdNoProgressReader{}
		if got := readThunderbirdVisionData(io.MultiReader(bytes.NewReader(signature), stuck), 12); got != nil || stuck.calls != 100 {
			t.Fatalf("reader did not terminate: result=%v calls=%d", got, stuck.calls)
		}
	})
}

func TestThunderbirdVisionRejectsStaleOversizedFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "stale.png")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10, 0, 0, 0, 0}); err == nil {
		err = f.Truncate(maxThunderbirdVisionBytes + 1)
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("fixture error %v %v", err, closeErr)
	}
	args := registerReadAttachment(t, thunderbirdDownloadedAttachment{Path: p, Name: "stale.png", ContentType: "image/png", Size: 12, CreatedAt: time.Now()})
	got, err := NewThunderbirdMail().attachmentResult(args)
	if err != nil || got.Err != nil || got.Image != nil || !strings.Contains(got.Text, "\"visionAttached\": false") {
		t.Fatalf("stale size bypassed image limit: err=%v resultErr=%v image=%v", err, got.Err, got.Image != nil)
	}
}

type thunderbirdDataAndErrorReader struct {
	data []byte
	err  error
}

func (r *thunderbirdDataAndErrorReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

func TestThunderbirdVisionReaderDataWithEOFOrFailure(t *testing.T) {
	actual := append([]byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10, 0, 0, 0, 0}, bytes.Repeat([]byte{0x5a}, 50)...)
	got := readThunderbirdVisionData(&thunderbirdDataAndErrorReader{data: actual, err: io.EOF}, int64(len(actual)))
	if got == nil || !bytes.Equal(got.Data, actual) {
		t.Fatal("reader data with EOF lost")
	}
	got = readThunderbirdVisionData(&thunderbirdDataAndErrorReader{data: actual, err: errors.New("fixture partial failure")}, int64(len(actual)))
	if got != nil {
		t.Fatal("reader data with error attached partial image")
	}
}
