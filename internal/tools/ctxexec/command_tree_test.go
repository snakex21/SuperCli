package ctxexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// The child signals readiness over a socket and holds it until killed. EOF
// verifies cancellation of the descendant, without polling processes or files.
func TestCommandTreeHelper(t *testing.T) {
	mode := os.Getenv("SUPERCLI_COMMAND_TREE_HELPER")
	if mode == "" {
		return
	}
	if mode == "child" {
		conn, err := net.Dial("tcp", os.Getenv("SUPERCLI_COMMAND_TREE_ADDR"))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("R")); err != nil {
			t.Fatal(err)
		}
		var b [1]byte
		_, _ = conn.Read(b[:])
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestCommandTreeHelper$")
	child.Env = replaceEnv(os.Environ(), "SUPERCLI_COMMAND_TREE_HELPER=child")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	os.Exit(0)
}

func TestRunnerCancellationStopsDescendantWithoutDefaultTimer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *Result, 1)
	errDone := make(chan error, 1)
	runner := New(t.TempDir())
	go func() {
		result, err := runner.Run(ctx, &Request{
			Command:  []string{os.Args[0], "-test.run=^TestCommandTreeHelper$"},
			EnvExtra: []string{"SUPERCLI_COMMAND_TREE_HELPER=parent", "SUPERCLI_COMMAND_TREE_ADDR=" + listener.Addr().String()},
		})
		done <- result
		errDone <- err
	}()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready [1]byte
	if _, err := io.ReadFull(conn, ready[:]); err != nil || ready[0] != 'R' {
		t.Fatalf("child readiness: %q, %v", ready, err)
	}
	cancel()
	select {
	case result := <-done:
		if err := <-errDone; err != nil {
			t.Fatal(err)
		}
		if result.ExitCode == 0 {
			t.Fatalf("canceled command succeeded: %s", fmt.Sprint(result))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled command did not return")
	}
	if n, err := conn.Read(ready[:]); n != 0 || !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) && !isNativeConnectionReset(err) {
		t.Fatalf("descendant still holds socket: n=%d, err=%v", n, err)
	}
}
