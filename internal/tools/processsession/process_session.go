// Package processsession provides bounded, workspace-scoped long-running
// command sessions. It complements ctx_execute: short commands remain cheaper
// there, while servers, watchers and interactive programs can be started once
// and awaited on completion or inspected without restarting them.
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
	"supercli/internal/tools/ctxexec"
)

const (
	maxActive       = 3
	maxHistory      = 12
	maxBufferBytes  = 64 << 10
	maxPollBytes    = 12 << 10
	maxWriteBytes   = 16 << 10
	defaultLifetime = 10 * time.Minute
	maxLifetime     = 24 * time.Hour
)

// Tool owns process sessions for one workspace.
type Tool struct {
	BaseDir           string
	DataDir           string
	Manager           *Manager
	captureScreenshot func(context.Context, json.RawMessage) (core.Result, error)
}

// New preserves the one-argument workspace constructor. The optional dataDir
// keeps snapshots beside the application data rather than inside a user project.
func New(baseDir string, dataDir ...string) *Tool {
	portableDir := baseDir
	if len(dataDir) > 0 && strings.TrimSpace(dataDir[0]) != "" {
		portableDir = dataDir[0]
	}
	return &Tool{BaseDir: baseDir, DataDir: portableDir, Manager: NewManager(baseDir)}
}

func (t *Tool) Spec() core.Tool {
	return core.Tool{
		Name:        "process_session",
		Description: "Start a long command; wait for exit, poll for diagnostics, write input, resize PTY, stop, list, or screenshot its owned window by id. Launch the GUI executable directly; shell-child windows are not inferred. Short commands use ctx_execute. pty=true merges stdout/stderr in a real terminal. Maximum 3 active sessions; output/lifetime capped.",
		Schema:      `{"type":"object","properties":{"action":{"type":"string","enum":["start","wait","poll","write","resize","stop","list","screenshot"]},"id":{"type":"string"},"command":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":32},"workdir":{"type":"string"},"env":{"type":"array","items":{"type":"string"},"maxItems":32,"description":"Optional KEY=VALUE entries"},"timeout_ms":{"type":"integer","minimum":1000,"maximum":86400000,"default":600000,"description":"Lifetime; default 10 min, opt in up to 24 h for long jobs"},"yield_ms":{"type":"integer","minimum":0,"maximum":1500,"default":250},"input":{"type":"string","maxLength":16384},"newline":{"type":"boolean","default":true},"pty":{"type":"boolean","default":false,"description":"Attach a real pseudo-terminal; stdout and stderr are merged"},"columns":{"type":"integer","minimum":20,"maximum":500,"default":100},"rows":{"type":"integer","minimum":5,"maximum":200,"default":30},"window_title":{"type":"string","maxLength":512,"description":"Optional title within the owned process for screenshot"},"attach":{"type":"boolean","default":false,"description":"Screenshot preview is automatic; true only for model pixel analysis"},"image_detail":{"type":"string","enum":["auto","original"]}},"required":["action"]}`,
		Fn:          t.Execute,
	}
}

type params struct {
	Action      string   `json:"action"`
	ID          string   `json:"id"`
	Command     []string `json:"command"`
	Workdir     string   `json:"workdir"`
	Env         []string `json:"env"`
	TimeoutMS   int      `json:"timeout_ms"`
	YieldMS     *int     `json:"yield_ms"`
	Input       string   `json:"input"`
	Newline     *bool    `json:"newline"`
	PTY         bool     `json:"pty"`
	Columns     int      `json:"columns"`
	Rows        int      `json:"rows"`
	WindowTitle string   `json:"window_title"`
	Attach      bool     `json:"attach"`
	ImageDetail string   `json:"image_detail"`
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
	case "wait":
		out, err = t.Manager.Wait(ctx, strings.TrimSpace(p.ID))
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
	case "screenshot":
		return t.screenshot(ctx, p)
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
	result := core.Result{Text: string(data)}
	if snap, ok := out.(snapshot); ok {
		result.CommandKey = snap.CommandKey
		if p.Action != "stop" {
			result.Err = snap.commandFailure()
		}
		if result.Err == nil && len(data) > core.ModelOutputInlineBytes {
			result.ModelPreview = snap.modelPreview()
		}
	}
	return result, nil
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
	id          string
	command     []string
	commandKey  *[32]byte
	workdir     string
	stdin       io.WriteCloser
	stdout      *streamBuffer
	stderr      *streamBuffer
	streams     sync.WaitGroup
	closeOutput func()
	done        chan struct{}
	cancel      context.CancelFunc
	waitFn      func() (int, error)
	killFn      func() error
	resizeFn    func(int, int) error
	pty         bool
	pid         int
	// Output draining may keep status=running after OS exit. Capture ownership
	// ends as soon as the native waiter observes exit, not when pipes finish.
	exited atomic.Bool

	mu               sync.Mutex
	started          time.Time
	ended            time.Time
	exitCode         int
	errText          string
	status           string
	stopRequested    bool
	outputIncomplete bool
	stdoutCursor     int64
	stderrCursor     int64
}

type snapshot struct {
	CommandKey       *[32]byte `json:"-"`
	ID               string    `json:"id"`
	Status           string    `json:"status"`
	Command          []string  `json:"command,omitempty"`
	Workdir          string    `json:"workdir,omitempty"`
	ExitCode         *int      `json:"exit_code,omitempty"`
	DurationMS       int64     `json:"duration_ms"`
	Stdout           string    `json:"stdout,omitempty"`
	Stderr           string    `json:"stderr,omitempty"`
	OmittedOut       int64     `json:"omitted_stdout_bytes,omitempty"`
	OmittedErr       int64     `json:"omitted_stderr_bytes,omitempty"`
	Error            string    `json:"error,omitempty"`
	PTY              bool      `json:"pty,omitempty"`
	OutputIncomplete bool      `json:"output_incomplete,omitempty"`
	OutputWarning    string    `json:"output_warning,omitempty"`
}

// Managing a process successfully is not evidence that its command succeeded.
// Keep the structured snapshot for the UI and expose failed exits through the
// same concise diagnostic path as ctx_execute. Listing/stopping is management.
func (s snapshot) commandFailure() error {
	if s.Status != "failed" && s.Status != "timeout" && !(s.Status == "done" && s.ExitCode != nil && *s.ExitCode != 0) {
		return nil
	}
	code := -1
	if s.ExitCode != nil {
		code = *s.ExitCode
	}
	if s.Status == "timeout" {
		code = ctxexec.ExitTimeout
	}
	result := ctxexec.Result{ExitCode: code, Error: s.Error, DurationMS: s.DurationMS, Stdout: s.Stdout, Stderr: s.Stderr, TruncatedStdout: s.OmittedOut > 0, TruncatedStderr: s.OmittedErr > 0, OutputIncomplete: s.OutputIncomplete, OutputWarning: s.OutputWarning}
	capture := ""
	summary := result.FailureSummary()
	if s.OutputIncomplete {
		capture = " output_incomplete=true"
		summary += "\n" + s.OutputWarning
	}
	return core.SelfContainedErr(fmt.Errorf("process_session %s%s: %s", s.ID, capture, summary))
}
