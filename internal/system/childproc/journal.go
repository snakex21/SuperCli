package childproc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)// Orphan-process journal: durable record of every long-lived child this
// process spawns (MCP servers, LSP servers, pty sessions).
//
// Windows Job Objects already kill the whole descendant tree on a clean
// shutdown, but a hard kill (crash, taskkill /F, power loss) never runs
// the cleanup path. The journal gives every spawn a persistent record;
// on the next startup Sweep terminates children whose owner process is
// gone, with per-process start-time stamps guarding against PID reuse
// (a recycled PID must never cause a kill of an unrelated process).
//
// The file lives in the data dir (<dataDir>/processes.jsonl), one JSON
// object per line, rewritten (pruned) at every sweep and on mark-
// inactive. All writes are best-effort: the journal must never fail a
// spawn.
const (
	// JournalFile is the journal name inside the data dir.
	JournalFile = "processes.jsonl"
	// journalMaxLines bounds the file when a broken loop keeps
	// spawning: sweep rewrites keep at most this many entries.
	journalMaxLines = 4096
)

// Entry is one spawn record. OwnerPID/OwnerStart identify the process
// that spawned the child; PID/StartNS identify the child itself.
type Entry struct {
	ID         string    `json:"id"`
	PID        int       `json:"pid"`
	OwnerPID   int       `json:"owner_pid"`
	OwnerStart int64     `json:"owner_start"` // creation stamp, platform unit
	StartNS    int64     `json:"start_ns"`    // creation stamp of the child
	Command    string    `json:"command"`
	Active     bool      `json:"active"`
	StartedAt  time.Time `json:"started_at"`
}

// Journal is the on-disk spawn record. The liveness/terminate hooks are
// fields so tests can substitute fake implementations.
type Journal struct {
	path string

	// alive reports whether pid still exists AND was created at
	// startNS (the start-time guard is what makes the sweep safe
	// against PID reuse).
	alive func(pid int, startNS int64) bool
	// terminate kills the process with the given pid.
	terminate func(pid int) error

	ownerPID   int
	ownerStart int64
}

// currentJournal holds the process-global journal. SetJournal installs
// it once at startup; nil means "no journaling" (zero overhead).
var (
	journalMu sync.Mutex
	journal   *Journal
)

// SetJournal installs the process journal at path. A subsequent Sweep
// cleans up leftovers from previous runs. Safe to call more than once
// (later calls replace the journal); "" disables journaling.
func SetJournal(path string) {
	journalMu.Lock()
	defer journalMu.Unlock()
	if path == "" {
		journal = nil
		return
	}
	j := &Journal{
		path:      path,
		alive:     alive,
		terminate: terminate,
	}
	j.ownerPID = os.Getpid()
	j.ownerStart = selfStartNS()
	journal = j
}

func currentJournal() *Journal {
	journalMu.Lock()
	defer journalMu.Unlock()
	return journal
}

// journalChild records a just-started child so a later sweep can find
// it. Best-effort: failures are swallowed (a spawn must never fail
// because the journal write did).
func journalChild(cmd *exec.Cmd) {
	j := currentJournal()
	if j == nil || cmd == nil || cmd.Process == nil {
		return
	}
	entry := Entry{
		ID:         fmt.Sprintf("%d-%d", j.ownerPID, cmd.Process.Pid),
		PID:        cmd.Process.Pid,
		OwnerPID:   j.ownerPID,
		OwnerStart: j.ownerStart,
		StartNS:    childStartNS(cmd.Process.Pid),
		Command:    cmd.Path,
		Active:     true,
		StartedAt:  time.Now(),
	}
	_ = j.append(entry)
}

// journalDone marks a child inactive after it exited (naturally or via
// Kill). The entry is kept (marked inactive) so the sweep can prune
// the file deterministically.
func journalDone(cmd *exec.Cmd) {
	j := currentJournal()
	if j == nil || cmd == nil || cmd.Process == nil {
		return
	}
	_ = j.setInactive(j.ownerPID, cmd.Process.Pid)
}

// Sweep terminates every journaled child whose owner process is gone
// and prunes stale records. Returns the number of processes
// terminated. No-op when no journal is installed. Safe to call at any
// time; the app calls it once at startup, before spawning anything.
func Sweep() int {
	j := currentJournal()
	if j == nil {
		return 0
	}
	entries := j.read()
	if len(entries) == 0 {
		_ = os.Remove(j.path) // nothing alive: drop the empty file
		return 0
	}
	kept := make([]Entry, 0, len(entries))
	killed := 0
	for _, e := range entries {
		if !e.Active {
			continue // stale record from a completed child
		}
		if e.OwnerPID > 0 && j.alive(e.OwnerPID, e.OwnerStart) {
			// Owner still running (a concurrent supercli, or our own
			// spawns): not an orphan, keep the record.
			kept = append(kept, e)
			continue
		}
		// Owner is gone. Terminate the child only when it is really
		// the journaled process (start-time guard against PID reuse).
		if e.PID > 0 && j.alive(e.PID, e.StartNS) {
			if err := j.terminate(e.PID); err == nil {
				killed++
			}
		}
		// Drop the record either way: it can never be an orphan again.
	}
	_ = j.rewrite(kept)
	return killed
}

func (j *Journal) append(e Entry) error {
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := f.Write(b); err != nil {
		return err
	}
	return f.Sync()
}

func (j *Journal) setInactive(ownerPID, pid int) error {
	entries := j.read()
	if len(entries) == 0 {
		return nil
	}
	changed := false
	for i := range entries {
		if entries[i].OwnerPID == ownerPID && entries[i].PID == pid && entries[i].Active {
			entries[i].Active = false
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return j.rewrite(entries)
}

func (j *Journal) read() []Entry {
	f, err := os.Open(j.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var out []Entry
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
		if len(out) >= journalMaxLines {
			break
		}
	}
	return out
}

// rewrite atomically replaces the journal file (temp + rename), so a
// concurrent reader never sees a half-written file.
func (j *Journal) rewrite(entries []Entry) error {
	if len(entries) == 0 {
		return os.Remove(j.path)
	}
	tmp := j.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, j.path)
}
