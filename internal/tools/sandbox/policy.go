package sandbox

import "fmt"

// Op names a sandboxed operation. The set is closed
// (no external package can invent new ops); F8+ may add
// more, but always via this enum so the policy stays
// in one file.
type Op string

const (
	// OpFileRead is read-only file access.
	OpFileRead Op = "file_read"
	// OpFileWrite is any write that creates or
	// modifies an existing file.
	OpFileWrite Op = "file_write"
	// OpFileDelete is unlink / rmdir.
	OpFileDelete Op = "file_delete"
	// OpBash is exec.Command on the host shell.
	OpBash Op = "bash"
	// OpNetwork is any HTTP fetch or socket use.
	OpNetwork Op = "network"
)

// Decision is the verdict from AllowDestructive. The
// boolean says "yes/no". Reason is human-readable and
// surfaced to the model + the user when a destructive
// op is denied.
type Decision struct {
	Allowed bool
	Reason  string
}

// AllowDestructive is the single entry point that
// gates file_write, file_delete, and bash against the
// sandbox policy. The path is expected to be already
// absolute and cleaned (callers should run it through
// ResolveSafe first).
//
// Policy (F7):
//
//	inside home   -> allow
//	outside home  -> deny (ErrDenied)
//	sensitive sys -> deny (ErrDenied)
//
// The model cannot override the policy from inside its
// own prompt. An F7+1 build will read a [tool.policy]
// table from the SKILL.md to allow per-path
// exceptions; for F7 the rules are hard-coded.
func AllowDestructive(home string, op Op, path string) (Decision, error) {
	if home == "" {
		return Decision{}, fmt.Errorf("sandbox: AllowDestructive: empty home")
	}
	if path == "" {
		return Decision{Allowed: false, Reason: "empty path"}, nil
	}
	switch op {
	case OpFileWrite, OpFileDelete, OpBash:
		// fall through to the policy check.
	default:
		// Read-only and network ops are not
		// destructive; callers can short-circuit
		// before calling this.
		return Decision{Allowed: true, Reason: "non-destructive op"}, nil
	}
	resolved, err := ResolveSafe(home, path)
	if err != nil {
		return Decision{Allowed: false, Reason: err.Error()}, err
	}
	if isSensitive(resolved) {
		return Decision{
			Allowed: false,
			Reason:  fmt.Sprintf("sensitive system path: %s", resolved),
		}, ErrDenied
	}
	if Unsandboxed {
		return Decision{Allowed: true, Reason: "unsandboxed"}, nil
	}
	return Decision{Allowed: true, Reason: "inside home"}, nil
}
