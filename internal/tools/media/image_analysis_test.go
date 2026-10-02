package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"
	"testing"
)

func analysisPNG(t testing.TB, source image.Image) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, source); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func analysisPattern(width, height int) *image.NRGBA {
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.SetNRGBA(x, y, color.NRGBA{uint8(x * 13), uint8(y * 29), uint8(x + y), 255})
		}
	}
	return source
}

func TestPrepareAnalysisImageSmallBytesAndOriginalRemainExact(t *testing.T) {
	source := analysisPattern(16, 9)
	var jpegData, gifData bytes.Buffer
	if err := jpeg.Encode(&jpegData, source, nil); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gifData, source, nil); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		name, media string
		data        []byte
	}{{"png", "image/png", analysisPNG(t, source)}, {"jpeg", "image/jpeg", jpegData.Bytes()}, {"gif", "image/gif", gifData.Bytes()}}
	for _, fixture := range fixtures {
		for _, detail := range []string{"auto", "original", ""} {
			t.Run(fixture.name+"/"+detail, func(t *testing.T) {
				got, media, info, err := prepareAnalysisImage(context.Background(), fixture.data, fixture.media, detail)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, fixture.data) || &got[0] != &fixture.data[0] || media != fixture.media {
					t.Fatal("small/original image was copied or changed")
				}
				if info.OriginalWidth != 16 || info.OriginalHeight != 9 || info.Width != 16 || info.Height != 9 || info.Resized || info.Bytes != len(fixture.data) || info.OriginalBytes != len(fixture.data) {
					t.Fatalf("metadata = %+v", info)
				}
			})
		}
	}
}

func TestPrepareAnalysisImage4KBoundedKeepsThinStrokesAndOriginal(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 3840, 2160))
	for y := 0; y < 2160; y++ {
		for x := 0; x < 3840; x++ {
			value := uint8(255)
			if x%3 == 0 {
				value = 0
			}
			source.SetNRGBA(x, y, color.NRGBA{value, value, value, 255})
		}
	}
	raw := analysisPNG(t, source)
	before := sha256.Sum256(raw)
	got, media, info, err := prepareAnalysisImage(context.Background(), raw, "image/png", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if media != "image/png" || info.Width != 1280 || info.Height != 720 || info.OriginalWidth != 3840 || info.OriginalHeight != 2160 || !info.Resized || info.Bytes != len(got) {
		t.Fatalf("metadata = %+v, media=%s", info, media)
	}
	decoded, _, err := image.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []image.Point{{0, 0}, {640, 360}, {1279, 719}} {
		r, g, b, a := decoded.At(point.X, point.Y).RGBA()
		if r/257 != 170 || g/257 != 170 || b/257 != 170 || a != 65535 {
			t.Fatalf("thin-stroke coverage lost at %v: %v", point, decoded.At(point.X, point.Y))
		}
	}
	if sha256.Sum256(raw) != before {
		t.Fatal("original encoded pixels changed")
	}
	original, _, originalInfo, err := prepareAnalysisImage(context.Background(), raw, "image/png", "original")
	if err != nil || !bytes.Equal(original, raw) || originalInfo.Resized || originalInfo.Width != 3840 || originalInfo.Height != 2160 {
		t.Fatalf("original precision changed: %+v, %v", originalInfo, err)
	}
}

func TestPrepareAnalysisImageResizedJPEGKeepsJPEGAndOriginalCropLossless(t *testing.T) {
	source := analysisPattern(1536, 1024)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 86}); err != nil {
		t.Fatal(err)
	}
	raw := encoded.Bytes()
	before := sha256.Sum256(raw)
	got, media, info, err := prepareAnalysisImage(context.Background(), raw, "image/jpeg", "auto")
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil || media != "image/jpeg" || format != "jpeg" || !info.Resized || config.Width != info.Width || config.Height != info.Height || info.Width > AnalysisMaxDimension || info.Height > AnalysisMaxDimension || info.Width*info.Height > AnalysisMaxPixels || info.Bytes != len(got) {
		t.Fatalf("JPEG analysis = media=%s format=%s info=%+v config=%+v err=%v", media, format, info, config, err)
	}
	original, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	region := &AnalysisImageRegion{X: 20, Y: 20, Width: 13, Height: 11}
	for _, detail := range []string{"original", "auto"} {
		crop, media, info, err := prepareAnalysisImageRegion(context.Background(), raw, "image/jpeg", detail, region)
		if err != nil {
			t.Fatal(err)
		}
		decoded, format, err := image.Decode(bytes.NewReader(crop))
		if err != nil || media != "image/png" || format != "png" || info.Resized {
			t.Fatalf("precise JPEG crop changed codec/pixels: %s/%s %+v %v", media, format, info, err)
		}
		for y := 0; y < region.Height; y++ {
			for x := 0; x < region.Width; x++ {
				want := color.NRGBAModel.Convert(original.At(x+region.X, y+region.Y))
				actual := color.NRGBAModel.Convert(decoded.At(x, y))
				if actual != want {
					t.Fatalf("JPEG original crop pixel (%d,%d) = %v, want %v", x, y, actual, want)
				}
			}
		}
	}
	if sha256.Sum256(raw) != before {
		t.Fatal("JPEG source bytes were modified")
	}
}

