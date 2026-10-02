package webgui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func TestSessionImagePreviewAndTranscript(t *testing.T) {
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "echo-test", "MCP image")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")
	writer := session.NewWriter(store, sess.ID)
	ref, err := writer.ExternalizeImage(context.Background(), "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeImage, Image: &ref}}}); err != nil {
		t.Fatal(err)
	}
	token := sessionImagePreviewPath(sess.ID, ref.Path)
	if token == "" {
		t.Fatal("missing portable image handle")
	}
	rec := httptest.NewRecorder()
	srv.handleAttachmentPreview(rec, httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(token), nil))
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatalf("preview %d %s", rec.Code, rec.Body.String())
	}
	transcript, err := srv.eng.transcript(context.Background(), sess.ID)
	if err != nil || len(transcript) != 1 || len(transcript[0].Attachments) != 1 || transcript[0].Attachments[0] != token {
		t.Fatalf("transcript=%+v err=%v", transcript, err)
	}
	// A different active project cannot use even a known valid session handle.
	srv.eng.setHome(t.TempDir())
	rec = httptest.NewRecorder()
	srv.handleAttachmentPreview(rec, httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(token), nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-project preview=%d", rec.Code)
	}
}

func TestSessionImagePreviewRejectsTraversalAndSymlink(t *testing.T) {
	srv := newTestServer(t, false)
	for _, token := range []string{"session:../secret.png", "session:a/../secret.png", "session:a/" + strings.Repeat("a", 64) + ".svg", "session:a\\b/" + strings.Repeat("a", 64) + ".png"} {
		if _, err := srv.eng.resolveSessionImagePreview(token); err == nil {
			t.Fatalf("accepted %q", token)
		}
	}
	store, _ := srv.eng.sessionStore()
	sess, _ := store.Create(srv.eng.Home(), "echo-test", "image")
	writer := session.NewWriter(store, sess.ID)
	ref, err := writer.ExternalizeImage(context.Background(), "image/png", []byte("pixels"))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "secret.png")
	if err = os.WriteFile(target, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(ref.Path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, ref.Path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err = srv.eng.resolveSessionImagePreview(sessionImagePreviewPath(sess.ID, ref.Path)); err == nil {
		t.Fatal("escaped session root")
	}
}

func TestWireNativeImageDoesNotIncludeBase64(t *testing.T) {
	ev, ok := toWireEvent(agent.ToolResultEvent{ID: "mcp", Images: []llm.ImageRef{{Path: "/session/image.png", Data: "SECRET_BASE64", URL: "https://untrusted.test/image.png", ID: "img_a", MediaType: "image/png"}}})
	raw, _ := json.Marshal(ev)
	if !ok || len(ev.Images) != 1 || strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "untrusted") {
		t.Fatalf("wire=%s", raw)
	}
}

func TestSessionImagePreviewHandlesPortableWindowsPath(t *testing.T) {
	name := strings.Repeat("a", 64) + ".png"
	want := "session:session-id/" + name
	if got := sessionImagePreviewPath("session-id", `C:\portable\session-media\old\`+name); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
