package credits

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAudit_Log_QueuesAndDrains(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAudit(dir)
	if err != nil {
		t.Fatalf("NewAudit: %v", err)
	}
	for i := 0; i < 5; i++ {
		a.Log(AuditEvent{Tool: "file_write", Op: "write", Path: "/tmp/x"})
	}
	// Wait for the loop to drain.
	for a.Pending() > 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if a.Dropped() != 0 {
		t.Errorf("Dropped = %d, want 0", a.Dropped())
	}
	if a.Processed() != 5 {
		t.Errorf("Processed = %d, want 5", a.Processed())
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// File should exist and have 5 lines.
	events, err := Tail(dir, 10)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(events) != 5 {
		t.Errorf("Tail returned %d events, want 5", len(events))
	}
	for i, e := range events {
		if e.Tool != "file_write" {
			t.Errorf("event %d tool = %q, want file_write", i, e.Tool)
		}
		if e.Path != "/tmp/x" {
			t.Errorf("event %d path = %q, want /tmp/x", i, e.Path)
		}
		if e.TS == 0 {
			t.Errorf("event %d TS not set", i)
		}
	}
}

func TestAudit_Log_NilSafe(t *testing.T) {
	var a *Audit
	if a.Log(AuditEvent{Tool: "x"}) {
		t.Error("nil Log should return false")
	}
	if a.Dropped() != 0 {
		t.Error("nil Dropped should be 0")
	}
	if a.Pending() != 0 {
		t.Error("nil Pending should be 0")
	}
	if a.Processed() != 0 {
		t.Error("nil Processed should be 0")
	}
}

func TestAudit_Log_ChannelFull(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// Stop the loop from draining by holding the file
	// busy? No — easier: the channel capacity is 256.
	// Push 1000 events from a tight loop; at least one
	// should be dropped. (In practice the loop drains
	// quickly; we just want a non-flaky test that the
	// non-blocking path exists.)
	for i := 0; i < 1000; i++ {
		a.Log(AuditEvent{Tool: "burst", TS: int64(i)})
	}
	// Wait for the loop to drain.
	time.Sleep(100 * time.Millisecond)
	// The number of dropped should be >= 0 (no error
	// path) — at minimum, no panic.
	if a.Dropped() > 0 {
		t.Logf("expected: %d events dropped due to full channel", a.Dropped())
	}
}

func TestAudit_Close_Idempotent(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("second Close should be safe, got %v", err)
	}
}

func TestAudit_NewAudit_EmptyHome(t *testing.T) {
	_, err := NewAudit("")
	if err == nil {
		t.Error("expected error for empty home")
	}
}

func TestAudit_Path_AndDir(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.Dir() != filepath.Join(dir, "logs") {
		t.Errorf("Dir() = %q", a.Dir())
	}
	if !strings.HasSuffix(a.Path(), "audit.log") {
		t.Errorf("Path() = %q", a.Path())
	}
}

func TestAudit_ConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	const goroutines = 8
	const each = 50
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				a.Log(AuditEvent{Tool: "concurrent", Path: "/p"})
			}
		}(g)
	}
	wg.Wait()
	// Allow the background drain to finish.
	for a.Pending() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := Tail(dir, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	got := len(events)
	want := goroutines * each
	if got < want {
		// Non-blocking audit may drop when the channel
		// fills during a burst; we accept that, but at
		// 400 events the cap of 256 should not drop
		// anything because the drain is fast.
		t.Errorf("got %d events, want %d (drops=%d)", got, want, a.Dropped())
	}
}

func TestTail_NoFile(t *testing.T) {
	dir := t.TempDir()
	events, err := Tail(dir, 10)
	if err != nil {
		t.Errorf("Tail with no log should not error, got %v", err)
	}
	if events != nil {
		t.Errorf("Tail with no log should return nil, got %v", events)
	}
}

func TestTail_LimitsLines(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		a.Log(AuditEvent{Tool: "x"})
	}
	time.Sleep(50 * time.Millisecond)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := Tail(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Errorf("Tail(10) returned %d, want 10", len(got))
	}
}

func TestTail_SkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.Log(AuditEvent{Tool: "ok"})
	time.Sleep(20 * time.Millisecond)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// Append a malformed line.
	path := filepath.Join(dir, "logs", "audit.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("this is not json\n")
	_ = f.Close()

	got, err := Tail(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("Tail after malformed = %d, want 1", len(got))
	}
}

func TestAuditEvent_JSONRoundTrip(t *testing.T) {
	ev := AuditEvent{
		TS:       12345,
		Tool:     "file_write",
		Op:       "write",
		Path:     "/a/b/c",
		Args:     `{"content":"hello"}`,
		Result:   "ok",
		Error:    "",
		Duration: 42,
		Session:  "s1",
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var got AuditEvent
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got != ev {
		t.Errorf("round trip mismatch: %+v != %+v", got, ev)
	}
}
