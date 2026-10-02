package webgui

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func writeThumbnailFixture(t testing.TB, dir string, width, height int) (string, []byte) {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x / 16), G: uint8(y / 8), B: 80, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "source.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, encoded.Bytes()
}

func TestAttachmentThumbnailBoundsCacheAndOriginal(t *testing.T) {
	root := t.TempDir()
	path, original := writeThumbnailFixture(t, root, 3840, 2160)
	dataDir := filepath.Join(root, "portable-data")
	s := &Server{eng: &Engine{home: root, dataDir: dataDir}}
	originalURL := "/api/attachment/preview?path=" + url.QueryEscape(path)
	for _, variant := range []string{"transcript", "composer"} {
		requestURL := originalURL + "&thumbnail=" + variant
		first := httptest.NewRecorder()
		s.handleAttachmentPreview(first, httptest.NewRequest(http.MethodGet, requestURL, nil))
		if first.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", variant, first.Code, first.Body.String())
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(first.Body.Bytes()))
		width, height, _ := attachmentThumbnailBounds(variant)
		if err != nil || config.Width > width || config.Height > height || first.Body.Len() > thumbnailMaxBytes {
			t.Fatalf("%s: size=%dx%d bytes=%d error=%v", variant, config.Width, config.Height, first.Body.Len(), err)
		}
		if variant == "transcript" && (config.Width != 780 || config.Height != 438) {
			t.Fatalf("unexpected 4K transcript size: %dx%d", config.Width, config.Height)
		}
		if first.Header().Get("Content-Type") != "image/png" || first.Header().Get("Cache-Control") != "no-store, private" {
			t.Fatal(first.Header())
		}
		// Repeating a request must reuse one disk derivative without rewriting it.
		cacheRoot := filepath.Join(dataDir, ".supercli", "attachment-thumbnails")
		before, _ := os.ReadDir(cacheRoot)
		var newest os.FileInfo
		for _, entry := range before {
			info, _ := entry.Info()
			if newest == nil || info.ModTime().After(newest.ModTime()) {
				newest = info
			}
		}
		second := httptest.NewRecorder()
		s.handleAttachmentPreview(second, httptest.NewRequest(http.MethodGet, requestURL, nil))
		after, _ := os.ReadDir(cacheRoot)
		cached, err := os.Stat(filepath.Join(cacheRoot, newest.Name()))
		if err != nil || len(after) != len(before) || !cached.ModTime().Equal(newest.ModTime()) || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
			t.Fatalf("derivative was not reused: %v", err)
		}
	}
	w := httptest.NewRecorder()
	s.handleAttachmentPreview(w, httptest.NewRequest(http.MethodGet, originalURL, nil))
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), original) {
		t.Fatalf("original preview changed: status=%d", w.Code)
	}
	req := httptest.NewRequest(http.MethodGet, originalURL, nil)
	req.Header.Set("Range", "bytes=0-23")
	w = httptest.NewRecorder()
	s.handleAttachmentPreview(w, req)
	if w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), original[:24]) {
		t.Fatalf("original range changed: status=%d", w.Code)
	}
}

func TestAttachmentThumbnailRejectsOversizedDimensionsBeforeDecode(t *testing.T) {
	root := t.TempDir()
	path, data := writeThumbnailFixture(t, root, 1, 1)
	// Valid PNG IHDR checksum with an enormous claimed pixel buffer. The IDAT
	// still holds one pixel, so allocating before checking dimensions is unsafe.
	binary.BigEndian.PutUint32(data[16:20], 16384)
	binary.BigEndian.PutUint32(data[20:24], 16384)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{eng: &Engine{home: root, dataDir: filepath.Join(root, "data")}}
	w := httptest.NewRecorder()
	s.handleAttachmentPreview(w, httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(path)+"&thumbnail=transcript", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized dimensions status=%d", w.Code)
	}
	entries, _ := os.ReadDir(filepath.Join(s.eng.DataDir(), ".supercli", "attachment-thumbnails"))
	if len(entries) != 0 {
		t.Fatal("rejected image left a derivative or temporary file")
	}
}

func TestAttachmentThumbnailSupportedCodecsAndTransparency(t *testing.T) {
	root := t.TempDir()
	s := &Server{eng: &Engine{home: root, dataDir: filepath.Join(root, "data")}}
	transparent := image.NewNRGBA(image.Rect(0, 0, 1000, 700))
	for y := 0; y < 700; y++ {
		for x := 0; x < 1000; x++ {
			transparent.SetNRGBA(x, y, color.NRGBA{200, 40, 80, 128})
		}
	}
	for _, format := range []string{"png", "jpeg", "gif"} {
		var encoded bytes.Buffer
		var err error
		switch format {
		case "png":
			err = png.Encode(&encoded, transparent)
		case "jpeg":
			err = jpeg.Encode(&encoded, transparent, nil)
		case "gif":
			err = gif.Encode(&encoded, transparent, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "codec."+format)
		if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		s.handleAttachmentPreview(w, httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(path)+"&thumbnail=transcript", nil))
		preview, err := png.Decode(bytes.NewReader(w.Body.Bytes()))
		if w.Code != http.StatusOK || err != nil || preview.Bounds().Dx() > 780 || preview.Bounds().Dy() > 520 {
			t.Fatalf("%s preview status=%d error=%v", format, w.Code, err)
		}
		if format == "png" {
			expected := color.NRGBAModel.Convert(transparent.At(10, 10)).(color.NRGBA)
			actual := color.NRGBAModel.Convert(preview.At(10, 10)).(color.NRGBA)
			delta := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
			if actual.A != expected.A || delta(actual.R, expected.R) > 1 || delta(actual.G, expected.G) > 1 || delta(actual.B, expected.B) > 1 {
				t.Fatalf("thumbnail lost transparency or color: got=%v want=%v", actual, expected)
			}
		}
	}
}

