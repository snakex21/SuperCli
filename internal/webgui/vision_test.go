package webgui

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleVisionTranscribeUsesActiveProvider(t *testing.T) {
	srv := newTestServer(t, false)
	image := base64.StdEncoding.EncodeToString([]byte("test image"))
	body := `{"imageBase64":"` + image + `","mimeType":"image/png","prompt":"Read this page"}`
	req := httptest.NewRequest(http.MethodPost, "/api/vision/transcribe", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleVisionTranscribe(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Read this page") {
		t.Fatalf("vision response did not contain provider output: %s", rec.Body.String())
	}
}

func TestHandleVisionTranscribeRejectsInvalidImage(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/vision/transcribe", strings.NewReader(
		`{"imageBase64":"not-base64","mimeType":"image/png","prompt":"read"}`,
	))
	rec := httptest.NewRecorder()
	srv.handleVisionTranscribe(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestVisionResultStripsReasoningAndKeepsFinalDocument(t *testing.T) {
	input := "<thinking>Analyze the old letter and choose a translation.</thinking>\n# Polski dokument\n\nTreść pisma."
	got := stripThinking(input)
	want := "# Polski dokument\n\nTreść pisma."
	if got != want {
		t.Fatalf("stripThinking() = %q, want %q", got, want)
	}
}

func TestVisionResultStripsAlternateReasoningWrapper(t *testing.T) {
	input := "<reflection>private analysis</reflection>Final transcription"
	if got := stripThinking(input); got != "Final transcription" {
		t.Fatalf("stripThinking() = %q", got)
	}
}
