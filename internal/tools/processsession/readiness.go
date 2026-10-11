package processsession

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"time"
)

// WaitReady waits on log writes or process completion, never on a poll timer.
// A port check is one local connection after the requested log evidence, not
// a periodic probe. Cancelling this wait does not stop the persistent process.
func (m *Manager) WaitReady(ctx context.Context, id, pattern string, port, timeoutMS int) (snapshot, error) {
	if pattern == "" || len(pattern) > 512 {
		return snapshot{}, errors.New("process_session: wait_ready requires ready_pattern (regex, 1..512 bytes)")
	}
	if port < 0 || port > 65535 {
		return snapshot{}, errors.New("process_session: ready_port must be 1..65535 or omitted")
	}
	lifetime, err := sessionLifetime(timeoutMS)
	if err != nil {
		return snapshot{}, err
	}
	match, err := regexp.Compile(pattern)
	if err != nil {
		return snapshot{}, fmt.Errorf("process_session: ready_pattern: %w", err)
	}
	if match.MatchString("") {
		return snapshot{}, errors.New("process_session: ready_pattern must require non-empty log evidence")
	}
	item, err := m.get(id)
	if err != nil {
		return snapshot{}, err
	}
	if lifetime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, lifetime)
		defer cancel()
	}
	for {
		if err := ctx.Err(); err != nil {
			return snapshot{}, fmt.Errorf("process_session: readiness not verified; process is not stopped: %w", err)
		}
		out, outChanged := item.stdout.observe()
		errOut, errChanged := item.stderr.observe()
		if match.Match(out) || match.Match(errOut) {
			if item.exited.Load() || !item.running() {
				return readinessExited(ctx, item)
			}
			if port != 0 {
				dialer := net.Dialer{Timeout: 5 * time.Second}
				connection, dialErr := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
				if dialErr != nil {
					if ctx.Err() == nil {
						item.mu.Lock()
						item.readyValid = false
						item.mu.Unlock()
					}
					return snapshot{}, fmt.Errorf("process_session: log matched but ready_port %d did not accept a connection (no retries): %w", port, dialErr)
				}
				_ = connection.Close()
			}
			item.mu.Lock()
			if item.status != "running" || item.exited.Load() {
				item.mu.Unlock()
				return readinessExited(ctx, item)
			}
			item.readyPattern, item.readyPort, item.readyAt = pattern, port, time.Now().UTC()
			item.readyValid = true
			item.mu.Unlock()
			snap := item.snapshot(maxBufferBytes)
			if !snap.Ready {
				return snap, fmt.Errorf("process_session %s: process exited while returning readiness evidence; %s", id, snap.StatusSummary)
			}
			return snap, nil
		}
		select {
		case <-ctx.Done():
			return snapshot{}, fmt.Errorf("process_session: readiness not verified; process is not stopped: %w", ctx.Err())
		case <-item.done:
			return readinessExited(ctx, item)
		case <-outChanged:
		case <-errChanged:
		}
	}
}

func readinessExited(ctx context.Context, item *process) (snapshot, error) {
	select {
	case <-ctx.Done():
		return snapshot{}, ctx.Err()
	case <-item.done:
	}
	snap := item.snapshot(maxBufferBytes)
	if err := snap.commandFailure(); err != nil {
		return snap, err
	}
	return snap, fmt.Errorf("process_session %s: exited before readiness was verified; %s", item.id, snap.StatusSummary)
}

func (s *snapshot) describeState() {
	seconds := float64(s.DurationMS) / 1000
	if s.Status == "running" {
		if s.Ready {
			s.StatusSummary = "Process running; readiness observed at " + s.ReadyAt + ". This is past evidence, not a health monitor."
		} else {
			if s.ReadyAt != "" {
				s.StatusSummary = "Process running; readiness not currently verified. Past observation: " + s.ReadyAt
			} else {
				s.StatusSummary = "Process started; readiness has not been verified."
			}
		}
		return
	}
	if s.ExitCode != nil {
		s.StatusSummary = fmt.Sprintf("Process %s: exited with code %d after %.3f s.", s.ID, *s.ExitCode, seconds)
	} else {
		s.StatusSummary = fmt.Sprintf("Process %s: %s after %.3f s.", s.ID, s.Status, seconds)
	}
	if s.ReadyAt != "" {
		s.StatusSummary += " Past readiness observation at " + s.ReadyAt + "; process is no longer running."
	}
}
