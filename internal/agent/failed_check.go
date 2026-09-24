package agent

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Retain only identities of failed verification commands. An unrelated read,
// edit or git diff is not a passing rerun. This is evidence for the existing
// goal-completion guard, not an automatic retry or a new final-answer gate.
type failedChecks struct {
	mu      sync.Mutex
	pending map[[sha256.Size]byte]struct{}
}

func (f *failedChecks) reset()           { f.mu.Lock(); f.pending = nil; f.mu.Unlock() }
func (f *failedChecks) unresolved() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.pending) > 0 }

func (l *Loop) recordCheckResult(tc llm.ToolCall, result tools.Result) {
	if tc.Name != "ctx_execute" && tc.Name != "process_session" {
		return
	}
	var args struct {
		Command []string `json:"command"`
		Workdir string   `json:"workdir"`
		Env     []string `json:"env_extra"`
	}
	if json.Unmarshal([]byte(tc.Arguments), &args) != nil {
		return
	}
	if tc.Name == "process_session" {
		var operation struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal([]byte(tc.Arguments), &operation)
		switch strings.ToLower(strings.TrimSpace(operation.Action)) {
		case "stop", "list", "resize":
			return // management success cannot resolve a failed command
		}
		// wait/poll/write may reveal the exit of a command started on a previous step.
		var snap struct {
			Command []string `json:"command"`
			Workdir string   `json:"workdir"`
			Status  string   `json:"status"`
		}
		if json.Unmarshal([]byte(result.Text), &snap) != nil || (snap.Status != "done" && snap.Status != "failed" && snap.Status != "timeout") {
			return
		}
		args.Command, args.Workdir = snap.Command, snap.Workdir
	}
	if !isVerificationCommand(args.Command) {
		return
	}
	if !filepath.IsAbs(args.Workdir) {
		args.Workdir = filepath.Join(l.baseDir, args.Workdir)
	}
	args.Workdir = filepath.Clean(args.Workdir)
	if len(args.Env) == 0 {
		args.Env = nil // omitted and explicitly empty environment additions match
	}
	// timeout and output caps are transport options: correcting them must allow
	// a successful rerun of the same command to resolve its earlier failure.
	encoded, _ := json.Marshal(args)
	key := sha256.Sum256(encoded)
	f := &l.failedChecks
	f.mu.Lock()
	defer f.mu.Unlock()
	if result.Err != nil {
		if f.pending == nil {
			f.pending = make(map[[sha256.Size]byte]struct{})
		}
		f.pending[key] = struct{}{}
	} else {
		delete(f.pending, key)
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
	case "make", "cmake", "zig":
		return command[1] == "test" || command[1] == "check" || command[1] == "build" || command[1] == "--build"
	}
	return false
}
