package webgui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildDirectAttachmentImagesNormalizesLargeScreenshot(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "large-screenshot.png")
	source := image.NewRGBA(image.Rect(0, 0, 2200, 1400))
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 80, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	images, err := buildDirectAttachmentImages(home, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0].MediaType != "image/jpeg" {
		t.Fatalf("normalized images = %+v", images)
	}
	data, err := base64.StdEncoding.DecodeString(images[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if config.Width > directImageMaxDimension || config.Height > directImageMaxDimension {
		t.Fatalf("normalized size = %dx%d, max dimension %d", config.Width, config.Height, directImageMaxDimension)
	}
	if len(data) > directImageMaxBytes {
		t.Fatalf("normalized bytes = %d, want <= %d", len(data), directImageMaxBytes)
	}
}

func TestBuildDirectAttachmentImagesIgnoresNonImage(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	images, err := buildDirectAttachmentImages(home, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("non-image became direct input: %+v", images)
	}
}
