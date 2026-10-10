//go:build windows

package checkpoint

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A native test executable owns Git's argv slot only for this fixture. It
// duplicates its actual stdout/stderr writer handles into the test host, so
// those writers deterministically outlive native exit even on a Job Object
// host. This tests real Windows pipes and native Wait, not fake wait functions.
// The earlier instant-command regression separately exercises real Git.
func TestMain(m *testing.M) {
	if os.Getenv("SUPERCLI_CHECKPOINT_PIPE_FIXTURE") == "1" {
		checkpointPipeNativeHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type checkpointPipeReady struct {
	PID    int
	Stdout uint64
	Stderr uint64
}

func checkpointPipeNativeHelper() {
	hostPID, err := strconv.ParseUint(os.Getenv("SUPERCLI_CHECKPOINT_PIPE_HOST"), 10, 32)
	if err != nil {
		os.Exit(71)
	}
	host, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, uint32(hostPID))
	if err != nil {
		os.Exit(72)
	}
	defer windows.CloseHandle(host)
	var out, stderr windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(os.Stdout.Fd()), host, &out, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		os.Exit(73)
	}
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(os.Stderr.Fd()), host, &stderr, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		os.Exit(74)
	}
	conn, err := net.DialTimeout("tcp", os.Getenv("SUPERCLI_CHECKPOINT_PIPE_ADDR"), 5*time.Second)
	if err != nil {
		os.Exit(75)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := json.NewEncoder(conn).Encode(checkpointPipeReady{os.Getpid(), uint64(out), uint64(stderr)}); err != nil {
		os.Exit(76)
	}
	var release [1]byte
	if _, err := io.ReadFull(conn, release[:]); err != nil {
		os.Exit(77)
	}
	switch os.Getenv("SUPERCLI_CHECKPOINT_PIPE_PROTOCOL") {
	case "tree":
		fmt.Fprint(os.Stdout, "100644 blob "+strings.Repeat("a", 40)+"\tordinary.txt\x00")
	case "blob":
		fmt.Fprint(os.Stdout, "ordinary.txt")
	default:
		fmt.Fprint(os.Stdout, "ordinary.txt\x00")
	}
	fmt.Fprint(os.Stderr, "native helper completed stderr")
}

func checkpointPipeFixture(t *testing.T) (*Manager, *net.TCPListener) {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.OpenFile(filepath.Join(dir, "git.exe"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SUPERCLI_CHECKPOINT_PIPE_FIXTURE", "1")
	t.Setenv("SUPERCLI_CHECKPOINT_PIPE_HOST", strconv.Itoa(os.Getpid()))
	t.Setenv("SUPERCLI_CHECKPOINT_PIPE_ADDR", listener.Addr().String())
	return &Manager{home: dir, repo: filepath.Join(dir, "objects.git")}, listener
}

func checkpointPipeFinishNative(t *testing.T, listener *net.TCPListener) func() {
	t.Helper()
	conn, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready checkpointPipeReady
	if err := json.NewDecoder(conn).Decode(&ready); err != nil {
		t.Fatal(err)
	}
	out := os.NewFile(uintptr(ready.Stdout), "held stdout writer")
	stderr := os.NewFile(uintptr(ready.Stderr), "held stderr writer")
	closeWriters := func() { _ = out.Close(); _ = stderr.Close() }
	t.Cleanup(closeWriters)
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(ready.PID))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if _, err := conn.Write([]byte("X")); err != nil {
		t.Fatal(err)
	}
	state, err := windows.WaitForSingleObject(handle, 5000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("native helper did not exit: state=%d err=%v", state, err)
	}
	// Both writer handles remain open after proven native exit. No status polling.
	return closeWriters
}

func TestCheckpointGitCombinedOutputHeldPipeWindows(t *testing.T) {
	for _, cancelAfterExit := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelAfterExit), func(t *testing.T) {
			manager, listener := checkpointPipeFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := manager.gitCommand(ctx, "rev-parse", "--git-dir")
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			closeWriters := checkpointPipeFinishNative(t, listener)
			if cancelAfterExit {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Error("incomplete inherited-pipe capture reported success")
				}
			case <-time.After(3 * time.Second):
				t.Error("Git factory Wait hung after native exit with output writers still open")
				cancel()
				closeWriters()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("fixture cleanup did not release native waiter")
				}
			}
			if !strings.Contains(output.String(), "ordinary.txt") || !strings.Contains(output.String(), "native helper completed stderr") {
				t.Fatalf("native output evidence lost: %q", output.String())
			}
		})
	}
}

func TestCheckpointGitScannerHeldPipeWindows(t *testing.T) {
	for _, cancelAfterExit := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelAfterExit), func(t *testing.T) {
			manager, listener := checkpointPipeFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			visited := make(chan string, 1)
			done := make(chan error, 1)
			go func() {
				done <- manager.eachWorkspacePath(ctx, func(path string) error { visited <- path; return nil })
			}()
			closeWriters := checkpointPipeFinishNative(t, listener)
			select {
			case path := <-visited:
				if path != "ordinary.txt" {
					t.Fatalf("unexpected scanner token %q", path)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("native enumeration token was not read")
			}
			if cancelAfterExit {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Error("incomplete enumeration accepted as a complete BEFORE")
				}
				if cancelAfterExit && !errors.Is(err, context.Canceled) {
					t.Errorf("scanner cancellation cause lost: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Error("scanner hung before Wait after native exit with output writers still open")
				cancel()
				closeWriters()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("fixture cleanup did not release scanner/native waiter")
				}
			}
		})
	}
}

// The same cancellation ownership must cover every manually consumed Git stream.
func TestCheckpointManualReadersCancelHeldPipeWindows(t *testing.T) {
	for _, kind := range []string{"narrow-tree", "restore-blob", "retention-tokens"} {
		t.Run(kind, func(t *testing.T) {
			manager, listener := checkpointPipeFixture(t)
			if err := os.Mkdir(manager.repo, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			switch kind {
			case "narrow-tree":
				t.Setenv("SUPERCLI_CHECKPOINT_PIPE_PROTOCOL", "tree")
				go func() {
					_, err := manager.readNarrowTree(ctx, strings.Repeat("a", 40), map[string]bool{"ordinary.txt": true})
					done <- err
				}()
			case "restore-blob":
				t.Setenv("SUPERCLI_CHECKPOINT_PIPE_PROTOCOL", "blob")
				content := "ordinary.txt"
				sum := sha1.Sum([]byte(fmt.Sprintf("blob %d%c%s", len(content), 0, content)))
				go func() {
					_, err := manager.stageRestoreBlob(ctx, "restored.txt", restoreBlob{hash: hex.EncodeToString(sum[:]), mode: 0600, size: int64(len(content)), exists: true})
					done <- err
				}()
			case "retention-tokens":
				audit := &StoreRetentionAudit{}
				go func() {
					done <- audit.gitTokens(ctx, manager.repo, nil, true, func(string) error { return nil }, "fixture")
				}()
			}
			closeWriters := checkpointPipeFinishNative(t, listener)
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("%s lost cancellation or accepted incomplete stream: %v", kind, err)
				}
			case <-time.After(3 * time.Second):
				t.Errorf("%s remains blocked after native exit/cancel", kind)
				closeWriters()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("native reader cleanup failed")
				}
			}
			temporary, err := filepath.Glob(filepath.Join(manager.home, ".supercli-restore-*"))
			if err != nil || len(temporary) != 0 {
				t.Fatalf("cancelled restore retained staging files: %v %v", temporary, err)
			}
		})
	}
}
