//go:build windows

package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateCanReplaceItsOwnRunningWindowsExecutable(t *testing.T) {
	if os.Getenv("SUPERCLI_UPDATE_TEST_HELPER") == "1" {
		m := newManager(os.Getenv("SUPERCLI_UPDATE_TEST_DIR"), "1.0.0")
		state, err := m.Install(context.Background())
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, state.Status)
		os.Exit(0)
	}
	m := updateFixture(t, nil)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(m.Dir, "supercli.exe")
	if err = os.WriteFile(target, raw, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(m.Dir, "supercli-web.exe"), []byte("old GUI"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Download(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30e9)
	defer cancel()
	command := exec.CommandContext(ctx, target, "-test.run=^TestUpdateCanReplaceItsOwnRunningWindowsExecutable$")
	command.Env = append(os.Environ(), "SUPERCLI_UPDATE_TEST_HELPER=1", "SUPERCLI_UPDATE_TEST_DIR="+m.Dir)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "installed") {
		t.Fatalf("running binary update: %v %s", err, output)
	}
	installed, err := os.ReadFile(target)
	if err != nil || string(installed) != "new supercli.exe" {
		t.Fatal("running executable was not replaced")
	}
	backups, _ := filepath.Glob(filepath.Join(m.Dir, "supercli-updates", "backup-1.0.1-*", "supercli.exe"))
	if len(backups) != 1 {
		t.Fatal("running executable backup missing")
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil || len(backup) != len(raw) {
		t.Fatal("running executable backup changed")
	}
}
