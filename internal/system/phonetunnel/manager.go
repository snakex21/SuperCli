package phonetunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"supercli/internal/system/childproc"
)

// Process permits deterministic tests without starting cloudflared or networking.
type Process interface {
	Stderr() io.Reader
	Wait() error
	Kill() error
}
type Factory func(path, target string) (Process, error)

type Status struct {
	State      string    `json:"state"`
	Host       string    `json:"host,omitempty"`
	Error      string    `json:"error,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Downloaded int64     `json:"downloaded"`
	Total      int64     `json:"total"`
	ExpiresAt  time.Time `json:"expires_at"`
	Generation uint64    `json:"-"`
}

type Manager struct {
	mu                          sync.Mutex
	Installer                   Installer
	Factory                     Factory
	StartupTimeout, IdleTimeout time.Duration
	status                      Status
	cancel                      context.CancelFunc
	active                      context.Context
	process                     Process
	done                        chan struct{}
}

func New(dataDir string) *Manager {
	return &Manager{Installer: Installer{DataDir: dataDir}, Factory: startProcess, StartupTimeout: 45 * time.Second, IdleTimeout: 24 * time.Hour, status: Status{State: "off", Total: DownloadSize}}
}
func (m *Manager) Status() Status                 { m.mu.Lock(); defer m.mu.Unlock(); return m.status }
func (m *Manager) Available() bool                { return m.Installer.Available() }
func (m *Manager) ActiveContext() context.Context { m.mu.Lock(); defer m.mu.Unlock(); return m.active }
func (m *Manager) Touch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State == "on" {
		m.status.ExpiresAt = time.Now().Add(m.IdleTimeout)
	}
}

func (m *Manager) Start(ctx context.Context, target string, consent bool, progress Progress) error {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return errors.New("phone tunnel is already running")
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.active = runCtx
	m.done = make(chan struct{})
	state := "downloading"
	if m.Installer.Available() {
		// Cached pinned tool: never show a download that will not happen.
		state = "starting"
	}
	m.status = Status{State: state, Total: DownloadSize, Generation: m.status.Generation + 1}
	generation := m.status.Generation
	m.mu.Unlock()
	go m.run(runCtx, target, consent, progress, generation)
	return nil
}

func (m *Manager) run(ctx context.Context, target string, consent bool, progress Progress, generation uint64) {
	reason := "error"
	var runErr error
	defer func() {
		m.mu.Lock()
		if m.status.Generation == generation {
			if m.status.Reason != "" {
				reason = m.status.Reason
			}
			m.status.State = "off"
			m.status.Host = ""
			m.status.Reason = reason
			if runErr != nil && ctx.Err() == nil {
				m.status.State = "error"
				m.status.Error = runErr.Error()
			}
			m.cancel()
			m.cancel = nil
			m.process = nil
			close(m.done)
		}
		m.mu.Unlock()
	}()
	path, err := m.Installer.Ensure(ctx, consent, func(n, total int64) {
		m.mu.Lock()
		m.status.Downloaded = n
		m.status.Total = total
		m.mu.Unlock()
		if progress != nil {
			progress(n, total)
		}
	})
	if err != nil {
		runErr = err
		return
	}
	if ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	m.status.State = "starting"
	m.mu.Unlock()
	p, err := m.Factory(path, target)
	if err != nil {
		runErr = err
		return
	}
	m.mu.Lock()
	m.process = p
	m.mu.Unlock()
	wait := make(chan error, 1)
	go func() { wait <- p.Wait() }()
	// Drain stderr even after finding the URL so cloudflared can never block on logs.
	urls := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(p.Stderr())
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			if u := tunnelURL.FindString(scanner.Text()); u != "" {
				select {
				case urls <- u[len("https://"):]:
				default:
				}
			}
		}
	}()
	defer func() { _ = p.Kill(); <-wait }()
	timer := time.NewTimer(m.StartupTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case err := <-wait:
		wait <- err
		runErr = fmt.Errorf("cloudflared exited before URL: %v", err)
		return
	case <-timer.C:
		runErr = errors.New("cloudflared URL timeout")
		return
	case host := <-urls:
		m.mu.Lock()
		m.status.State = "on"
		m.status.Host = host
		m.status.ExpiresAt = time.Now().Add(m.IdleTimeout)
		m.mu.Unlock()
	}
	ticker := time.NewTicker(min(time.Second, m.IdleTimeout))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-wait:
			wait <- err
			runErr = fmt.Errorf("cloudflared exited: %v", err)
			return
		case <-ticker.C:
			m.mu.Lock()
			idle := !time.Now().Before(m.status.ExpiresAt)
			if idle {
				m.status.Reason = "idle"
			}
			m.mu.Unlock()
			if idle {
				return
			}
		}
	}
}

func (m *Manager) Stop(reason string) {
	m.mu.Lock()
	if m.cancel == nil {
		m.mu.Unlock()
		return
	}
	m.status.Reason = reason
	m.status.Host = ""
	m.cancel()
	done := m.done
	m.mu.Unlock()
	<-done
}

var tunnelURL = regexp.MustCompile(`https://[a-z0-9]+(?:-[a-z0-9]+)*\.trycloudflare\.com\b`)

type commandProcess struct {
	cmd    *exec.Cmd
	scope  *childproc.Scope
	stderr io.ReadCloser
}

func startProcess(path, target string) (Process, error) {
	cmd := exec.Command(path, "tunnel", "--url", target, "--no-autoupdate")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	scope, err := childproc.Start(cmd)
	if err != nil {
		stderr.Close()
		return nil, err
	}
	return &commandProcess{cmd, scope, stderr}, nil
}
func (p *commandProcess) Stderr() io.Reader { return p.stderr }
func (p *commandProcess) Wait() error       { return p.cmd.Wait() }
func (p *commandProcess) Kill() error       { defer p.scope.Close(); return p.scope.Kill(p.cmd) }
