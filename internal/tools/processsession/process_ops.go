// Package processsession provides bounded, workspace-scoped long-running
// command sessions. It complements ctx_execute: short commands remain cheaper
// there, while servers, watchers and interactive programs can be started once
// and polled without blocking an agent turn.
package processsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"supercli/internal/system/childproc"
	"supercli/internal/tools/sandbox"
)

func (m *Manager) Start(p params) (snapshot, error) {
	if len(p.Command) == 0 {
		return snapshot{}, errors.New("process_session: command is required for start")
	}
	for i, arg := range p.Command {
		if strings.TrimSpace(arg) == "" {
			return snapshot{}, fmt.Errorf("process_session: command[%d] is empty", i)
		}
	}
	workdir := m.baseDir
	if strings.TrimSpace(p.Workdir) != "" {
		resolved, err := sandbox.ResolveSafe(m.baseDir, p.Workdir)
		if err != nil {
			return snapshot{}, fmt.Errorf("process_session: workdir: %w", err)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return snapshot{}, fmt.Errorf("process_session: workdir is not a directory: %s", p.Workdir)
		}
		workdir = resolved
	}
	env, err := validatedEnv(p.Env)
	if err != nil {
		return snapshot{}, err
	}
	lifetime := defaultLifetime
	if p.TimeoutMS > 0 {
		lifetime = time.Duration(p.TimeoutMS) * time.Millisecond
	}
	if lifetime > maxLifetime {
		lifetime = maxLifetime
	}
	if lifetime < time.Second {
		lifetime = time.Second
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return snapshot{}, errors.New("process_session: manager closed")
	}
	m.pruneLocked()
	active := 0
	for _, item := range m.items {
		if item.running() {
			active++
		}
	}
	if active >= maxActive {
		m.mu.Unlock()
		return snapshot{}, fmt.Errorf("process_session: active limit reached (%d); stop or reuse a session", maxActive)
	}
	id := fmt.Sprintf("proc-%d", m.nextID.Add(1))
	procCtx, cancel := context.WithTimeout(context.Background(), lifetime)
	item := &process{id: id, command: append([]string(nil), p.Command...), workdir: workdir, stdout: newStreamBuffer(maxBufferBytes), stderr: newStreamBuffer(maxBufferBytes), done: make(chan struct{}), cancel: cancel, started: time.Now(), exitCode: -1, status: "running", pty: p.PTY}
	if p.PTY {
		columns, rows := terminalSize(p.Columns, p.Rows)
		terminal, startErr := startPTY(p.Command, workdir, append(os.Environ(), env...), columns, rows)
		if startErr != nil {
			cancel()
			m.mu.Unlock()
			return snapshot{}, fmt.Errorf("process_session: pty start: %w", startErr)
		}
		item.stdin = terminal
		item.waitFn = terminal.Wait
		item.killFn = terminal.Kill
		item.resizeFn = terminal.Resize
		item.streams.Add(1)
		go func() { defer item.streams.Done(); _, _ = io.Copy(item.stdout, terminal) }()
	} else {
		cmd := exec.CommandContext(procCtx, p.Command[0], p.Command[1:]...)
		cmd.Dir = workdir
		cmd.Env = append(os.Environ(), env...)
		stdin, pipeErr := cmd.StdinPipe()
		if pipeErr != nil {
			cancel()
			m.mu.Unlock()
			return snapshot{}, fmt.Errorf("process_session: stdin: %w", pipeErr)
		}
		stdout, pipeErr := cmd.StdoutPipe()
		if pipeErr != nil {
			cancel()
			m.mu.Unlock()
			return snapshot{}, fmt.Errorf("process_session: stdout: %w", pipeErr)
		}
		stderr, pipeErr := cmd.StderrPipe()
		if pipeErr != nil {
			cancel()
			m.mu.Unlock()
			return snapshot{}, fmt.Errorf("process_session: stderr: %w", pipeErr)
		}
		scope, startErr := childproc.Start(cmd)
		if startErr != nil {
			cancel()
			m.mu.Unlock()
			return snapshot{}, fmt.Errorf("process_session: start: %w", startErr)
		}
		item.stdin = stdin
		item.waitFn = func() (int, error) {
			waitErr := cmd.Wait()
			_ = scope.Close()
			if cmd.ProcessState != nil {
				return cmd.ProcessState.ExitCode(), waitErr
			}
			return -1, waitErr
		}
		item.killFn = func() error {
			if cmd.Process == nil {
				return nil
			}
			return scope.Kill(cmd)
		}
		item.streams.Add(2)
		go func() { defer item.streams.Done(); _, _ = io.Copy(item.stdout, stdout) }()
		go func() { defer item.streams.Done(); _, _ = io.Copy(item.stderr, stderr) }()
	}
	m.items[id] = item
	m.mu.Unlock()

	go item.wait(procCtx)

	yield := 250 * time.Millisecond
	if p.YieldMS != nil && *p.YieldMS >= 0 {
		yield = time.Duration(*p.YieldMS) * time.Millisecond
	}
	if yield > 1500*time.Millisecond {
		yield = 1500 * time.Millisecond
	}
	if yield > 0 {
		select {
		case <-item.done:
		case <-time.After(yield):
		}
	}
	return item.poll(), nil
}

