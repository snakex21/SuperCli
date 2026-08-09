package webgui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/tools/sandbox"
)

// Direct image attachments follow the same lightweight principle as modern
// coding CLIs: send pixels in the first user message, without requiring a
// read_image tool round trip, but normalize large screenshots before base64.
const (
	directImageMaxDimension = 1280
	directImageMaxBytes     = 2 << 20
)

func buildDirectAttachmentImages(home string, paths []string) ([]llm.ImageRef, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	images := make([]llm.ImageRef, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, raw := range paths {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		resolved, err := sandbox.ResolveSafe(absHome, raw)
		if err != nil {
			return nil, err
		}
		key := resolved
		if filepath.Separator == '\\' {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		file, err := os.Open(resolved)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxChatAttachmentBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) > maxChatAttachmentBytes {
			continue
		}
		mediaType := http.DetectContentType(data)
		if !allowedDirectImageMIME(mediaType) {
			continue
		}
		data, mediaType = normalizeDirectImage(data, mediaType)
		images = append(images, llm.ImageRef{
			MediaType: mediaType,
			Data:      base64.StdEncoding.EncodeToString(data),
			Name:      filepath.Base(resolved),
		})
	}
	return images, nil
}

func allowedDirectImageMIME(mediaType string) bool {
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// normalizeDirectImage caps expensive screenshots at 1280 px and roughly 2
// MiB. PNG/JPEG/GIF are decoded by the standard library; a small WebP is sent
// unchanged because Go deliberately has no WebP decoder in the standard tree.
func normalizeDirectImage(data []byte, mediaType string) ([]byte, string) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return data, mediaType
	}
	needsResize := config.Width > directImageMaxDimension || config.Height > directImageMaxDimension
	needsReencode := len(data) > directImageMaxBytes || mediaType == "image/gif"
	if !needsResize && !needsReencode {
		return data, mediaType
	}
	if mediaType == "image/webp" {
		return data, mediaType
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, mediaType
	}
	targetWidth, targetHeight := scaledImageSize(config.Width, config.Height, directImageMaxDimension)
	target := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	draw.Draw(target, target.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	nearestScale(target, source)
	var encoded bytes.Buffer
	quality := 86
	if len(data) > directImageMaxBytes*2 {
		quality = 78
	}
	if err := jpeg.Encode(&encoded, target, &jpeg.Options{Quality: quality}); err != nil {
		return data, mediaType
	}
	return encoded.Bytes(), "image/jpeg"
}

func scaledImageSize(width, height, maxDimension int) (int, int) {
	if width <= 0 || height <= 0 {
		return 1, 1
	}
	if width <= maxDimension && height <= maxDimension {
		return width, height
	}
	if width >= height {
		return maxDimension, max(1, height*maxDimension/width)
	}
	return max(1, width*maxDimension/height), maxDimension
}

func nearestScale(dst *image.RGBA, src image.Image) {
	sb := src.Bounds()
	db := dst.Bounds()
	for y := 0; y < db.Dy(); y++ {
		sy := sb.Min.Y + y*sb.Dy()/db.Dy()
		for x := 0; x < db.Dx(); x++ {
			sx := sb.Min.X + x*sb.Dx()/db.Dx()
			dst.Set(x, y, src.At(sx, sy))
		}
	}
}

// Keep gif linked for image.Decode registration even though animated images
// are flattened to their first frame during normalization.
var _ = gif.GIF{}
