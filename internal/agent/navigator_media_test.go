package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Use the real HTTP serializer: inspecting history alone cannot show that a
// dormant file is read again or that its pixels reach a helper endpoint.
func TestNavigatorDoesNotReloadHistoricalImage(t *testing.T) {
	for _, storage := range []string{"inline", "file", "missing-file"} {
		t.Run(storage, func(t *testing.T) {
			captured := make(chan []byte, 2)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				captured <- body
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"mode\\\":\\\"advisor\\\"}\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			caps := llm.NewCapabilityRegistry()
			caps.Register(llm.ModelInfo{ID: "navigator-vision-fixture", Vision: true, VisionKnown: true, Source: llm.SourceProvider})
			side, err := llm.NewOpenAI(llm.OpenAIConfig{
				BaseURL: srv.URL + "/v1", Model: "navigator-vision-fixture", Capabilities: caps,
			})
			if err != nil {
				t.Fatal(err)
			}
			main := &stubProvider{name: "main", scripts: [][]llm.Delta{{{Content: "answer", FinishReason: "stop"}}}}
			l, err := NewLoop(LoopConfig{
				Provider: main, NavigatorProvider: side, Registry: tools.NewRegistry(),
				System: "project policy", EnableNavigator: true, MaxSteps: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			img := &llm.ImageRef{ID: "img_prior", Name: "diagram.png", MediaType: "image/png"}
			if storage == "inline" {
				img.Data = strings.Repeat("AQID", 4096)
			} else {
				img.Path = filepath.Join(t.TempDir(), "diagram.png")
				if storage == "file" {
					if err := os.WriteFile(img.Path, []byte(strings.Repeat("pixels", 1024)), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			l.Messages = append(l.Messages,
				llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{
					{Type: llm.PartTypeText, Text: "Earlier design diagram"},
					{Type: llm.PartTypeImage, Image: img},
				}},
				llm.Message{Role: llm.RoleAssistant, Content: "Earlier explanation"},
			)
			const prompt = "Which tradeoff would make sense?"
			ch, err := l.Run(context.Background(), prompt)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, ch) {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if len(captured) != 1 {
				t.Fatalf("helper HTTP calls=%d, want 1 without reading the old image file", len(captured))
			}
			wire := string(<-captured)
			t.Logf("navigator request bytes=%d", len(wire))
			if strings.Contains(wire, "image_url") || strings.Contains(wire, "data:image/") {
				t.Error("historical pixels were sent to the helper")
			}
			if !strings.Contains(wire, "Earlier design diagram") || !strings.Contains(wire, "diagram.png") {
				t.Error("helper lost the caption or attachment label")
			}
			if strings.Count(wire, prompt) != 1 {
				t.Error("current user prompt was sent more than once")
			}
			if l.route != RouteAdvisor || main.calls != 1 {
				t.Fatalf("classification/main answer changed: route=%s main=%d", l.route, main.calls)
			}
			if requestHasImage(main.reqs[0]) || img.Active {
				t.Error("history image was reactivated")
			}
		})
	}
}

func TestNavigatorTextProjectionIsBoundedAndPreservesHistory(t *testing.T) {
	parts := []llm.ContentPart{
		{Type: llm.PartTypeReasoning, Reasoning: &llm.ReasoningBlock{
			Format: llm.ReasoningChat, Model: "source", Scope: "source",
			Data: json.RawMessage("{\"reasoning_content\":\"native-private\"}"),
		}},
	}
	for i := 0; i < 10; i++ {
		parts = append(parts, llm.ContentPart{Type: llm.PartTypeText, Text: strings.Repeat("żółć", 170)})
	}
	l := makeLoop(t, &stubProvider{name: "main"}, tools.NewRegistry(), "policy")
	l.Messages = append(l.Messages,
		llm.Message{Role: llm.RoleUser, Content: "HIDDEN-DIALOGUE"},
		llm.Message{Role: llm.RoleUser, Content: "earlier question"},
		llm.Message{Role: llm.RoleAssistant, Parts: parts},
		llm.Message{Role: llm.RoleAssistant, Parts: parts[:1]},
		llm.Message{Role: llm.RoleAssistant, Content: "<think>legacy-private</think>public answer"},
		llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "Attached image from tool read_image:"},
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{ID: "tool-image", Name: "result.png"}},
		}},
		llm.Message{Role: llm.RoleUser, Content: "<task-notification>worker done</task-notification>"},
		llm.Message{Role: llm.RoleUser, Content: "current request"},
	)
	if err := l.HideRange(1, 2); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(l.Messages)
	messages := l.navigatorMessages("current request")
	wire, _ := json.Marshal(messages)
	if strings.Contains(string(wire), "native-private") || strings.Contains(string(wire), "legacy-private") ||
		strings.Contains(string(wire), "HIDDEN-DIALOGUE") || strings.Contains(string(wire), "Attached image from tool") ||
		strings.Contains(string(wire), "<task-notification>") {
		t.Error("non-dialogue history entered helper context")
	}
	if !requestContainsText(messages, "public answer") || !requestContainsText(messages, "earlier question") {
		t.Error("visible dialogue was displaced by internal messages")
	}
	for _, m := range messages[1 : len(messages)-1] {
		if len(m.Parts) != 0 || m.Content == "" || utf8.RuneCountInString(m.Content) > 501 {
			t.Errorf("unbounded/empty/non-text history message: parts=%d runes=%d", len(m.Parts), utf8.RuneCountInString(m.Content))
		}
	}
	after, _ := json.Marshal(l.Messages)
	if string(before) != string(after) {
		t.Fatal("helper projection changed canonical history")
	}
}

