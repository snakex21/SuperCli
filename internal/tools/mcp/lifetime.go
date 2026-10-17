package mcp

import (
	"sync"
	"time"
)

// NewLeasedManager starts no processes. Leases pin its lazy servers across an
// entire run, including user questions. busy conservatively protects detached
// workers that retain this manager's bridge after their parent run completes.
func NewLeasedManager(configs map[string]ServerConfig, idle time.Duration, busy func() bool) *Manager {
	m := NewManager(configs)
	m.idleTimeout, m.busy = idle, busy
	return m
}

// Acquire pins this project's servers until the idempotent release is called.
// Bridge calls also take a lease, so an idle shutdown cannot interrupt an RPC.
func (m *Manager) Acquire() func() {
	m.leaseMu.Lock()
	m.leases++
	m.idleGeneration++
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
	m.leaseMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.leaseMu.Lock()
			defer m.leaseMu.Unlock()
			m.leases--
			if m.leases == 0 && !m.closed && m.idleTimeout > 0 {
				m.scheduleIdleLocked()
			}
		})
	}
}

func (m *Manager) isClosed() bool {
	m.leaseMu.Lock()
	defer m.leaseMu.Unlock()
	return m.closed
}

// Close permanently disables idle timers and stops all servers. Call only once
// no run/worker can still use this manager (or during application shutdown).
func (m *Manager) Close() {
	m.leaseMu.Lock()
	defer m.leaseMu.Unlock()
	m.closed = true
	m.idleGeneration++
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
	m.StopAll()
}

func (m *Manager) scheduleIdleLocked() {
	m.idleGeneration++
	generation := m.idleGeneration
	m.idleTimer = time.AfterFunc(m.idleTimeout, func() {
		m.leaseMu.Lock()
		defer m.leaseMu.Unlock()
		if m.leases != 0 || m.closed || generation != m.idleGeneration {
			return
		}
		if m.busy != nil && m.busy() {
			m.scheduleIdleLocked()
			return
		}
		// Hold leaseMu while stopping: a new run must not acquire a lease halfway
		// through shutdown. It can lazily restart the same manager afterwards.
		m.StopAll()
		m.idleTimer = nil
	})
}
