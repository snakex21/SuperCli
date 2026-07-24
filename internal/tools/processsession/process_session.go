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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supercli/internal/tools/core"
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
