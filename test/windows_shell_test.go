//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

func init() {
	_ = json.Marshal
}

// TestIntegration_WindowsShell_CmdEcho runs echo via CMD.
func TestIntegration_WindowsShell_CmdEcho(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only test")
	}
	dir := t.TempDir()
	runner := ctxexec.New(dir)
	tool := tools.NewCtxExecuteTool(runner, dir)
	reg := tools.NewRegistry()
	reg.MustRegister(tool.Spec())

	res, err := reg.Execute(context.Background(), "ctx_execute",
		json.RawMessage(`{"command":["cmd","/c","echo hello_from_cmd"]}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("tool error: %v", res.Err)
	}
	if !strings.Contains(res.Text, "hello_from_cmd") {
		t.Errorf("expected 'hello_from_cmd' in output: %s", res.Text)
	}
	t.Logf("cmd echo: %s", res.Text)
}

// TestIntegration_WindowsShell_PowerShellEcho runs echo via PowerShell.
func TestIntegration_WindowsShell_PowerShellEcho(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only test")
	}
	dir := t.TempDir()
	runner := ctxexec.New(dir)
	tool := tools.NewCtxExecuteTool(runner, dir)
	reg := tools.NewRegistry()
	reg.MustRegister(tool.Spec())

	res, err := reg.Execute(context.Background(), "ctx_execute",
		json.RawMessage(`{"command":["powershell","-Command","Write-Output hello_from_powershell"]}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("tool error: %v", res.Err)
	}
	if !strings.Contains(res.Text, "hello_from_powershell") {
		t.Errorf("expected 'hello_from_powershell' in output: %s", res.Text)
	}
	t.Logf("powershell echo: %s", res.Text)
}

// TestIntegration_WindowsShell_Explorer launches explorer and
// checks it doesn't crash (exit code 0 or 1 is acceptable).
func TestIntegration_WindowsShell_Explorer(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only test")
	}
	dir := t.TempDir()
	runner := ctxexec.New(dir)
	tool := tools.NewCtxExecuteTool(runner, dir)
	reg := tools.NewRegistry()
	reg.MustRegister(tool.Spec())

	res, err := reg.Execute(context.Background(), "ctx_execute",
		json.RawMessage(`{"command":["explorer","."]}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Explorer may open a window or just return. Either is fine.
	// We just verify the tool didn't crash.
	if res.Err != nil {
		t.Logf("explorer error (may be expected in headless): %v", res.Err)
	}
	t.Logf("explorer result: %s", res.Text)
}

// TestIntegration_WindowsShell_StartExplorer launches explorer
// via cmd /c start.
func TestIntegration_WindowsShell_StartExplorer(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only test")
	}
	dir := t.TempDir()
	runner := ctxexec.New(dir)
	tool := tools.NewCtxExecuteTool(runner, dir)
	reg := tools.NewRegistry()
	reg.MustRegister(tool.Spec())

	res, err := reg.Execute(context.Background(), "ctx_execute",
		json.RawMessage(`{"command":["cmd","/c","start","explorer","."]}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Err != nil {
		t.Logf("start explorer error: %v", res.Err)
	}
	t.Logf("start explorer result: %s", res.Text)
}

// TestIntegration_WindowsShell_EnvVars checks that %USERNAME%
// returns a non-empty value.
func TestIntegration_WindowsShell_EnvVars(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only test")
	}
	dir := t.TempDir()
	runner := ctxexec.New(dir)
	tool := tools.NewCtxExecuteTool(runner, dir)
	reg := tools.NewRegistry()
	reg.MustRegister(tool.Spec())

	res, err := reg.Execute(context.Background(), "ctx_execute",
		json.RawMessage(`{"command":["cmd","/c","echo %USERNAME%"]}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("tool error: %v", res.Err)
	}
	out := strings.TrimSpace(res.Text)
	if out == "" || out == "%USERNAME%" {
		t.Errorf("USERNAME should be non-empty, got: %q", out)
	}
	t.Logf("USERNAME: %s", out)
}

// TestIntegration_WindowsShell_CurrentDir checks that cd returns
// the current working directory.
func TestIntegration_WindowsShell_CurrentDir(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only test")
	}
	dir := t.TempDir()
	runner := ctxexec.New(dir)
	tool := tools.NewCtxExecuteTool(runner, dir)
	reg := tools.NewRegistry()
	reg.MustRegister(tool.Spec())

	res, err := reg.Execute(context.Background(), "ctx_execute",
		json.RawMessage(`{"command":["cmd","/c","cd"]}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("tool error: %v", res.Err)
	}
	// Result is JSON; parse to extract stdout.
	var result struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(res.Text), &result); err != nil {
		t.Fatalf("parse result: %v\nraw: %s", err, res.Text)
	}
	stdout := strings.TrimSpace(result.Stdout)
	if !strings.Contains(stdout, filepath.Base(dir)) {
		t.Errorf("expected working dir to contain %q, got: %q", filepath.Base(dir), stdout)
	}
	t.Logf("cd: %s", stdout)
}

// TestIntegration_WindowsShell_ConcurrentCommands runs 5
// concurrent ctx_execute calls to check for races.
func TestIntegration_WindowsShell_ConcurrentCommands(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only test")
	}
	dir := t.TempDir()
	runner := ctxexec.New(dir)
	tool := tools.NewCtxExecuteTool(runner, dir)
	reg := tools.NewRegistry()
	reg.MustRegister(tool.Spec())

	const n = 5
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			res, err := reg.Execute(context.Background(), "ctx_execute",
				json.RawMessage(`{"command":["cmd","/c","echo concurrent_test"]}`))
			if err != nil {
				errs <- err
				return
			}
			if res.Err != nil {
				errs <- res.Err
				return
			}
			if !strings.Contains(res.Text, "concurrent_test") {
				errs <- fmt.Errorf("output missing 'concurrent_test': %s", res.Text)
				return
			}
			errs <- nil
		}()
	}
	for i := 0; i < n; i++ {
		if e := <-errs; e != nil {
			t.Errorf("concurrent call %d: %v", i, e)
		}
	}
}

// TestIntegration_WindowsShell_CmdMultipleArgs tests cmd with
// multiple arguments.
func TestIntegration_WindowsShell_CmdMultipleArgs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only test")
	}
	dir := t.TempDir()
	runner := ctxexec.New(dir)
	tool := tools.NewCtxExecuteTool(runner, dir)
	reg := tools.NewRegistry()
	reg.MustRegister(tool.Spec())

	res, err := reg.Execute(context.Background(), "ctx_execute",
		json.RawMessage(`{"command":["cmd","/c","echo","arg1","arg2","arg3"]}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("tool error: %v", res.Err)
	}
	if !strings.Contains(res.Text, "arg1") || !strings.Contains(res.Text, "arg2") {
		t.Errorf("expected all args in output: %s", res.Text)
	}
	t.Logf("multi-arg: %s", res.Text)
}
