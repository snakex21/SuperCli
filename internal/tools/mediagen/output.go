package mediagen

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

// Output is compatible with show_media. Bytes are kept on disk, never attached
// to a model turn merely because generation finished.
type Output struct {
	Type      string `json:"type"`
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
	Attached  bool   `json:"attached"`
}

func (t *Tool) saveOutput(ctx context.Context, source io.Reader, limit int64) (result core.Result, err error) {
	if err = ctx.Err(); err != nil {
		return result, err
	}
	r := &contextReader{ctx: ctx, reader: source}
	var header [512]byte
	n, readErr := io.ReadFull(r, header[:min(int64(len(header)), limit+1)])
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, errors.New("could not decode generated media")
	}
	if int64(n) > limit {
		return result, errors.New("generated media exceeds byte limit")
	}
	mime := http.DetectContentType(header[:n])
	ext := outputExtension(t.kind, mime)
	if ext == "" {
		return result, fmt.Errorf("provider output is not a supported %s file", t.kind)
	}
	base := t.baseDir
	if base == "" {
		base = "."
	}
	base, err = sandbox.ResolveWithin(base, ".")
	if err != nil {
		return result, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return result, err
	}
	defer root.Close()
	if err = root.Mkdir("generated", 0700); err != nil && !os.IsExist(err) {
		return result, err
	}
	dir, err := root.OpenRoot("generated")
	if err != nil {
		return result, fmt.Errorf("generated directory must remain inside workspace: %w", err)
	}
	defer dir.Close()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return result, err
	}
	name := t.kind + "-" + hex.EncodeToString(random[:]) + ext
	file, err := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	saved := false
	defer func() {
		file.Close()
		if !saved {
			_ = dir.Remove(name)
		}
	}()
	// The destination is exclusive and root-scoped; no path argument, overwrite,
	// symlink escape or provider-controlled filename is possible.
	writerSource := io.MultiReader(bytes.NewReader(header[:n]), r)
	size, err := io.CopyBuffer(file, io.LimitReader(writerSource, limit+1), make([]byte, 32*1024))
	if err != nil {
		return result, fmt.Errorf("save generated media: %w", err)
	}
	if size > limit {
		return result, errors.New("generated media exceeds byte limit")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = file.Close(); err != nil {
		return result, err
	}
	metadata, err := json.Marshal(Output{Type: t.kind, Path: filepath.Join(base, "generated", name), MediaType: mime, Bytes: size, Attached: false})
	if err != nil {
		return result, err
	}
	saved = true
	return core.Result{Text: string(metadata)}, nil
}
func outputExtension(kind, mime string) string {
	if kind == "image" {
		switch mime {
		case "image/png":
			return ".png"
		case "image/jpeg":
			return ".jpg"
		case "image/webp":
			return ".webp"
		}
	}
	if kind == "video" {
		switch mime {
		case "video/mp4":
			return ".mp4"
		case "video/webm":
			return ".webm"
		}
	}
	return ""
}
