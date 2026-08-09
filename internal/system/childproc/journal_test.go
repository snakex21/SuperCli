package childproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// resetJournal disables the global journal so tests do not leak into
// each other or into the real process.
func resetJournal(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetJournal("") })
	SetJournal("")
}

func TestSweep_NoJournalIsNoOp(t *testing.T) {
	resetJournal(t)
	if n := Sweep(); n != 0 {
		t.Fatalf("Sweep with no journal = %d, want 0", n)
	}
}

func TestSweep_EmptyFileIsRemoved(t *testing.T) {
	resetJournal(t)
	dir := t.TempDir()
	path := filepath.Join(dir, JournalFile)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	SetJournal(path)
	Sweep()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty journal file survived sweep: %v", err)
	}
}

// TestJournal_Lifecycle drives a real entry through journalChild and
// journalDone with the CURRENT process as the fake child (its start
// stamp is readable on every platform). The entry must be active after
// the spawn, inactive after done, and the sweep must neither kill our
// own process nor keep the record.
func TestJournal_Lifecycle(t *testing.T) {
	resetJournal(t)
	path := filepath.Join(t.TempDir(), JournalFile)
	SetJournal(path)

	cmd := &exec.Cmd{Path: os.Args[0], Process: &os.Process{Pid: os.Getpid()}}
	journalChild(cmd)

	j := currentJournal()
	entries := j.read()
	if len(entries) != 1 || !entries[0].Active {
		t.Fatalf("after spawn: entries = %+v, want one active", entries)
	}
	if entries[0].OwnerPID != os.Getpid() || entries[0].PID != os.Getpid() {
		t.Fatalf("entry identity wrong: %+v", entries[0])
	}
	if entries[0].StartNS < 0 {
		t.Fatalf("child start stamp missing: %+v", entries[0])
	}

	journalDone(cmd)
	entries = j.read()
	if len(entries) != 1 || entries[0].Active {
		t.Fatalf("after done: entries = %+v, want one inactive", entries)
	}

	// The sweep must keep (not kill) children of a live owner: we ARE
	// the owner here.
	if n := Sweep(); n != 0 {
		t.Fatalf("sweep killed %d process(es) with a live owner", n)
	}
}

// fakeProcs implements the liveness/terminate hooks.
type fakeProcs struct {
	aliveSet   map[int]bool  // pid -> alive
	startSet   map[int]int64 // pid -> creation stamp
	terminated []int
}

func (f *fakeProcs) isAlive(pid int, startNS int64) bool {
	alive, ok := f.aliveSet[pid]
	if !ok {
		return false
	}
	if startNS > 0 {
		want, ok := f.startSet[pid]
		if ok && want != startNS {
			return false // PID reused: different creation stamp
		}
	}
	return alive
}

func (f *fakeProcs) terminate(pid int) error {
	f.terminated = append(f.terminated, pid)
	return nil
}

func seededJournal(t *testing.T, entries []Entry) (*Journal, *fakeProcs) {
	t.Helper()
	resetJournal(t)
	path := filepath.Join(t.TempDir(), JournalFile)
	SetJournal(path)
	j := currentJournal()
	f := &fakeProcs{aliveSet: map[int]bool{}, startSet: map[int]int64{}}
	j.alive = f.isAlive
	j.terminate = f.terminate
	for _, e := range entries {
		if err := j.append(e); err != nil {
			t.Fatal(err)
		}
	}
	return j, f
}

func TestSweep_KillsOrphanWithDeadOwner(t *testing.T) {
	_, f := seededJournal(t, []Entry{{
		PID: 1001, OwnerPID: 42, OwnerStart: 111,
		StartNS: 222, Command: "mcp-server", Active: true,
		StartedAt: time.Now(),
	}})
	f.aliveSet[42] = false // owner gone
	f.aliveSet[1001] = true
	f.startSet[1001] = 222

	if n := Sweep(); n != 1 {
		t.Fatalf("Sweep = %d, want 1", n)
	}
	if len(f.terminated) != 1 || f.terminated[0] != 1001 {
		t.Fatalf("terminated = %v, want [1001]", f.terminated)
	}
}

func TestSweep_SkipsLiveOwner(t *testing.T) {
	_, f := seededJournal(t, []Entry{{
		PID: 1001, OwnerPID: 42, OwnerStart: 111,
		StartNS: 222, Active: true,
	}})
	f.aliveSet[42] = true // owner still running

	if n := Sweep(); n != 0 {
		t.Fatalf("Sweep = %d, want 0", n)
	}
	if len(f.terminated) != 0 {
		t.Fatalf("terminated = %v, want none", f.terminated)
	}
	// The record must survive for the live owner.
	j := currentJournal()
	if got := j.read(); len(got) != 1 {
		t.Fatalf("live-owner record pruned: %+v", got)
	}
}

func TestSweep_PidReuseIsNotKilled(t *testing.T) {
	_, f := seededJournal(t, []Entry{{
		PID: 1001, OwnerPID: 42, OwnerStart: 111,
		StartNS: 222, Active: true,
	}})
	f.aliveSet[42] = false
	// The pid is alive again but belongs to a DIFFERENT process
	// (creation stamp differs): must not be killed.
	f.aliveSet[1001] = true
	f.startSet[1001] = 999

	if n := Sweep(); n != 0 {
		t.Fatalf("Sweep = %d, want 0 (PID reuse)", n)
	}
	if len(f.terminated) != 0 {
		t.Fatalf("terminated = %v, want none", f.terminated)
	}
	// The stale record is dropped either way.
	if got := currentJournal().read(); len(got) != 0 {
		t.Fatalf("stale record kept: %+v", got)
	}
}

func TestSweep_DropsInactiveAndDeadChildren(t *testing.T) {
	j, f := seededJournal(t, []Entry{
		{PID: 1001, OwnerPID: 42, OwnerStart: 111, StartNS: 222, Active: true},
		{PID: 1002, OwnerPID: 42, OwnerStart: 111, StartNS: 223, Active: true},
		{PID: 1003, OwnerPID: 42, OwnerStart: 111, StartNS: 224, Active: false},
	})
	f.aliveSet[42] = false
	f.aliveSet[1001] = true
	f.startSet[1001] = 222   // orphan: killed
	f.aliveSet[1002] = false // child already dead: nothing to kill, drop

	if n := Sweep(); n != 1 {
		t.Fatalf("Sweep = %d, want 1", n)
	}
	if len(f.terminated) != 1 || f.terminated[0] != 1001 {
		t.Fatalf("terminated = %v, want [1001]", f.terminated)
	}
	if got := j.read(); len(got) != 0 {
		t.Fatalf("records survived sweep: %+v", got)
	}
}

func TestJournal_RewritePreservesValidJSON(t *testing.T) {
	resetJournal(t)
	path := filepath.Join(t.TempDir(), JournalFile)
	SetJournal(path)
	j := currentJournal()

	if err := j.append(Entry{PID: 1, OwnerPID: 2, Active: true}); err != nil {
		t.Fatal(err)
	}
	if err := j.setInactive(2, 1); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"active":false`) {
		t.Fatalf("inactive rewrite lost: %s", raw)
	}
	entries := j.read()
	if len(entries) != 1 || entries[0].Active {
		t.Fatalf("read after rewrite = %+v", entries)
	}
}
