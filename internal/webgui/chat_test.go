package webgui

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
)

func TestHandleChat_RejectsGet(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/chat", nil)
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/chat: status = %d, want 405", rec.Code)
	}
}

func TestHandleChat_BadJSON(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: status = %d, want 400", rec.Code)
	}
}

func TestHandleChat_EmptyPrompt(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"   "}`))
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty prompt: status = %d, want 400", rec.Code)
	}
}

func TestHandleChat_StreamsEcho(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"hello"}`))
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	// Every frame is an SSE data line carrying a JSON wire event.
	if !strings.Contains(body, "data: ") {
		t.Errorf("no SSE frames in body: %q", body)
	}
	// The echo run must terminate with a done event.
	if !strings.Contains(body, `"type":"done"`) {
		t.Errorf("stream missing done event: %q", body)
	}
	// Echo emits its prefix and body as separate provider deltas. They should
	// cross the HTTP boundary as one text frame, followed by the immediate
	// terminal frame.
	if got := strings.Count(body, `"type":"message"`); got != 1 {
		t.Errorf("message SSE frames = %d, want 1: %q", got, body)
	}
}

func TestHandleChat_AttachmentOnlyPromptAndRichReaders(t *testing.T) {
	srv := newTestServer(t, false)
	path := filepath.Join(srv.eng.Home(), "scan.png")
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(chatRequest{Attachments: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.handleChat(rec, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(string(payload))))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Inspect and describe the attached files") ||
		!strings.Contains(body, "read_image") ||
		!strings.Contains(body, "scan.png") {
		t.Fatalf("attachment context did not reach the model: %s", body)
	}
	srv.eng.diagnosticMu.RLock()
	registry := srv.eng.diagnosticRegistry
	srv.eng.diagnosticMu.RUnlock()
	if registry == nil {
		t.Fatal("tool registry was not captured")
	}
	for _, name := range []string{"read_image", "read_pdf", "read_docx", "read_xlsx", "read_zip"} {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("attachment reader %q is not registered", name)
		}
	}
}

func TestHandleChat_SendsImageDirectlyInFirstModelRequest(t *testing.T) {
	var mu sync.Mutex
	var requests [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"widzę obraz\"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	home := t.TempDir()
	cfg := config.Config{
		Provider: config.ProviderOpenAI,
		Model:    "qwen3.5-9b-uncensored-hauhaucs-aggressive",
		BaseURL:  upstream.URL + "/v1",
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	srv := NewServer(eng, false)

	imagePath := filepath.Join(home, "pixel.png")
	// Use the complete 1x1 PNG fixture already exercised by attachment upload.
	pngData := mustDecodeBase64(t, "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err := os.WriteFile(imagePath, pngData, 0o600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(chatRequest{Prompt: "co widzisz?", Attachments: []string{imagePath}})
	rec := httptest.NewRecorder()
	srv.handleChat(rec, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(string(payload))))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d: %s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("model requests = %d, want one direct multimodal call", len(requests))
	}
	body := string(requests[0])
	if !strings.Contains(body, `"type":"image_url"`) || !strings.Contains(body, "data:image/png;base64,") {
		t.Fatalf("first model request did not contain a direct image: %s", body)
	}
}

func mustDecodeBase64(t *testing.T, value string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestHandleChat_ImmediatelyPersistsPersonalFacts(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"prompt":"Cześć, mam na imię Maks."}`))
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	global, err := memory.OpenStore(srv.eng.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	defer global.Close()
	entries, err := global.Recent(memory.ScopePreference, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.Contains(entries[0].Content, "Maks") {
		t.Fatalf("personal fact was not persisted immediately: %+v", entries)
	}
}

func TestHandleChat_RewindFeedbackReachesModelWithoutExtraCall(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"prompt":"try again","rewound":true,"rewind_reason":"wrong file was changed","rewind_files":true}`))
	rec := httptest.NewRecorder()
	srv.handleChat(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "rewind_feedback") || !strings.Contains(body, "wrong file was changed") ||
		!strings.Contains(body, "restored the affected workspace files") {
		t.Fatalf("model response did not receive rewind feedback: %s", body)
	}
	if got := strings.Count(body, `"type":"done"`); got != 1 {
		t.Fatalf("done events = %d, want one model run: %s", got, body)
	}
}
