package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// A subprocess of this Go test binary speaks MCP over stdio. No downloads,
// external executable dependencies, or network access are involved.
func TestLeaseMCPHelper(t *testing.T) {
	if os.Getenv("SUPERCLI_TEST_LEASE_MCP") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue
		}
		result := any(map[string]any{})
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "lease-fake", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "echo", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "ok"}}}
		}
		if encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}) != nil {
			break
		}
	}
	os.Exit(0)
}

func leasedTestManager(t *testing.T, busy func() bool) *Manager {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := NewLeasedManager(map[string]ServerConfig{"fake": {Command: exe, Args: []string{"-test.run=^TestLeaseMCPHelper$"}, Env: map[string]string{"SUPERCLI_TEST_LEASE_MCP": "1"}}}, 25*time.Millisecond, busy)
	t.Cleanup(m.Close)
	return m
}
func waitLeaseCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("MCP lifetime condition timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func startLeaseServer(t *testing.T, m *Manager) *Server {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, _ := m.Get("fake")
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestProjectLeasesKeepIndependentServersAliveAndReapIdle(t *testing.T) {
	a, b := leasedTestManager(t, nil), leasedTestManager(t, nil)
	if a.Statuses()[0].Running || b.Statuses()[0].Running {
		t.Fatal("manager was not lazy")
	}
	releaseA, releaseA2, releaseB := a.Acquire(), a.Acquire(), b.Acquire()
	sa, sb := startLeaseServer(t, a), startLeaseServer(t, b)
	releaseA()
	releaseA() // Releasing one of two runs is idempotent.
	time.Sleep(75 * time.Millisecond)
	if !sa.Status().Running {
		t.Fatal("another run's lease was ignored")
	}
	releaseA2()
	waitLeaseCondition(t, func() bool { return !sa.Status().Running })
	if !sb.Status().Running {
		t.Fatal("project A idle shutdown killed project B")
	}
	releaseAgain := a.Acquire()
	if !startLeaseServer(t, a).Status().Running {
		t.Fatal("idle runtime did not restart")
	}
	releaseAgain()
	releaseB()
	waitLeaseCondition(t, func() bool { return !sa.Status().Running && !sb.Status().Running })
}

func TestProjectLeaseProtectsDetachedWorkersAndCloseCancelsTimer(t *testing.T) {
	var busy atomic.Bool
	busy.Store(true)
	m := leasedTestManager(t, busy.Load)
	release := m.Acquire()
	s := startLeaseServer(t, m)
	release()
	time.Sleep(75 * time.Millisecond)
	if !s.Status().Running {
		t.Fatal("detached worker lost its server")
	}
	busy.Store(false)
	waitLeaseCondition(t, func() bool { return !s.Status().Running })
	release = m.Acquire()
	startLeaseServer(t, m)
	release()
	m.Close()
	m.leaseMu.Lock()
	closed, timer := m.closed, m.idleTimer
	m.leaseMu.Unlock()
	if !closed || timer != nil || s.Status().Running {
		t.Fatal("Close left idle work or server running")
	}
}

func TestBridgeTakesAndReleasesLease(t *testing.T) {
	m := leasedTestManager(t, nil)
	result, err := NewBridge(m).run(context.Background(), json.RawMessage(`{"action":"call","server":"fake","tool":"echo","arguments":{}}`))
	if err != nil || result.Err != nil {
		t.Fatalf("bridge call: %+v %v", result, err)
	}
	waitLeaseCondition(t, func() bool { return !m.Statuses()[0].Running })
}
