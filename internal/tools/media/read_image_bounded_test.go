package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type cancelImageReader struct {
	source *bytes.Reader
	cancel context.CancelFunc
	reads  int
}

func (r *cancelImageReader) Read(p []byte) (int, error) {
	r.reads++
	n, err := r.source.Read(p)
	if r.reads == 2 {
		r.cancel()
	}
	return n, err
}
func TestReadBoundedImageRejectsGrowthShrinkAndCancellation(t *testing.T) {
	data := append(append([]byte(nil), pngHeader...), make([]byte, 2*1024*1024)...)
	for _, size := range []int64{int64(len(data) - 1), int64(len(data) + 1)} {
		if _, _, err := readBoundedImage(context.Background(), bytes.NewReader(data), size); err == nil {
			t.Fatal("accepted changed file")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &cancelImageReader{source: bytes.NewReader(data), cancel: cancel}
	if _, _, err := readBoundedImage(ctx, reader, int64(len(data))); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}
func TestReadBoundedImageRejectsHeaderBeforePayload(t *testing.T) {
	reader := bytes.NewReader(bytes.Repeat([]byte("X"), 1<<20))
	if _, _, err := readBoundedImage(context.Background(), reader, 1<<20); err == nil {
		t.Fatal("invalid image accepted")
	}
	pos, _ := reader.Seek(0, io.SeekCurrent)
	if pos != 512 {
		t.Fatalf("read %d bytes of rejected file", pos)
	}
}
func BenchmarkReadImagePayload(b *testing.B) {
	for _, valid := range []bool{true, false} {
		name := "invalid-8MiB"
		if valid {
			name = "png-1920x1080-original"
		}
		for _, baseline := range []bool{true, false} {
			arm := "optimized"
			if baseline {
				arm = "baseline"
			}
			b.Run(name+"/"+arm, func(b *testing.B) {
				dir := b.TempDir()
				data := bytes.Repeat([]byte("X"), 8<<20)
				if valid {
					img := image.NewNRGBA(image.Rect(0, 0, 1920, 1080))
					seed := uint32(1)
					for i := 0; i < len(img.Pix); i += 4 {
						for j := 0; j < 3; j++ {
							seed = 1664525*seed + 1013904223
							img.Pix[i+j] = byte(seed >> 24)
						}
						img.Pix[i+3] = 255
					}
					var encoded bytes.Buffer
					if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&encoded, img); err != nil {
						b.Fatal(err)
					}
					data = encoded.Bytes()
					if _, err := png.Decode(bytes.NewReader(data)); err != nil {
						b.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(dir, "input.png"), data, 0600); err != nil {
					b.Fatal(err)
				}
				data = nil
				tool := NewReadImage(dir, 0)
				args := json.RawMessage(`{"path":"input.png","image_detail":"original"}`)
				execute := tool.Execute
				if baseline {
					execute = tool.executeBaseline
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					res, err := execute(context.Background(), args)
					if valid && (err != nil || res.Image == nil) {
						b.Fatal(err)
					}
					if !valid && err == nil {
						b.Fatal("accepted invalid image")
					}
				}
			})
		}
	}
}
