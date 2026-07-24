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
	// HideWindow replaces SysProcAttr wholesale, so set CmdLine afterwards.
	childproc.HideWindow(c)
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.CmdLine = "/c " + command
	return c
}
