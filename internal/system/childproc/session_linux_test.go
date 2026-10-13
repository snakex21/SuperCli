//go:build linux

package childproc

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// sessionID reads field 6 (session) of /proc/<pid>/stat.
func sessionID(t *testing.T, pid int) string {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	fields := strings.Fields(s[strings.LastIndexByte(s, ')')+2:]) // fields[0] == field 3
	return fields[3]
}

func TestStartDetachesChildFromTerminalSession(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	cmd.SysProcAttr = nil
	scope, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scope.Kill(cmd); _ = cmd.Wait() }()
	if got, own := sessionID(t, cmd.Process.Pid), sessionID(t, os.Getpid()); got == own {
		t.Fatalf("child shares session %s with parent", own)
	}
	if got := sessionID(t, cmd.Process.Pid); got != fmt.Sprint(cmd.Process.Pid) {
		t.Fatalf("child session = %s, want its own pid %d", got, cmd.Process.Pid)
	}
}
