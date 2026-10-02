package media

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"strings"
)

const (
	// Analysis images are separate from the original portable preview file.
	AnalysisMaxDimension     = 1280
	AnalysisMaxPixels        = 1 << 20
	AnalysisMaxDecodedPixels = 32 << 20
	AnalysisMaxEncodedBytes  = 16 << 20
)

// AnalysisImageRegion is expressed in pixels of the original image.
type AnalysisImageRegion struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// AnalysisImageInfo describes the pixels sent to the model. Preview pixels stay
// in the original file, including when the analysis image is cropped or resized.
type AnalysisImageInfo struct {
	OriginalWidth  int                  `json:"original_width,omitempty"`
	OriginalHeight int                  `json:"original_height,omitempty"`
	Width          int                  `json:"width,omitempty"`
	Height         int                  `json:"height,omitempty"`
	OriginalBytes  int                  `json:"original_bytes"`
	Bytes          int                  `json:"bytes"`
	Resized        bool                 `json:"resized"`
	Detail         string               `json:"detail"`
	Region         *AnalysisImageRegion `json:"crop,omitempty"`
}

func prepareAnalysisImage(ctx context.Context, data []byte, mediaType, detail string) ([]byte, string, AnalysisImageInfo, error) {
	return prepareAnalysisImageRegion(ctx, data, mediaType, detail, nil)
}

