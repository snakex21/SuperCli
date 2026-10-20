package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func TestSavedToolOutputSessionAndWorkspace(t *testing.T) {
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Create(srv.eng.Home(), "fixture", "human outputs")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, owner.ID)
	handle := "out_abcdef123456"
	full := strings.Repeat("pełny wynik\n", 1000) + "MIDDLE_CANARY"
	if err = writer.SaveToolOutput(t.Context(), handle, full); err != nil {
		t.Fatal(err)
	}
	msg := llm.Message{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "read", Content: "[large tool output: 15000 bytes; preview follows; handle=" + handle + "]\npreview"}
	if err = writer.AppendMessage(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	transcript, err := srv.eng.transcript(t.Context(), owner.ID)
	if err != nil || len(transcript) != 1 || transcript[0].ToolOutputHandle != handle || transcript[0].Content != msg.Content {
		t.Fatal("transcript must carry only a lazy reference", err)
	}
	read := func(sessionID, ref, method string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/tool-output?session="+sessionID+"&handle="+ref, nil)
		w := httptest.NewRecorder()
		srv.handleSavedToolOutput(w, r)
		return w
	}
	response := read(owner.ID, handle, "GET")
	var data struct {
		Text string `json:"text"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &data) != nil || data.Text != full || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("full output changed", response.Code)
	}
	for index, cwd := range []string{srv.eng.Home(), t.TempDir()} {
		other, err := store.Create(cwd, "fixture", "other")
		if err != nil {
			t.Fatal(err)
		}
		otherHandle := "out_" + strings.Repeat(string(rune('a'+index)), 12)
		if err = session.NewWriter(store, other.ID).SaveToolOutput(t.Context(), otherHandle, full); err != nil {
			t.Fatal(err)
		}
		if read(other.ID, handle, "GET").Code != http.StatusNotFound {
			t.Fatal("cross-session output exposed")
		}
		if cwd != srv.eng.Home() && read(other.ID, otherHandle, "GET").Code != http.StatusNotFound {
			t.Fatal("foreign workspace output exposed")
		}
	}
	if read(owner.ID, handle, "POST").Code != http.StatusMethodNotAllowed || read(owner.ID, "invalid", "GET").Code != http.StatusNotFound {
		t.Fatal("invalid request accepted")
	}
}
