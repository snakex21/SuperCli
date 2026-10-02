package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

// ReadImageTool loads an image file from disk and returns it as
// base64 + MIME type for the agent loop to attach to the next
// LLM turn.
//
// Safety:
//
//   - Paths are resolved relative to BaseDir; absolute paths must
//     pass the same workspace sandbox policy as relative paths.
//   - Files larger than MaxBytes are rejected.
//   - Only files whose detected MIME type is an image format are
//     accepted. Non-image files return an error.
type ReadImageTool struct {
	BaseDir  string
	MaxBytes int64
}

// NewReadImage returns a ReadImageTool. Default MaxBytes is 10 MiB.
// BaseDir defaults to "." (current directory).
func NewReadImage(baseDir string, maxBytes int64) *ReadImageTool {
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	if baseDir == "" {
		baseDir = "."
	}
	return &ReadImageTool{BaseDir: baseDir, MaxBytes: maxBytes}
}

// Spec returns the Tool descriptor. The Fn field is the same
// closure as Execute, just typed for the registry.
func (t *ReadImageTool) Spec() Tool {
	return Tool{
		Name:        "read_image",
		Description: "Read image pixels for analysis. Large images are bounded by default; use crop with original-image coordinates for small text/regions, or image_detail:original for full resolution. Original file stays unchanged. PNG, JPEG, GIF, WebP.",
		ReadOnly:    true,
		Schema: `{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Path to the image file, absolute or relative to the working directory."},
				"image_detail": {"type":"string","enum":["auto","original"],"description":"auto (default) bounds analysis pixels; original sends full resolution."},
				"crop": {"type":"object","description":"Read a precise region using original image pixel coordinates.","properties":{"x":{"type":"integer","minimum":0},"y":{"type":"integer","minimum":0},"width":{"type":"integer","minimum":1},"height":{"type":"integer","minimum":1}},"required":["x","y","width","height"]}
			},
			"required": ["path"]
		}`,
		Fn: t.Execute,
	}
}

// Execute reads the file at args.path and returns the image.
// args is the raw JSON: {"path": "..."}.
func (t *ReadImageTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var params struct {
		Path        string               `json:"path"`
		ImageDetail string               `json:"image_detail"`
		Crop        *AnalysisImageRegion `json:"crop"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{Err: fmt.Errorf("read_image: bad args: %w", err)}, err
	}
	if params.ImageDetail != "" && params.ImageDetail != "auto" && params.ImageDetail != "original" {
		err := fmt.Errorf("read_image: image_detail must be auto or original")
		return Result{Err: err}, err
	}
	if params.Path == "" {
		err := fmt.Errorf("read_image: path is required")
		return Result{Err: err}, err
	}

	full, err := sandbox.ResolveSafe(t.BaseDir, params.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("read_image: %w", err)}, nil
	}

	// Reject named pipes/devices before Open, which could otherwise block.
	preflight, err := os.Stat(full)
	if err != nil {
		return Result{Err: err}, err
	}
	if !preflight.Mode().IsRegular() {
		err = fmt.Errorf("read_image: %q is not a regular file", full)
		return Result{Err: err}, err
	}
	file, err := os.Open(full)
	if err != nil {
		err = fmt.Errorf("read_image: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Result{Err: err}, err
	}
	if !info.Mode().IsRegular() {
		err = fmt.Errorf("read_image: %q is not a regular file", full)
		return Result{Err: err}, err
	}
	limit := min(t.MaxBytes, int64(DefaultMaxScreenshotBytes))
	if info.Size() > limit {
		err = fmt.Errorf("read_image: file too large: %d bytes > %d max", info.Size(), limit)
		return Result{Err: err}, err
	}
	data, mime, err := readBoundedImage(ctx, file, info.Size())
	if err != nil {
		err = fmt.Errorf("read_image: %q: %w", filepath.Base(full), err)
		return Result{Err: err}, err
	}

	analysisData, analysisType, analysis, err := prepareAnalysisImageRegion(ctx, data, mime, params.ImageDetail, params.Crop)
	if err != nil {
		return Result{Err: err}, err
	}
	text := fmt.Sprintf("Loaded image %s (%d bytes, %s)", params.Path, info.Size(), mime)
	if analysis.Resized || params.Crop != nil {
		metadata, _ := json.Marshal(analysis)
		text += "\nAnalysis image: " + string(metadata)
	}
	return Result{
		Text:  text,
		Image: &ImageContent{MediaType: analysisType, Data: analysisData},
	}, nil
}

// detectImageMIME sniffs the file magic for the most common image
// formats. Returns the MIME type or "" if no signature matches.
// PNG / JPEG / GIF / WebP only — that's the set OpenAI accepts.
func detectImageMIME(data []byte) string {
	if len(data) < 12 {
		return ""
	}
	// PNG: 89 50 4E 47 0D 0A 1A 0A
	if data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	// JPEG: FF D8 FF
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	// GIF: "GIF87a" or "GIF89a"
	if string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a" {
		return "image/gif"
	}
	// WebP: "RIFF....WEBP"
	if string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}

// SupportedImageMIMEs returns the set of MIME types ReadImageTool
// can detect. Useful for capability checks.
func SupportedImageMIMEs() []string {
	return []string{"image/png", "image/jpeg", "image/gif", "image/webp"}
}

// Sniff before allocating the payload. A 10 MiB text/HTML response mislabeled
// as PNG costs only a small header, and a file that grows cannot bypass limits.
func readBoundedImage(ctx context.Context, source io.Reader, size int64) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	var header [512]byte
	n, err := io.ReadFull(source, header[:min(int64(len(header)), size)])
	if err != nil {
		return nil, "", err
	}
	mime := detectImageMIME(header[:n])
	if mime == "" {
		return nil, "", fmt.Errorf("not a recognised image (magic bytes)")
	}
	data := make([]byte, int(size))
	copy(data, header[:n])
	for offset := n; offset < len(data); {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		end := min(offset+1024*1024, len(data))
		count, err := io.ReadFull(source, data[offset:end])
		if err != nil {
			return nil, "", err
		}
		offset += count
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	var extra [1]byte
	if n, err := source.Read(extra[:]); n != 0 || err != io.EOF {
		return nil, "", fmt.Errorf("file changed while reading")
	}
	return data, mime, nil
}
