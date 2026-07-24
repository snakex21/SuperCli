//go:build windows

package shellescape

import (
	"context"
	"os/exec"
	"syscall"

	"supercli/internal/system/childproc"
)

// newShellCmd builds the child process that runs a shell-escape command.
//
// The command line is handed to cmd.exe raw instead of going through argv.
// Go joins argv into a Windows command line using the CommandLineToArgvW
// convention, which escapes an embedded double quote as \" . cmd.exe does not
// implement that convention: it passes the backslash straight through. So
//
//	echo ^<link rel="stylesheet" href="style.css"^> > index.html
//
// wrote  <link rel=\"stylesheet\" href=\"style.css\">  into the file and still
// exited 0 - the command "succeeded" while corrupting its output. Setting
// SysProcAttr.CmdLine bypasses Go's quoting so cmd.exe receives exactly the
// bytes the caller wrote.
//
// CmdLine does not carry argv[0]; the executable itself comes from cmd.Path,
// which exec.CommandContext resolves from "cmd".
func newShellCmd(ctx context.Context, command string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd")
	// HideWindow merges into SysProcAttr (it used to replace it), so CmdLine
	// survives regardless of order - including the second HideWindow call that
	// childproc.Start makes when the command is launched.
	childproc.HideWindow(c)
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.CmdLine = "/c " + command
	return c
}

// killShellTree terminates cmd.exe together with every process it started.
//
// Killing cmd.exe alone did not enforce the timeout: a grandchild such as
// ping.exe inherits the stdout/stderr pipe handles, and Wait blocks until those
// handles are closed, so a 200 ms deadline on "ping -n 30" returned after 29 s.
// The Job Object that childproc.Scope owns is closed here, and its
// kill-on-job-close limit takes the whole tree down at once.
func killShellTree(cmd *exec.Cmd, scope *childproc.Scope) error {
	// Scope.Kill is nil-safe and falls back to killing the shell directly when
	// the job could not be created (some managed hosts forbid nested jobs).
	return scope.Kill(cmd)
}