func TestAttachmentThumbnailGenerationSerializesAndCancelsWait(t *testing.T) {
	s := &Server{}
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- s.withAttachmentThumbnailGeneration(context.Background(), func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.withAttachmentThumbnailGeneration(ctx, func() error { t.Error("canceled request decoded an image"); return nil }); err != context.Canceled {
		t.Fatalf("canceled wait returned %v", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	var active, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			if err := s.withAttachmentThumbnailGeneration(context.Background(), func() error {
				value := active.Add(1)
				for old := peak.Load(); value > old && !peak.CompareAndSwap(old, value); old = peak.Load() {
				}
				runtime.Gosched()
				active.Add(-1)
				return nil
			}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if peak.Load() != 1 || active.Load() != 0 || len(s.thumbnailGate) != 0 {
		t.Fatalf("decode concurrency=%d active=%d gate=%d", peak.Load(), active.Load(), len(s.thumbnailGate))
	}
}

func TestAttachmentThumbnailCancellationCleansTemporaryFile(t *testing.T) {
	root := t.TempDir()
	path, _ := writeThumbnailFixture(t, root, 32, 32)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, _ := file.Stat()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = createAttachmentThumbnail(ctx, file, info, root, filepath.Join(root, "target.png"), 100, 96)
	if err == nil {
		t.Fatal("canceled thumbnail generation succeeded")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "source.png" {
		t.Fatal("canceled generation left files behind", entries)
	}
}

func TestAttachmentThumbnailCacheBoundsAndCrashCleanup(t *testing.T) {
	for _, size := range []int64{1, thumbnailMaxBytes} {
		root := t.TempDir()
		for i := 0; i < thumbnailCacheMaxFiles+3; i++ {
			file, err := os.Create(filepath.Join(root, fmtThumbnailName(i)))
			if err != nil {
				t.Fatal(err)
			}
			// Sparse files verify the byte budget without allocating contents.
			if err := file.Truncate(size); err != nil {
				t.Fatal(err)
			}
			file.Close()
		}
		os.WriteFile(filepath.Join(root, ".thumbnail-interrupted.tmp"), []byte("partial"), 0o600)
		if err := pruneAttachmentThumbnails(root); err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(root)
		var total int64
		for _, entry := range entries {
			info, _ := entry.Info()
			total += info.Size()
			if filepath.Ext(entry.Name()) != ".png" {
				t.Fatal("crash temporary file was retained")
			}
		}
		if len(entries) >= thumbnailCacheMaxFiles || total > thumbnailCacheMaxBytes-thumbnailMaxBytes {
			t.Fatalf("cache count=%d bytes=%d", len(entries), total)
		}
	}
}

func TestAttachmentThumbnailConcurrentDuplicateRequestsReuseDerivative(t *testing.T) {
	root := t.TempDir()
	path, _ := writeThumbnailFixture(t, root, 640, 360)
	s := &Server{eng: &Engine{home: root, dataDir: filepath.Join(root, "data")}}
	requestURL := "/api/attachment/preview?path=" + url.QueryEscape(path) + "&thumbnail=transcript"
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			w := httptest.NewRecorder()
			s.handleAttachmentPreview(w, httptest.NewRequest(http.MethodGet, requestURL, nil))
			if w.Code != http.StatusOK {
				t.Errorf("concurrent thumbnail status=%d", w.Code)
			}
		})
	}
	wg.Wait()
	entries, err := os.ReadDir(filepath.Join(s.eng.DataDir(), ".supercli", "attachment-thumbnails"))
	if err != nil || len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".png" {
		t.Fatalf("duplicate requests left %d cache files, error=%v", len(entries), err)
	}
}

func fmtThumbnailName(index int) string {
	return string(rune('a'+index/26)) + string(rune('a'+index%26)) + ".png"
}

func BenchmarkAttachmentThumbnailCold4K(b *testing.B) {
	root := b.TempDir()
	path, _ := writeThumbnailFixture(b, root, 3840, 2160)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		file, err := os.Open(path)
		if err != nil {
			b.Fatal(err)
		}
		info, _ := file.Stat()
		err = createAttachmentThumbnail(context.Background(), file, info, root, filepath.Join(root, "target.png"), 780, 520)
		file.Close()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAttachmentThumbnailCached4K(b *testing.B) {
	root := b.TempDir()
	path, _ := writeThumbnailFixture(b, root, 3840, 2160)
	s := &Server{eng: &Engine{home: root, dataDir: filepath.Join(root, "data")}}
	request := httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(path)+"&thumbnail=transcript", nil)
	w := httptest.NewRecorder()
	s.handleAttachmentPreview(w, request)
	if w.Code != http.StatusOK {
		b.Fatal(w.Code)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w := httptest.NewRecorder()
		s.handleAttachmentPreview(w, request)
		if w.Code != http.StatusOK {
			b.Fatal(w.Code)
		}
	}
}
