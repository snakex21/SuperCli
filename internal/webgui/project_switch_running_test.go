package webgui

import (
	"context"
	"errors"
	"testing"
	"time"

	"supercli/internal/storage/memory"
)

func TestProjectSwitchAllowedWhileRunActive(t *testing.T) {
	dataDir := t.TempDir()
	projA := t.TempDir()
	projB := t.TempDir()
	ws := &memory.Workspace{}
	ws.Upsert(memory.Project{Name: "a", Path: projA})
	ws.Upsert(memory.Project{Name: "b", Path: projB})
	if err := memory.SaveWorkspace(dataDir, ws); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(echoConfig(), projA, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	finish := eng.beginActiveRun()
	defer finish()
	if err := eng.projectAction("use", "b", "", ""); err != nil {
		t.Fatalf("switch during run: %v", err)
	}
	if got := eng.Home(); got != projB {
		t.Fatalf("Home = %q, want %q", got, projB)
	}
	// Moving or removing a project still waits for the run.
	if err := eng.projectAction("relocate", "a", "", t.TempDir()); !errors.Is(err, errProjectWorkActive) {
		t.Fatalf("relocate during run = %v, want errProjectWorkActive", err)
	}
	if err := eng.projectAction("remove", "a", "", ""); !errors.Is(err, errProjectWorkActive) {
		t.Fatalf("remove during run = %v, want errProjectWorkActive", err)
	}
}

func TestProjectSwitchDefersMCPReloadUntilIdle(t *testing.T) {
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	pending := func() bool {
		eng.activeRunMu.Lock()
		defer eng.activeRunMu.Unlock()
		return eng.mcpReloadPending
	}

	eng.setHome(t.TempDir())
	if pending() {
		t.Fatal("idle switch left the MCP reload pending")
	}
	finish := eng.beginActiveRun()
	eng.setHome(t.TempDir())
	if !pending() {
		t.Fatal("switch during a run reloaded MCP under the running conversation")
	}
	finish()
	if pending() {
		t.Fatal("MCP reload was not applied after the run ended")
	}
}

func TestClaimSessionRunAdmitsOneRunPerConversation(t *testing.T) {
	eng := &Engine{}
	release, err := eng.claimSessionRun(context.Background(), "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !eng.sessionRunActive("s1") {
		t.Fatal("claimed conversation is not reported as running")
	}
	if _, err := eng.claimSessionRun(context.Background(), "s1", 0); !errors.Is(err, errSessionBusy) {
		t.Fatalf("second claim = %v, want errSessionBusy", err)
	}
	other, err := eng.claimSessionRun(context.Background(), "s2", 0)
	if err != nil {
		t.Fatalf("another conversation was blocked: %v", err)
	}
	other()

	go func() {
		time.Sleep(30 * time.Millisecond)
		release()
	}()
	next, err := eng.claimSessionRun(context.Background(), "s1", 2*time.Second)
	if err != nil {
		t.Fatalf("waiting claim after release: %v", err)
	}
	next()
	release() // idempotent
	if eng.sessionRunActive("s1") {
		t.Fatal("released conversation still reported as running")
	}
}

func TestDeleteSessionRefusesRunningConversation(t *testing.T) {
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "echo", "running")
	if err != nil {
		t.Fatal(err)
	}
	release, err := srv.eng.claimSessionRun(context.Background(), sess.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.eng.deleteSession(sess.ID); !errors.Is(err, errSessionBusy) {
		t.Fatalf("delete running = %v, want errSessionBusy", err)
	}
	release()
	if err := srv.eng.deleteSession(sess.ID); err != nil {
		t.Fatalf("delete after run: %v", err)
	}
}
