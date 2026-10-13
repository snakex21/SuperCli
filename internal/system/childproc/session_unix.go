//go:build unix

package childproc

import (
	"os/exec"
	"syscall"
)

// detachSession starts the child in a new session without a controlling
// terminal. A child sharing the TUI's terminal could open /dev/tty (ssh host
// key confirmation, git credential prompts, sudo, pinentry): in the TUI's
// foreground group it competed with the TUI for keystrokes, and in a separate
// process group it was stopped by SIGTTIN and the agent waited forever. With
// no controlling terminal such prompts fail immediately instead.
//
// Setsid also makes the child a process-group leader (pgid == pid), so group
// kills keep working. Setpgid must stay off: setpgid on a session leader fails.
func detachSession(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setpgid = false
}
