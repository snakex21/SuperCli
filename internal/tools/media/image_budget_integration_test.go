package media

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func budgetFixture(t *testing.T) []byte {
	t.Helper()
	frame := image.NewNRGBA(image.Rect(0, 0, 1400, 700))
	for y := 0; y < 700; y++ {
		for x := 0; x < 1400; x++ {
			c := color.NRGBA{R: 220, A: 255}
			if x >= 700 {
				c = color.NRGBA{G: 180, A: 255}
			}
			frame.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, frame); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestScreenshotAnalysisIsBoundedButSavedOriginalIsExact(t *testing.T) {
	raw := budgetFixture(t)
	for _, detail := range []string{"auto", "original"} {
		t.Run(detail, func(t *testing.T) {
			tool := NewSendScreenshot(t.TempDir(), nil)
			tool.ScreenCapture = func(context.Context) ([]byte, string, error) { return raw, "image/png", nil }
			args, _ := json.Marshal(map[string]any{"source": "screen", "attach": true, "image_detail": detail})
			res, err := tool.Execute(context.Background(), args)
			if err != nil || res.Image == nil {
				t.Fatalf("result=%+v error=%v", res, err)
			}
			var meta struct {
				Path     string
				Analysis AnalysisImageInfo
			}
			if err := json.Unmarshal([]byte(res.Text), &meta); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(meta.Path)
			if err != nil || !bytes.Equal(saved, raw) {
				t.Fatal("saved original altered", err)
			}
			cfg, err := png.DecodeConfig(bytes.NewReader(res.Image.Data))
			if err != nil {
				t.Fatal(err)
			}
			if detail == "auto" && (cfg.Width > 1280 || cfg.Height > 1280 || !meta.Analysis.Resized) {
				t.Fatalf("unbounded config=%+v metadata=%+v", cfg, meta.Analysis)
			}
			if detail == "original" && (!bytes.Equal(res.Image.Data, raw) || meta.Analysis.Resized) {
				t.Fatal("explicit original not preserved")
			}
		})
	}
	tool := NewSendScreenshot(t.TempDir(), nil)
	called := false
	tool.ScreenCapture = func(context.Context) ([]byte, string, error) { called = true; return raw, "image/png", nil }
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"source":"screen","image_detail":"invalid"}`))
	if err == nil || res.Err == nil || called {
		t.Fatalf("invalid detail captured screen: %+v %v %v", res, err, called)
	}
}

func TestReadImageBudgetAndPreciseRegionPreserveFile(t *testing.T) {
	raw := budgetFixture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "screen.png")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadImage(dir, 0)
	reg := NewRegistry()
	reg.MustRegister(tool.Spec())
	res, err := reg.Execute(context.Background(), "read_image", json.RawMessage(`{"path":"screen.png"}`))
	if err != nil || res.Image == nil {
		t.Fatalf("result=%+v error=%v", res, err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(res.Image.Data))
	if err != nil || cfg.Width > 1280 {
		t.Fatalf("config=%+v error=%v", cfg, err)
	}
	res, err = reg.Execute(context.Background(), "read_image", json.RawMessage(`{"path":"screen.png","crop":{"x":700,"y":10,"width":100,"height":80},"image_detail":"original"}`))
	if err != nil || res.Image == nil {
		t.Fatalf("result=%+v error=%v", res, err)
	}
	crop, err := png.Decode(bytes.NewReader(res.Image.Data))
	if err != nil {
		t.Fatal(err)
	}
	if crop.Bounds().Dx() != 100 || crop.Bounds().Dy() != 80 {
		t.Fatalf("crop bounds=%v", crop.Bounds())
	}
	if c := color.NRGBAModel.Convert(crop.At(0, 0)).(color.NRGBA); c != (color.NRGBA{G: 180, A: 255}) {
		t.Fatalf("wrong original-coordinate crop=%v", c)
	}
	saved, _ := os.ReadFile(path)
	if !bytes.Equal(saved, raw) {
		t.Fatal("read_image altered original file")
	}
	res, err = reg.Execute(context.Background(), "read_image", json.RawMessage(`{"path":"screen.png","crop":{"x":1390,"y":0,"width":30,"height":10}}`))
	if err == nil || res.Err == nil || res.Image != nil {
		t.Fatalf("invalid crop accepted=%+v %v", res, err)
	}
}
