//go:build windows

package ctxexec

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHiddenPowerShellOutputKeepsOEMPolishAndGroupingSpace(t *testing.T) {
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell unavailable")
	}
	// PowerShell itself folds Polish letters when the system OEM code page
	// cannot represent them (e.g. 437 on CI runners); nothing to decode then.
	if cp, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetOEMCP").Call(); cp != 852 {
		t.Skipf("system OEM code page %d cannot represent Polish text", cp)
	}
	result, err := New(t.TempDir()).Run(context.Background(), &Request{Command: []string{ps, "-NoProfile", "-Command", `[Threading.Thread]::CurrentThread.CurrentCulture = [Globalization.CultureInfo]::GetCultureInfo('pl-PL'); '{0:N0} zażółć' -f 18847`}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("PowerShell: %+v %v", result, err)
	}
	if !strings.Contains(result.Stdout, "zażółć") || !strings.Contains(result.Stdout, "18\u00a0847") || strings.ContainsRune(result.Stdout, '\uFFFD') {
		t.Fatalf("corrupted OEM output: %q", result.Stdout)
	}
	if strings.Contains(result.RetainedJSON(), `\ufffd`) {
		t.Fatal("retained JSON corrupted")
	}
}
