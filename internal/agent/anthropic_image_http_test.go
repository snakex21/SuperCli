package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

// Exercise attachment admission, real read_image execution, optional durable
// storage, lazy activation and the native Anthropic HTTP transport together.
// A mocked SSE endpoint inspects actual encoded pixels; no model or API key is
// needed. In particular, a tool image must not split a parallel result batch.
func TestAnthropicAgentAttachmentAndToolImagesReachHTTP(t *testing.T) {
	for _, durable := range []bool{false, true} {
		for _, count := range []int{1, 2} {
			t.Run(fmt.Sprintf("durable=%t/tool_images=%d", durable, count), func(t *testing.T) {
				home := t.TempDir()
				pixels := make([][]byte, count)
				paths := make([]string, count)
				for i := range pixels {
					img := image.NewRGBA(image.Rect(0, 0, 16, 16))
					img.SetRGBA(7, 9, color.RGBA{R: uint8(190 + i), G: 31, B: 47, A: 255})
					var encoded bytes.Buffer
					if err := png.Encode(&encoded, img); err != nil {
						t.Fatal(err)
					}
					pixels[i] = encoded.Bytes()
					paths[i] = fmt.Sprintf("obraz_żółć_%d.png", i)
					if err := os.WriteFile(filepath.Join(home, paths[i]), pixels[i], 0600); err != nil {
						t.Fatal(err)
					}
				}

				var mu sync.Mutex
				var requests []anthropicImageHTTPFixtureRequest
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request anthropicImageHTTPFixtureRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Errorf("request JSON: %v", err)
						http.Error(w, "invalid fixture request", http.StatusBadRequest)
						return
					}
					mu.Lock()
					requests = append(requests, request)
					index := len(requests)
					mu.Unlock()
					w.Header().Set("Content-Type", "text/event-stream")
					emit := func(event any) {
						data, err := json.Marshal(event)
						if err != nil {
							t.Errorf("SSE fixture: %v", err)
							return
						}
						fmt.Fprintf(w, "data: %s\n\n", data)
					}
					if index == 1 {
						for i, path := range paths {
							emit(map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{
								"type": "tool_use", "id": fmt.Sprintf("read_%d", i), "name": "read_image", "input": map[string]any{"path": path},
							}})
							emit(map[string]any{"type": "content_block_stop", "index": i})
						}
						emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}})
					} else {
						emit(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Fixture image inspected."}})
						emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}})
					}
					emit(map[string]any{"type": "message_stop"})
				}))
				defer server.Close()
				caps := llm.NewCapabilityRegistry()
				// Like a new official /models listing or a text-only probe: the
				// model exists, but omission of modalities proves nothing about vision.
				const model = "claude-opus-5-5"
				caps.Register(llm.ModelInfo{ID: model, Source: llm.SourceExternal})
				provider, err := llm.NewAnthropic(llm.AnthropicConfig{BaseURL: server.URL, Model: model, Capabilities: caps, HTTPClient: server.Client()})
				if err != nil {
					t.Fatal(err)
				}
				reg := tools.NewRegistry()
				reg.MustRegister(tools.NewReadImage(home, 0).Spec())
				reg.MarkAlwaysOn("read_image")
				cfg := LoopConfig{Provider: provider, Caps: caps, Registry: reg, BaseDir: home, MaxSteps: 4}
				var store *session.Store
				if durable {
					store, err = session.OpenStore(filepath.Join(home, "portable-data"))
					if err != nil {
						t.Fatal(err)
					}
					defer store.Close()
					sess, err := store.Create(home, model, "image HTTP fixture")
					if err != nil {
						t.Fatal(err)
					}
					cfg.Writer = session.NewWriter(store, sess.ID)
				}
				loop, err := NewLoop(cfg)
				if err != nil {
					t.Fatal(err)
				}
				loop.SetNextUserImages([]llm.ImageRef{{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(pixels[0]), Name: paths[0]}})
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				run := func(prompt string) {
					events, err := loop.Run(ctx, prompt)
					if err != nil {
						t.Fatal(err)
					}
					done, results := false, 0
					for event := range events {
						switch e := event.(type) {
						case ErrorEvent:
							t.Fatalf("loop error: %v", e.Err)
						case ToolResultEvent:
							if e.Err != nil {
								t.Fatalf("tool error: %v", e.Err)
							}
							results++
						case DoneEvent:
							done = true
						}
					}
					if !done || (prompt == "Inspect the attached image and the repository images." && results != count) {
						t.Fatalf("done=%t tool results=%d, want %d", done, results, count)
					}
				}
				run("Inspect the attached image and the repository images.")
				mu.Lock()
				firstRun := append([]anthropicImageHTTPFixtureRequest(nil), requests...)
				mu.Unlock()
				if len(firstRun) != 2 {
					t.Fatalf("HTTP requests=%d; want attachment -> tool pixels -> answer", len(firstRun))
				}
				assertAnthropicImageHTTPPixels(t, firstRun[0], pixels[:1])
				assertAnthropicImageHTTPPixels(t, firstRun[1], pixels)
				for i := 0; i < count; i++ {
					id := fmt.Sprintf("read_%d", i)
					uses, results := 0, 0
					for _, message := range firstRun[1].Messages {
						for _, block := range message.Content {
							if block.Type == "tool_use" && block.ID == id {
								uses++
								if message.Role != "assistant" {
									t.Fatal("tool_use lost assistant role")
								}
							}
							if block.Type == "tool_result" && block.ToolUseID == id {
								results++
								if message.Role != "user" || !strings.Contains(block.Content, paths[i]) {
									t.Fatal("tool_result lost its user role or original source path")
								}
							}
						}
					}
					if uses != 1 || results != 1 {
						t.Fatalf("tool %s: use=%d result=%d; image carrier split the batch", id, uses, results)
					}
				}
				for _, message := range loop.Messages {
					for _, part := range message.Parts {
						if part.Image == nil {
							continue
						}
						if part.Image.Active {
							t.Fatal("pixels remained active after the accepted request")
						}
						if durable {
							rel, err := filepath.Rel(filepath.Join(home, "portable-data"), part.Image.Path)
							if err != nil || !filepath.IsLocal(rel) || part.Image.Data != "" {
								t.Fatal("durable image was not stored lazily inside portable data")
							}
						}
					}
				}
				run("Continue with your findings.")
				mu.Lock()
				all := append([]anthropicImageHTTPFixtureRequest(nil), requests...)
				mu.Unlock()
				if len(all) != 3 {
					t.Fatalf("continuation requests=%d, want exactly one additional call", len(all))
				}
				assertAnthropicImageHTTPPixels(t, all[2], nil)
			})
		}
	}
}