func TestPrepareAnalysisImagePixelBudgetAndAlpha(t *testing.T) {
	raw := analysisPNG(t, analysisPattern(1500, 1500))
	got, _, info, err := prepareAnalysisImage(context.Background(), raw, "image/png", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if info.Width > AnalysisMaxDimension || info.Height > AnalysisMaxDimension || info.Width*info.Height > AnalysisMaxPixels || !info.Resized {
		t.Fatalf("pixel budget = %+v", info)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil || config.Width != info.Width || config.Height != info.Height {
		t.Fatalf("encoded dimensions mismatch: %+v, %v", config, err)
	}
	alpha := image.NewNRGBA(image.Rect(0, 0, 1500, 1))
	for x := 0; x < 1500; x++ {
		pixel := color.NRGBA{255, 0, 0, 255}
		if x >= 751 {
			pixel = color.NRGBA{0, 0, 255, 0}
		}
		alpha.SetNRGBA(x, 0, pixel)
	}
	got, _, _, err = prepareAnalysisImage(context.Background(), analysisPNG(t, alpha), "image/png", "auto")
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	pixel := color.NRGBAModel.Convert(decoded.At(640, 0)).(color.NRGBA)
	if pixel.R != 255 || pixel.B != 0 || pixel.A == 0 || pixel.A == 255 {
		t.Fatalf("transparent edge has halo: %+v", pixel)
	}
}

func TestPrepareAnalysisImageRegionIsExactAndBounded(t *testing.T) {
	source := analysisPattern(8, 6)
	raw := analysisPNG(t, source)
	before := sha256.Sum256(raw)
	region := &AnalysisImageRegion{X: 2, Y: 1, Width: 3, Height: 4}
	for _, detail := range []string{"auto", "original"} {
		got, media, info, err := prepareAnalysisImageRegion(context.Background(), raw, "image/png", detail, region)
		if err != nil {
			t.Fatal(err)
		}
		if media != "image/png" || info.OriginalWidth != 8 || info.OriginalHeight != 6 || info.Width != 3 || info.Height != 4 || info.Resized || info.Region == nil || *info.Region != *region {
			t.Fatalf("crop metadata = %+v", info)
		}
		decoded, _, err := image.Decode(bytes.NewReader(got))
		if err != nil {
			t.Fatal(err)
		}
		for y := 0; y < 4; y++ {
			for x := 0; x < 3; x++ {
				want := source.At(x+2, y+1)
				if color.NRGBAModel.Convert(decoded.At(x, y)) != color.NRGBAModel.Convert(want) {
					t.Fatalf("crop pixel (%d,%d) = %v, want %v", x, y, decoded.At(x, y), want)
				}
			}
		}
		region.X = 3
		if info.Region.X != 2 {
			t.Fatal("metadata retained mutable region pointer")
		}
		region.X = 2
	}
	if sha256.Sum256(raw) != before {
		t.Fatal("crop changed original bytes")
	}
	raw = analysisPNG(t, analysisPattern(2000, 1500))
	region = &AnalysisImageRegion{X: 100, Y: 100, Width: 1500, Height: 1400}
	got, _, info, err := prepareAnalysisImageRegion(context.Background(), raw, "image/png", "auto", region)
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil || config.Width != info.Width || config.Height != info.Height || info.Width*info.Height > AnalysisMaxPixels || !info.Resized || info.OriginalWidth != 2000 || info.Region.Width != 1500 {
		t.Fatalf("bounded crop = %+v, %v", info, err)
	}
}

func TestPrepareAnalysisImageOriginalCropPreserves16BitPixels(t *testing.T) {
	source := image.NewNRGBA64(image.Rect(0, 0, 3, 2))
	source.SetNRGBA64(1, 0, color.NRGBA64{1234, 2345, 3456, 65535})
	source.SetNRGBA64(1, 1, color.NRGBA64{4567, 5678, 6789, 32768})
	raw := analysisPNG(t, source)
	got, _, info, err := prepareAnalysisImageRegion(context.Background(), raw, "image/png", "original", &AnalysisImageRegion{X: 1, Y: 0, Width: 1, Height: 2})
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	if info.Resized || info.Width != 1 || info.Height != 2 {
		t.Fatalf("16-bit crop metadata = %+v", info)
	}
	for y := 0; y < 2; y++ {
		gotColor := color.NRGBA64Model.Convert(decoded.At(0, y)).(color.NRGBA64)
		wantColor := source.NRGBA64At(1, y)
		if gotColor != wantColor {
			t.Fatalf("16-bit crop (%d) = %+v, want %+v", y, gotColor, wantColor)
		}
	}
}

func TestPrepareAnalysisImageRejectsInvalidRegionWithoutClamping(t *testing.T) {
	raw := analysisPNG(t, analysisPattern(8, 6))
	for _, region := range []AnalysisImageRegion{{-1, 0, 1, 1}, {0, -1, 1, 1}, {0, 0, 0, 1}, {0, 0, 1, 0}, {8, 0, 1, 1}, {0, 6, 1, 1}, {7, 0, 2, 1}, {0, 5, 1, 2}, {1, 1, math.MaxInt, math.MaxInt}} {
		_, _, _, err := prepareAnalysisImageRegion(context.Background(), raw, "image/png", "auto", &region)
		if err == nil {
			t.Fatalf("accepted region %+v", region)
		}
	}
}

func analysisPNGConfig(width, height uint32) []byte {
	var data bytes.Buffer
	data.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
	payload := make([]byte, 13)
	binary.BigEndian.PutUint32(payload, width)
	binary.BigEndian.PutUint32(payload[4:], height)
	payload[8], payload[9] = 8, 2
	binary.Write(&data, binary.BigEndian, uint32(13))
	data.WriteString("IHDR")
	data.Write(payload)
	hash := crc32.NewIEEE()
	hash.Write([]byte("IHDR"))
	hash.Write(payload)
	binary.Write(&data, binary.BigEndian, hash.Sum32())
	return data.Bytes()
}
func TestPrepareAnalysisImageLimitsAndLegacyHeader(t *testing.T) {
	_, _, _, err := prepareAnalysisImage(context.Background(), make([]byte, AnalysisMaxEncodedBytes+1), "image/png", "auto")
	if err == nil || !strings.Contains(err.Error(), "encoded limit") {
		t.Fatalf("encoded guard = %v", err)
	}
	_, _, _, err = prepareAnalysisImage(context.Background(), analysisPNGConfig(32769, 1024), "image/png", "auto")
	if err == nil || !strings.Contains(err.Error(), "decoded limit") {
		t.Fatalf("decoded guard = %v", err)
	}
	_, _, _, err = prepareAnalysisImage(context.Background(), []byte("legacy"), "image/png", "unexpected")
	if err == nil {
		t.Fatal("invalid detail accepted")
	}
	header := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	got, media, info, err := prepareAnalysisImage(context.Background(), header, "image/png", "auto")
	if err != nil || !bytes.Equal(got, header) || media != "image/png" || info.Width != 0 {
		t.Fatalf("legacy header compatibility = %+v, %v", info, err)
	}
	webp := []byte("RIFF0000WEBPVP8X0000000000")
	got, media, info, err = prepareAnalysisImage(context.Background(), webp, "image/webp", "auto")
	if err != nil || !bytes.Equal(got, webp) || media != "image/webp" || info.Width != 0 {
		t.Fatalf("unsupported WebP decoder compatibility = %+v, %v", info, err)
	}
	_, _, _, err = prepareAnalysisImageRegion(context.Background(), webp, "image/webp", "auto", &AnalysisImageRegion{Width: 1, Height: 1})
	if err == nil {
		t.Fatal("undecodable crop was falsely accepted")
	}
}

func TestPrepareAnalysisImageCancellationAndEncodingLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := prepareAnalysisImage(ctx, []byte("legacy"), "image/png", "auto")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	var buffer bytes.Buffer
	writer := &analysisContextWriter{ctx: ctx, buffer: &buffer, limit: 100}
	if _, err := writer.Write([]byte("payload")); !errors.Is(err, context.Canceled) || buffer.Len() != 0 {
		t.Fatalf("canceled encoder wrote bytes: %v", err)
	}
	writer = &analysisContextWriter{ctx: context.Background(), buffer: &buffer, limit: 3}
	if _, err := writer.Write([]byte("four")); err == nil || buffer.Len() != 0 {
		t.Fatal("encoder overflow wrote bytes")
	}
}

func BenchmarkPrepareAnalysisImage4K(b *testing.B) {
	source := analysisPattern(3840, 2160)
	pngData := analysisPNG(b, source)
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, source, &jpeg.Options{Quality: 86}); err != nil {
		b.Fatal(err)
	}
	for _, fixture := range []struct {
		name, media string
		data        []byte
	}{{"png", "image/png", pngData}, {"jpeg", "image/jpeg", jpegData.Bytes()}} {
		for _, detail := range []string{"auto", "original"} {
			b.Run(fixture.name+"/"+detail, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(fixture.data)))
				for i := 0; i < b.N; i++ {
					got, _, info, err := prepareAnalysisImage(context.Background(), fixture.data, fixture.media, detail)
					if err != nil {
						b.Fatal(err)
					}
					if len(got) != info.Bytes {
						b.Fatal("invalid output metadata")
					}
					b.ReportMetric(float64(len(got)), "out-B")
					b.ReportMetric(float64(info.Width), "width-px")
					b.ReportMetric(float64(info.Height), "height-px")
				}
			})
		}
	}
}
