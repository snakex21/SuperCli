package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Default bounds for the send_screenshot
// tool. A clipboard image is rarely bigger
// than 16 MB; if it is, the user is probably
// trying to send a multi-megapixel screen
// capture and should be told to crop first.
const (
	DefaultMaxScreenshotBytes = 16 * 1024 * 1024 // 16 MB
)

// SendScreenshotTool captures an image from
// the OS clipboard, gates on the model's
// vision capability (F16), and returns the
// image as a Result.Image so the agent loop
// can attach it to the next model request.
//
// The capture itself is OS-specific
// (Windows: PowerShell + System.Windows.Forms
// Clipboard, macOS: osascript + NSPasteboard,
// Linux: xclip / wl-paste). The OS shim is
// isolated behind the ClipboardCapture
// interface so tests can inject a fake.
//
// Safety: the bytes that come out of the
// clipboard are typed (PNG/JPEG/etc.) — we
// only accept a known image magic header
// (checked in captureOS) so a clipboard
// stuffed with arbitrary bytes can't
// smuggle a file into the agent.
type SendScreenshotTool struct {
	BaseDir   string
	HasVision func(model string) bool // injected from F16 caps
	Capture   ClipboardCapture        // injected; default = osCapture{}
	MaxBytes  int64
}

// ClipboardCapture is the small interface
// the tool uses to pull an image from the
// OS clipboard. The default implementation
// (osCapture) shells out to platform-specific
// commands; tests inject a fake.
type ClipboardCapture interface {
	// Capture returns the raw image bytes
	// (PNG, JPEG, or other) and the MIME
	// type. Returns an error if the
	// clipboard holds no image or the OS
	// shim fails.
	Capture() (data []byte, mediaType string, err error)
}

// NewSendScreenshot returns a SendScreenshotTool
// with default bounds. hasVision is the F16
// capability gate — when the current model
// does not support image input, the tool
// refuses with a clear error suggesting
// --list-models.
func NewSendScreenshot(baseDir string, hasVision func(string) bool) *SendScreenshotTool {
	if baseDir == "" {
		baseDir = "."
	}
	return &SendScreenshotTool{
		BaseDir:   baseDir,
		HasVision: hasVision,
		Capture:   osCapture{},
		MaxBytes:  DefaultMaxScreenshotBytes,
	}
}

// Spec returns the Tool descriptor.
func (t *SendScreenshotTool) Spec() Tool {
	return Tool{
		Name:        "send_screenshot",
		Description: "Capture the OS clipboard image (e.g. a screenshot) and attach it to the next model message. Refuses if the current model does not support vision — use --list-models to find one that does. Saves a copy to <home>/.supercli/snapshots/ for the audit trail.",
		Schema: `{
  "type": "object",
  "properties": {
    "model": {"type": "string", "description": "The model ID currently in use. The tool checks F16 vision capability against this ID."}
  },
  "required": ["model"]
}`,
		Fn: t.Execute,
	}
}

