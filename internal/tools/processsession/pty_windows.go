//go:build windows

package processsession

import (
	"fmt"
	"sync"
	"syscall"

	"github.com/charmbracelet/x/conpty"
	"golang.org/x/sys/windows"
)

type windowsPTY struct {
	terminal *conpty.ConPty
	handle   windows.Handle
	mu       sync.Mutex
	exited   bool
}

func startPTY(command []string, workdir string, env []string, columns, rows int) (ptyProcess, error) {
	terminal, err := conpty.New(columns, rows, 0)
	if err != nil {
		return nil, err
	}
	_, handle, err := terminal.Spawn(command[0], command, &syscall.ProcAttr{Dir: workdir, Env: env})
	if err != nil {
		_ = terminal.Close()
		return nil, err
	}
	return &windowsPTY{terminal: terminal, handle: windows.Handle(handle)}, nil
}

func (p *windowsPTY) Read(dst []byte) (int, error)  { return p.terminal.Read(dst) }
func (p *windowsPTY) Write(src []byte) (int, error) { return p.terminal.Write(src) }
func (p *windowsPTY) Close() error                  { return p.terminal.Close() }
func (p *windowsPTY) Resize(columns, rows int) error {
	return p.terminal.Resize(columns, rows)
}

func (p *windowsPTY) Wait() (int, error) {
	_, waitErr := windows.WaitForSingleObject(p.handle, windows.INFINITE)
	var code uint32
	exitErr := windows.GetExitCodeProcess(p.handle, &code)
	p.mu.Lock()
	p.exited = true
	p.mu.Unlock()
	_ = p.terminal.Close()
	_ = windows.CloseHandle(p.handle)
	if waitErr != nil {
		return -1, waitErr
	}
	if exitErr != nil {
		return -1, exitErr
	}
	if code != 0 {
		return int(code), fmt.Errorf("exit status %d", code)
	}
	return int(code), nil
}

func (p *windowsPTY) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exited {
		return nil
	}
	return windows.TerminateProcess(p.handle, 1)
}
