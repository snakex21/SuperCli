package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"supercli/internal/tools/sandbox"
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
// the OS clipboard and returns the
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
	BaseDir       string
	Capture       ClipboardCapture // injected; default = osCapture{}
	MaxBytes      int64
	ScreenCapture func(context.Context) ([]byte, string, error)
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
// with default bounds. The legacy hasVision argument is intentionally ignored:
// provider catalogs are incomplete, so the selected API receives the image and
// remains the source of truth about whether it can process it.
func NewSendScreenshot(baseDir string, _ func(string) bool) *SendScreenshotTool {
	return &SendScreenshotTool{
		BaseDir:  baseDir,
		Capture:  osCapture{},
		MaxBytes: DefaultMaxScreenshotBytes,
	}
}

// Spec returns the Tool descriptor.
func (t *SendScreenshotTool) Spec() Tool {
	return Tool{
		Name:        "send_screenshot",
		Description: "Capture this computer's desktop (source:screen) or clipboard image (default) using native OS access, and save it in the portable snapshots folder. No image-generation model is needed. attach:false saves/shows it without sending pixels to the model.",
		Schema: `{
  "type": "object",
  "properties": {
    "source": {"type": "string", "enum": ["clipboard", "screen"], "description": "clipboard (default) reads a copied image; screen takes a new desktop screenshot."},
    "attach": {"type": "boolean", "description": "Default true. False saves/shows the image without attaching pixels to the model."}
  }
}`,
		Fn:         t.Execute,
		RepairArgs: repairScreenshotArgs,
	}
}

// Older callers supplied a model field that this tool has always ignored.
// Remove only that field, then let the registry validate all real arguments.
func repairScreenshotArgs(raw json.RawMessage) (json.RawMessage, bool) {
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil {
		return nil, false
	}
	if _, ok := args["model"]; !ok {
		return nil, false
	}
	delete(args, "model")
	out, err := json.Marshal(args)
	return out, err == nil
}

// Execute captures the clipboard image,
// saves a snapshot and returns Result with the image attached.
func (t *SendScreenshotTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	if strings.TrimSpace(t.BaseDir) == "" {
		err := fmt.Errorf("send_screenshot: portable snapshots directory unavailable")
		return Result{Err: err}, err
	}
	var params struct {
		Source string `json:"source"`
		Attach *bool  `json:"attach"`
	}
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &params); err != nil {
			return Result{Err: fmt.Errorf("send_screenshot: bad args: %w", err)}, err
		}
	}

	source := params.Source
	if source == "" {
		source = "clipboard"
	}
	if source != "clipboard" && source != "screen" {
		err := fmt.Errorf("send_screenshot: source must be clipboard or screen")
		return Result{Err: err}, err
	}
	attached := params.Attach == nil || *params.Attach
	var data []byte
	var mediaType string
	var err error
	if source == "screen" {
		if t.ScreenCapture != nil {
			data, mediaType, err = t.ScreenCapture(ctx)
		} else {
			data, mediaType, err = captureScreen(ctx, t.BaseDir)
		}
	} else {
		capture := t.Capture
		if capture == nil {
			capture = osCapture{}
		}
		if contextual, ok := capture.(interface {
			CaptureContext(context.Context) ([]byte, string, error)
		}); ok {
			data, mediaType, err = contextual.CaptureContext(ctx)
		} else {
			data, mediaType, err = capture.Capture()
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		err := fmt.Errorf("send_screenshot: capture failed: %w", err)
		return Result{Err: err}, err
	}
	if int64(len(data)) > t.MaxBytes {
		err := fmt.Errorf("send_screenshot: clipboard image too large: %d > %d", len(data), t.MaxBytes)
		return Result{Err: err}, err
	}
	detectedType := sniffMediaType(data)
	if mediaType == "" {
		mediaType = detectedType
	}
	if detectedType == "" || mediaType != detectedType {
		err := fmt.Errorf("send_screenshot: clipboard bytes do not look like a known image format (magic header missing)")
		return Result{Err: err}, err
	}

	// Save a copy for the audit trail. We
	// use a timestamped filename so multiple
	// captures in the same session don't
	// collide.
	path, saveErr := t.saveSnapshot(source, data, mediaType)
	if saveErr != nil && !attached {
		err := fmt.Errorf("send_screenshot: save failed: %w", saveErr)
		return Result{Err: err}, err
	}
	metadata := struct {
		Type        string `json:"type"`
		Source      string `json:"source"`
		Path        string `json:"path,omitempty"`
		PreviewPath string `json:"preview_path,omitempty"`
		MediaType   string `json:"media_type"`
		Bytes       int    `json:"bytes"`
		Attached    bool   `json:"attached"`
		SaveError   string `json:"save_error,omitempty"`
	}{Type: "image", Source: source, Path: path, MediaType: mediaType, Bytes: len(data), Attached: attached}
	if saveErr != nil {
		metadata.SaveError = saveErr.Error()
	} else {
		metadata.PreviewPath = "snapshot:" + filepath.Base(path)
	}
	text, _ := json.Marshal(metadata)
	res := Result{Text: string(text)}
	if attached {
		res.Image = &ImageContent{MediaType: mediaType, Data: data}
	}
	return res, nil
}

// saveSnapshot writes the captured bytes
// under <BaseDir>/.supercli/snapshots/ with
// a timestamped filename. The directory is
// created on demand.
func (t *SendScreenshotTool) saveSnapshot(source string, data []byte, mediaType string) (string, error) {
	base, err := filepath.Abs(t.BaseDir)
	if err != nil {
		return "", err
	}
	dir, err := sandbox.ResolveWithin(base, filepath.Join(".supercli", "snapshots"))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, source+"-*"+mediaTypeExt(mediaType))
	if err != nil {
		return "", err
	}
	path := f.Name()
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return "", writeErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
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
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' &&
		data[4] == 0x0D && data[5] == 0x0A && data[6] == 0x1A && data[7] == 0x0A {
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
	return osCapture{}.CaptureContext(context.Background())
}

func (osCapture) CaptureContext(ctx context.Context) ([]byte, string, error) {
	switch runtime.GOOS {
	case "windows":
		return captureClipboardWindows(ctx)
	case "darwin":
		return captureClipboardDarwin(ctx)
	case "linux":
		return captureClipboardLinux(ctx)
	default:
		return nil, "", fmt.Errorf("send_screenshot: no clipboard shim for %s", runtime.GOOS)
	}
}
