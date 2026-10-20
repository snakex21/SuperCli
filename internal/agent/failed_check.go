package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

// Retain only identities of failed verification commands. An unrelated read,
// edit or git diff is not a passing rerun. This is evidence for the existing
// goal-completion guard, not an automatic retry or a new final-answer gate.
type failedChecks struct {
	mu              sync.Mutex
	generation      uint64
	pending         map[[sha256.Size]byte]verificationObservation
	latest          map[[sha256.Size]byte]uint64
	known           map[[sha256.Size]byte]verificationObservation
	revision        uint64
	changedSequence uint64
	scopeID         string
	goalState       core.VerificationState
}

// Observation order is process-wide because independent workers may complete
// the same check concurrently. Delayed delivery must not replace newer evidence.
var verificationSequence atomic.Uint64

type verificationObservation struct {
	key             [sha256.Size]byte
	failed          bool
	sequence        uint64
	started         time.Time
	startedSequence uint64
	runID           string
	scope           verificationScope
	coverage        bool
	stale           bool
}

func (f *failedChecks) reset() {
	f.mu.Lock()
	f.resetLocked()
	f.mu.Unlock()
}

func (f *failedChecks) resetLocked() {
	f.generation++
	f.revision++
	f.pending, f.latest, f.known = nil, nil, nil
	f.changedSequence = 0
}

func (f *failedChecks) unresolved() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.pending) > 0 }

func (f *failedChecks) stateRevision() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.revision
}

func (l *Loop) bindVerificationGoalScope() {
	if l.registry == nil {
		return
	}
	tool, ok := l.registry.Get("goal")
	if !ok || tool.VerificationState == nil {
		return
	}
	state := tool.VerificationState()
	l.failedChecks.mu.Lock()
	defer l.failedChecks.mu.Unlock()
	if l.failedChecks.scopeID != state.Scope {
		l.failedChecks.resetLocked()
		l.failedChecks.scopeID = state.Scope
	}
	if l.failedChecks.goalState != state {
		l.failedChecks.goalState = state
		l.failedChecks.revision++
	}
}

// Only executor observations and real goal mutations advance this revision.
// Rewording a claim or polling the same terminal execution is not new evidence.
func (f *failedChecks) goalChanged() { f.mu.Lock(); f.revision++; f.mu.Unlock() }

func (f *failedChecks) workspaceChanged() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workspaceChangedLocked()
}

func (f *failedChecks) workspaceChangedLocked() {
	f.changedSequence = core.NextExecutionSequence()
	f.revision++
	for key, observation := range f.known {
		observation.stale = true
		if f.pending == nil {
			f.pending = make(map[[sha256.Size]byte]verificationObservation)
		}
		f.pending[key] = observation
	}
}

func (l *Loop) recordGoalTransition(tc llm.ToolCall) {
	if tc.Name != "goal" {
		return
	}
	var args struct{ Action string }
	if json.Unmarshal([]byte(tc.Arguments), &args) != nil {
		return
	}
	switch args.Action {
	case "set", "abandon", "mark_done":
		// A confirmed new/terminal goal is a new observer generation. Late
		// reports from the previous goal cannot resurrect its failures.
		l.failedChecks.reset()
	case "add_task", "complete_task", "skip_task", "decompose":
		l.failedChecks.goalChanged()
	}
}

func isGoalMarkDone(name string, raw json.RawMessage) bool {
	var args struct{ Action string }
	return name == "goal" && json.Unmarshal(raw, &args) == nil && args.Action == "mark_done"
}

func (f *failedChecks) blocker() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var first verificationObservation
	for _, observation := range f.pending {
		if first.sequence == 0 || observation.sequence < first.sequence {
			first = observation
		}
	}
	state := "failed or not run"
	if first.stale {
		state = "code changed since verification"
	}
	return fmt.Sprintf("goal: %s: %s (check %x, goal %q, run %q, state %d); execute a passing rerun or a proven covering suite in the same mode/environment on current code before marking the work complete", state, first.scope.label(), first.key[:4], f.scopeID, first.runID, f.revision)
}

