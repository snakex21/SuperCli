//go:build windows

package childproc

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestMain doubles as the helper processes: a parent that starts a sleeping
// child and prints its PID, like the Git-for-Windows launcher does.
func TestMain(m *testing.M) {
	switch os.Getenv("SUPERCLI_TREE_HELPER") {
	case "parent":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "SUPERCLI_TREE_HELPER=child")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		os.Stdout.WriteString(strconv.Itoa(child.Process.Pid) + "\n")
		_ = child.Wait()
		os.Exit(0)
	case "child":
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	case "orphan-git":
		// Starts git (the last four arguments) and exits at once, leaving
		// it orphaned.
		args := os.Args[len(os.Args)-4:]
		git := exec.Command(args[0], args[1:]...)
		git.Stdin = os.Stdin
		if err := git.Start(); err != nil {
			os.Exit(3)
		}
		os.Stdout.WriteString(strconv.Itoa(git.Process.Pid) + "\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	event, _ := windows.WaitForSingleObject(h, 0)
	return event == uint32(windows.WAIT_TIMEOUT)
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
	t.Fatalf("descendant %d survived the cancelled command", pid)
}

// Cancelling a command ends its child too (before: only the launcher died
// and its child kept running, orphaned).
func TestCancelledTreeLeavesNoDescendants(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), "SUPERCLI_TREE_HELPER=parent")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := StartTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	child, _ := strconv.Atoi(strings.TrimSpace(line))
	if child <= 0 || !processAlive(child) {
		t.Fatalf("fixture child not running: %q", line)
	}
	cancel()
	_ = tree.Wait()
	waitGone(t, child)
}

// The same with real Git for Windows: "git" is the cmd\git.exe launcher; a
// cancelled long git leaves no git.exe descendant behind.
func TestCancelledGitLeavesNoGitDescendants(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	// Prefer the launcher (what SuperCli finds on a usual PATH).
	if launcher := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(git))), "cmd", "git.exe"); strings.EqualFold(filepath.Base(filepath.Dir(filepath.Dir(git))), "mingw64") {
		if _, statErr := os.Stat(launcher); statErr == nil {
			git = launcher
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, git, "cat-file", "--batch") // waits on stdin forever
	cmd.Dir = t.TempDir()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	tree, err := StartTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	root := cmd.Process.Pid
	var tracked []int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tracked = descendants(root)
		if len(tracked) > 0 || !strings.EqualFold(filepath.Base(filepath.Dir(git)), "cmd") {
			break // the launcher started its real git (or git is not the launcher)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("git %s: launcher %d, descendants %v", git, root, tracked)
	cancel()
	_ = tree.Wait()
	for _, pid := range append(tracked, root) {
		waitGone(t, pid)
	}
}

// descendants lists the processes started (transitively) by pid.
func descendants(pid int) []int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	parents := map[int]int{}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		parents[int(entry.ProcessID)] = int(entry.ParentProcessID)
	}
	var out []int
	frontier := map[int]bool{pid: true}
	for changed := true; changed; {
		changed = false
		for child, parent := range parents {
			if frontier[parent] && !frontier[child] {
				frontier[child] = true
				out = append(out, child)
				changed = true
			}
		}
	}
	return out
}

// An orphaned git of this install's checkpoint store is ended at startup; a
// git with another command line (the user's own) is left alone.
func TestSweepEndsOnlyThisInstallsOrphanedCheckpointGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	data := filepath.Join(t.TempDir(), "supercli-data")
	store := filepath.Join(data, "checkpoints", "abc", "objects.git")
	other := t.TempDir()
	for _, dir := range []string{store, other} {
		if out, err := exec.Command(git, "init", "--bare", "-q", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
	}
	start := func(gitDir string) int {
		t.Helper()
		helper := exec.Command(os.Args[0], "-test.run=^$", git, "--git-dir="+gitDir, "cat-file", "--batch")
		helper.Env = append(os.Environ(), "SUPERCLI_TREE_HELPER=orphan-git")
		// git waits on stdin: keep the write end open for the whole test.
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		helper.Stdin = r
		t.Cleanup(func() { w.Close(); r.Close() })
		out, err := helper.Output()
		if err != nil {
			t.Fatalf("helper: %v", err)
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		if pid <= 0 {
			t.Fatalf("helper output %q", out)
		}
		t.Cleanup(func() { _ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run() })
		return pid
	}
	mine := start(store)
	users := start(other)
	if !processAlive(mine) || !processAlive(users) {
		t.Skip("git exited at once in this environment")
	}
	if n := SweepOrphanedCheckpointGit(data); n < 1 {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(mine))
		line, ok := "", false
		if err == nil {
			line, ok = processCommandLine(h)
			windows.CloseHandle(h)
		}
		t.Fatalf("swept %d; data %s; command line %q (%v, open %v)", n, data, line, ok, err)
	}
	waitGone(t, mine)
	if !processAlive(users) {
		t.Fatal("the user's own git was ended")
	}
}