func (p *process) wait(procCtx context.Context) {
	type waitResult struct {
		code int
		err  error
	}
	waited := make(chan waitResult, 1)
	go func() {
		code, err := p.waitFn()
		waited <- waitResult{code: code, err: err}
	}()
	var result waitResult
	select {
	case result = <-waited:
	case <-procCtx.Done():
		_ = p.killFn()
		result = <-waited
	}
	p.streams.Wait()
	p.mu.Lock()
	p.ended = time.Now()
	p.exitCode = result.code
	switch {
	case p.stopRequested:
		p.status = "stopped"
	case errors.Is(procCtx.Err(), context.DeadlineExceeded):
		p.status = "timeout"
	case result.err != nil:
		p.status = "failed"
		p.errText = result.err.Error()
	default:
		p.status = "done"
	}
	p.mu.Unlock()
	_ = p.stdin.Close()
	p.cancel()
	close(p.done)
}

func (m *Manager) Poll(id string) (snapshot, error) {
	item, err := m.get(id)
	if err != nil {
		return snapshot{}, err
	}
	return item.poll(), nil
}

func (p *process) poll() snapshot {
	p.mu.Lock()
	out, nextOut, omittedOut := p.stdout.readFrom(p.stdoutCursor, maxPollBytes)
	errOut, nextErr, omittedErr := p.stderr.readFrom(p.stderrCursor, maxPollBytes)
	p.stdoutCursor, p.stderrCursor = nextOut, nextErr
	status, code, errText := p.status, p.exitCode, p.errText
	started, ended := p.started, p.ended
	p.mu.Unlock()
	duration := time.Since(started)
	if !ended.IsZero() {
		duration = ended.Sub(started)
	}
	if p.pty {
		out = stripTerminalControl(out)
	}
	s := snapshot{ID: p.id, Status: status, Command: append([]string(nil), p.command...), Workdir: p.workdir, DurationMS: duration.Milliseconds(), Stdout: strings.ToValidUTF8(string(out), "?"), Stderr: strings.ToValidUTF8(string(errOut), "?"), OmittedOut: omittedOut, OmittedErr: omittedErr, Error: errText, PTY: p.pty}
	if status != "running" && code >= 0 {
		s.ExitCode = &code
	}
	return s
}

func (m *Manager) Write(id, input string, newline bool) (snapshot, error) {
	if len(input) > maxWriteBytes {
		return snapshot{}, fmt.Errorf("process_session: input too large (%d bytes; max %d)", len(input), maxWriteBytes)
	}
	item, err := m.get(id)
	if err != nil {
		return snapshot{}, err
	}
	item.mu.Lock()
	if item.status != "running" {
		item.mu.Unlock()
		return snapshot{}, fmt.Errorf("process_session: %s is %s", id, item.status)
	}
	stdin := item.stdin
	item.mu.Unlock()
	if newline {
		if item.pty {
			input += "\r"
		} else {
			input += "\n"
		}
	}
	if _, err := io.WriteString(stdin, input); err != nil {
		return snapshot{}, fmt.Errorf("process_session: write %s: %w", id, err)
	}
	return item.poll(), nil
}

