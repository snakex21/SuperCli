package agent

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

// Retain only identities of failed verification commands. An unrelated read,
// edit or git diff is not a passing rerun. This is evidence for the existing
// goal-completion guard, not an automatic retry or a new final-answer gate.
type failedChecks struct {
	mu         sync.Mutex
	generation uint64
	pending    map[[sha256.Size]byte]struct{}
	latest     map[[sha256.Size]byte]uint64
}

// Observation order is process-wide because independent workers may complete
// the same check concurrently. Delayed delivery must not replace newer evidence.
var verificationSequence atomic.Uint64

type verificationObservation struct {
	key      [sha256.Size]byte
	failed   bool
	sequence uint64
}

func (f *failedChecks) reset() {
	f.mu.Lock()
	f.generation++
	f.pending, f.latest = nil, nil
	f.mu.Unlock()
}

func (f *failedChecks) unresolved() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.pending) > 0 }

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
	if f.latest == nil {
		f.latest = make(map[[sha256.Size]byte]uint64)
	}
	f.latest[observation.key] = observation.sequence
	if observation.failed {
		if f.pending == nil {
			f.pending = make(map[[sha256.Size]byte]struct{})
		}
		f.pending[observation.key] = struct{}{}
	} else {
		delete(f.pending, observation.key)
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
	observation := verificationObservation{key: key, failed: result.Err != nil, sequence: verificationSequence.Add(1)}
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
