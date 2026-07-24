package webgui

import (
	"testing"

	"supercli/internal/agent"
)

func TestShouldConfirmCloseOnlyForActiveWork(t *testing.T) {
	dataDir := t.TempDir()
	if shouldConfirmClose(dataDir, func() bool { return false }) {
		t.Fatal("idle application requested close confirmation")
	}
	if !shouldConfirmClose(dataDir, func() bool { return true }) {
		t.Fatal("active application did not request close confirmation")
	}

}

func TestEngineActiveRunAccounting(t *testing.T) {
	engine := &Engine{}
	if engine.HasActiveWork() {
		t.Fatal("new engine reports active work")
	}
	finish := engine.beginActiveRun()
	if !engine.HasActiveWork() {
		t.Fatal("active run was not reported")
	}
	finish()
	finish()
	if engine.HasActiveWork() {
		t.Fatal("finished run remains active")
	}

	engine.workers = agent.NewWorkerRegistry()
	engine.workers.Add("explore", "active delegated work", nil)
	if !engine.HasActiveWork() {
		t.Fatal("active delegated worker was not reported")
	}
}
