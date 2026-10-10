package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCheckpointStreamNativeHelper(t *testing.T) {
	mode := os.Getenv("SUPERCLI_CHECKPOINT_STREAM_HELPER")
	if mode == "" {
		return
	}
	if mode == "large" {
		fmt.Fprint(os.Stdout, strings.Repeat("ścieżka/with spaces & <tag>.txt\x00", 65536), "FINAL_STDOUT_TAIL\x00")
		fmt.Fprint(os.Stderr, "FINAL_STDERR_TAIL")
		os.Exit(0)
	}
	conn, err := net.DialTimeout("tcp", os.Getenv("SUPERCLI_CHECKPOINT_STREAM_ADDR"), 5*time.Second)
	if err != nil {
		os.Exit(81)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	fmt.Fprint(os.Stdout, "READY\n")
	if _, err := conn.Write([]byte("R")); err != nil {
		os.Exit(82)
	}
	var release [1]byte
	if _, err := io.ReadFull(conn, release[:]); err != nil {
		os.Exit(83)
	}
	fmt.Fprint(os.Stdout, "FINAL_LIVE_TAIL\n")
	os.Exit(0)
}

func checkpointStreamNativeCommand(t *testing.T, ctx context.Context, mode string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := (&Manager{home: t.TempDir()}).gitCommand(ctx, "fixture")
	// Keep the production command factory's lifecycle and pipe configuration.
	cmd.Path = exe
	cmd.Args = []string{exe, "-test.run=^TestCheckpointStreamNativeHelper$"}
	cmd.Env = append(cmd.Env, "SUPERCLI_CHECKPOINT_STREAM_HELPER="+mode)
	return cmd
}

func TestCheckpointCommandStreamNativeLargeOutputComplete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := checkpointStreamNativeCommand(t, ctx, "large")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stream, err := startCheckpointCommandStream(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(stream.Stdout)
	if waitErr := stream.Wait(); readErr != nil || waitErr != nil {
		t.Fatalf("valid complete native output failed: read=%v wait=%v", readErr, waitErr)
	}
	expected := strings.Repeat("ścieżka/with spaces & <tag>.txt\x00", 65536) + "FINAL_STDOUT_TAIL\x00"
	if string(data) != expected || stderr.String() != "FINAL_STDERR_TAIL" {
		t.Fatalf("complete native tail bytes lost: stdout=%d/%d stderr=%q", len(data), len(expected), stderr.String())
	}
}

func TestCheckpointCommandStreamNativeLiveProducer(t *testing.T) {
	for _, mode := range []string{"cancel", "delayed-exit"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := checkpointStreamNativeCommand(t, ctx, "live")
			cmd.Env = append(cmd.Env, "SUPERCLI_CHECKPOINT_STREAM_ADDR="+listener.Addr().String())
			stream, err := startCheckpointCommandStream(ctx, cmd)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var ready [1]byte
			if _, err := io.ReadFull(conn, ready[:]); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct {
				data       []byte
				read, wait error
			}, 1)
			go func() {
				data, readErr := io.ReadAll(stream.Stdout)
				waitErr := stream.Wait()
				done <- struct {
					data       []byte
					read, wait error
				}{data, readErr, waitErr}
			}()
			if mode == "cancel" {
				cancel()
			} else {
				// A reader waiting on a live native process must outlive the drain grace.
				select {
				case <-done:
					t.Fatal("drain timer killed a live native producer")
				case <-time.After(checkpointOutputDrainGrace + 500*time.Millisecond):
				}
				if _, err := conn.Write([]byte("X")); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case result := <-done:
				if mode == "cancel" {
					if !errors.Is(result.wait, context.Canceled) {
						t.Fatalf("native cancellation was lost: %+v", result)
					}
				} else if result.read != nil || result.wait != nil || string(result.data) != "READY\nFINAL_LIVE_TAIL\n" {
					t.Fatalf("valid delayed output changed: %+v", result)
				}
			case <-time.After(3 * time.Second):
				cancel()
				t.Fatal("native cancellation/completion did not release the stream owner")
			}
		})
	}
}
