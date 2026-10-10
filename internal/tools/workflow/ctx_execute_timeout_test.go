package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"supercli/internal/tools/ctxexec"
)

func TestCtxTimeoutHelper(t *testing.T) {
	mode := os.Getenv("SUPERCLI_TIMEOUT_HELPER")
	if mode == "" {
		return
	}
	fmt.Println("STARTED")
	switch mode {
	case "wait":
		time.Sleep(10 * time.Second)
	case "long":
		time.Sleep(31 * time.Second)
	}
	fmt.Println("FINISHED")
	os.Exit(0)
}

func ctxTimeoutArgs(t *testing.T, mode string, timeout int) json.RawMessage {
	t.Helper()
	args, err := json.Marshal(map[string]any{
		"command":    []string{os.Args[0], "-test.run=^TestCtxTimeoutHelper$"},
		"env_extra":  []string{"SUPERCLI_TIMEOUT_HELPER=" + mode},
		"timeout_ms": timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	return args
}

func TestCtxExecuteAcceptsLongExplicitTimeout(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry()
	reg.MustRegister(NewCtxExecuteTool(ctxexec.New(root), root).Spec())
	for _, timeout := range []int{0, 60000, 120000, 300000, 300001, 1800000} {
		result, err := reg.Execute(context.Background(), "ctx_execute", ctxTimeoutArgs(t, "quick", timeout))
		if err != nil || result.Err != nil || !strings.Contains(result.Text, "FINISHED") {
			t.Fatalf("timeout=%d: %v / %v / %s", timeout, err, result.Err, result.Text)
		}
	}
	// Negative and unrepresentable values must not overflow into a short timer.
	result, err := reg.Execute(context.Background(), "ctx_execute", ctxTimeoutArgs(t, "quick", -1))
	if err == nil && result.Err == nil {
		t.Fatal("negative timeout was accepted")
	}
}

func TestCtxExecutePreservesParentCancellation(t *testing.T) {
	for _, scenario := range []struct {
		deadline bool
		timeout  int
	}{{false, 0}, {true, 0}, {false, 120000}, {true, 120000}} {
		t.Run(fmt.Sprintf("deadline=%v/timeout=%d", scenario.deadline, scenario.timeout), func(t *testing.T) {
			root := t.TempDir()
			runner := ctxexec.New(root)
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if scenario.deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				want = context.DeadlineExceeded
			} else {
				// Dispatch reached the runner. Cancel before starting the child
				// instead of a timing-dependent sleep or polling a process.
				runner.Now = func() time.Time { cancel(); return time.Now() }
			}
			defer cancel()
			result, err := NewCtxExecuteTool(runner, root).Execute(ctx, ctxTimeoutArgs(t, "wait", scenario.timeout))
			if !errors.Is(result.Err, want) && !errors.Is(err, want) {
				t.Fatalf("lost cancellation cause: %v / %v", result.Err, err)
			}
		})
	}
}

func TestCtxExecuteOwnTimeoutRemainsFailure(t *testing.T) {
	root := t.TempDir()
	result, err := NewCtxExecuteTool(ctxexec.New(root), root).Execute(context.Background(), ctxTimeoutArgs(t, "wait", 100))
	if err != nil || result.Err == nil || !strings.Contains(result.Err.Error(), "timeout") {
		t.Fatalf("missing command timeout: %v / %v", result.Err, err)
	}
	if errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatal("command timeout was confused with an interrupted turn")
	}
}

// Opt-in wall-clock reproduction of the old 30-second ceiling. Ordinary test
// runs stay fast; this experiment needs no model, network or provider credit.
func TestCtxExecuteLongCommandLive(t *testing.T) {
	if os.Getenv("SUPERCLI_TEST_LONG_COMMAND") != "1" {
		t.Skip("set SUPERCLI_TEST_LONG_COMMAND=1 for the 31-second regression")
	}
	root := t.TempDir()
	result, err := NewCtxExecuteTool(ctxexec.New(root), root).Execute(context.Background(), ctxTimeoutArgs(t, "long", 0))
	if err != nil || result.Err != nil || !strings.Contains(result.Text, "FINISHED") {
		t.Fatalf("long command did not finish: %v / %v / %s", err, result.Err, result.Text)
	}
}