// Bind the observer at delegation dispatch, not when a background goroutine
// eventually starts. Previous-run workers cannot affect a later user request.
func (f *failedChecks) observer() func(verificationObservation) {
	f.mu.Lock()
	generation := f.generation
	f.mu.Unlock()
	return func(observation verificationObservation) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.generation == generation {
			f.recordLocked(observation)
		}
	}
}

func (f *failedChecks) record(observation verificationObservation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked(observation)
}

func (f *failedChecks) recordLocked(observation verificationObservation) {
	if observation.sequence <= f.latest[observation.key] {
		return
	}
	if previous, ok := f.known[observation.key]; ok && observation.startedSequence != 0 && previous.startedSequence != 0 {
		if observation.startedSequence < previous.startedSequence || (observation.startedSequence == previous.startedSequence && observation.failed == previous.failed) {
			return // Delayed completion/poll cannot replace a newer execution.
		}
	}
	if !observation.failed && observation.startedSequence != 0 && observation.startedSequence < f.changedSequence {
		return // A command started before an observed edit cannot verify that edit.
	}
	if f.latest == nil {
		f.latest = make(map[[sha256.Size]byte]uint64)
	}
	f.latest[observation.key] = observation.sequence
	if f.known == nil {
		f.known = make(map[[sha256.Size]byte]verificationObservation)
	}
	f.known[observation.key] = observation
	f.revision++
	if observation.failed {
		if f.pending == nil {
			f.pending = make(map[[sha256.Size]byte]verificationObservation)
		}
		f.pending[observation.key] = observation
	} else {
		delete(f.pending, observation.key)
		if observation.coverage {
			for key, failed := range f.pending {
				if observation.scope.covers(failed.scope) ||
					(failed.stale && !failed.failed && observation.scope.revalidatesSuccessful(failed.scope)) {
					delete(f.pending, key)
					f.latest[key] = observation.sequence
					// Preserve the original requirement for a later code mutation.
					failed.failed, failed.stale, failed.started = false, false, observation.started
					failed.startedSequence = observation.startedSequence
					f.known[key] = failed
				}
			}
		}
	}
}

