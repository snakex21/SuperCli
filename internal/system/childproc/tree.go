package childproc

import (
	"bytes"
	"errors"
	"os/exec"
	"sync"
)

// Tree-scoped helper commands (after 1.0.41). exec.CommandContext cancels by
// killing only the started process. On Windows "git" is the Git-for-Windows
// launcher (cmd\git.exe), which runs the real mingw64 git as a child: a
// timeout killed the launcher and left the real git scanning a 224 GB tree
// with no parent (136 such processes, 40% of a 16-core machine, 3.6 GB).
//
// StartTree starts cmd in a kill-on-close job (Windows) or its own process
// group (elsewhere): cancelling the command's context, Kill, and SuperCli's
// own exit end the whole tree. Output/CombinedOutput/Run are the usual exec
// shapes on top of it. Unlike Start, helper commands are not journaled: they
// are short and frequent, and the job already ends them with SuperCli.

// TreeCmd is a started command with its process-tree scope.
type TreeCmd struct {
	Cmd   *exec.Cmd
	mu    sync.Mutex
	scope *Scope
}

// StartTree starts cmd so that cancellation ends its whole process tree.
// Call Wait (not cmd.Wait) to release the scope.
func StartTree(cmd *exec.Cmd) (*TreeCmd, error) {
	t := &TreeCmd{Cmd: cmd}
	if cmd.Cancel != nil { // made with CommandContext: cancel ends the tree
		cmd.Cancel = func() error { return t.Kill() }
	}
	scope, err := startTree(cmd)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.scope = scope
	t.mu.Unlock()
	return t, nil
}

// Kill ends the command and every descendant.
func (t *TreeCmd) Kill() error {
	t.mu.Lock()
	scope := t.scope
	t.mu.Unlock()
	if scope != nil {
		return scope.killTree(t.Cmd)
	}
	if t.Cmd.Process != nil {
		return t.Cmd.Process.Kill()
	}
	return nil
}

// Wait waits for the command, then ends any descendant still running (a
// child that outlived its parent) and releases the scope.
func (t *TreeCmd) Wait() error {
	err := t.Cmd.Wait()
	t.mu.Lock()
	scope := t.scope
	t.scope = nil
	t.mu.Unlock()
	if scope != nil {
		_ = scope.Close()
	}
	return err
}

// Run is cmd.Run with the process tree scoped.
func Run(cmd *exec.Cmd) error {
	t, err := StartTree(cmd)
	if err != nil {
		return err
	}
	return t.Wait()
}

// Output is cmd.Output with the process tree scoped.
func Output(cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr *bytes.Buffer
	if cmd.Stderr == nil {
		stderr = &bytes.Buffer{}
		cmd.Stderr = stderr
	}
	err := Run(cmd)
	if stderr != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitErr.Stderr = stderr.Bytes()
		}
	}
	return stdout.Bytes(), err
}

// CombinedOutput is cmd.CombinedOutput with the process tree scoped.
func CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stdout != nil || cmd.Stderr != nil {
		return nil, errors.New("exec: Stdout or Stderr already set")
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := Run(cmd)
	return out.Bytes(), err
}
