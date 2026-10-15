package webgui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supercli/internal/storage/memory"
)

func TestMCPRuntimeIsPerProject(t *testing.T) {
	projA := t.TempDir()
	projB := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projB, ".supercli"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig := "[mcp.servers.only-b]\ncommand = \"not-started\"\n"
	if err := os.WriteFile(filepath.Join(projB, ".supercli", "config.toml"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(echoConfig(), projA, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	if manager := eng.mcpRuntimeFor(projA); manager != nil {
		t.Fatalf("project A got servers: %v", manager.Names())
	}
	b := eng.mcpRuntimeFor(projB)
	if b == nil || len(b.Names()) != 1 || b.Names()[0] != "only-b" {
		t.Fatalf("project B runtime = %#v", b)
	}
	if b.Statuses()[0].Running {
		t.Fatal("discovery started the server")
	}
	// A run in A while B is active keeps using A's (empty) runtime, and B's
	// runtime is reused rather than rebuilt.
	finish := eng.beginActiveRun()
	eng.setHome(projB)
	if eng.mcpRuntime() != b {
		t.Fatal("active project B did not reuse its runtime")
	}
	if eng.mcpRuntimeFor(projA) != nil {
		t.Fatal("project A picked up B's servers")
	}
	finish()
}

func TestListProjectsMarksProjectWithRunningConversation(t *testing.T) {
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
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(projA, "echo", "long job")
	if err != nil {
		t.Fatal(err)
	}
	release, err := eng.claimSessionRun(context.Background(), sess.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.projectAction("use", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	running := map[string]int{}
	for _, p := range eng.listProjects() {
		running[p.Name] = p.Running
	}
	if running["a"] != 1 || running["b"] != 0 {
		t.Fatalf("running = %v, want a=1 b=0", running)
	}
	release()
	for _, p := range eng.listProjects() {
		if p.Running != 0 {
			t.Fatalf("%s still marked running after release", p.Name)
		}
	}
}

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
