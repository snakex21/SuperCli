// Package goal is the F8 long-term objective tracker.
//
// A Goal is a unit of work the user wants the agent to
// pursue across one or more sessions. Each goal has a
// title, an optional success criteria string, an ordered
// list of Tasks, and a status. The Service keeps an
// in-memory pointer to the "active" goal (the most
// recently created one with status=active) and renders a
// `[current_goal]` block for the system prompt.
//
// Three storage layers cooperate:
//
//   1. Storage    (storage.go)  — SQLite CRUD
//   2. Service    (service.go)  — in-memory state + prompt
//                                 injection
//   3. Decompose  (decompose.go) — model-driven task
//                                 suggestion (opt-in)
//
// The package does NOT depend on agent, llm, tui, or
// reflect. It only depends on database/sql and the
// storage package's `*sql.DB`. This keeps the dependency
// graph acyclic and lets the TUI / tool layer import it
// without dragging in the agent loop.
package goal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Status is the lifecycle state of a Goal or Task. The
// zero value is StatusActive; callers should set it
// explicitly to avoid surprises.
type Status string

const (
	// StatusActive is the default state. Visible to the
	// model in the system prompt.
	StatusActive Status = "active"
	// StatusPaused means the goal is on hold but still
	// tracked. The model does NOT see paused goals in
	// the system prompt.
	StatusPaused Status = "paused"
	// StatusDone means the user marked the goal
	// complete. Kept in SQLite for history; invisible
	// to the model.
	StatusDone Status = "done"
	// StatusAbandoned means the user gave up. Same
	// visibility rules as StatusDone.
	StatusAbandoned Status = "abandoned"

	// TaskPending is the default for a new task.
	TaskPending Status = "pending"
	// TaskInProgress is set when the model or user
	// has started working on the task.
	TaskInProgress Status = "in_progress"
	// TaskDone is final.
	TaskDone Status = "done"
	// TaskSkipped is final; the task is recorded but
	// was deliberately not done.
	TaskSkipped Status = "skipped"
)

// ValidGoalStatus reports whether s is one of the four
// valid goal statuses.
func ValidGoalStatus(s Status) bool {
	switch s {
	case StatusActive, StatusPaused, StatusDone, StatusAbandoned:
		return true
	}
	return false
}

// ValidTaskStatus reports whether s is one of the four
// valid task statuses.
func ValidTaskStatus(s Status) bool {
	switch s {
	case TaskPending, TaskInProgress, TaskDone, TaskSkipped:
		return true
	}
	return false
}

// Goal is a long-term objective.
//
// Notes (F8 design):
//   - Description is the user's free-form prose.
//   - SuccessCriteria is what "done" means. Optional.
//   - Notes accumulates progress notes (time-stamped
//     free text) appended by `/goal note ...`.
//   - ParentSessionID is the session that created the
//     goal; useful for "what was I doing when I started
//     this?" queries.
type Goal struct {
	ID              string
	Title           string
	Description     string
	SuccessCriteria string
	Notes           string
	Status          Status
	CreatedAt       time.Time
	CompletedAt     *time.Time
	ParentSessionID string
}

// Task is one ordered step under a Goal.
type Task struct {
	ID          string
	GoalID      string
	Seq         int
	Title       string
	Status      Status
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// ErrNotFound is returned when a lookup by id misses.
var ErrNotFound = errors.New("goal: not found")

// ErrEmptyTitle is returned when a goal is created
// without a title. Titles are required.
var ErrEmptyTitle = errors.New("goal: title is empty")

// ErrInvalidStatus is returned when a status string
// doesn't match one of the known values.
var ErrInvalidStatus = errors.New("goal: invalid status")

// validateStatus returns nil for valid goal statuses and
// ErrInvalidStatus otherwise.
func validateStatus(s Status) error {
	if ValidGoalStatus(s) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidStatus, s)
}