func (m *Manager) Resize(id string, columns, rows int) (snapshot, error) {
	item, err := m.get(id)
	if err != nil {
		return snapshot{}, err
	}
	if columns < 20 || columns > 500 || rows < 5 || rows > 200 {
		return snapshot{}, errors.New("process_session: resize requires columns 20..500 and rows 5..200")
	}
	item.mu.Lock()
	if item.status != "running" {
		status := item.status
		item.mu.Unlock()
		return snapshot{}, fmt.Errorf("process_session: %s is %s", id, status)
	}
	resize := item.resizeFn
	item.mu.Unlock()
	if resize == nil {
		return snapshot{}, fmt.Errorf("process_session: %s was not started with pty=true", id)
	}
	if err := resize(columns, rows); err != nil {
		return snapshot{}, fmt.Errorf("process_session: resize %s: %w", id, err)
	}
	return item.poll(), nil
}

func (m *Manager) Stop(ctx context.Context, id string) (snapshot, error) {
	item, err := m.get(id)
	if err != nil {
		return snapshot{}, err
	}
	item.mu.Lock()
	if item.status == "running" {
		item.stopRequested = true
		item.cancel()
	}
	item.mu.Unlock()
	select {
	case <-item.done:
	case <-ctx.Done():
		return snapshot{}, ctx.Err()
	case <-time.After(2 * time.Second):
		_ = item.killFn()
		<-item.done
	}
	return item.poll(), nil
}

func (m *Manager) List() []snapshot {
	m.mu.Lock()
	m.pruneLocked()
	items := make([]*process, 0, len(m.items))
	for _, item := range m.items {
		items = append(items, item)
	}
	m.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].started.Before(items[j].started) })
	out := make([]snapshot, 0, len(items))
	for _, item := range items {
		item.mu.Lock()
		status, code, started, ended := item.status, item.exitCode, item.started, item.ended
		item.mu.Unlock()
		duration := time.Since(started)
		if !ended.IsZero() {
			duration = ended.Sub(started)
		}
		s := snapshot{ID: item.id, Status: status, Command: append([]string(nil), item.command...), Workdir: item.workdir, DurationMS: duration.Milliseconds(), PTY: item.pty}
		if status != "running" && code >= 0 {
			s.ExitCode = &code
		}
		out = append(out, s)
	}
	return out
}

func (m *Manager) get(id string) (*process, error) {
	if id == "" {
		return nil, errors.New("process_session: id is required")
	}
	m.mu.Lock()
	item := m.items[id]
	m.mu.Unlock()
	if item == nil {
		return nil, fmt.Errorf("process_session: unknown id %q", id)
	}
	return item, nil
}

func (m *Manager) pruneLocked() {
	if len(m.items) <= maxHistory {
		return
	}
	var finished []*process
	for _, item := range m.items {
		if !item.running() {
			finished = append(finished, item)
		}
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].ended.Before(finished[j].ended) })
	for len(m.items) > maxHistory && len(finished) > 0 {
		delete(m.items, finished[0].id)
		finished = finished[1:]
	}
}

func (p *process) running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status == "running"
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	items := make([]*process, 0, len(m.items))
	for _, item := range m.items {
		items = append(items, item)
	}
	m.mu.Unlock()
	for _, item := range items {
		item.mu.Lock()
		if item.status == "running" {
			item.stopRequested = true
			item.cancel()
		}
		item.mu.Unlock()
	}
	for _, item := range items {
		select {
		case <-item.done:
		case <-time.After(2 * time.Second):
			_ = item.killFn()
		}
	}
}
