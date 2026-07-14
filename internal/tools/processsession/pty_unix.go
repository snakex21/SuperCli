//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package processsession

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type unixPTY struct {
	terminal *os.File
	cmd      *exec.Cmd
}

func startPTY(command []string, workdir string, env []string, columns, rows int) (ptyProcess, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = workdir
	cmd.Env = env
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(columns), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixPTY{terminal: terminal, cmd: cmd}, nil
}

func (p *unixPTY) Read(dst []byte) (int, error)  { return p.terminal.Read(dst) }
func (p *unixPTY) Write(src []byte) (int, error) { return p.terminal.Write(src) }
func (p *unixPTY) Close() error                  { return p.terminal.Close() }
func (p *unixPTY) Resize(columns, rows int) error {
	return pty.Setsize(p.terminal, &pty.Winsize{Cols: uint16(columns), Rows: uint16(rows)})
}
func (p *unixPTY) Wait() (int, error) {
	err := p.cmd.Wait()
	_ = p.terminal.Close()
	if p.cmd.ProcessState == nil {
		return -1, err
	}
	return p.cmd.ProcessState.ExitCode(), err
}
func (p *unixPTY) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}
