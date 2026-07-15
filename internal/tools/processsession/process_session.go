// Package processsession provides bounded, workspace-scoped long-running
// command sessions. It complements ctx_execute: short commands remain cheaper
// there, while servers, watchers and interactive programs can be started once
// and polled without blocking an agent turn.
package processsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supercli/internal/system/childproc"
	"supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

const (
	maxActive       = 3
	maxHistory      = 12
	maxBufferBytes  = 64 << 10
	maxPollBytes    = 12 << 10
	maxWriteBytes   = 16 << 10
	defaultLifetime = 10 * time.Minute
	maxLifetime     = 30 * time.Minute
)

// Tool owns process sessions for one workspace.
type Tool struct {
	BaseDir string
	Manager *Manager
}

func New(baseDir string) *Tool {
	return &Tool{BaseDir: baseDir, Manager: NewManager(baseDir)}
}

func (t *Tool) Spec() core.Tool {
	return core.Tool{
		Name:        "process_session",
		Description: "Manage a bounded long-running command without blocking the agent. Actions: start, poll, write, resize, stop, list. Use ctx_execute for ordinary commands. Set pty=true only for programs that require a real terminal; PTY output is merged and cleaned of terminal control codes. At most 3 active sessions; output and lifetime are capped.",
		Schema:      `{"type":"object","properties":{"action":{"type":"string","enum":["start","poll","write","resize","stop","list"]},"id":{"type":"string"},"command":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":32},"workdir":{"type":"string"},"env":{"type":"array","items":{"type":"string"},"maxItems":32,"description":"Optional KEY=VALUE entries"},"timeout_ms":{"type":"integer","minimum":1000,"maximum":1800000,"default":600000},"yield_ms":{"type":"integer","minimum":0,"maximum":1500,"default":250},"input":{"type":"string","maxLength":16384},"newline":{"type":"boolean","default":true},"pty":{"type":"boolean","default":false,"description":"Attach a real pseudo-terminal; stdout and stderr are merged"},"columns":{"type":"integer","minimum":20,"maximum":500,"default":100},"rows":{"type":"integer","minimum":5,"maximum":200,"default":30}},"required":["action"]}`,
		Fn:          t.Execute,
	}
}

type params struct {
	Action    string   `json:"action"`
	ID        string   `json:"id"`
	Command   []string `json:"command"`
	Workdir   string   `json:"workdir"`
	Env       []string `json:"env"`
	TimeoutMS int      `json:"timeout_ms"`
	YieldMS   *int     `json:"yield_ms"`
	Input     string   `json:"input"`
	Newline   *bool    `json:"newline"`
	PTY       bool     `json:"pty"`
	Columns   int      `json:"columns"`
	Rows      int      `json:"rows"`
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (core.Result, error) {
	var p params
	if err := json.Unmarshal(raw, &p); err != nil {
		return core.Result{Err: fmt.Errorf("process_session: bad args: %w", err)}, nil
	}
	if t.Manager == nil {
		return core.Result{Err: errors.New("process_session: manager unavailable")}, nil
	}
	p.Action = strings.ToLower(strings.TrimSpace(p.Action))
	var (
		out any
		err error
	)
	switch p.Action {
	case "start":
		out, err = t.Manager.Start(p)
	case "poll":
		out, err = t.Manager.Poll(strings.TrimSpace(p.ID))
	case "write":
		newline := true
		if p.Newline != nil {
			newline = *p.Newline
		}
		out, err = t.Manager.Write(strings.TrimSpace(p.ID), p.Input, newline)
	case "resize":
		out, err = t.Manager.Resize(strings.TrimSpace(p.ID), p.Columns, p.Rows)
	case "stop":
		out, err = t.Manager.Stop(ctx, strings.TrimSpace(p.ID))
	case "list":
		out = t.Manager.List()
	default:
		err = fmt.Errorf("process_session: unknown action %q", p.Action)
	}
	if err != nil {
		return core.Result{Err: err}, nil
	}
	data, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		return core.Result{Err: fmt.Errorf("process_session: marshal: %w", marshalErr)}, nil
	}
	return core.Result{Text: string(data)}, nil
}

