package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"supercli/internal/tools"
)

// rewindFixture keeps the "current transcript" of several conversations: a
// receipt stays valid until its conversation is truncated from it.
type rewindFixture struct {
	mu   sync.Mutex
	live map[string]int64 // session -> message id at seq 1
}

func (f *rewindFixture) validate(_ context.Context, sid string, seq int, id int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return seq == 1 && f.live[sid] == id, nil
}

func (f *rewindFixture) truncate(sid string) func(context.Context) error {
	return func(context.Context) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.live, sid)
		return nil
	}
}

func fixtureTurn(t *testing.T, m *Manager, f *rewindFixture, sid string, id int64) *Turn {
	t.Helper()
	f.mu.Lock()
	f.live[sid] = id
	f.mu.Unlock()
	turn := m.NewTurn(sid, "prompt")
	turn.SetUserMessageReceipt(1, id)
	return turn
}

func writeInTurn(t *testing.T, turn *Turn, home, path, content string) {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"path": path, "content": content})
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), string(args))
}

// A command the checkpoint cannot scope makes the turn hold the whole
// workspace (like a shell command of a running agent).
func holdWholeWorkspace(t *testing.T, turn *Turn) {
	t.Helper()
	spec := tools.Tool{Name: "ctx_execute", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }}
	runSnapshotTool(t, turn.Wrap(spec), `{"command":"echo"}`)
}

func completeTurn(t *testing.T, turn *Turn) {
	t.Helper()
	if record, err := turn.Complete(context.Background()); err != nil || record == nil {
		t.Fatalf("complete: %+v %v", record, err)
	}
}

// One conversation is rewound while another (A) runs in the same project.
// Only the files decide: a conversation with no file changes (the "weather"
// fixture) or with other files rewinds; overlapping files are listed, and
// both the conversation-only and the forced rewind work; A's own later
// rewind still works.
func TestRewindWhileAnotherSessionRunsDependsOnlyOnFiles(t *testing.T) {
	parallelNativeTest(t)
	ctx := context.Background()
	home := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "d.txt"} {
		writeCheckpointFixture(t, filepath.Join(home, name), "before")
	}
	m := openSnapshotTestManager(t, home)
	f := &rewindFixture{live: map[string]int64{}}
	m.SetUserReceiptValidator(f.validate)

	c := fixtureTurn(t, m, f, "C", 31)
	writeInTurn(t, c, home, "a.txt", "C")
	completeTurn(t, c)
	b := fixtureTurn(t, m, f, "B", 21)
	writeInTurn(t, b, home, "b.txt", "B")
	completeTurn(t, b)
	d := fixtureTurn(t, m, f, "D", 41)
	writeInTurn(t, d, home, "d.txt", "D")
	completeTurn(t, d)
	f.mu.Lock()
	f.live["W"] = 11 // the weather question: no file changes at all
	f.mu.Unlock()

	// A runs now and has changed a.txt.
	a := fixtureTurn(t, m, f, "A", 51)
	writeInTurn(t, a, home, "a.txt", "A")

	if _, err := m.RewindTranscript(ctx, "W", 1, 11, true, f.truncate("W")); err != nil {
		t.Fatalf("no-file conversation: %v", err)
	}
	if _, err := m.RewindTranscript(ctx, "B", 1, 21, true, f.truncate("B")); err != nil {
		t.Fatalf("non-overlapping files: %v", err)
	}
	assertFileBytes(t, filepath.Join(home, "b.txt"), []byte("before"))

	called := false
	_, err := m.RewindTranscript(ctx, "C", 1, 31, true, func(context.Context) error { called = true; return nil })
	var overlap *RewindOverlapError
	if !errors.As(err, &overlap) || !errors.Is(err, ErrActiveTurn) || called {
		t.Fatalf("overlap: %v called=%v", err, called)
	}
	if len(overlap.Overlaps) != 1 || overlap.Overlaps[0] != (RewindOverlap{File: "a.txt", Session: "A"}) {
		t.Fatalf("overlaps = %+v", overlap.Overlaps)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("A"))
	// "Rewind conversation only".
	if _, err := m.RewindTranscript(ctx, "C", 1, 31, false, f.truncate("C")); err != nil {
		t.Fatalf("conversation only: %v", err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("A"))

	// A's own rewind while it runs stays refused.
	if _, err := m.RewindTranscript(ctx, "A", 1, 51, true, f.truncate("A")); !errors.Is(err, ErrActiveTurn) {
		t.Fatalf("own live turn: %v", err)
	}
	// A now runs a command: it holds the whole workspace, so D's d.txt
	// overlaps; "rewind anyway" restores it.
	holdWholeWorkspace(t, a)
	if _, err := m.RewindTranscript(ctx, "D", 1, 41, true, f.truncate("D")); !errors.As(err, &overlap) || overlap.Overlaps[0].File != "d.txt" {
		t.Fatalf("whole-workspace overlap: %v", err)
	}
	if _, err := m.RewindTranscriptWith(ctx, "D", 1, 41, true, true, f.truncate("D")); err != nil {
		t.Fatalf("rewind anyway: %v", err)
	}
	assertFileBytes(t, filepath.Join(home, "d.txt"), []byte("before"))

	// A finishes; its own rewind still works after the others'.
	completeTurn(t, a)
	if _, err := m.RewindTranscript(ctx, "A", 1, 51, true, f.truncate("A")); err != nil {
		t.Fatalf("A after the others: %v", err)
	}
	assertFileBytes(t, filepath.Join(home, "a.txt"), []byte("C"))
}

// A conversation of another project never sees this project's live turns.
func TestRewindIgnoresRunningSessionOfAnotherProject(t *testing.T) {
	parallelNativeTest(t)
	ctx := context.Background()
	homeX, homeY, data := t.TempDir(), t.TempDir(), t.TempDir()
	writeCheckpointFixture(t, filepath.Join(homeX, "a.txt"), "before")
	writeCheckpointFixture(t, filepath.Join(homeY, "a.txt"), "before")
	mx, err := Open(homeX, data)
	if err != nil {
		t.Skip(err)
	}
	my, err := Open(homeY, data)
	if err != nil {
		t.Fatal(err)
	}
	f := &rewindFixture{live: map[string]int64{}}
	mx.SetUserReceiptValidator(f.validate)
	my.SetUserReceiptValidator(f.validate)
	y := fixtureTurn(t, my, f, "Y", 21)
	writeInTurn(t, y, homeY, "a.txt", "Y")
	completeTurn(t, y)
	x := fixtureTurn(t, mx, f, "X", 11)
	writeInTurn(t, x, homeX, "a.txt", "X")
	holdWholeWorkspace(t, x) // even a whole-workspace run elsewhere
	if _, err := my.RewindTranscript(ctx, "Y", 1, 21, true, f.truncate("Y")); err != nil {
		t.Fatalf("other project's run blocked the rewind: %v", err)
	}
	assertFileBytes(t, filepath.Join(homeY, "a.txt"), []byte("before"))
	assertFileBytes(t, filepath.Join(homeX, "a.txt"), []byte("X"))
	completeTurn(t, x)
	if _, err := mx.RewindTranscript(ctx, "X", 1, 11, true, f.truncate("X")); err != nil {
		t.Fatalf("X after Y: %v", err)
	}
	assertFileBytes(t, filepath.Join(homeX, "a.txt"), []byte("before"))
}
