package llm

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fileImageEncodingFixture(tb testing.TB, size int) *ImageRef {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "synthetic-image.bin")
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		tb.Fatal(err)
	}
	return &ImageRef{MediaType: "image/png", Path: path, Active: true}
}

func TestFileImageEncodingPreservesSemantics(t *testing.T) {
	for _, size := range []int{0, 1, 2, 3, 4, 255, 256, 1022, 1023, 1024, 1025, 22718, 84084, 348258, 699020} {
		img := fileImageEncodingFixture(t, size)
		original := *img
		got, err := resolveImageURL(img)
		want, wantErr := previousImageURL(img)
		if got != want || imageEncodingError(err) != imageEncodingError(wantErr) {
			t.Fatalf("size=%d URI or error changed", size)
		}
		if *img != original {
			t.Fatal("image ref mutated on first call")
		}
		if _, err := resolveImageURL(img); imageEncodingError(err) != imageEncodingError(wantErr) || *img != original {
			t.Fatal("image ref mutated")
		}
	}
	missing := filepath.Join(t.TempDir(), "missing.png")
	img := fileImageEncodingFixture(t, 13)
	for _, candidate := range []*ImageRef{
		nil, {},
		{Path: missing}, {Path: missing, MediaType: "image/png"},
		{Path: img.Path},
		{URL: "https://example.invalid/one", Path: missing},
		{URL: "data:image/png;base64,already", Data: "ignored", Path: missing},
		{Data: "not-base64", MediaType: "image/png", Path: missing},
		{Data: "already-base64", Path: missing},
		{MediaType: "invalid ☃ type", Path: img.Path},
		{MediaType: strings.Repeat("m", 1<<16), Path: img.Path},
	} {
		got, err := resolveImageURL(candidate)
		want, wantErr := previousImageURL(candidate)
		if got != want || imageEncodingError(err) != imageEncodingError(wantErr) {
			t.Fatal("precedence, incomplete input or read error changed")
		}
	}
}

func imageEncodingError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func fileImageEncodingMessages(img *ImageRef) []Message {
	return []Message{
		{Role: RoleSystem, Content: "Fixture stable policy."},
		{Role: RoleUser, Parts: []ContentPart{{Type: PartTypeText, Text: "Inspect this synthetic image."}, {Type: PartTypeImage, Image: img}}},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "fixture-call", Name: "read_lines", Arguments: "{\"file\":\"fixture.go\",\"from\":1,\"to\":4}"}}},
		{Role: RoleTool, Name: "read_lines", ToolCallID: "fixture-call", Content: "Fixture read-only result."},
		{Role: RoleSystem, Content: "Fixture trailing context."},
	}
}

func TestFileImageEncodingPreservesProviderBodies(t *testing.T) {
	defs := []ToolDef{{Name: "read_lines", Description: "Read bounded source lines.", Schema: "{\"type\":\"object\",\"properties\":{\"file\":{\"type\":\"string\"},\"from\":{\"type\":\"integer\"},\"to\":{\"type\":\"integer\"}},\"required\":[\"file\"]}"}}
	for _, size := range []int{22718, 84084, 348258, 699020} {
		img := fileImageEncodingFixture(t, size)
		referenceURI, err := previousImageURL(img)
		if err != nil {
			t.Fatal(err)
		}
		reference := *img
		reference.URL = referenceURI
		for _, format := range []string{"openai", "responses", "anthropic"} {
			build := func(image *ImageRef) ([]byte, error) {
				messages := fileImageEncodingMessages(image)
				switch format {
				case "openai":
					return buildOpenAIRequest("fixture", messages, defs, true, true)
				case "responses":
					return buildCodexRequestWithEffort("fixture", messages, defs, true, "")
				default:
					return buildAnthropicRequestWithSampling("fixture", messages, defs, true, 256, Sampling{})
				}
			}
			got, err := build(img)
			want, wantErr := build(&reference)
			if err != nil || wantErr != nil || !bytes.Equal(got, want) {
				t.Fatalf("%s/%d body or error differs", format, size)
			}
		}
	}
}

var fileImageURLSink string
var fileImageBodySink []byte

func BenchmarkFileImageEncoding(b *testing.B) {
	for _, size := range []int{22718, 84084, 348258, 699020} {
		for _, mode := range []string{"url", "request"} {
			b.Run(fmt.Sprintf("%d/%s", size, mode), func(b *testing.B) {
				img := fileImageEncodingFixture(b, size)
				messages := fileImageEncodingMessages(img)
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if mode == "url" {
						url, err := resolveImageURL(img)
						if err != nil {
							b.Fatal(err)
						}
						fileImageURLSink = url
					} else {
						body, err := buildOpenAIRequest("fixture", messages, nil, true, true)
						if err != nil {
							b.Fatal(err)
						}
						fileImageBodySink = body
					}
				}
			})
		}
	}
}

func previousImageURL(img *ImageRef) (string, error) {
	if img == nil {
		return "", fmt.Errorf("image part with nil Image")
	}
	if img.URL != "" {
		return img.URL, nil
	}
	data := img.Data
	if data == "" && img.Path != "" {
		buf, err := os.ReadFile(img.Path)
		if err != nil {
			return "", fmt.Errorf("read image %q: %w", img.Path, err)
		}
		data = base64.StdEncoding.EncodeToString(buf)
	}
	if img.MediaType == "" || data == "" {
		return "", fmt.Errorf("image part: incomplete (need URL, Data, or Path+MediaType)")
	}
	return "data:" + img.MediaType + ";base64," + data, nil
}
