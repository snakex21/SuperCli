package agent

import (
	"strings"
	"testing"
)

func TestVerificationFullSuiteRevalidatesStaleSuccessfulSelection(t *testing.T) {
	for _, filtered := range []bool{false, true} {
		name := "package"
		if filtered {
			name = "regex selection"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			loop := &Loop{baseDir: root}
			command := []string{"go", "test", "./pkg", "-count=1", "-timeout=2m"}
			if filtered {
				command = append(command, "-run", "TestValue|TestBoundary")
			}
			initial, result := executionFixture(root, command, "ok fixture/pkg 0.1s", 0)
			loop.recordCheckResult(initial, result)
			if loop.failedChecks.unresolved() {
				t.Fatal("initial successful check failed")
			}
			loop.failedChecks.workspaceChanged()
			if !loop.failedChecks.unresolved() || !strings.Contains(loop.failedChecks.blocker(), "code changed since verification") {
				t.Fatal("real code change did not invalidate previous verification")
			}
			current, pass := executionFixture(root, []string{"go", "test", "./...", "-count=1", "-timeout=4m"}, "ok fixture/pkg 0.2s\nok fixture/other 0.1s", 0)
			loop.recordCheckResult(current, pass)
			if loop.failedChecks.unresolved() {
				t.Fatal("full current suite did not revalidate the stale successful check: " + loop.failedChecks.blocker())
			}
			// The new PASS must still expire after another real edit.
			loop.failedChecks.workspaceChanged()
			if !loop.failedChecks.unresolved() {
				t.Fatal("revalidation disabled later mutation checks")
			}
		})
	}
}

func TestVerificationFullSuiteRevalidationKeepsCompatibilityAndEvidence(t *testing.T) {
	for _, name := range []string{"race", "count", "environment", "workdir", "narrow filter", "package selection", "missing package", "incomplete", "skipped", "no tests", "failed current run"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			loop := &Loop{baseDir: root}
			initialCommand := []string{"go", "test", "./pkg", "-run", "TestValue|TestBoundary", "-count=1"}
			if name == "race" {
				initialCommand = append(initialCommand, "-race")
			}
			if name == "count" {
				initialCommand[len(initialCommand)-1] = "-count=3"
			}
			initial, result := executionFixture(root, initialCommand, "ok fixture/pkg 0.1s", 0)
			loop.recordCheckResult(initial, result)
			loop.failedChecks.workspaceChanged()
			command := []string{"go", "test", "./...", "-count=1"}
			if name == "narrow filter" {
				command = append(command, "-run", "TestDifferent")
			}
			if name == "package selection" {
				command[2] = "./other"
			}
			currentRoot := root
			if name == "workdir" {
				currentRoot = t.TempDir()
			}
			current, pass := executionFixture(currentRoot, command, "ok fixture/pkg 0.2s", 0)
			switch name {
			case "environment":
				pass.Execution.Environment = [32]byte{99}
			case "missing package":
				pass.Execution.Stdout = "ok fixture/other 0.2s"
			case "incomplete":
				pass.Execution.OutputComplete = false
			case "skipped":
				pass.Execution.Stdout = "--- SKIP: TestValue (0.00s)\nok fixture/pkg 0.2s"
			case "no tests":
				pass.Execution.Stdout = "ok fixture/pkg 0.2s [no tests to run]"
			case "failed current run":
				pass.Execution.ExitCode = 1
				pass.Execution.Stdout = "FAIL fixture/pkg 0.2s"
			}
			loop.recordCheckResult(current, pass)
			if !loop.failedChecks.unresolved() {
				t.Fatal("incompatible or unproven execution cleared stale verification")
			}
		})
	}
}

func TestVerificationFullSuiteRevalidationRetainsRecoveredFailurePackages(t *testing.T) {
	root := t.TempDir()
	initial, priorResult := executionFixture(root, []string{"go", "test", "./..."}, "ok fixture/other 0.1s\nFAIL fixture/required 0.1s", 1)
	loop := &Loop{baseDir: root}
	loop.recordCheckResult(initial, priorResult)
	var previous verificationScope
	for _, observation := range loop.failedChecks.known {
		previous = observation.scope
	}
	_, pass := executionFixture(root, []string{"go", "test", "./..."}, "ok fixture/other 0.2s", 0)
	current := describeVerification(pass.Execution.Command, root, pass.Execution.Environment)
	current.observeGoOutput(pass.Execution.Stdout)
	if current.revalidatesSuccessful(previous) {
		t.Fatal("previously failed package was lost while revalidating recovered evidence")
	}
	current.observeGoOutput("ok fixture/other 0.2s\nok fixture/required 0.2s")
	if !current.revalidatesSuccessful(previous) {
		t.Fatal("complete package evidence was rejected")
	}
}

func TestVerificationFullSuiteDoesNotForgiveActualFilteredFailure(t *testing.T) {
	root := t.TempDir()
	loop := &Loop{baseDir: root}
	initial, result := executionFixture(root, []string{"go", "test", "./pkg", "-run", "TestValue|TestBoundary", "-count=1"}, "FAIL fixture/pkg 0.1s", 1)
	loop.recordCheckResult(initial, result)
	loop.failedChecks.workspaceChanged()
	current, pass := executionFixture(root, []string{"go", "test", "./...", "-count=1"}, "ok fixture/pkg 0.2s", 0)
	loop.recordCheckResult(current, pass)
	if !loop.failedChecks.unresolved() {
		t.Fatal("a previously failing filtered selection was forgiven without its required execution evidence")
	}
}