func prepareAnalysisImageRegion(ctx context.Context, data []byte, mediaType, detail string, region *AnalysisImageRegion) ([]byte, string, AnalysisImageInfo, error) {
	detail = strings.ToLower(strings.TrimSpace(detail))
	if detail == "" {
		detail = "auto"
	}
	info := AnalysisImageInfo{OriginalBytes: len(data), Bytes: len(data), Detail: detail}
	if err := ctx.Err(); err != nil {
		return nil, "", info, err
	}
	if detail != "auto" && detail != "original" {
		return nil, "", info, fmt.Errorf("image_detail must be auto or original")
	}
	if len(data) > AnalysisMaxEncodedBytes {
		return nil, "", info, fmt.Errorf("analysis image exceeds %d-byte encoded limit", AnalysisMaxEncodedBytes)
	}
	if region != nil {
		copy := *region
		info.Region = &copy
		if region.X < 0 || region.Y < 0 || region.Width <= 0 || region.Height <= 0 {
			return nil, "", info, fmt.Errorf("image region requires nonnegative x/y and positive width/height")
		}
	}
	config, format, err := image.DecodeConfig(&analysisContextReader{ctx: ctx, reader: bytes.NewReader(data)})
	if err != nil {
		if err := ctx.Err(); err != nil {
			return nil, "", info, err
		}
		if region != nil {
			return nil, "", info, fmt.Errorf("image region requires a decodable PNG, JPEG or GIF: %w", err)
		}
		// Preserve the existing magic-header validation contract for small/legacy
		// payloads. The standard image package has no WebP decoder; encoded bytes
		// remain capped and dimensions are deliberately absent when unknown.
		return data, mediaType, info, nil
	}
	if config.Width <= 0 || config.Height <= 0 {
		return nil, "", info, fmt.Errorf("analysis image has invalid dimensions")
	}
	info.OriginalWidth, info.OriginalHeight = config.Width, config.Height
	if uint64(config.Width)*uint64(config.Height) > AnalysisMaxDecodedPixels {
		return nil, "", info, fmt.Errorf("analysis image exceeds %d-pixel decoded limit", AnalysisMaxDecodedPixels)
	}
	width, height := config.Width, config.Height
	if region != nil {
		if region.X >= width || region.Y >= height || region.Width > width-region.X || region.Height > height-region.Y {
			return nil, "", info, fmt.Errorf("image region is outside original %dx%d image", width, height)
		}
		width, height = region.Width, region.Height
	}
	targetWidth, targetHeight := width, height
	if detail == "auto" {
		targetWidth, targetHeight = analysisImageSize(width, height)
	}
	info.Width, info.Height = targetWidth, targetHeight
	info.Resized = targetWidth != width || targetHeight != height
	if region == nil && !info.Resized {
		return data, mediaType, info, nil
	}
	source, _, err := image.Decode(&analysisContextReader{ctx: ctx, reader: bytes.NewReader(data)})
	if err != nil {
		return nil, "", info, fmt.Errorf("decode analysis image: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, "", info, err
	}
	if source.Bounds().Dx() != config.Width || source.Bounds().Dy() != config.Height {
		return nil, "", info, fmt.Errorf("analysis image dimensions changed during decode")
	}
	if region != nil {
		rectangle := image.Rect(source.Bounds().Min.X+region.X, source.Bounds().Min.Y+region.Y, source.Bounds().Min.X+region.X+region.Width, source.Bounds().Min.Y+region.Y+region.Height)
		if subimage, ok := source.(interface {
			SubImage(image.Rectangle) image.Image
		}); ok {
			source = subimage.SubImage(rectangle)
		} else {
			source = analysisImageView{source: source, rectangle: rectangle}
		}
	}
	if info.Resized {
		source, err = analysisAreaScale(ctx, source, targetWidth, targetHeight)
		if err != nil {
			return nil, "", info, err
		}
	}
	var buffer bytes.Buffer
	writer := &analysisContextWriter{ctx: ctx, buffer: &buffer, limit: AnalysisMaxEncodedBytes}
	analysisType := "image/png"
	if detail == "auto" && info.Resized && format == "jpeg" {
		analysisType = "image/jpeg"
		// A JPEG source is opaque, including its resampled target. Share that
		// target's pixel buffer through RGBA so the JPEG encoder uses its fast
		// path instead of boxing one NRGBA color per pixel. No pixel copy.
		jpegSource := source
		if target, ok := source.(*image.NRGBA); ok {
			jpegSource = &image.RGBA{Pix: target.Pix, Stride: target.Stride, Rect: target.Rect}
		}
		err = jpeg.Encode(writer, jpegSource, &jpeg.Options{Quality: 90})
	} else {
		encoder := png.Encoder{CompressionLevel: png.BestSpeed}
		err = encoder.Encode(writer, source)
	}
	if err != nil {
		return nil, "", info, fmt.Errorf("encode analysis image: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, "", info, err
	}
	info.Bytes = buffer.Len()
	return buffer.Bytes(), analysisType, info, nil
}

func analysisImageSize(width, height int) (int, int) {
	scale := math.Min(1, float64(AnalysisMaxDimension)/float64(max(width, height)))
	if pixels := float64(width) * float64(height) * scale * scale; pixels > AnalysisMaxPixels {
		scale *= math.Sqrt(float64(AnalysisMaxPixels) / pixels)
	}
	return max(1, int(float64(width)*scale)), max(1, int(float64(height)*scale))
}

type analysisContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *analysisContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

type analysisContextWriter struct {
	ctx    context.Context
	buffer *bytes.Buffer
	limit  int
}

func (w *analysisContextWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(data) > w.limit-w.buffer.Len() {
		return 0, fmt.Errorf("analysis image exceeds %d-byte encoded limit", w.limit)
	}
	return w.buffer.Write(data)
}

type analysisImageView struct {
	source    image.Image
	rectangle image.Rectangle
}

func (v analysisImageView) ColorModel() color.Model { return v.source.ColorModel() }
func (v analysisImageView) Bounds() image.Rectangle { return v.rectangle }
func (v analysisImageView) At(x, y int) color.Color { return v.source.At(x, y) }

// Boundary overlaps and span are prepared once per axis. Interior pixels
// contribute exactly one; do not recompute min/max for every sampled pixel.
type analysisArea struct {
	first, last                   int
	firstWeight, lastWeight, span float64
}

func analysisAreas(source, target int) []analysisArea {
	areas := make([]analysisArea, target)
	scale := float64(source) / float64(target)
	for i := range areas {
		left, right := float64(i)*scale, float64(i+1)*scale
		first, last := int(math.Floor(left)), min(source-1, int(math.Ceil(right))-1)
		areas[i] = analysisArea{
			first: first, last: last, span: right - left,
			firstWeight: math.Min(right, float64(first+1)) - math.Max(left, float64(first)),
			lastWeight:  math.Min(right, float64(last+1)) - math.Max(left, float64(last)),
		}
	}
	return areas
}
func (a analysisArea) weight(position int) float64 {
	if position == a.first {
		return a.firstWeight
	}
	if position == a.last {
		return a.lastWeight
	}
	return 1
}

// Area sampling keeps narrow text strokes represented when reducing a screen.
// Accumulate premultiplied channels, so transparent edges do not acquire halos.
// Only the decoded source and bounded target retain pixel-sized storage.
func analysisAreaScale(ctx context.Context, source image.Image, width, height int) (image.Image, error) {
	bounds := source.Bounds()
	xAreas, yAreas := analysisAreas(bounds.Dx(), width), analysisAreas(bounds.Dy(), height)
	target := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y, yArea := range yAreas {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x, xArea := range xAreas {
			if x&31 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			var red, green, blue, alpha float64
			for sy := yArea.first; sy <= yArea.last; sy++ {
				verticalWeight := yArea.weight(sy)
				for sx := xArea.first; sx <= xArea.last; sx++ {
					weight := verticalWeight * xArea.weight(sx)
					r, g, b, a := analysisPixel(source, bounds.Min.X+sx, bounds.Min.Y+sy)
					red += r * weight
					green += g * weight
					blue += b * weight
					alpha += a * weight
				}
			}
			offset := target.PixOffset(x, y)
			if alpha > 0 {
				target.Pix[offset] = analysisByte(red * 255 / alpha)
				target.Pix[offset+1] = analysisByte(green * 255 / alpha)
				target.Pix[offset+2] = analysisByte(blue * 255 / alpha)
			}
			target.Pix[offset+3] = analysisByte(alpha / (xArea.span * yArea.span))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return target, nil
}
func analysisByte(value float64) uint8 { return uint8(math.Max(0, math.Min(255, math.Round(value)))) }
func analysisPixel(source image.Image, x, y int) (r, g, b, a float64) {
	switch source := source.(type) {
	case *image.NRGBA:
		offset := source.PixOffset(x, y)
		pixel := source.Pix[offset : offset+4]
		a = float64(pixel[3])
		scale := a / 255
		return float64(pixel[0]) * scale, float64(pixel[1]) * scale, float64(pixel[2]) * scale, a
	case *image.RGBA:
		offset := source.PixOffset(x, y)
		pixel := source.Pix[offset : offset+4]
		return float64(pixel[0]), float64(pixel[1]), float64(pixel[2]), float64(pixel[3])
	case *image.YCbCr:
		pixel := source.YCbCrAt(x, y)
		red, green, blue := color.YCbCrToRGB(pixel.Y, pixel.Cb, pixel.Cr)
		return float64(red), float64(green), float64(blue), 255
	case *image.NRGBA64:
		offset := source.PixOffset(x, y)
		pixel := source.Pix[offset : offset+8]
		alpha := float64(uint16(pixel[6])<<8 | uint16(pixel[7]))
		scale := alpha / 65535 / 257
		return float64(uint16(pixel[0])<<8|uint16(pixel[1])) * scale, float64(uint16(pixel[2])<<8|uint16(pixel[3])) * scale, float64(uint16(pixel[4])<<8|uint16(pixel[5])) * scale, alpha / 257
	case *image.RGBA64:
		offset := source.PixOffset(x, y)
		pixel := source.Pix[offset : offset+8]
		return float64(uint16(pixel[0])<<8|uint16(pixel[1])) / 257, float64(uint16(pixel[2])<<8|uint16(pixel[3])) / 257, float64(uint16(pixel[4])<<8|uint16(pixel[5])) / 257, float64(uint16(pixel[6])<<8|uint16(pixel[7])) / 257
	case *image.Gray16:
		offset := source.PixOffset(x, y)
		value := float64(uint16(source.Pix[offset])<<8|uint16(source.Pix[offset+1])) / 257
		return value, value, value, 255
	case *image.Gray:
		value := float64(source.Pix[source.PixOffset(x, y)])
		return value, value, value, 255
	default:
		red, green, blue, alpha := source.At(x, y).RGBA()
		return float64(red) / 257, float64(green) / 257, float64(blue) / 257, float64(alpha) / 257
	}
}
