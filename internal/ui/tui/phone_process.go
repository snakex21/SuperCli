package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/system/childproc"
)

type phoneEvent struct {
	kind        string
	text        string
	done, total int64
	err         error
}

// Unknown log lines never become UI text or pairing links.
func parsePhoneLine(line string) (phoneEvent, bool) {
	kind, text, _ := strings.Cut(strings.TrimSuffix(line, "\r"), " ")
	e := phoneEvent{kind: kind, text: text}
	switch kind {
	case "SUPERCLI_PHONE_NEED_DOWNLOAD", "SUPERCLI_PHONE_PROGRESS":
		fields := strings.Fields(text)
		count := 1
		if kind == "SUPERCLI_PHONE_PROGRESS" {
			count = 2
		}
		if len(fields) != count {
			return phoneEvent{}, false
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || n < 0 {
			return phoneEvent{}, false
		}
		e.total = n
		if count == 2 {
			e.done = n
			e.total, err = strconv.ParseInt(fields[1], 10, 64)
			if err != nil || e.total < 0 || (e.total > 0 && e.done > e.total) {
				return phoneEvent{}, false
			}
		}
	case "SUPERCLI_PHONE_URL":
		u, err := url.Parse(text)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.ContainsAny(text, " \t\r\n\x1b") {
			return phoneEvent{}, false
		}
	case "SUPERCLI_PHONE_CLOSED":
		e.text = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, text)
	default:
		return phoneEvent{}, false
	}
	return e, true
}

type phoneProcess interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Wait() (int, error)
	Kill() error
}

type phoneLaunchers struct {
	executable func() (string, error)
	stat       func(string) (os.FileInfo, error)
	start      func(*exec.Cmd) (phoneProcess, error)
}

type scopedPhoneProcess struct {
	cmd    *exec.Cmd
	scope  *childproc.Scope
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *scopedPhoneProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *scopedPhoneProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *scopedPhoneProcess) Kill() error           { return p.scope.Kill(p.cmd) }
func (p *scopedPhoneProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	_ = p.scope.Close()
	if p.cmd.ProcessState != nil {
		return p.cmd.ProcessState.ExitCode(), err
	}
	return -1, err
}

func defaultPhoneLaunchers() phoneLaunchers {
	return phoneLaunchers{executable: os.Executable, stat: os.Stat, start: func(cmd *exec.Cmd) (phoneProcess, error) {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return nil, err
		}
		scope, err := childproc.Start(cmd)
		if err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, err
		}
		return &scopedPhoneProcess{cmd: cmd, scope: scope, stdin: stdin, stdout: stdout}, nil
	}}
}

type phoneSession struct {
	ctx      context.Context
	cancel   context.CancelFunc
	events   chan phoneEvent
	commands chan string
	done     chan struct{}
}

type phoneEventMsg struct {
	session *phoneSession
	event   phoneEvent
}

func newPhoneSession() *phoneSession {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	return &phoneSession{ctx: ctx, cancel: cancel, events: make(chan phoneEvent, 16), commands: make(chan string, 4), done: make(chan struct{})}
}

func (s *phoneSession) emit(e phoneEvent) {
	select {
	case s.events <- e:
	case <-s.ctx.Done():
	}
}

func waitPhoneEvent(s *phoneSession) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-s.events
		if !ok {
			return phoneEventMsg{session: s, event: phoneEvent{kind: "exit"}}
		}
		return phoneEventMsg{session: s, event: e}
	}
}

func launchPhoneCmd(s *phoneSession, home, dataDir string, allow bool, launch phoneLaunchers) tea.Cmd {
	return func() tea.Msg {
		process, err := launchPhoneProcess(home, dataDir, allow, launch)
		if err != nil {
			s.cancel()
			close(s.done)
			return phoneEventMsg{session: s, event: phoneEvent{kind: "exit", err: err}}
		}
		go s.run(process)
		return waitPhoneEvent(s)()
	}
}

func launchPhoneProcess(home, dataDir string, allow bool, launch phoneLaunchers) (phoneProcess, error) {
	executable, err := launch.executable()
	if err != nil {
		return nil, err
	}
	binary := previewBinaryPath(executable, runtime.GOOS)
	info, err := launch.stat(binary)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("supercli-web executable is not a regular file")
	}
	args := []string{"--phone", "--home", home, "--workspace", home, "--data-dir", dataDir}
	if allow {
		args = append(args, "--phone-allow-download")
	}
	cmd := childproc.NoConsoleWindow(exec.Command(binary, args...))
	cmd.Dir = filepath.Dir(binary)
	return launch.start(cmd)
}

func (s *phoneSession) run(p phoneProcess) {
	defer close(s.done)
	defer close(s.events)
	defer s.cancel()
	defer p.Stdin().Close()
	defer p.Stdout().Close()
	// Drain stdout before Wait closes its pipe. Cancellation remains independent
	// of both the scanner and a potentially blocked stdin write.
	finished := make(chan phoneEvent, 1)
	go func() {
		var need *phoneEvent
		scanner := bufio.NewScanner(p.Stdout())
		scanner.Buffer(make([]byte, 4096), 64*1024)
		for scanner.Scan() {
			if e, ok := parsePhoneLine(scanner.Text()); ok {
				if e.kind == "SUPERCLI_PHONE_NEED_DOWNLOAD" {
					copy := e
					need = &copy
				} else {
					s.emit(e)
				}
			}
		}
		code, err := p.Wait()
		if need != nil && code == 3 && scanner.Err() == nil {
			finished <- *need
			return
		}
		if scanner.Err() != nil {
			err = scanner.Err()
		}
		finished <- phoneEvent{kind: "exit", err: err}
	}()
	writes := make(chan error, 1)
	writing := false
	for {
		select {
		case e := <-finished:
			s.emit(e)
			return
		case <-s.ctx.Done():
			// Even when pair is stuck in Write, a bounded grace period ends in a
			// Scope kill; only the helper owned by this session is affected.
			go func() { _, _ = io.WriteString(p.Stdin(), "stop\n") }()
			timer := time.NewTimer(750 * time.Millisecond)
			select {
			case <-finished:
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
				_ = p.Kill()
				<-finished
			}
			return
		case command := <-s.commands:
			if !writing {
				writing = true
				go func() { _, err := io.WriteString(p.Stdin(), command+"\n"); writes <- err }()
			}
		case err := <-writes:
			writing = false
			if err != nil {
				s.emit(phoneEvent{kind: "error", err: fmt.Errorf("phone command: %w", err)})
				s.cancel()
			}
		}
	}
}
