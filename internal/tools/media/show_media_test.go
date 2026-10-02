package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestShowMediaMetadataWithoutModelPixels(t *testing.T) {
	dir := t.TempDir()
	tool := NewShowMedia(dir)
	cases := []struct {
		name, kind string
		header     []byte
	}{
		{"image.png", "image", pngHeader},
		{"video.mp4", "video", []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom")},
		{"sound.wav", "audio", []byte("RIFF\x24\x00\x00\x00WAVEfmt ")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, c.name)
			if err := os.WriteFile(path, c.header, 0600); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]string{"path": c.name})
			res, err := tool.Execute(context.Background(), args)
			if err != nil || res.Err != nil || res.Image != nil {
				t.Fatalf("res=%+v err=%v", res, err)
			}
			var metadata screenshotMetadata
			if err := json.Unmarshal([]byte(res.Text), &metadata); err != nil {
				t.Fatal(err)
			}
			if metadata.Type != c.kind || metadata.Path != path || metadata.Bytes != len(c.header) || metadata.Attached {
				t.Fatalf("metadata=%+v", metadata)
			}
			data, _ := os.ReadFile(path)
			if !bytes.Equal(data, c.header) {
				t.Fatal("preview changed source")
			}
		})
	}
}

func TestShowMediaErrorsAndBoundedFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewShowMedia(dir)
	if err := os.WriteFile(filepath.Join(dir, "text.png"), []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{}`, `{"path":"missing.png"}`, `{"path":"."}`, `{"path":"text.png"}`, `[1]`} {
		res, err := tool.Execute(context.Background(), json.RawMessage(args))
		if err == nil && res.Err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
	f, err := os.Create(filepath.Join(dir, "large.png"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(pngHeader); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxMediaPreviewBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"large.png"}`))
	if err != nil || res.Err == nil {
		t.Fatalf("oversized preview=%+v %v", res, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err = tool.Execute(ctx, json.RawMessage(`{"path":"text.png"}`))
	if !errors.Is(err, context.Canceled) || !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("cancel=%+v %v", res, err)
	}
}

func BenchmarkShowMediaMetadata(b *testing.B) {
	dir := b.TempDir()
	tool := NewShowMedia(dir)
	f, err := os.Create(filepath.Join(dir, "large.png"))
	if err != nil {
		b.Fatal(err)
	}
	f.Write(pngHeader)
	f.Truncate(8 << 20)
	f.Close()
	args := json.RawMessage(`{"path":"large.png"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, err := tool.Execute(context.Background(), args)
		if err != nil || res.Err != nil {
			b.Fatalf("%+v %v", res, err)
		}
	}
}
