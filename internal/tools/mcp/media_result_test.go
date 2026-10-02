package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

var mcpPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")

func TestDecodeToolResultNativeImagesAndText(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"content": []map[string]string{
		{"type": "text", "text": "before"},
		{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(mcpPNG)},
		{"type": "text", "text": "after"},
		{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(mcpPNG)},
	}})
	res, err := decodeToolResult(raw)
	if err != nil || res.Text != "beforeafter" || len(res.Images) != 2 {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	for _, img := range res.Images {
		if img.MediaType != "image/png" || !bytes.Equal(img.Data, mcpPNG) {
			t.Fatal("image bytes changed")
		}
	}
	if strings.Contains(res.Text, base64.StdEncoding.EncodeToString(mcpPNG)) {
		t.Fatal("binary bytes leaked into text")
	}
}

func TestDecodeToolResultStructuredOnlyAndError(t *testing.T) {
	for _, raw := range []string{`{"structuredContent":{"ok":true}}`, `{"content":[{"type":"text","text":"{\"ok\":true}"}],"structuredContent":{"ok":true},"isError":true}`} {
		res, err := decodeToolResult(json.RawMessage(raw))
		if err != nil || res.Text != `{"ok":true}` {
			t.Fatalf("%+v %v", res, err)
		}
		if strings.Contains(raw, `"isError":true`) && !res.IsError {
			t.Fatal("error lost")
		}
	}
}

func TestDecodeToolResultPreservesDistinctStructuredContent(t *testing.T) {
	tests := []struct {
		name       string
		texts      []string
		structured string
		want       string
	}{
		{
			name:       "prose and structured data",
			texts:      []string{"Found one result"},
			structured: `{"items":[{"id":42}]}`,
			want:       "Found one result\n" + `{"items":[{"id":42}]}`,
		},
		{
			name:       "distinct JSON values",
			texts:      []string{`{"count":1}`},
			structured: `{"count":2}`,
			want:       "{\"count\":1}\n{\"count\":2}",
		},
		{
			name:       "equivalent whitespace and nested key order",
			texts:      []string{" {\n  \"items\": [{\"b\":2, \"a\":1}], \"ok\": true\n} "},
			structured: `{"ok":true,"items":[{"a":1,"b":2}]}`,
			want:       " {\n  \"items\": [{\"b\":2, \"a\":1}], \"ok\": true\n} ",
		},
		{
			name:       "duplicate JSON block beside prose",
			texts:      []string{"Summary\n", `{"ok":true}`},
			structured: `{"ok":true}`,
			want:       "Summary\n" + `{"ok":true}`,
		},
		{
			name:       "duplicate JSON split across blocks",
			texts:      []string{`{"ok":`, `true}`},
			structured: `{"ok":true}`,
			want:       `{"ok":true}`,
		},
		{
			name:       "distinct integers above float64 precision",
			texts:      []string{`{"id":9007199254740992}`},
			structured: `{"id":9007199254740993}`,
			want:       "{\"id\":9007199254740992}\n{\"id\":9007199254740993}",
		},
		{
			name:       "JSON prefix followed by prose",
			texts:      []string{`{"ok":true} additional text`},
			structured: `{"ok":true}`,
			want:       "{\"ok\":true} additional text\n{\"ok\":true}",
		},
		{
			name:       "JSON prefix followed by another value",
			texts:      []string{`{"ok":true} {"other":true}`},
			structured: `{"ok":true}`,
			want:       "{\"ok\":true} {\"other\":true}\n{\"ok\":true}",
		},
		{
			name:       "array order is significant",
			texts:      []string{`{"items":[1,2]}`},
			structured: `{"items":[2,1]}`,
			want:       "{\"items\":[1,2]}\n{\"items\":[2,1]}",
		},
		{
			name:       "null structured content",
			texts:      []string{"Summary"},
			structured: `null`,
			want:       "Summary",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var content []map[string]string
			for _, text := range tt.texts {
				content = append(content, map[string]string{"type": "text", "text": text})
			}
			raw, err := json.Marshal(map[string]any{
				"content":           content,
				"structuredContent": json.RawMessage(tt.structured),
				"isError":           true,
			})
			if err != nil {
				t.Fatal(err)
			}
			res, err := decodeToolResult(raw)
			if err != nil {
				t.Fatal(err)
			}
			if res.Text != tt.want {
				t.Fatalf("text = %q, want %q", res.Text, tt.want)
			}
			if !res.IsError {
				t.Fatal("error flag lost")
			}
		})
	}
}

func TestDecodeToolResultRejectsInvalidMedia(t *testing.T) {
	for _, image := range []map[string]string{
		{"type": "image", "mimeType": "image/png", "data": "not base64"},
		{"type": "image", "mimeType": "image/svg+xml", "data": base64.StdEncoding.EncodeToString([]byte("<svg></svg>"))},
		{"type": "image", "mimeType": "image/jpeg", "data": base64.StdEncoding.EncodeToString(mcpPNG)},
		{"type": "image", "mimeType": "image/png", "data": strings.Repeat("A", base64.StdEncoding.EncodedLen(maxMCPImageBytes)+4)},
	} {
		raw, _ := json.Marshal(map[string]any{"content": []any{image}})
		if _, err := decodeToolResult(raw); err == nil {
			t.Fatal("accepted invalid image")
		}
	}
	image := map[string]string{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(mcpPNG)}
	var images []any
	for i := 0; i <= maxMCPImages; i++ {
		images = append(images, image)
	}
	raw, _ := json.Marshal(map[string]any{"content": images})
	if _, err := decodeToolResult(raw); err == nil {
		t.Fatal("accepted unbounded image count")
	}
}

func BenchmarkMCPFragmentedText(b *testing.B) {
	for _, n := range []int{100, 1000} {
		for _, baseline := range []bool{true, false} {
			arm := "optimized"
			if baseline {
				arm = "baseline"
			}
			b.Run(fmt.Sprint(n)+"/"+arm, func(b *testing.B) {
				var blocks []map[string]string
				for i := 0; i < n; i++ {
					blocks = append(blocks, map[string]string{"type": "text", "text": strings.Repeat("x", 128)})
				}
				raw, _ := json.Marshal(map[string]any{"content": blocks})
				decode := decodeToolResult
				if baseline {
					decode = decodeToolResultBaseline
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					res, err := decode(raw)
					if err != nil || len(res.Text) != n*128 {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