func (l *Loop) recordCheckResult(tc llm.ToolCall, result tools.Result) {
	if tc.Name != "ctx_execute" && tc.Name != "process_session" {
		return
	}
	var args struct {
		Command    []string `json:"command"`
		Workdir    string   `json:"workdir"`
		Env        []string `json:"env_extra"`
		Action     string   `json:"action"`
		ID         string   `json:"id"`
		ProcessEnv []string `json:"env"`
	}
	if json.Unmarshal([]byte(tc.Arguments), &args) != nil {
		// Execute already accepts JSON-encoded argv/env arrays. A passing
		// rerun must not stay unresolved just because its wire format changed.
		if l.registry == nil || json.Unmarshal(l.registry.CoerceArgs(tc.Name, json.RawMessage(tc.Arguments)), &args) != nil {
			return
		}
	}
	var unknownProcess string
	if tc.Name == "process_session" {
		action := strings.ToLower(strings.TrimSpace(args.Action))
		switch action {
		case "stop", "list", "resize":
			return // management success cannot resolve a failed command
		}
		// wait/poll/write may reveal the exit of a command started on a previous step.
		var snap struct {
			ID      string   `json:"id"`
			Command []string `json:"command"`
			Workdir string   `json:"workdir"`
			Status  string   `json:"status"`
		}
		if json.Unmarshal([]byte(result.Text), &snap) != nil || (snap.Status != "done" && snap.Status != "failed" && snap.Status != "timeout") {
			return
		}
		args.Command, args.Workdir = snap.Command, snap.Workdir
		args.Env = args.ProcessEnv
		if result.CommandKey == nil && action != "start" {
			// A legacy/custom snapshot without execution metadata cannot prove
			// its environment matches a different process or foreground check.
			unknownProcess = snap.ID
			if unknownProcess == "" {
				unknownProcess = args.ID
			}
			if unknownProcess == "" {
				unknownProcess = tc.ID
			}
			unknownProcess = "unknown-process-environment:" + unknownProcess
		}
	}
	if result.Execution != nil {
		args.Command, args.Workdir = result.Execution.Command, result.Execution.Workdir
	}
	if !isVerificationCommand(args.Command) {
		return
	}
	if !filepath.IsAbs(args.Workdir) {
		args.Workdir = filepath.Join(l.baseDir, args.Workdir)
	}
	// Environment order is immaterial and repeated keys use their last value.
	// timeout and output caps are transport options, not a different test.
	var key [sha256.Size]byte
	if result.CommandKey != nil {
		key = *result.CommandKey
	} else {
		key = core.VerificationCommandKey(args.Command, args.Workdir, args.Env)
	}
	if unknownProcess != "" {
		encoded, _ := json.Marshal(struct {
			Process string
			Command [sha256.Size]byte
		}{unknownProcess, key})
		key = sha256.Sum256(encoded)
	}
	if result.Execution != nil {
		// The legacy key intentionally preserves its public/internal contract.
		// Verification also binds the effective inherited executor environment.
		var identity [2 * sha256.Size]byte
		copy(identity[:sha256.Size], key[:])
		copy(identity[sha256.Size:], result.Execution.Environment[:])
		key = sha256.Sum256(identity[:])
	}
	failed := result.Err != nil
	var started time.Time
	environment := core.CommandKey(nil, args.Workdir, args.Env)
	coverage := false
	if execution := result.Execution; execution != nil {
		started, environment = execution.StartedAt, execution.Environment
		failed = failed || !execution.Completed || execution.ExitCode != 0
		if !failed {
			failed = !execution.OutputComplete || verificationSkipped(execution.Stdout, execution.Stderr)
			coverage = !failed && goTestExecuted(execution.Stdout)
		}
	} else if !failed {
		// Legacy/custom executors need at least an explicit successful exit.
		// Worker reports (including JSON/text saying PASS) never enter here.
		var exit struct {
			Code           *int `json:"exit_code"`
			Stdout, Stderr string
		}
		decodeErr := json.Unmarshal([]byte(result.Text), &exit)
		if tc.Name == "process_session" && decodeErr == nil && exit.Code == nil {
			return // Legacy polling with no exit status is neither failure nor PASS.
		}
		failed = decodeErr != nil || exit.Code == nil || *exit.Code != 0 || verificationSkipped(exit.Stdout, exit.Stderr)
	}
	observation := verificationObservation{key: key, failed: failed, sequence: verificationSequence.Add(1), started: started,
		runID: l.runID, scope: describeVerification(args.Command, args.Workdir, environment), coverage: coverage}
	if result.Execution != nil {
		observation.startedSequence = result.Execution.StartedSequence
		observation.scope.observeGoOutput(result.Execution.Stdout)
		if !result.Execution.OutputComplete {
			observation.scope.goTest = false
		}
	}
	l.failedChecks.record(observation)
	if l.verificationObserver != nil {
		l.verificationObserver(observation)
	}
}

func isVerificationCommand(command []string) bool {
	if len(command) == 0 {
		return false
	}
	exe := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(command[0], "\\", "/"))), ".exe")
	if exe == "pytest" || exe == "pytest3" || exe == "ctest" {
		return true
	}
	if len(command) < 2 {
		return false
	}
	switch exe {
	case "go":
		return command[1] == "test" || command[1] == "vet" || command[1] == "build"
	case "cargo":
		return command[1] == "test" || command[1] == "check" || command[1] == "clippy" || command[1] == "build"
	case "dotnet":
		return command[1] == "test" || command[1] == "build"
	case "npm", "npm.cmd", "pnpm", "pnpm.cmd", "yarn", "yarn.cmd", "bun":
		action := command[1]
		if action == "run" && len(command) > 2 {
			action = command[2]
		}
		return action == "test" || action == "build" || action == "lint" || action == "typecheck"
	case "python", "python3", "python3.12", "python3.13":
		return repeatableCheckCommand(command)
	case "cmd", "powershell", "pwsh", "sh", "bash", "dash", "zsh":
		return isVerificationCommand(unwrapVerificationShell(command))
	case "make", "cmake", "zig":
		return command[1] == "test" || command[1] == "check" || command[1] == "build" || command[1] == "--build"
	}
	return false
}
