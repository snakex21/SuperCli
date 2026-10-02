package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"path/filepath"
	"strings"
)

// PreviewCapturedImage is shared by native and headless adapters. The UI sees
// original pixels through the normal authorized snapshot route; the provider
// receives no image unless explicitly requested, then the same analysis budget.
func PreviewCapturedImage(ctx context.Context, dataDir, source string, data []byte, attach bool, detail string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(dataDir) == "" {
		return Result{}, fmt.Errorf("portable data directory is required")
	}
	if len(data) > DefaultMaxScreenshotBytes {
		return Result{}, fmt.Errorf("captured image exceeds 16 MiB")
	}
	if detail != "" && detail != "auto" && detail != "original" {
		return Result{}, fmt.Errorf("image_detail must be auto or original")
	}
	mime := sniffMediaType(data)
	if mime != "image/png" && mime != "image/jpeg" {
		return Result{}, fmt.Errorf("headless capture must be PNG or JPEG")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 32<<20 {
		return Result{}, fmt.Errorf("invalid headless image or dimensions exceed 32 megapixels")
	}
	var pixels []byte
	var pixelType string
	var analysis *AnalysisImageInfo
	if attach {
		var info AnalysisImageInfo
		pixels, pixelType, info, err = prepareAnalysisImage(ctx, data, mime, detail)
		if err != nil {
			return Result{}, err
		}
		analysis = &info
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	tool := NewSendScreenshot(dataDir, nil)
	path, err := tool.saveSnapshot(source, data, mime)
	if err != nil {
		return Result{}, err
	}
	metadata := struct {
		Type        string             `json:"type"`
		Source      string             `json:"source"`
		Path        string             `json:"path"`
		PreviewPath string             `json:"preview_path"`
		MediaType   string             `json:"media_type"`
		Bytes       int                `json:"bytes"`
		Attached    bool               `json:"attached"`
		Analysis    *AnalysisImageInfo `json:"analysis,omitempty"`
	}{"image", source, path, "snapshot:" + filepath.Base(path), mime, len(data), attach, analysis}
	text, err := json.Marshal(metadata)
	if err != nil {
		return Result{}, err
	}
	result := Result{Text: string(text)}
	if attach {
		result.Image = &ImageContent{MediaType: pixelType, Data: pixels}
	}
	return result, nil
}
