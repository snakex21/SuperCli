//go:build !windows

package shellescape

import (
	"context"
	"os/exec"

	"supercli/internal/system/childproc"
)

// newShellCmd builds the child process that runs a shell-escape command.
//
// POSIX shells take the command as a single argv element, which exec passes
// through untouched, so no special handling is needed here. The Windows
// counterpart in shell_windows.go has to work around cmd.exe's quoting rules.
func newShellCmd(ctx context.Context, command string) *exec.Cmd {
	c := exec.CommandContext(ctx, "sh", "-c", command)
	childproc.HideWindow(c)
	return c
}
