package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

type toolImageFixtureWriter struct {
	ref       llm.ImageRef
	err       error
	calls     int
	mediaType string
	raw       []byte
}

func (*toolImageFixtureWriter) AppendMessage(context.Context, llm.Message) error { return nil }
func (*toolImageFixtureWriter) UpdateUsage(int, int) error                       { return nil }
func (w *toolImageFixtureWriter) ExternalizeImage(_ context.Context, media string, raw []byte) (llm.ImageRef, error) {
	w.calls++
	w.mediaType = media
	w.raw = bytes.Clone(raw)
	return w.ref, w.err
}

type toolImagePlainWriter struct{}

func (toolImagePlainWriter) AppendMessage(context.Context, llm.Message) error { return nil }
func (toolImagePlainWriter) UpdateUsage(int, int) error                       { return nil }

func TestToolImageRefPreservesExternalizedRepresentation(t *testing.T) {
	raw := []byte{0x89, 'P', 'N', 'G', 0, '\r', '\n', 0xff, 0xfe}
	tests := []struct {
		name string
		ref  llm.ImageRef
	}{
		{"file", llm.ImageRef{MediaType: "image/png", Path: "session-media/original.png", ID: "img_original", Name: "saved name"}},
		{"inline", llm.ImageRef{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(raw), ID: "img_inline"}},
		{"url", llm.ImageRef{URL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw), ID: "img_url"}},
		{"all_fields", llm.ImageRef{URL: "https://fixture.invalid/image.png", MediaType: "image/png", Data: "saved-data", Path: "saved-path", ID: "saved-id", Name: "saved-name", Active: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &toolImageFixtureWriter{ref: tt.ref}
			loop := &Loop{writer: writer}
			image := &tools.ImageContent{MediaType: "image/png", Data: bytes.Clone(raw)}
			got := loop.toolImageRef(context.Background(), "fixture_image", image)
			want := tt.ref
			want.Name = "tool fixture_image"
			want.Active = true
			if !reflect.DeepEqual(*got, want) {
				t.Fatalf("image = %+v, want %+v", *got, want)
			}
			if writer.calls != 1 || writer.mediaType != image.MediaType || !bytes.Equal(writer.raw, raw) {
				t.Fatalf("externalizer did not receive exact input: %+v", writer)
			}
			if writer.ref != tt.ref || !bytes.Equal(image.Data, raw) {
				t.Fatal("image conversion mutated its input or the stored reference")
			}
		})
	}
}

func TestToolImageRefFallbackRetainsOriginalBytes(t *testing.T) {
	raw := []byte{0x89, 'P', 'N', 'G', 0, '\r', '\n', 0xff, 0xfe}
	saveErr := errors.New("fixture storage failure")
	var nilDurableWriter *session.Writer
	tests := []struct {
		name   string
		writer SessionWriter
	}{
		{"no_writer", nil},
		{"plain_writer", toolImagePlainWriter{}},
		{"nil_durable_writer", nilDurableWriter},
		{"storage_error", &toolImageFixtureWriter{err: saveErr}},
		{"error_with_ref", &toolImageFixtureWriter{err: saveErr, ref: llm.ImageRef{MediaType: "image/png", Path: "ignored-path", ID: "ignored-id"}}},
		{"empty_ref", &toolImageFixtureWriter{}},
		{"only_id", &toolImageFixtureWriter{ref: llm.ImageRef{ID: "img_incomplete"}}},
		{"only_media", &toolImageFixtureWriter{ref: llm.ImageRef{MediaType: "image/png"}}},
		{"path_without_media", &toolImageFixtureWriter{ref: llm.ImageRef{Path: "incomplete.png", ID: "img_incomplete"}}},
		{"data_without_media", &toolImageFixtureWriter{ref: llm.ImageRef{Data: "incomplete"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image := &tools.ImageContent{MediaType: "image/png", Data: bytes.Clone(raw)}
			got := (&Loop{writer: tt.writer}).toolImageRef(context.Background(), "fixture_image", image)
			want := llm.ImageRef{
				MediaType: image.MediaType, Data: base64.StdEncoding.EncodeToString(raw),
				Name: "tool fixture_image", Active: true,
			}
			if *got != want {
				t.Fatalf("fallback image = %+v, want %+v", *got, want)
			}
			decoded, err := base64.StdEncoding.DecodeString(got.Data)
			if err != nil || !bytes.Equal(decoded, raw) || !bytes.Equal(image.Data, raw) {
				t.Fatalf("fallback lost or changed pixels: %v", err)
			}
			if writer, ok := tt.writer.(*toolImageFixtureWriter); ok {
				if writer.calls != 1 || writer.mediaType != image.MediaType || !bytes.Equal(writer.raw, raw) {
					t.Fatalf("externalizer did not receive exact input: %+v", writer)
				}
			}
		})
	}
}

func toolImageFixtureInvoke(t testing.TB, writer SessionWriter, result tools.Result, executionErr error) (toolResult, []Event) {
	t.Helper()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name: "fixture_image", Description: "image fixture", Schema: "{}", ReadOnly: true,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return result, executionErr },
	})
	loop, err := NewLoop(LoopConfig{
		Provider: &stubProvider{name: "fixture"}, Registry: reg, Writer: writer, MaxSteps: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 8)
	got := loop.invoke(context.Background(), llm.ToolCall{ID: "image1", Name: "fixture_image", Arguments: "{}"}, events)
	var collected []Event
	for len(events) > 0 {
		collected = append(collected, <-events)
	}
	return got, collected
}

