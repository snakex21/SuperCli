package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/checkpoint"
	"supercli/internal/tools"
)

// rewindTurn starts a checkpoint turn for the "return here" prompt (seq 3)
// and writes the given files in it.
func rewindTurn(t *testing.T, srv *Server, manager *checkpoint.Manager, sessionID string, files map[string]string) *checkpoint.Turn {
	t.Helper()
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ReadUserReceiptAt(context.Background(), sessionID, 3)
	if err != nil {
		t.Fatal(err)
	}
	turn := manager.NewTurn(sessionID, "return here")
	turn.SetUserMessageReceipt(receipt.Seq, receipt.ID)
	for path, content := range files {
		args, _ := json.Marshal(map[string]string{"path": path, "content": content})
		result, _ := turn.Wrap(tools.NewWriteFile(srv.eng.Home()).Spec()).Fn(context.Background(), args)
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	return turn
}

func completeRewindTurn(t *testing.T, turn *checkpoint.Turn) {
	t.Helper()
	if record, err := turn.Complete(context.Background()); err != nil || record == nil {
		t.Fatalf("checkpoint record=%+v err=%v", record, err)
	}
}

// postRewindRaw posts a rewind and returns the raw response.
func postRewindRaw(t *testing.T, srv *Server, sessionID string, files, force bool) *httptest.ResponseRecorder {
	t.Helper()
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ReadUserReceiptAt(context.Background(), sessionID, 3)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"session_id":%q,"selected_seq":3,"selected_message_id":"%d","rewind_files":%t,"force":%t}`, sessionID, receipt.ID, files, force)
	recorder := httptest.NewRecorder()
	srv.handleSessionRewind(recorder, httptest.NewRequest(http.MethodPost, "/api/session/rewind", strings.NewReader(body)))
	return recorder
}

// markRunning makes the conversation look like it runs right now (its own
// run plus the engine-wide counter that used to block every rewind).
func markRunning(t *testing.T, srv *Server, sessionID string) func() {
	t.Helper()
	release, err := srv.eng.claimSessionRun(context.Background(), sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	srv.eng.activeRuns.Add(1)
	return func() { srv.eng.activeRuns.Add(-1); release() }
}

func readRewindFixture(t *testing.T, path string) string {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// Session A runs (its own run, with a live checkpoint turn that changed
// app.txt). Other conversations of the same project rewind by files only:
// one with no file changes at all and one with other files just rewind;
// an overlapping one gets the list, then "conversation only" and "anyway"
// both work, and A's own rewind still works after it finishes.
func TestHandleSessionRewindWhileAnotherSessionRuns(t *testing.T) {
	srv := newTestServer(t, false)
	// The test server keeps its data in its home; a separate project folder
	// lets a whole-workspace capture run as it does for users.
	srv.eng.setHome(t.TempDir())
	manager, err := srv.eng.checkpointManager(srv.eng.Home())
	if err != nil {
		t.Skip(err)
	}
	home := srv.eng.Home()
	for _, name := range []string{"app.txt", "notes.txt", "other.txt"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("before"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := createRewindSession(t, srv, "minecraft mod")
	weather, _ := createRewindSession(t, srv, "weather")
	b, _ := createRewindSession(t, srv, "notes")
	c, _ := createRewindSession(t, srv, "app tweak")
	d, _ := createRewindSession(t, srv, "other")

	completeRewindTurn(t, rewindTurn(t, srv, manager, c.ID, map[string]string{"app.txt": "C"}))
	completeRewindTurn(t, rewindTurn(t, srv, manager, b.ID, map[string]string{"notes.txt": "B"}))
	completeRewindTurn(t, rewindTurn(t, srv, manager, d.ID, map[string]string{"other.txt": "D"}))

	stop := markRunning(t, srv, a.ID)
	aTurn := rewindTurn(t, srv, manager, a.ID, map[string]string{"app.txt": "A"})

	// No file changes: rewinds with files requested, no dialog.
	if rec := postRewindRaw(t, srv, weather.ID, true, false); rec.Code != http.StatusOK {
		t.Fatalf("weather rewind: %d %s", rec.Code, rec.Body.String())
	}
	// Other files: restored.
	if rec := postRewindRaw(t, srv, b.ID, true, false); rec.Code != http.StatusOK {
		t.Fatalf("non-overlapping rewind: %d %s", rec.Code, rec.Body.String())
	}
	if got := readRewindFixture(t, filepath.Join(home, "notes.txt")); got != "before" {
		t.Fatalf("notes.txt = %q", got)
	}
	// Overlap: 409 with the list and the holder's title; nothing changed.
	rec := postRewindRaw(t, srv, c.ID, true, false)
	var overlap struct {
		Error    string `json:"error"`
		Overlaps []struct{ File, Session, Title string }
	}
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &overlap) != nil || overlap.Error != "rewind_overlap" ||
		len(overlap.Overlaps) != 1 || overlap.Overlaps[0].File != "app.txt" || overlap.Overlaps[0].Session != a.ID || overlap.Overlaps[0].Title != "minecraft mod" {
		t.Fatalf("overlap: %d %s", rec.Code, rec.Body.String())
	}
	if got := readRewindFixture(t, filepath.Join(home, "app.txt")); got != "A" {
		t.Fatalf("refused rewind changed app.txt: %q", got)
	}
	// "Rewind conversation only".
	if rec := postRewindRaw(t, srv, c.ID, false, false); rec.Code != http.StatusOK {
		t.Fatalf("conversation only: %d %s", rec.Code, rec.Body.String())
	}
	if got := readRewindFixture(t, filepath.Join(home, "app.txt")); got != "A" {
		t.Fatalf("conversation-only rewind changed app.txt: %q", got)
	}
	// "Rewind anyway": A also runs a command now, so it holds every file.
	spec := tools.Tool{Name: "ctx_execute", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }}
	if result, _ := aTurn.Wrap(spec).Fn(context.Background(), json.RawMessage(`{"command":"echo"}`)); result.Err != nil {
		t.Fatal(result.Err)
	}
	if rec := postRewindRaw(t, srv, d.ID, true, false); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "other.txt") {
		t.Fatalf("whole-workspace overlap: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postRewindRaw(t, srv, d.ID, true, true); rec.Code != http.StatusOK {
		t.Fatalf("rewind anyway: %d %s", rec.Code, rec.Body.String())
	}
	if got := readRewindFixture(t, filepath.Join(home, "other.txt")); got != "before" {
		t.Fatalf("other.txt = %q", got)
	}

	// A finishes; its own rewind works after the others'.
	stop()
	completeRewindTurn(t, aTurn)
	if rec := postRewindRaw(t, srv, a.ID, true, false); rec.Code != http.StatusOK {
		t.Fatalf("A rewind: %d %s", rec.Code, rec.Body.String())
	}
	if got := readRewindFixture(t, filepath.Join(home, "app.txt")); got != "C" {
		t.Fatalf("app.txt after A's rewind = %q", got)
	}
}

// A conversation's own run still blocks its rewind.
func TestHandleSessionRewindRefusesOwnRunningSession(t *testing.T) {
	srv := newTestServer(t, false)
	a, _ := createRewindSession(t, srv, "running")
	stop := markRunning(t, srv, a.ID)
	defer stop()
	if rec := postRewindRaw(t, srv, a.ID, false, false); rec.Code != http.StatusConflict {
		t.Fatalf("own run: %d %s", rec.Code, rec.Body.String())
	}
}

// A conversation in another project rewinds freely (conversation and files,
// no dialog) while a session of project X runs, even one holding every file.
func TestHandleSessionRewindIgnoresOtherProjectRun(t *testing.T) {
	srv := newTestServer(t, false)
	homeX, homeY := t.TempDir(), t.TempDir()
	srv.eng.setHome(homeX)
	for _, home := range []string{homeX, homeY} {
		if err := os.WriteFile(filepath.Join(home, "app.txt"), []byte("before"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	managerX, err := srv.eng.checkpointManager(homeX)
	if err != nil {
		t.Skip(err)
	}
	x, _ := createRewindSession(t, srv, "project X")
	stop := markRunning(t, srv, x.ID)
	defer stop()
	xTurn := rewindTurn(t, srv, managerX, x.ID, map[string]string{"app.txt": "X"})
	spec := tools.Tool{Name: "ctx_execute", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }}
	if result, _ := xTurn.Wrap(spec).Fn(context.Background(), json.RawMessage(`{"command":"echo"}`)); result.Err != nil {
		t.Fatal(result.Err)
	}

	srv.eng.setHome(homeY)
	managerY, err := srv.eng.checkpointManager(homeY)
	if err != nil {
		t.Fatal(err)
	}
	y, _ := createRewindSession(t, srv, "project Y")
	completeRewindTurn(t, rewindTurn(t, srv, managerY, y.ID, map[string]string{"app.txt": "Y"}))
	if rec := postRewindRaw(t, srv, y.ID, true, false); rec.Code != http.StatusOK {
		t.Fatalf("project Y rewind: %d %s", rec.Code, rec.Body.String())
	}
	if got := readRewindFixture(t, filepath.Join(homeY, "app.txt")); got != "before" {
		t.Fatalf("Y app.txt = %q", got)
	}
	if got := readRewindFixture(t, filepath.Join(homeX, "app.txt")); got != "X" {
		t.Fatalf("X app.txt = %q", got)
	}
	y2, _ := createRewindSession(t, srv, "project Y chat")
	if rec := postRewindRaw(t, srv, y2.ID, false, false); rec.Code != http.StatusOK {
		t.Fatalf("project Y conversation rewind: %d %s", rec.Code, rec.Body.String())
	}
	srv.eng.setHome(homeX)
	completeRewindTurn(t, xTurn)
}
