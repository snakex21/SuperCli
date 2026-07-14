package processsession

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProcessSessionHelper(t *testing.T) {
	if os.Getenv("SUPERCLI_PROCESS_HELPER") != "1" {
		return
	}
	args := os.Args
	mode := "echo"
	for i, arg := range args {
		if arg == "--" && i+1 < len(args) {
			mode = args[i+1]
		}
	}
	switch mode {
	case "stdin":
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Print("got:" + line)
	case "sleep":
		time.Sleep(30 * time.Second)
	default:
		fmt.Println("hello-out")
		fmt.Fprintln(os.Stderr, "hello-err")
	}
	os.Exit(0)
}

func helperCommand(mode string) []string {
	return []string{os.Args[0], "-test.run=TestProcessSessionHelper", "--", mode}
}

func execute(t *testing.T, tool *Tool, value any) (snapshot, error) {
	t.Helper()
	raw, _ := json.Marshal(value)
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		return snapshot{}, err
	}
	if res.Err != nil {
		return snapshot{}, res.Err
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(res.Text), &snap); err != nil {
		return snapshot{}, err
	}
	return snap, nil
}

func TestStartCapturesBoundedOutputAndCompletion(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	snap, err := execute(t, tool, map[string]any{"action": "start", "command": helperCommand("echo"), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 1000})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	stdout.WriteString(snap.Stdout)
	stderr.WriteString(snap.Stderr)
	deadline := time.Now().Add(5 * time.Second)
	for snap.Status == "running" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		snap, err = execute(t, tool, map[string]any{"action": "poll", "id": snap.ID})
		if err != nil {
			t.Fatal(err)
		}
		stdout.WriteString(snap.Stdout)
		stderr.WriteString(snap.Stderr)
	}
	if snap.Status != "done" || snap.ExitCode == nil || *snap.ExitCode != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if !strings.Contains(stdout.String(), "hello-out") || !strings.Contains(stderr.String(), "hello-err") {
		t.Fatalf("missing streams: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestWriteAndPollInteractiveProcess(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	start, err := execute(t, tool, map[string]any{"action": "start", "command": helperCommand("stdin"), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, tool, map[string]any{"action": "write", "id": start.ID, "input": "ping"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := execute(t, tool, map[string]any{"action": "poll", "id": start.ID})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(snap.Stdout, "got:ping") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("interactive output did not arrive")
}

func TestStopCancelsProcess(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	start, err := execute(t, tool, map[string]any{"action": "start", "command": helperCommand("sleep"), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 0})
	if err != nil {
		t.Fatal(err)
	}
	stop, err := execute(t, tool, map[string]any{"action": "stop", "id": start.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stop.Status != "stopped" {
		t.Fatalf("status = %q", stop.Status)
	}
}

func TestPTYCapturesMergedOutputAndResizes(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	snap, err := execute(t, tool, map[string]any{
		"action": "start", "command": helperCommand("echo"),
		"env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 1000,
		"pty": true, "columns": 90, "rows": 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !snap.PTY {
		t.Fatalf("PTY marker missing: %+v", snap)
	}
	var output strings.Builder
	output.WriteString(snap.Stdout)
	deadline := time.Now().Add(5 * time.Second)
	for snap.Status == "running" && time.Now().Before(deadline) {
		if _, err := execute(t, tool, map[string]any{"action": "resize", "id": snap.ID, "columns": 110, "rows": 35}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
		snap, err = execute(t, tool, map[string]any{"action": "poll", "id": snap.ID})
		if err != nil {
			t.Fatal(err)
		}
		output.WriteString(snap.Stdout)
	}
	if snap.Status != "done" || snap.ExitCode == nil || *snap.ExitCode != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if !strings.Contains(output.String(), "hello-out") || !strings.Contains(output.String(), "hello-err") {
		t.Fatalf("PTY did not merge both streams: %q", output.String())
	}
	if strings.Contains(output.String(), "\x1b") || strings.Contains(output.String(), "\r") {
		t.Fatalf("terminal control bytes leaked: %q", output.String())
	}
}

func TestPTYInteractiveWriteAndResize(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	start, err := execute(t, tool, map[string]any{
		"action": "start", "command": helperCommand("stdin"),
		"env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 0,
		"pty": true, "columns": 80, "rows": 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, tool, map[string]any{"action": "resize", "id": start.ID, "columns": 120, "rows": 40}); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, tool, map[string]any{"action": "write", "id": start.ID, "input": "ping"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var output strings.Builder
	for time.Now().Before(deadline) {
		snap, err := execute(t, tool, map[string]any{"action": "poll", "id": start.ID})
		if err != nil {
			t.Fatal(err)
		}
		output.WriteString(snap.Stdout)
		if strings.Contains(output.String(), "got:ping") {
			return
		}
		if snap.Status != "running" {
			t.Fatalf("PTY exited before reading input: %+v output=%q", snap, output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("interactive PTY output did not arrive: %q", output.String())
}

func TestResizeRequiresPTY(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	start, err := execute(t, tool, map[string]any{"action": "start", "command": helperCommand("sleep"), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, tool, map[string]any{"action": "resize", "id": start.ID, "columns": 80, "rows": 24}); err == nil || !strings.Contains(err.Error(), "pty=true") {
		t.Fatalf("resize error = %v", err)
	}
}

func TestStripTerminalControl(t *testing.T) {
	input := []byte("plain\r\n\x1b[31mred\x1b[0m\x1b]0;title\a!\rnext")
	got := string(stripTerminalControl(input))
	if got != "plain\nred!\nnext" {
		t.Fatalf("got %q", got)
	}
}

func TestStreamBufferReportsDroppedBytes(t *testing.T) {
	b := newStreamBuffer(5)
	_, _ = b.Write([]byte("12345678"))
	got, cursor, omitted := b.readFrom(0, 10)
	if string(got) != "45678" || cursor != 8 || omitted != 3 {
		t.Fatalf("got=%q cursor=%d omitted=%d", got, cursor, omitted)
	}
}