type anthropicImageHTTPFixtureRequest struct {
	Messages []struct {
		Role    string
		Content []struct {
			Type, ID, Text, Content string
			ToolUseID               string `json:"tool_use_id"`
			Source                  *struct {
				Type, Data string
				MediaType  string `json:"media_type"`
			}
		}
	}
}

func assertAnthropicImageHTTPPixels(t *testing.T, request anthropicImageHTTPFixtureRequest, want [][]byte) {
	t.Helper()
	var got [][]byte
	for _, message := range request.Messages {
		for _, block := range message.Content {
			if block.Type != "image" {
				if strings.Contains(block.Text, "does not support images") {
					t.Fatal("unknown metadata substituted an unsupported-image claim")
				}
				continue
			}
			if message.Role != "user" || block.Source == nil || block.Source.Type != "base64" || block.Source.MediaType != "image/png" {
				t.Fatal("wrong native Anthropic image shape")
			}
			raw, err := base64.StdEncoding.DecodeString(block.Source.Data)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
				t.Fatalf("invalid PNG on HTTP wire: %v", err)
			}
			got = append(got, raw)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("HTTP images=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("image %d changed before HTTP encoding", i)
		}
	}
}

func TestToolImageCarriersFollowCompleteBatchAcrossSchedulers(t *testing.T) {
	for _, mode := range []string{"parallel", "sequential", "mixed runs", "file waves", "cancel after first"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reg := tools.NewRegistry()
			calls := []llm.ToolCall{
				{ID: "first", Name: "read_image", Arguments: `{"path":"fixture.png"}`},
				{ID: "second", Name: "write_file", Arguments: `{"path":"fixture.png","content":"updated"}`},
				{ID: "third", Name: "fixture_read", Arguments: "{}"},
			}
			if mode == "file waves" {
				calls[2].Name, calls[2].Arguments = "read_image", `{"path":"fixture.png"}`
			}
			for i, name := range []string{"read_image", "write_file", "fixture_read"} {
				readOnly := i != 1
				if mode == "sequential" || mode == "cancel after first" {
					readOnly = false
				}
				if mode == "parallel" {
					readOnly = true
				}
				reg.MustRegister(tools.Tool{Name: name, Description: "Fixture image producer", Schema: "{}", ReadOnly: readOnly,
					Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
						if mode == "cancel after first" {
							cancel()
						}
						return tools.Result{Text: "image ready", Image: &tools.ImageContent{MediaType: "image/png", Data: []byte("fixture pixels")}}, nil
					},
					Verify: func(tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: true} },
				})
				reg.Activate(name)
			}
			writer := &recordingWriter{}
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg, Writer: writer, BaseDir: home})
			if err != nil {
				t.Fatal(err)
			}
			loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: calls})
			if mode == "file waves" {
				waves, ok := loop.toolConflictWaves(calls)
				if !ok || len(waves) != 3 {
					t.Fatalf("fixture did not exercise three conflicting waves: %v, %v", waves, ok)
				}
			}
			events := make(chan Event, 24)
			var ok bool
			var outcomes []callOutcome
			switch mode {
			case "sequential":
				ok, outcomes = loop.invokeToolCallsSequential(ctx, calls, events)
			case "parallel":
				ok, outcomes = loop.invokeCallsParallel(ctx, calls, events)
			default:
				ok, outcomes = loop.invokeToolCalls(ctx, calls, events)
			}
			if !ok || len(outcomes) != len(calls) {
				t.Fatalf("incomplete batch: ok=%t, outcomes=%d", ok, len(outcomes))
			}
			images := len(calls)
			if mode == "cancel after first" {
				images = 1
			}
			if len(loop.Messages) != 1+len(calls)+images || len(writer.messages) != len(calls)+images {
				t.Fatalf("lost followups: history=%d persisted=%d", len(loop.Messages), len(writer.messages))
			}
			for i, call := range calls {
				for _, message := range []llm.Message{loop.Messages[i+1], writer.messages[i]} {
					if message.Role != llm.RoleTool || message.ToolCallID != call.ID {
						t.Fatalf("carrier split protocol result %d: %+v", i, message)
					}
				}
			}
			for i := 0; i < images; i++ {
				for _, message := range []llm.Message{loop.Messages[1+len(calls)+i], writer.messages[len(calls)+i]} {
					if message.Role != llm.RoleUser || len(message.Parts) != 2 || message.Parts[1].Image == nil ||
						!message.Parts[1].Image.ToolOutputCarrier || message.Parts[1].Image.SourceToolCallID != calls[i].ID {
						t.Fatal("auxiliary image lost its original call provenance")
					}
				}
			}
		})
	}
}