// Execute captures the clipboard image,
// gates on vision support, saves a snapshot,
// and returns Result with the image attached.
func (t *SendScreenshotTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var params struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{Err: fmt.Errorf("send_screenshot: bad args: %w", err)}, err
	}
	if params.Model == "" {
		err := fmt.Errorf("send_screenshot: model is required (pass the current model ID)")
		return Result{Err: err}, err
	}
	// F16 vision gate. If the gate is missing
	// (caller didn't wire it), we treat that
	// as "no vision" — safer than silently
	// allowing the call.
	if t.HasVision == nil || !t.HasVision(params.Model) {
		hint := "use --list-models to find a vision-capable model"
		if t.HasVision == nil {
			hint = "internal: capability gate is not wired; " + hint
		}
		err := fmt.Errorf("send_screenshot: model %q does not support vision (%s)", params.Model, hint)
		return Result{Err: err}, err
	}

	capture := t.Capture
	if capture == nil {
		capture = osCapture{}
	}
	data, mediaType, err := capture.Capture()
	if err != nil {
		err := fmt.Errorf("send_screenshot: capture failed: %w", err)
		return Result{Err: err}, err
	}
	if int64(len(data)) > t.MaxBytes {
		err := fmt.Errorf("send_screenshot: clipboard image too large: %d > %d", len(data), t.MaxBytes)
		return Result{Err: err}, err
	}
	if mediaType == "" {
		mediaType = sniffMediaType(data)
	}
	if mediaType == "" {
		err := fmt.Errorf("send_screenshot: clipboard bytes do not look like a known image format (magic header missing)")
		return Result{Err: err}, err
	}

	// Save a copy for the audit trail. We
	// use a timestamped filename so multiple
	// captures in the same session don't
	// collide.
	path, err := t.saveSnapshot(data, mediaType)
	if err != nil {
		// Don't fail the whole call for an
		// audit-trail write error — log and
		// proceed.
		path = fmt.Sprintf("(save failed: %v)", err)
	}

	return Result{
		Text: fmt.Sprintf("Captured screenshot from clipboard (%s, %d bytes). Saved to %s. The image is attached to this result and will be sent to the model with the next message.",
			mediaType, len(data), path),
		Image: &ImageContent{MediaType: mediaType, Data: data},
	}, nil
}

// saveSnapshot writes the captured bytes
// under <BaseDir>/.supercli/snapshots/ with
// a timestamped filename. The directory is
// created on demand.
func (t *SendScreenshotTool) saveSnapshot(data []byte, mediaType string) (string, error) {
	dir := filepath.Join(t.BaseDir, ".supercli", "snapshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ext := mediaTypeExt(mediaType)
	ts := time.Now().UTC().Format("20060102-150405.000")
	name := fmt.Sprintf("clipboard-%s%s", ts, ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// mediaTypeExt maps a MIME type to a
// filename extension. Unknown types get
// ".bin" so the model can still reference
// the file path.
func mediaTypeExt(mediaType string) string {
	switch mediaType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	default:
		return ".bin"
	}
}

// sniffMediaType returns the MIME type for
// the most common image formats, or "" if
// the bytes don't look like a known image.
// Used as a fallback when the OS shim
// reports a format but doesn't report a
// MIME type. Each format check handles its
// own minimum length — the smallest valid
// magic is BMP's "BM" (2 bytes); JPEG needs
// 3; GIF needs 6; PNG needs 8; WebP needs 12.
func sniffMediaType(data []byte) string {
	// PNG: 89 50 4E 47 0D 0A 1A 0A
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	// JPEG: FF D8 FF
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	// GIF: GIF87a or GIF89a
	if len(data) >= 6 && data[0] == 'G' && data[1] == 'I' && data[2] == 'F' && data[3] == '8' &&
		(data[4] == '7' || data[4] == '9') && data[5] == 'a' {
		return "image/gif"
	}
	// BMP: BM
	if len(data) >= 2 && data[0] == 'B' && data[1] == 'M' {
		return "image/bmp"
	}
	// WebP: RIFF....WEBP
	if len(data) >= 12 && data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' &&
		data[8] == 'W' && data[9] == 'E' && data[10] == 'B' && data[11] == 'P' {
		return "image/webp"
	}
	return ""
}

// osCapture is the default ClipboardCapture
// that shells out to the OS. It dispatches
// on runtime.GOOS so the binary stays
// cross-platform-compilable; on platforms
// without a shim, it returns a clear error.
type osCapture struct{}

// Capture implements ClipboardCapture by
// dispatching to the platform-specific
// helper. The helpers are tiny wrappers
// around the OS clipboard API.
func (osCapture) Capture() ([]byte, string, error) {
	switch runtime.GOOS {
	case "windows":
		return captureClipboardWindows()
	case "darwin":
		return captureClipboardDarwin()
	case "linux":
		return captureClipboardLinux()
	default:
		return nil, "", fmt.Errorf("send_screenshot: no clipboard shim for %s", runtime.GOOS)
	}
}
