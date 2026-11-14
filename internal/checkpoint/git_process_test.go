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

const checkpointStreamSlowPayloadLines = 64

func checkpointStreamSlowPayload() string {
	var b strings.Builder
	for i := 0; i < checkpointStreamSlowPayloadLines; i++ {
		fmt.Fprintf(&b, "slow-consumer-line-%04d\n", i)
	}
	return b.String()
}

func TestCheckpointStreamNativeHelper(t *testing.T) {
	mode := os.Getenv("SUPERCLI_CHECKPOINT_STREAM_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("ścieżka/with spaces & <tag>.txt\x00", 65536), "FINAL_STDOUT_TAIL\x00")
		fmt.Fprint(os.Stderr, "FINAL_STDERR_TAIL")
		os.Exit(0)
	case "slow-consumer":
		// The payload fits in the pipe buffer, so the native process exits long
		// before the consumer has read it. Exit closes the TCP connection, which
		// tells the test that native exit has happened.
		conn, err := net.DialTimeout("tcp", os.Getenv("SUPERCLI_CHECKPOINT_STREAM_ADDR"), 30*time.Second)
		if err != nil {
			os.Exit(81)
		}
		fmt.Fprint(os.Stdout, checkpointStreamSlowPayload())
		_ = conn.Close()
		os.Exit(0)
	case "hold-parent":
		// Hand the stdout pipe to a grandchild that writes nothing, then exit.
		exe, err := os.Executable()
		if err != nil {
			os.Exit(84)
		}
		child := exec.Command(exe, "-test.run=^TestCheckpointStreamNativeHelper$")
		child.Env = append(os.Environ(), "SUPERCLI_CHECKPOINT_STREAM_HELPER=hold-grandchild")
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			os.Exit(85)
		}
		fmt.Fprint(os.Stdout, "PARENT_DONE\n")
		os.Exit(0)
	case "hold-grandchild":
		// Keep the inherited stdout writer open until the test releases us.
		conn, err := net.DialTimeout("tcp", os.Getenv("SUPERCLI_CHECKPOINT_STREAM_ADDR"), 30*time.Second)
		if err != nil {
			os.Exit(86)
		}
		_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
		var release [1]byte
		_, _ = conn.Read(release[:])
		os.Exit(0)
	}
	conn, err := net.DialTimeout("tcp", os.Getenv("SUPERCLI_CHECKPOINT_STREAM_ADDR"), 30*time.Second)
	if err != nil {
		os.Exit(81)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
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

func checkpointStreamListener(t *testing.T, timeout time.Duration) *net.TCPListener {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := listener.SetDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	return listener
}

func TestCheckpointCommandStreamNativeLargeOutputComplete(t *testing.T) {
	setCheckpointOutputDrainGrace(t, defaultCheckpointOutputDrainGrace)
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

// A consumer that is slow after native exit (busy between reads, small reads)
// must not be cut off: the drain grace counts from the last read progress.
func TestCheckpointCommandStreamSlowConsumerAfterExitCompletes(t *testing.T) {
	grace := 100 * time.Millisecond
	setCheckpointOutputDrainGrace(t, grace)
	listener := checkpointStreamListener(t, scaledTestTimeout(30*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := checkpointStreamNativeCommand(t, ctx, "slow-consumer")
	cmd.Env = append(cmd.Env, "SUPERCLI_CHECKPOINT_STREAM_ADDR="+listener.Addr().String())
	stream, err := startCheckpointCommandStream(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := listener.Accept()
	if err != nil {
		_ = stream.Wait()
		t.Fatal(err)
	}
	// EOF on the control connection means the native process has exited.
	_ = conn.SetDeadline(time.Now().Add(scaledTestTimeout(30 * time.Second)))
	_, _ = io.Copy(io.Discard, conn)
	_ = conn.Close()
	// Busy consumer outside Read: several graces pass without any read.
	time.Sleep(3 * grace)
	var data []byte
	buf := make([]byte, 48)
	var readErr error
	for {
		n, err := stream.Stdout.Read(buf)
		data = append(data, buf[:n]...)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
		// Processing each chunk takes most of a grace; total far exceeds it.
		time.Sleep(grace * 7 / 10)
	}
	waitErr := stream.Wait()
	if readErr != nil || waitErr != nil {
		t.Fatalf("slow consumer was cut off: read=%v wait=%v (ErrWaitDelay=%t) after %d bytes", readErr, waitErr, errors.Is(waitErr, exec.ErrWaitDelay), len(data))
	}
	if string(data) != checkpointStreamSlowPayload() {
		t.Fatalf("slow consumer lost bytes: got %d want %d", len(data), len(checkpointStreamSlowPayload()))
	}
}

// A grandchild holding the stdout writer open while delivering nothing must
// still be bounded shortly after the drain grace.
func TestCheckpointCommandStreamHeldPipeWithoutProgressIsBounded(t *testing.T) {
	skipSlowNativeInShort(t)
	grace := 200 * time.Millisecond
	setCheckpointOutputDrainGrace(t, grace)
	listener := checkpointStreamListener(t, scaledTestTimeout(30*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := checkpointStreamNativeCommand(t, ctx, "hold-parent")
	cmd.Env = append(cmd.Env, "SUPERCLI_CHECKPOINT_STREAM_ADDR="+listener.Addr().String())
	stream, err := startCheckpointCommandStream(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := listener.Accept()
	if err != nil {
		cancel()
		_ = stream.Wait()
		t.Fatal(err)
	}
	// Closing the control connection releases the grandchild.
	defer conn.Close()
	type result struct {
		data       []byte
		read, wait error
		elapsed    time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		data, readErr := io.ReadAll(stream.Stdout)
		waitErr := stream.Wait()
		done <- result{data, readErr, waitErr, time.Since(start)}
	}()
	select {
	case r := <-done:
		if !errors.Is(r.wait, exec.ErrWaitDelay) {
			t.Fatalf("held pipe without progress was not reported incomplete: %+v", r)
		}
		if string(r.data) != "PARENT_DONE\n" {
			t.Fatalf("parent output lost: %q", r.data)
		}
		if r.elapsed < grace/2 { // Coarse: the reader starts after native start.
			t.Fatalf("held pipe closed before the drain grace: %v < %v", r.elapsed, grace)
		}
	case <-time.After(grace + scaledTestTimeout(20*time.Second)):
		cancel()
		t.Fatal("held pipe without progress was not bounded by the drain grace")
	}
}

func TestCheckpointCommandStreamNativeLiveProducer(t *testing.T) {
	grace := 300 * time.Millisecond
	setCheckpointOutputDrainGrace(t, grace)
	for _, mode := range []string{"cancel", "delayed-exit"} {
		t.Run(mode, func(t *testing.T) {
			listener := checkpointStreamListener(t, scaledTestTimeout(30*time.Second))
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
				cancel()
				_ = stream.Wait()
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(scaledTestTimeout(30 * time.Second))); err != nil {
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
				case <-time.After(3 * grace):
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
			case <-time.After(grace + scaledTestTimeout(20*time.Second)):
				cancel()
				t.Fatal("native cancellation/completion did not release the stream owner")
			}
		})
	}
}
