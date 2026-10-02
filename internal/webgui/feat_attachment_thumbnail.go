package webgui

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

const (
	thumbnailSourceMaxDimension = 16384
	thumbnailSourceMaxPixels    = 32 << 20
	thumbnailMaxBytes           = 2 << 20
	thumbnailCacheMaxBytes      = 64 << 20
	thumbnailCacheMaxFiles      = 128
)

var (
	errThumbnailInvalid     = errors.New("image is too large or invalid for a thumbnail")
	errThumbnailUnsupported = errors.New("thumbnail format is unavailable")
)

func attachmentThumbnailBounds(variant string) (int, int, bool) {
	switch variant {
	case "transcript":
		return 780, 520, true // twice the largest rendered transcript preview
	case "composer":
		return 100, 96, true
	default:
		return 0, 0, false
	}
}

func (s *Server) serveAttachmentThumbnail(w http.ResponseWriter, r *http.Request, source *os.File, info os.FileInfo, full, mediaType, variant string) {
	width, height, ok := attachmentThumbnailBounds(variant)
	if !ok {
		http.Error(w, "unknown thumbnail size", http.StatusBadRequest)
		return
	}
	if mediaType != "image/png" && mediaType != "image/jpeg" && mediaType != "image/gif" {
		http.Error(w, errThumbnailUnsupported.Error(), http.StatusUnsupportedMediaType)
		return
	}
	if s.eng.DataDir() == "" {
		http.Error(w, "portable thumbnail directory is unavailable", http.StatusServiceUnavailable)
		return
	}
	root := filepath.Join(s.eng.DataDir(), ".supercli", "attachment-thumbnails")
	key := sha256.Sum256([]byte(fmt.Sprintf("v1\x00%s\x00%d\x00%d\x00%s", full, info.Size(), info.ModTime().UnixNano(), variant)))
	path := filepath.Join(root, fmt.Sprintf("%x.png", key))
	thumbnail, err := openAttachmentThumbnail(path, width, height)
	if err != nil {
		err = s.withAttachmentThumbnailGeneration(r.Context(), func() error {
			// A concurrent request may have generated this derivative while we
			// waited. Recheck disk inside the gate instead of retaining a map.
			if cached, err := openAttachmentThumbnail(path, width, height); err == nil {
				return cached.Close()
			}
			if err := os.MkdirAll(root, 0o700); err != nil {
				return err
			}
			if err := pruneAttachmentThumbnails(root); err != nil {
				return err
			}
			return createAttachmentThumbnail(r.Context(), source, info, root, path, width, height)
		})
		if err == nil {
			thumbnail, err = openAttachmentThumbnail(path, width, height)
		}
	}
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		status := http.StatusServiceUnavailable
		if errors.Is(err, errThumbnailInvalid) {
			status = http.StatusBadRequest
		}
		http.Error(w, "thumbnail is unavailable", status)
		return
	}
	defer thumbnail.Close()
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "image/png")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), thumbnail)
}

