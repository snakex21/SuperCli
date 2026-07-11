package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/tools/fileops"
)

// ReadImageTool loads an image file from disk and returns it as
// base64 + MIME type for the agent loop to attach to the next
// LLM turn.
//
// Safety:
//
//   - Paths are resolved relative to BaseDir; absolute paths are
//     allowed but the user is responsible for not leaking secrets.
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
		Description: "Read an image file from disk and attach it to the conversation. Returns the image plus a short text summary. Supported formats: PNG, JPEG, GIF, WebP.",
		Schema: `{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Path to the image file, absolute or relative to the working directory."}
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
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{Err: fmt.Errorf("read_image: bad args: %w", err)}, err
	}
	if params.Path == "" {
		err := fmt.Errorf("read_image: path is required")
		return Result{Err: err}, err
	}

	full := params.Path
	if !filepath.IsAbs(full) {
		full = filepath.Join(t.BaseDir, full)
	}

	info, err := os.Stat(full)
	if err != nil {
		err = fmt.Errorf("read_image: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}
	if info.IsDir() {
		err := fmt.Errorf("read_image: %q is a directory", full)
		return Result{Err: err}, err
	}
	if info.Size() > t.MaxBytes {
		err := fmt.Errorf("read_image: file too large: %d bytes > %d max", info.Size(), t.MaxBytes)
		return Result{Err: err}, err
	}

	data, err := os.ReadFile(full)
	if err != nil {
		err = fmt.Errorf("read_image: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}

	mime := detectImageMIME(data)
	if mime == "" {
		err := fmt.Errorf("read_image: %q is not a recognised image (magic bytes)", filepath.Base(full))
		return Result{Err: err}, err
	}

	// Return raw bytes; the agent loop will base64-encode for
	// llm.ImageRef using llm.EncodeBase64.
	return Result{
		Text: fmt.Sprintf("Loaded image %s (%d bytes, %s)", params.Path, info.Size(), mime),
		Image: &ImageContent{
			MediaType: mime,
			Data:      data,
		},
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

// to keep `strings` in imports for the future "image magic
// extension" code path planned for F2.
var _ = strings.HasSuffix
