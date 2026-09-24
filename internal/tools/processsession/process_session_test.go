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
	case "fail":
		fmt.Fprintln(os.Stderr, "specific failing assertion")
		os.Exit(7)
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
	start, err := execute(t, tool, map[string]any{"action": "start", "command": helperCommand("echo"), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 0})
	if err != nil {
		t.Fatal(err)
	}
	final := waitCompletion(t, tool, start.ID)
	if final.Status != "done" || final.ExitCode == nil || *final.ExitCode != 0 {
		t.Fatalf("snapshot = %+v", final)
	}
	if !strings.Contains(start.Stdout+final.Stdout, "hello-out") || !strings.Contains(start.Stderr+final.Stderr, "hello-err") {
		t.Fatalf("missing streams: start=%+v final=%+v", start, final)
	}
}

func TestWriteAndWaitInteractiveProcess(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	start, err := execute(t, tool, map[string]any{"action": "start", "command": helperCommand("stdin"), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 0})
	if err != nil {
		t.Fatal(err)
	}
	written, err := execute(t, tool, map[string]any{"action": "write", "id": start.ID, "input": "ping"})
	if err != nil {
		t.Fatal(err)
	}
	final := waitCompletion(t, tool, start.ID)
	if final.Status != "done" || !strings.Contains(start.Stdout+written.Stdout+final.Stdout, "got:ping") {
		t.Fatalf("interactive output missing: %+v %+v %+v", start, written, final)
	}
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

func TestPTYCapturesMergedOutputAndCompletion(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	start, err := execute(t, tool, map[string]any{"action": "start", "command": helperCommand("echo"), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 0, "pty": true, "columns": 90, "rows": 24})
	if err != nil {
		t.Fatal(err)
	}
	final := waitCompletion(t, tool, start.ID)
	if !final.PTY || final.Status != "done" || final.ExitCode == nil || *final.ExitCode != 0 {
		t.Fatalf("snapshot = %+v", final)
	}
	output := start.Stdout + final.Stdout
	if !strings.Contains(output, "hello-out") || !strings.Contains(output, "hello-err") {
		t.Fatalf("PTY did not merge both streams: %q", output)
	}
	if strings.ContainsAny(output, "\x1b\r") {
		t.Fatalf("terminal control bytes leaked: %q", output)
	}
}

func TestPTYInteractiveWriteAndResize(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	start, err := execute(t, tool, map[string]any{"action": "start", "command": helperCommand("stdin"), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 0, "pty": true, "columns": 80, "rows": 24})
	if err != nil {
		t.Fatal(err)
	}
	resized, err := execute(t, tool, map[string]any{"action": "resize", "id": start.ID, "columns": 120, "rows": 40})
	if err != nil {
		t.Fatal(err)
	}
	written, err := execute(t, tool, map[string]any{"action": "write", "id": start.ID, "input": "ping"})
	if err != nil {
		t.Fatal(err)
	}
	final := waitCompletion(t, tool, start.ID)
	output := start.Stdout + resized.Stdout + written.Stdout + final.Stdout
	if final.Status != "done" || !strings.Contains(output, "got:ping") {
		t.Fatalf("interactive PTY output missing: %+v output=%q", final, output)
	}
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

func waitCompletion(t *testing.T, tool *Tool, id string) snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, _ := json.Marshal(map[string]string{"action": "wait", "id": id})
	result, err := tool.Execute(ctx, raw)
	if err != nil || result.Err != nil {
		t.Fatalf("wait: %+v %v", result, err)
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(result.Text), &snap); err != nil {
		t.Fatal(err)
	}
	return snap
}