func (t *Tool) Close() {
	if t != nil && t.Manager != nil {
		t.Manager.Close()
	}
}

type Manager struct {
	baseDir string
	mu      sync.Mutex
	nextID  atomic.Uint64
	items   map[string]*process
	closed  bool
}

func NewManager(baseDir string) *Manager {
	return &Manager{baseDir: baseDir, items: make(map[string]*process)}
}

type process struct {
	id       string
	command  []string
	workdir  string
	stdin    io.WriteCloser
	stdout   *streamBuffer
	stderr   *streamBuffer
	streams  sync.WaitGroup
	done     chan struct{}
	cancel   context.CancelFunc
	waitFn   func() (int, error)
	killFn   func() error
	resizeFn func(int, int) error
	pty      bool

	mu            sync.Mutex
	started       time.Time
	ended         time.Time
	exitCode      int
	errText       string
	status        string
	stopRequested bool
	stdoutCursor  int64
	stderrCursor  int64
}

type snapshot struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	Command    []string `json:"command,omitempty"`
	Workdir    string   `json:"workdir,omitempty"`
	ExitCode   *int     `json:"exit_code,omitempty"`
	DurationMS int64    `json:"duration_ms"`
	Stdout     string   `json:"stdout,omitempty"`
	Stderr     string   `json:"stderr,omitempty"`
	OmittedOut int64    `json:"omitted_stdout_bytes,omitempty"`
	OmittedErr int64    `json:"omitted_stderr_bytes,omitempty"`
	Error      string   `json:"error,omitempty"`
	PTY        bool     `json:"pty,omitempty"`
}

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

func terminalSize(columns, rows int) (int, int) {
	if columns < 20 || columns > 500 {
		columns = 100
	}
	if rows < 5 || rows > 200 {
		rows = 30
	}
	return columns, rows
}

// stripTerminalControl keeps terminal UX bytes out of the model context. It
// removes CSI/OSC escape sequences and normalizes carriage returns while
// preserving ordinary UTF-8 output.
func stripTerminalControl(src []byte) []byte {
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); {
		switch src[i] {
		case '\r':
			if i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			out = append(out, '\n')
			i++
		case 0x1b:
			i++
			if i >= len(src) {
				continue
			}
			switch src[i] {
			case '[':
				i++
				for i < len(src) {
					b := src[i]
					i++
					if b >= 0x40 && b <= 0x7e {
						break
					}
				}
			case ']':
				i++
				for i < len(src) {
					if src[i] == 0x07 {
						i++
						break
					}
					if src[i] == 0x1b && i+1 < len(src) && src[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			default:
				i++
			}
		default:
			out = append(out, src[i])
			i++
		}
	}
	return out
}

func validatedEnv(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for i, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\x00\r\n") || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("process_session: env[%d] must be KEY=VALUE", i)
		}
		out = append(out, value)
	}
	return out, nil
}

type streamBuffer struct {
	mu   sync.Mutex
	max  int
	base int64
	data []byte
}

func newStreamBuffer(max int) *streamBuffer { return &streamBuffer{max: max} }

func (b *streamBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		drop := len(b.data) - b.max
		b.data = append([]byte(nil), b.data[drop:]...)
		b.base += int64(drop)
	}
	return n, nil
}

func (b *streamBuffer) readFrom(cursor int64, max int) ([]byte, int64, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	omitted := int64(0)
	if cursor < b.base {
		omitted = b.base - cursor
		cursor = b.base
	}
	end := b.base + int64(len(b.data))
	if cursor > end {
		cursor = end
	}
	start := int(cursor - b.base)
	data := b.data[start:]
	if len(data) > max {
		data = data[:max]
	}
	out := append([]byte(nil), data...)
	return out, cursor + int64(len(data)), omitted
}
