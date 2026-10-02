package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"supercli/internal/tools/sandbox"
)

const MaxMediaPreviewBytes = 32 << 20

// ShowMediaTool presents a local output without reading its pixels into model
// history. The GUI fetches/streams the file only when the user opens the preview.
type ShowMediaTool struct{ BaseDir string }

func NewShowMedia(baseDir string) *ShowMediaTool { return &ShowMediaTool{BaseDir: baseDir} }

func (t *ShowMediaTool) Spec() Tool {
	return Tool{Name: "show_media", ReadOnly: true,
		Description: "Show a local image, MP4/WebM video or MP3/WAV/Ogg audio file to the user without sending its pixels/bytes to the model. Returns preview metadata; use read_image separately if visual inspection is needed.",
		Schema:      `{"type":"object","properties":{"path":{"type":"string","description":"Local file path."}},"required":["path"]}`,
		Fn:          t.Execute}
}

func (t *ShowMediaTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{Err: fmt.Errorf("show_media: bad args: %w", err)}, nil
	}
	if strings.TrimSpace(params.Path) == "" {
		return Result{Err: fmt.Errorf("show_media: path is required")}, nil
	}
	path, err := sandbox.ResolveSafe(t.BaseDir, params.Path)
	if err != nil {
		return Result{Err: err}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return Result{Err: err}, nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Result{Err: err}, nil
	}
	if !info.Mode().IsRegular() || info.Size() > MaxMediaPreviewBytes {
		return Result{Err: fmt.Errorf("show_media: preview requires a regular file up to 32 MiB")}, nil
	}
	var header [512]byte
	n, err := f.Read(header[:])
	if err != nil && err != io.EOF {
		return Result{Err: err}, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	mime := http.DetectContentType(header[:n])
	kind := mediaPreviewKind(mime)
	if kind == "" {
		return Result{Err: fmt.Errorf("show_media: unsupported media type %s", mime)}, nil
	}
	text, _ := json.Marshal(struct {
		Type      string `json:"type"`
		Path      string `json:"path"`
		MediaType string `json:"media_type"`
		Bytes     int64  `json:"bytes"`
		Attached  bool   `json:"attached"`
	}{Type: kind, Path: path, MediaType: mime, Bytes: info.Size()})
	return Result{Text: string(text)}, nil
}

func mediaPreviewKind(mime string) string {
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return "image"
	case "video/mp4", "video/webm":
		return "video"
	case "audio/mpeg", "audio/wave", "audio/wav", "audio/x-wav", "audio/ogg", "application/ogg":
		return "audio"
	default:
		return ""
	}
}
