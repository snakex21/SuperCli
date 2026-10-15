package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestAnthropicUnknownMetadataSendsActualAttachmentAndToolPixels(t *testing.T) {
	for _, source := range []Source{SourceExternal, SourceProbe, SourceProvider, SourceUnknown} {
		for _, toolImage := range []bool{false, true} {
			t.Run(source.String()+map[bool]string{false: "/attachment", true: "/tool"}[toolImage], func(t *testing.T) {
				img := image.NewRGBA(image.Rect(0, 0, 16, 16))
				img.SetRGBA(7, 9, color.RGBA{R: 211, G: 31, B: 47, A: 255})
				var pixels bytes.Buffer
				if err := png.Encode(&pixels, img); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "fixture.png")
				if err := os.WriteFile(path, pixels.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
				imageRef := &ImageRef{Path: path, MediaType: "image/png", Active: true}
				user := Message{Role: RoleUser, Parts: []ContentPart{{Type: PartTypeText, Text: "Inspect the supplied pixels."}, {Type: PartTypeImage, Image: imageRef}}}
				messages := []Message{user}
				if toolImage {
					imageRef.ToolOutputCarrier = true
					imageRef.SourceToolCallID = "read-image"
					messages = []Message{{Role: RoleUser, Content: "Inspect the fixture image."}, {Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "read-image", Name: "read_image", Arguments: `{"path":"fixture.png"}`}}}, {Role: RoleTool, ToolCallID: "read-image", Name: "read_image", Content: "Loaded image fixture.png"}, user}
				}
				requests := 0
				var captured *string
				server, captured := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
					requests++
					var request anthropicRequest
					if err := json.Unmarshal([]byte(*captured), &request); err != nil {
						t.Fatal(err)
					}
					found := false
					for _, message := range request.Messages {
						for _, block := range message.Content {
							if block.Type == "image" {
								found = true
								if block.Source == nil || block.Source.Type != "base64" || block.Source.MediaType != "image/png" {
									t.Fatal("incorrect Anthropic image source")
								}
								raw, err := base64.StdEncoding.DecodeString(block.Source.Data)
								if err != nil {
									t.Fatal(err)
								}
								if !bytes.Equal(raw, pixels.Bytes()) {
									t.Fatal("image bytes changed or were replaced with a path")
								}
								decoded, err := png.Decode(bytes.NewReader(raw))
								if err != nil {
									t.Fatal(err)
								}
								if decoded.Bounds() != img.Bounds() {
									t.Fatal("decoded image dimensions changed")
								}
							}
						}
					}
					if !found {
						t.Error("unknown metadata silently removed the image")
					}
					sseResponse(w, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"fixture accepted"}}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`)
				})
				caps := NewCapabilityRegistry()
				caps.Register(ModelInfo{ID: "new-vision-model", Source: source})
				provider, err := NewAnthropic(AnthropicConfig{BaseURL: server.URL, Model: "new-vision-model", Capabilities: caps})
				if err != nil {
					t.Fatal(err)
				}
				if !provider.SupportsVision() {
					t.Fatal("unknown dynamic metadata reported confirmed text-only")
				}
				stream, err := provider.Complete(context.Background(), messages, nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, delta := range drainDeltas(t, stream) {
					if delta.Err != nil {
						t.Fatal(delta.Err)
					}
				}
				if requests != 1 {
					t.Fatalf("requests=%d; want one image request", requests)
				}
			})
		}
	}
}

func TestVisionAttemptKeepsExplicitTextOnlyMetadataAndUserOverrides(t *testing.T) {
	for _, source := range []Source{SourceExternal, SourceProbe, SourceProvider, SourceUnknown, SourceCatalog, SourceSeed} {
		caps := NewCapabilityRegistry()
		caps.Register(ModelInfo{ID: "explicit-text", Source: source, VisionKnown: true})
		if caps.AllowsVisionAttempt("explicit-text") {
			t.Fatalf("explicit false ignored for %v", source)
		}
	}
	for _, source := range []Source{SourceCatalog, SourceSeed} {
		caps := NewCapabilityRegistry()
		caps.Register(ModelInfo{ID: "legacy-text", Source: source})
		if caps.AllowsVisionAttempt("legacy-text") {
			t.Fatalf("legacy user/seed override ignored: %v", source)
		}
	}
}