func TestNavigatorCurrentImageNeedsOnlyMainModel(t *testing.T) {
	for _, sideProvider := range []bool{false, true} {
		for _, auto := range []bool{false, true} {
			t.Run(fmt.Sprintf("side=%t/auto=%t", sideProvider, auto), func(t *testing.T) {
				// If the navigator incorrectly runs on main, this first answer is
				// deliberately parseable. The call count detects that extra inference.
				main := &stubProvider{name: "main", scripts: [][]llm.Delta{{
					{Content: "{\"mode\":\"chat\"}", FinishReason: "stop"},
				}}}
				side := &scriptedNavProvider{name: "side", content: "{\"mode\":\"chat\"}"}
				l := makeLoop(t, main, tools.NewRegistry(), "project policy")
				l.navigate, l.navAuto = true, auto
				if sideProvider {
					l.navProvider = side
				}
				l.SetNextCoordinatorAddon("CURRENT-REPO-CONTEXT")
				l.SetNextUserImages([]llm.ImageRef{{
					ID: "img_current", Name: "current.png", MediaType: "image/png", Data: "AQID",
				}})
				ch, err := l.Run(context.Background(), "hej")
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range drainEvents(t, ch) {
					if e, ok := event.(ErrorEvent); ok {
						t.Fatal(e.Err)
					}
				}
				if side.calls != 0 || main.calls != 1 || l.route != RouteCoordinator {
					t.Fatalf("new attachment took helper/light path: side=%d main=%d route=%s", side.calls, main.calls, l.route)
				}
				if !requestHasImage(main.reqs[0]) || !requestContainsText(main.reqs[0], "CURRENT-REPO-CONTEXT") {
					t.Fatal("main request lost current pixels or project context")
				}
				for _, m := range l.Messages {
					for _, part := range m.Parts {
						if part.Image != nil && part.Image.Active {
							t.Error("accepted main request did not deactivate the attachment")
						}
					}
				}
			})
		}
	}
}

func TestImageRequestKeepsCoordinatorContextOnWire(t *testing.T) {
	for _, tc := range []struct {
		protocol string
		navigate bool
	}{{"chat", false}, {"chat", true}, {"zen-responses", false}, {"zen-responses", true}} {
		t.Run(fmt.Sprintf("%s/navigator=%t", tc.protocol, tc.navigate), func(t *testing.T) {
			captured := make(chan []byte, 8)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				captured <- body
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.protocol == "chat" {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Please specify the desired behavior.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				} else {
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Please specify the desired behavior.\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n")
				}
			}))
			defer srv.Close()
			caps := llm.NewCapabilityRegistry()
			caps.Register(llm.ModelInfo{ID: "main-vision-fixture", Vision: true, VisionKnown: true, Source: llm.SourceProvider})
			var main llm.Provider
			var err error
			if tc.protocol == "chat" {
				main, err = llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "main-vision-fixture", Capabilities: caps})
			} else {
				main, err = llm.NewResponses(llm.ResponsesConfig{BaseURL: "https://opencode.ai/zen/v1", Model: "main-vision-fixture", Capabilities: caps, HTTPClient: &http.Client{Transport: workerRequestTransport(func(req *http.Request) (*http.Response, error) {
					clone := req.Clone(req.Context())
					clone.URL.Scheme, clone.URL.Host = "http", strings.TrimPrefix(srv.URL, "http://")
					clone.Host = clone.URL.Host
					return http.DefaultTransport.RoundTrip(clone)
				})}})
			}
			if err != nil {
				t.Fatal(err)
			}
			writer := &recordingWriter{}
			l, err := NewLoop(LoopConfig{
				Provider: main, Registry: tools.NewRegistry(), Writer: writer,
				EnableNavigator: tc.navigate, System: "project policy", MaxSteps: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			l.SetNextUserAddon("USER-ADDON")
			l.SetNextCoordinatorAddon("CURRENT-REPO-CONTEXT")
			l.SetNextUserImages([]llm.ImageRef{{
				ID: "img_current", Name: "current.png", MediaType: "image/png", Data: "AQID",
			}})
			const prompt = "Fix the parser bug in the project."
			hint := implementationVerificationHint(prompt)
			if hint == "" {
				t.Fatal("fixture does not exercise the implementation hint")
			}
			ch, err := l.Run(context.Background(), prompt)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, ch) {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if len(captured) != 1 {
				t.Fatalf("HTTP calls=%d, want one main request", len(captured))
			}
			type wireMessage struct {
				Role    string
				Content json.RawMessage
			}
			var request struct {
				Messages []wireMessage
				Input    []wireMessage
			}
			if err := json.Unmarshal(<-captured, &request); err != nil {
				t.Fatal(err)
			}
			messages := request.Messages
			if tc.protocol == "zen-responses" {
				messages = request.Input
			}
			found := false
			for _, message := range messages {
				if message.Role != "user" || !strings.Contains(string(message.Content), "image_url") {
					continue
				}
				found = true
				var parts []struct {
					Type string
					Text string
				}
				if err := json.Unmarshal(message.Content, &parts); err != nil {
					t.Fatal(err)
				}
				var text strings.Builder
				for _, part := range parts {
					if part.Type == "text" || part.Type == "input_text" {
						text.WriteString(part.Text)
					}
				}
				for _, want := range []string{prompt, "USER-ADDON", "CURRENT-REPO-CONTEXT", hint} {
					if !strings.Contains(text.String(), want) {
						t.Errorf("multimodal HTTP message lost %q", want)
					}
				}
			}
			if !found {
				t.Fatal("main request lost the image")
			}
			for _, message := range writer.messages {
				if message.Role != llm.RoleUser {
					continue
				}
				if messageDraftText(message) != prompt || !message.HasImage() {
					t.Fatal("provider-only additions polluted the persisted user message")
				}
			}
		})
	}
}