func (s *Server) withAttachmentThumbnailGeneration(ctx context.Context, generate func() error) error {
	s.thumbnailOnce.Do(func() { s.thumbnailGate = make(chan struct{}, 1) })
	select {
	case s.thumbnailGate <- struct{}{}:
		defer func() { <-s.thumbnailGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return generate()
}

func openAttachmentThumbnail(path string, width, height int) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > thumbnailMaxBytes) {
		err = errThumbnailInvalid
	}
	if err == nil {
		var config image.Config
		config, _, err = image.DecodeConfig(file)
		if err == nil && (config.Width <= 0 || config.Height <= 0 || config.Width > width || config.Height > height) {
			err = errThumbnailInvalid
		}
	}
	if err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

type thumbnailContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r thumbnailContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

type thumbnailBoundedWriter struct {
	ctx       context.Context
	w         io.Writer
	remaining int
}

func (w *thumbnailBoundedWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > w.remaining {
		return 0, errThumbnailInvalid
	}
	n, err := w.w.Write(p)
	w.remaining -= n
	return n, err
}

func createAttachmentThumbnail(ctx context.Context, file *os.File, info os.FileInfo, root, target string, maxWidth, maxHeight int) error {
	if info.Size() <= 0 || info.Size() > maxChatAttachmentBytes {
		return errThumbnailInvalid
	}
	reader := func() io.Reader { return thumbnailContextReader{ctx, io.LimitReader(file, maxChatAttachmentBytes+1)} }
	config, format, err := image.DecodeConfig(reader())
	if err != nil || (format != "png" && format != "jpeg" && format != "gif") || config.Width <= 0 || config.Height <= 0 ||
		config.Width > thumbnailSourceMaxDimension || config.Height > thumbnailSourceMaxDimension ||
		int64(config.Width)*int64(config.Height) > thumbnailSourceMaxPixels {
		return errThumbnailInvalid
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	// Decode directly from the file: do not retain another encoded-image copy.
	source, _, err := image.Decode(reader())
	if err != nil {
		return err
	}
	width, height := config.Width, config.Height
	if width > maxWidth || height > maxHeight {
		if int64(width)*int64(maxHeight) >= int64(height)*int64(maxWidth) {
			height, width = max(1, height*maxWidth/width), maxWidth
		} else {
			width, height = max(1, width*maxHeight/height), maxHeight
		}
	}
	preview := image.NewRGBA(image.Rect(0, 0, width, height))
	if err := scaleAttachmentThumbnail(ctx, preview, source); err != nil {
		return err
	}
	output, err := os.CreateTemp(root, ".thumbnail-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	err = encoder.Encode(&thumbnailBoundedWriter{ctx: ctx, w: output, remaining: thumbnailMaxBytes}, preview)
	closeErr := output.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := file.Stat()
	if err != nil || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return errThumbnailInvalid
	}
	// A corrupt older derivative may exist; the generation gate prevents a
	// reader from ever seeing a partial replacement.
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(output.Name(), target)
}

func scaleAttachmentThumbnail(ctx context.Context, dst *image.RGBA, src image.Image) error {
	sb, db := src.Bounds(), dst.Bounds()
	for y := 0; y < db.Dy(); y++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		sy := (float64(y)+0.5)*float64(sb.Dy())/float64(db.Dy()) - 0.5
		y0 := max(0, int(sy))
		y1 := min(y0+1, sb.Dy()-1)
		fy := max(0, sy-float64(y0))
		for x := 0; x < db.Dx(); x++ {
			sx := (float64(x)+0.5)*float64(sb.Dx())/float64(db.Dx()) - 0.5
			x0 := max(0, int(sx))
			x1 := min(x0+1, sb.Dx()-1)
			fx := max(0, sx-float64(x0))
			r00, g00, b00, a00 := attachmentThumbnailPixel(src, sb.Min.X+x0, sb.Min.Y+y0)
			r10, g10, b10, a10 := attachmentThumbnailPixel(src, sb.Min.X+x1, sb.Min.Y+y0)
			r01, g01, b01, a01 := attachmentThumbnailPixel(src, sb.Min.X+x0, sb.Min.Y+y1)
			r11, g11, b11, a11 := attachmentThumbnailPixel(src, sb.Min.X+x1, sb.Min.Y+y1)
			blend := func(a, b, c, d uint32) uint8 {
				return uint8(((1-fy)*((1-fx)*float64(a)+fx*float64(b)) + fy*((1-fx)*float64(c)+fx*float64(d))) / 257)
			}
			dst.SetRGBA(x, y, color.RGBA{blend(r00, r10, r01, r11), blend(g00, g10, g01, g11), blend(b00, b10, b01, b11), blend(a00, a10, a01, a11)})
		}
	}
	return nil
}

func attachmentThumbnailPixel(src image.Image, x, y int) (r, g, b, a uint32) {
	// image.Image.At boxes a fresh color value for every sample. Read the
	// standard PNG/JPEG/GIF storage directly to avoid millions of allocations.
	switch source := src.(type) {
	case *image.RGBA:
		index := source.PixOffset(x, y)
		pixel := source.Pix[index : index+4]
		return uint32(pixel[0]) * 257, uint32(pixel[1]) * 257, uint32(pixel[2]) * 257, uint32(pixel[3]) * 257
	case *image.NRGBA:
		index := source.PixOffset(x, y)
		pixel := source.Pix[index : index+4]
		alpha := uint32(pixel[3]) * 257
		return uint32(pixel[0]) * alpha / 255, uint32(pixel[1]) * alpha / 255, uint32(pixel[2]) * alpha / 255, alpha
	case *image.RGBA64:
		index := source.PixOffset(x, y)
		pixel := source.Pix[index : index+8]
		return uint32(pixel[0])<<8 | uint32(pixel[1]), uint32(pixel[2])<<8 | uint32(pixel[3]),
			uint32(pixel[4])<<8 | uint32(pixel[5]), uint32(pixel[6])<<8 | uint32(pixel[7])
	case *image.NRGBA64:
		index := source.PixOffset(x, y)
		pixel := source.Pix[index : index+8]
		alpha := uint32(pixel[6])<<8 | uint32(pixel[7])
		return (uint32(pixel[0])<<8 | uint32(pixel[1])) * alpha / 65535,
			(uint32(pixel[2])<<8 | uint32(pixel[3])) * alpha / 65535,
			(uint32(pixel[4])<<8 | uint32(pixel[5])) * alpha / 65535, alpha
	case *image.Gray:
		value := uint32(source.Pix[source.PixOffset(x, y)]) * 257
		return value, value, value, 65535
	case *image.Gray16:
		index := source.PixOffset(x, y)
		value := uint32(source.Pix[index])<<8 | uint32(source.Pix[index+1])
		return value, value, value, 65535
	case *image.YCbCr:
		yi, ci := source.YOffset(x, y), source.COffset(x, y)
		red, green, blue := color.YCbCrToRGB(source.Y[yi], source.Cb[ci], source.Cr[ci])
		return uint32(red) * 257, uint32(green) * 257, uint32(blue) * 257, 65535
	case *image.Paletted:
		return source.Palette[source.ColorIndexAt(x, y)].RGBA()
	default:
		return src.At(x, y).RGBA()
	}
}

func pruneAttachmentThumbnails(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var files []os.FileInfo
	var bytes int64
	for _, entry := range entries {
		if entry.Type().IsRegular() && len(entry.Name()) > len(".thumbnail-.tmp") &&
			entry.Name()[:len(".thumbnail-")] == ".thumbnail-" && filepath.Ext(entry.Name()) == ".tmp" {
			// No generator is running while this gate is held; these are remnants
			// of a process interrupted before its deferred cleanup could run.
			if err := os.Remove(filepath.Join(root, entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if entry.Type().IsRegular() && filepath.Ext(entry.Name()) == ".png" {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files = append(files, info)
			bytes += info.Size()
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime().Before(files[j].ModTime()) })
	// Reserve one maximum-size derivative before decoding the next source.
	for _, file := range files {
		if len(files) < thumbnailCacheMaxFiles && bytes <= thumbnailCacheMaxBytes-thumbnailMaxBytes {
			break
		}
		if err := os.Remove(filepath.Join(root, file.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
		bytes -= file.Size()
		files = files[1:]
	}
	return nil
}