// generateID returns a short, time-prefixed id. The id
// is NOT a UUID; it's `<unixnano>-<rand>`. We use
// time.Now().UnixNano() for natural ordering and a 4-byte
// random suffix to avoid collisions when two goals are
// created in the same nanosecond (rare but possible on
// fast machines).
func generateID(randBytes func() string) string {
	now := nowFn().UnixNano()
	return fmt.Sprintf("g-%d-%s", now, randBytes())
}

// generateTaskID is the variant for tasks. Uses the
// same shape but a different prefix for readability
// when scanning the DB.
func generateTaskID(randBytes func() string) string {
	now := nowFn().UnixNano()
	return fmt.Sprintf("t-%d-%s", now, randBytes())
}

// nowFn is the clock used by generateID / generateTaskID.
// Tests override it to force "same nanosecond" conditions
// and verify id uniqueness under clock stalls. Production
// code never touches it; the default is time.Now.
var nowFn = time.Now

// shortHash returns the last 6 chars of the input as a
// hex string. Used as the "rand" suffix for ids. The
// caller controls the source string; for the default
// path we use fmt.Sprintf("%x", ...) on a small int.
func shortHash(seed int) string {
	const digits = "0123456789abcdef"
	if seed < 0 {
		seed = -seed
	}
	out := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		out[i] = digits[seed%16]
		seed /= 16
	}
	return string(out)
}

// defaultRandBytes is the production id randomizer. It
// returns 16 hex chars (8 bytes, 64 bits of entropy) from
// crypto/rand so ids are unique even when many are
// generated in the same nanosecond.
//
// History: the original implementation pulled the lower
// 32 bits of time.Now().UnixNano() and hashed them. That
// "random" suffix was itself time-derived, so two ids
// generated in the same nanosecond (or with the same
// lower 32 bits of UnixNano) produced the same suffix
// and clashed on the UNIQUE(id) constraint. Symptom was
// a flaky TestGoalTool_Decompose_* that called AddTask
// 5 times in a tight loop and hit
// `UNIQUE constraint failed: goal_tasks.id`. The fix is
// real entropy; the time prefix is now redundant for
// uniqueness (kept only for natural sort order) and the
// suffix is the source of truth.
//
// crypto/rand.Read on a healthy OS is non-failing (it
// reads from /dev/urandom on Unix and BCryptGenRandom on
// Windows, and the package's init() panics if the source
// is unavailable). We still defend against a Read error
// by falling back to a time-derived suffix, which is the
// old broken behavior — better to insert with a likely-
// unique id than to refuse the insert.
func defaultRandBytes() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	// Theoretical fallback: combine two UnixNano reads so
	// consecutive calls land in different nanoseconds, plus
	// a counter to break ties.
	return shortHash(int(time.Now().UnixNano()&0xFFFFFFFF)) +
		shortHash(int(time.Now().UnixNano()>>32) & 0xFFFFFFFF)
}

// renderGoalMarkdown is the human-readable form of a
// goal + its tasks. Used by `/goal show` and the `goal`
// tool's `show` action.
func renderGoalMarkdown(g *Goal, tasks []Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", g.Title)
	if g.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", g.Description)
	}
	if g.SuccessCriteria != "" {
		fmt.Fprintf(&b, "\n## Success criteria\n\n%s\n", g.SuccessCriteria)
	}
	fmt.Fprintf(&b, "\nstatus: %s\n", g.Status)
	if g.Notes != "" {
		fmt.Fprintf(&b, "\n## Notes\n\n%s\n", g.Notes)
	}
	if len(tasks) > 0 {
		fmt.Fprintf(&b, "\n## Tasks\n")
		for _, t := range tasks {
			mark := " "
			switch t.Status {
			case TaskDone:
				mark = "x"
			case TaskSkipped:
				mark = "~"
			case TaskInProgress:
				mark = ">"
			}
			fmt.Fprintf(&b, "- [%s] %d. %s\n", mark, t.Seq, t.Title)
		}
	}
	return b.String()
}
