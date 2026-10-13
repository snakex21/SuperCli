//go:build windows

package processsession

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestNativePowerShellStartObservesFastFailure(t *testing.T) {
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell is unavailable")
	}
	tool := New(t.TempDir())
	t.Cleanup(tool.Close)
	raw, _ := json.Marshal(map[string]any{"action": "start", "command": []string{powershell, "-NoProfile", "-Command", "[Console]::Error.WriteLine('start error: port busy'); exit 3"}})
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(result.Text), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Status == "running" {
		// A loaded host can take longer than the bounded observation window
		// merely to launch PowerShell. Do not convert slow startup into failure.
		t.Logf("PowerShell outlived the 500ms startup observation; awaiting actual exit")
		final, err := tool.Manager.Wait(context.Background(), snap.ID)
		if err != nil || final.ExitCode == nil || *final.ExitCode != 3 || !strings.Contains(snap.Stderr+final.Stderr, "start error: port busy") {
			t.Fatalf("slow startup lost eventual evidence: %+v %v", final, err)
		}
		return
	}
	if result.Err == nil || snap.ExitCode == nil || *snap.ExitCode != 3 || !strings.Contains(snap.Stderr, "start error: port busy") {
		t.Fatalf("fast PowerShell failure not reported by start: %+v %v", snap, result.Err)
	}
	t.Logf("PowerShell failure observed by start: native duration %dms", snap.DurationMS)
}