func TestToolImageExternalizeDoesNotHideToolErrors(t *testing.T) {
	for _, executionError := range []bool{false, true} {
		t.Run(fmt.Sprintf("execution_error=%t", executionError), func(t *testing.T) {
			wantErr := errors.New("fixture tool failure")
			result := tools.Result{Text: "image failed", Image: &tools.ImageContent{MediaType: "image/png", Data: []byte("original pixels")}}
			var executionErr error
			if executionError {
				executionErr = wantErr
			} else {
				result.Err = wantErr
			}
			writer := &toolImageFixtureWriter{ref: llm.ImageRef{MediaType: "image/png", Path: "ignored.png"}}
			got, events := toolImageFixtureInvoke(t, writer, result, executionErr)
			if !got.failed || len(got.followUps) != 1 || got.followUps[0].HasImage() || writer.calls != 0 {
				t.Fatalf("failed tool changed image/error behavior: result=%+v, writer=%+v", got, writer)
			}
			var resultEvent *ToolResultEvent
			for _, event := range events {
				if e, ok := event.(ToolResultEvent); ok {
					copy := e
					resultEvent = &copy
				}
			}
			if resultEvent == nil || !errors.Is(resultEvent.Err, wantErr) {
				t.Fatalf("tool error not preserved: %+v", resultEvent)
			}
		})
	}
}

type toolImageWireRoundTripper func(*http.Request) (*http.Response, error)

func (f toolImageWireRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Capture the real provider request through its injected transport. No server,
// network, model, or token generation is used.
func toolImageFixtureWire(t *testing.T, providerName string, messages []llm.Message) []byte {
	t.Helper()
	captureErr := errors.New("fixture request captured")
	var body []byte
	client := &http.Client{Transport: toolImageWireRoundTripper(func(req *http.Request) (*http.Response, error) {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		return nil, captureErr
	})}
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "fixture-vision", Vision: true, VisionKnown: true, ToolUse: true, Stream: true})
	var provider llm.Provider
	var err error
	switch providerName {
	case "openai":
		cachePrompt := false
		provider, err = llm.NewOpenAI(llm.OpenAIConfig{
			BaseURL: "http://fixture.invalid/v1", Model: "fixture-vision",
			HTTPClient: client, Capabilities: caps, CachePrompt: &cachePrompt,
		})
	case "anthropic":
		provider, err = llm.NewAnthropic(llm.AnthropicConfig{
			BaseURL: "http://fixture.invalid", APIKey: "fixture", Model: "fixture-vision",
			HTTPClient: client, Capabilities: caps,
		})
	default:
		t.Fatalf("unsupported fixture provider: %q", providerName)
	}
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.Complete(context.Background(), messages, nil)
	if err != nil && !errors.Is(err, captureErr) {
		t.Fatalf("provider failed before capture: %v", err)
	}
	if stream != nil {
		for delta := range stream {
			if delta.Err != nil && !errors.Is(delta.Err, captureErr) {
				t.Fatalf("provider failed before capture: %v", delta.Err)
			}
		}
	}
	if len(body) == 0 {
		t.Fatal("provider did not send an image request")
	}
	return body
}

func TestToolImageExternalizeDurableStoreAndProviderWire(t *testing.T) {
	home := t.TempDir()
	store, err := session.OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(home, "fixture-vision", "tool image")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	raw := append([]byte{0x89, 'P', 'N', 'G', 0, '\r', '\n'}, bytes.Repeat([]byte{0x00, 0xff, 0xfe, 0x01, 0x7f}, 1700)...)
	for _, mediaType := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
		t.Run(mediaType, func(t *testing.T) {
			got, _ := toolImageFixtureInvoke(t, writer, tools.Result{
				Text: "captured", Image: &tools.ImageContent{MediaType: mediaType, Data: raw},
			}, nil)
			if got.failed || len(got.followUps) != 2 || len(got.followUps[1].Parts) != 2 {
				t.Fatalf("successful image result: %+v", got)
			}
			img := got.followUps[1].Parts[1].Image
			sum := sha256.Sum256(raw)
			if img == nil || img.MediaType != mediaType || img.ID != fmt.Sprintf("img_%x", sum[:6]) ||
				img.Path == "" || img.Data != "" || img.URL != "" || !img.Active || img.Name != "tool fixture_image" {
				t.Fatalf("durable image metadata = %+v", img)
			}
			relative, err := filepath.Rel(home, img.Path)
			if err != nil || !filepath.IsLocal(relative) {
				t.Fatalf("image escaped portable store: %q, %v", img.Path, err)
			}
			stored, err := os.ReadFile(img.Path)
			if err != nil || !bytes.Equal(stored, raw) {
				t.Fatalf("stored image changed: %v", err)
			}
			durable := got.followUps[1]
			inline := durable
			inline.Parts = append([]llm.ContentPart(nil), durable.Parts...)
			inline.Parts[1].Image = &llm.ImageRef{
				MediaType: mediaType, Data: base64.StdEncoding.EncodeToString(raw),
				Name: img.Name, Active: true,
			}
			for _, providerName := range []string{"openai", "anthropic"} {
				t.Run(providerName, func(t *testing.T) {
					before := toolImageFixtureWire(t, providerName, []llm.Message{inline})
					after := toolImageFixtureWire(t, providerName, []llm.Message{durable})
					if !bytes.Equal(before, after) {
						t.Fatalf("provider request changed after externalization:\nbefore=%s\nafter=%s", before, after)
					}
					if !bytes.Contains(after, []byte(base64.StdEncoding.EncodeToString(raw))) {
						t.Fatal("provider request does not contain the exact original pixels")
					}
				})
			}
		})
	}
}
