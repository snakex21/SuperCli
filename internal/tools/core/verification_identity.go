package core

import (
	"crypto/sha256"
	"path/filepath"
	"strings"
)

// VerificationCommandKey identifies equivalent verification evidence. Output
// verbosity is immaterial for a plain go test package list; all other options,
// the executable, package order, workdir and explicit environment stay exact.
// This is NOT a command-output cache key: those use CommandKey.
func VerificationCommandKey(command []string, workdir string, env []string) [sha256.Size]byte {
	return CommandKey(verificationCommand(command), workdir, env)
}

func verificationCommand(command []string) []string {
	if len(command) < 2 || command[1] != "test" {
		return command
	}
	executable := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(command[0], "\\", "/"))), ".exe")
	if executable != "go" {
		return command
	}
	changed := false
	for _, arg := range command[2:] {
		if goVerboseFlag(arg) {
			changed = true
		} else if strings.HasPrefix(arg, "-") || arg == "" {
			// Unknown flags may consume the next token (even a literal "-v"), alter
			// coverage or forward custom arguments. Keep their exact identity.
			return command
		}
	}
	if !changed {
		return command
	}
	normalized := make([]string, 0, len(command))
	normalized = append(normalized, command[:2]...)
	for _, arg := range command[2:] {
		if !goVerboseFlag(arg) {
			normalized = append(normalized, arg)
		}
	}
	return normalized
}

func goVerboseFlag(arg string) bool {
	switch arg {
	case "-v", "-v=true", "-v=false", "-test.v", "-test.v=true", "-test.v=false":
		return true
	}
	return false
}
